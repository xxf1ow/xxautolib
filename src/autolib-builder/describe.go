package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type moduleDescription struct {
	SystemName    string            `json:"system_name"`
	Architecture  string            `json:"architecture"`
	Modules       []describedModule `json:"modules"`
	GoModuleFiles []string          `json:"go_module_files"`
	BuilderFiles  []string          `json:"builder_files"`
}

type describedModule struct {
	Name                string              `json:"name"`
	Depends             []string            `json:"depends"`
	PlatformIndependent bool                `json:"platform_independent"`
	SystemDependencies  map[string][]string `json:"system_dependencies"`
	Configuration       json.RawMessage     `json:"configuration"`
	InputFiles          []string            `json:"input_files"`
	AdapterFile         string              `json:"adapter_file"`
}

func describeModules(sourceRoot, systemName, architecture string, names []string) (moduleDescription, error) {
	result := moduleDescription{SystemName: systemName, Architecture: normalizeArchitecture(architecture), Modules: []describedModule{}, GoModuleFiles: []string{"go.mod", "go.sum"}, BuilderFiles: []string{}}
	if strings.TrimSpace(sourceRoot) == "" {
		return result, fmt.Errorf("describe: source root is required")
	}
	if systemName != "Linux" && systemName != "Windows" {
		return result, fmt.Errorf("describe: unsupported system name %q", systemName)
	}
	if result.Architecture != "x86_64" && result.Architecture != "aarch64" {
		return result, fmt.Errorf("describe: unsupported architecture %q", architecture)
	}
	if len(names) == 0 {
		return result, fmt.Errorf("describe: at least one module is required")
	}
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return result, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return result, fmt.Errorf("source root: %w", err)
	}
	definitions, err := loadModuleClosure(root, names, systemName, result.Architecture)
	if err != nil {
		return result, err
	}
	for _, definition := range definitions {
		module, err := describeModule(root, definition)
		if err != nil {
			return result, err
		}
		result.Modules = append(result.Modules, module)
	}
	for _, name := range result.GoModuleFiles {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			return result, fmt.Errorf("describe input %s: %w", name, err)
		}
	}
	engine := filepath.Join(root, "src", "autolib-builder")
	entries, err := os.ReadDir(engine)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, "ci_") {
			continue
		}
		if strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".cmake") {
			result.BuilderFiles = append(result.BuilderFiles, filepath.ToSlash(filepath.Join("src", "autolib-builder", name)))
		}
	}
	result.BuilderFiles = append(result.BuilderFiles, "CMakeLists.txt")
	slices.Sort(result.BuilderFiles)
	return result, nil
}

func describeModule(root string, definition *moduleDefinition) (describedModule, error) {
	result := describedModule{Name: definition.Name, Depends: slices.Clone(definition.Depends), PlatformIndependent: definition.PlatformIndependent, SystemDependencies: map[string][]string{}, InputFiles: []string{}}
	if result.Depends == nil {
		result.Depends = []string{}
	}
	for _, manager := range []string{"apt", "choco"} {
		result.SystemDependencies[manager] = slices.Clone(definition.SystemDependencies[manager])
		if result.SystemDependencies[manager] == nil {
			result.SystemDependencies[manager] = []string{}
		}
	}
	paths := map[string]bool{}
	toRelative := func(value string, allowPlaceholder bool) (string, error) {
		if allowPlaceholder && resourceReference.MatchString(value) {
			if filepath.IsAbs(value) {
				return "", fmt.Errorf("referenced path escapes source root: %s", value)
			}
			return value, nil
		}
		path := value
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(definition.Path), path)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return "", fmt.Errorf("referenced path %s: %w", value, err)
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return "", fmt.Errorf("referenced path escapes source root: %s", value)
		}
		return filepath.ToSlash(rel), nil
	}
	resources := map[string]sourceDefinition{}
	for name, source := range definition.Resources {
		normalized := normalizedSource(&source)
		resources[name] = *normalized
	}
	if resources == nil {
		resources = map[string]sourceDefinition{}
	}
	copies := slices.Clone(definition.Copies)
	for i := range copies {
		if copies[i].Files == nil {
			copies[i].Files = []string{}
		}
		if copies[i].Directories == nil {
			copies[i].Directories = []string{}
		}
	}
	configuration := map[string]any{
		"Name": definition.Name, "Kind": definition.Kind, "Version": definition.Version,
		"Sentinel": definition.Sentinel, "SourceSubdir": definition.SourceSubdir,
		"ConfigDir": definition.ConfigDir, "PkgconfigDir": definition.PkgconfigDir,
		"PackageName": definition.PackageName, "Source": normalizedSource(definition.Source),
		"Resources": resources, "Cache": definition.Cache,
		"Patches": slices.Clone(definition.Patches), "Steps": slices.Clone(definition.Steps), "Copies": copies,
	}
	if definition.Resources == nil {
		configuration["Resources"] = map[string]sourceDefinition{}
	}
	if definition.Cache == nil {
		configuration["Cache"] = map[string]any{}
	}
	if definition.Patches == nil {
		configuration["Patches"] = []patchDefinition{}
	}
	if definition.Steps == nil {
		configuration["Steps"] = []stepDefinition{}
	}
	if definition.Copies == nil {
		configuration["Copies"] = []copyDefinition{}
	}
	for i := range definition.Patches {
		path, err := toRelative(definition.Patches[i].File, false)
		if err != nil {
			return result, fmt.Errorf("module %s: %w", definition.Name, err)
		}
		configuration["Patches"].([]patchDefinition)[i].File = path
		paths[path] = true
	}
	steps := slices.Clone(definition.Steps)
	for i := range steps {
		if steps[i].Commands == nil {
			steps[i].Commands = [][]string{}
		}
		if steps[i].Dependees == nil {
			steps[i].Dependees = []string{}
		}
		if steps[i].Dependers == nil {
			steps[i].Dependers = []string{}
		}
		if steps[i].Depends == nil {
			steps[i].Depends = []string{}
		}
		for j, value := range steps[i].Depends {
			path, err := toRelative(value, true)
			if err != nil {
				return result, fmt.Errorf("module %s step %s: %w", definition.Name, steps[i].Name, err)
			}
			steps[i].Depends[j] = path
			if path != value || !resourceReference.MatchString(value) {
				paths[path] = true
			}
		}
		if steps[i].WorkingDirectory != "" {
			value := steps[i].WorkingDirectory
			path, err := toRelative(value, true)
			if err != nil {
				return result, fmt.Errorf("module %s step %s: %w", definition.Name, steps[i].Name, err)
			}
			steps[i].WorkingDirectory = path
		}
	}
	configuration["Steps"] = steps
	adapter := filepath.Join(filepath.Dir(definition.Path), "autolib-import.cmake")
	if _, err := os.Stat(adapter); err == nil {
		result.AdapterFile, err = toRelative(adapter, false)
		if err != nil {
			return result, err
		}
	} else if !os.IsNotExist(err) {
		return result, err
	}
	result.InputFiles = make([]string, 0, len(paths))
	for path := range paths {
		result.InputFiles = append(result.InputFiles, path)
	}
	slices.Sort(result.InputFiles)
	encoded, err := json.Marshal(configuration)
	if err != nil {
		return result, err
	}
	result.Configuration = encoded
	return result, nil
}

func normalizedSource(source *sourceDefinition) *sourceDefinition {
	if source == nil {
		return nil
	}
	result := *source
	if result.URL == nil {
		result.URL = []string{}
	}
	return &result
}

func runDescribe(args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	flags := flag.NewFlagSet("describe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var sourceRoot, systemName, architecture string
	flags.StringVar(&sourceRoot, "source-root", "", "source root")
	flags.StringVar(&systemName, "system-name", "", "Linux or Windows")
	flags.StringVar(&architecture, "architecture", "", "target architecture")
	var modules repeatedFlag
	flags.Var(&modules, "module", "module name (repeatable)")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		return fail(fmt.Errorf("describe: unexpected positional argument %q", flags.Arg(0)))
	}
	visited := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	for _, key := range []string{"source-root", "system-name", "architecture"} {
		if !visited[key] || flags.Lookup(key).Value.String() == "" {
			return fail(fmt.Errorf("describe: --%s is required and cannot be empty", key))
		}
	}
	if len(modules) == 0 {
		return fail(fmt.Errorf("describe: at least one --module is required"))
	}
	description, err := describeModules(sourceRoot, systemName, architecture, modules)
	if err != nil {
		return fail(err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(description); err != nil {
		return fail(err)
	}
	return 0
}
