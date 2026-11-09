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
	SystemName     string            `json:"system_name"`
	Architecture   string            `json:"architecture"`
	Roots          []string          `json:"roots"`
	RequestedRoots []string          `json:"requested_roots"`
	FilteredRoots  []filteredRoot    `json:"filtered_roots"`
	Modules        []describedModule `json:"modules"`
	GoModuleFiles  []string          `json:"go_module_files"`
	BuilderFiles   []string          `json:"builder_files"`
}

type describedModule struct {
	Name               string              `json:"name"`
	Version            string              `json:"version"`
	Target             string              `json:"target"`
	Depends            []string            `json:"depends"`
	PackagePlatform    string              `json:"package_platform"`
	SystemDependencies map[string][]string `json:"system_dependencies"`
	InputFiles         []string            `json:"input_files"`
	AdapterFile        string              `json:"adapter_file"`
}

func describeModules(sourceRoot, systemName, architecture string, names []string) (moduleDescription, error) {
	return describeSelection(sourceRoot, systemName, architecture, names, false)
}

func describeSelection(sourceRoot, systemName, architecture string, requests []string, discover bool) (moduleDescription, error) {
	result := moduleDescription{SystemName: systemName, Architecture: normalizeArchitecture(architecture), Roots: []string{}, Modules: []describedModule{}, GoModuleFiles: []string{"go.mod", "go.sum"}, BuilderFiles: []string{}}
	if strings.TrimSpace(sourceRoot) == "" {
		return result, fmt.Errorf("describe: source root is required")
	}
	if _, err := buildPlatform(systemName, result.Architecture); err != nil {
		return result, err
	}
	if discover && len(requests) != 0 {
		return result, fmt.Errorf("describe: --discover and --module are mutually exclusive")
	}
	if !discover && len(requests) == 0 {
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
	selection, err := selectModuleClosure(root, requests, systemName, result.Architecture, discover)
	if err != nil {
		return result, err
	}
	result.RequestedRoots, result.Roots, result.FilteredRoots = selection.RequestedRoots, selection.Roots, selection.FilteredRoots
	definitions := selection.Modules
	byRef := map[string]*moduleDefinition{}
	for _, d := range definitions {
		byRef[(moduleRef{d.Name, d.Version}).String()] = d
	}
	for _, definition := range definitions {
		deps := []*moduleDefinition{}
		for _, ref := range definition.Depends {
			deps = append(deps, byRef[ref])
		}
		if err := validateCacheOwnership(definition, strings.ToLower(systemName)+"-"+result.Architecture, deps); err != nil {
			return result, err
		}
		module, err := describeModule(root, definition, strings.ToLower(systemName)+"-"+result.Architecture)
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
		if entry.IsDir() || strings.HasSuffix(name, "_test.go") {
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

func describeModule(root string, definition *moduleDefinition, platform string) (describedModule, error) {
	result := describedModule{Name: definition.Name, Version: definition.Version, Target: moduleTarget(moduleRef{definition.Name, definition.Version}), Depends: slices.Clone(definition.Depends), PackagePlatform: platform, SystemDependencies: map[string][]string{}, InputFiles: []string{}}
	if slices.Contains(definition.PackagePlatforms, "any-any") {
		result.PackagePlatform = "any-any"
	}
	if result.Depends == nil {
		result.Depends = []string{}
	}
	manager := map[string]string{"windows": "choco", "linux": "apt", "darwin": "brew"}[strings.SplitN(platform, "-", 2)[0]]
	result.SystemDependencies[manager] = slices.Clone(definition.SystemDependencies[manager])
	if result.SystemDependencies[manager] == nil {
		result.SystemDependencies[manager] = []string{}
	}

	paths := map[string]bool{}
	toRelative := func(value string) (string, error) {
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
	for _, input := range []string{definition.Path, filepath.Join(filepath.Dir(filepath.Dir(definition.Path)), "package.toml")} {
		path, err := toRelative(input)
		if err != nil {
			return result, err
		}
		paths[path] = true
	}
	for _, patch := range definition.Patches {
		path, err := toRelative(patch.File)
		if err != nil {
			return result, err
		}
		paths[path] = true
	}
	for _, step := range definition.Steps {
		for _, value := range step.Depends {
			if directoryTag.MatchString(value) {
				continue
			}
			path, err := toRelative(value)
			if err != nil {
				return result, err
			}
			paths[path] = true
		}
	}

	adapter := filepath.Join(filepath.Dir(definition.Path), "autolib-import.cmake")
	if _, err := os.Stat(adapter); err == nil {
		result.AdapterFile, err = toRelative(adapter)
		if err != nil {
			return result, err
		}
		paths[result.AdapterFile] = true
	} else if !os.IsNotExist(err) {
		return result, err
	}
	result.InputFiles = make([]string, 0, len(paths))
	for path := range paths {
		result.InputFiles = append(result.InputFiles, path)
	}
	slices.Sort(result.InputFiles)
	return result, nil
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
	discover := flags.Bool("discover", false, "discover all recipe versions")
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
	if !*discover && len(modules) == 0 {
		return fail(fmt.Errorf("describe: at least one --module is required"))
	}
	description, err := describeSelection(sourceRoot, systemName, architecture, modules, *discover)
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
