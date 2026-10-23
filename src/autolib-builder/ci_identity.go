package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type ciBuildInputs struct {
	SourceRoot, OS, Arch string
	Context              buildContext
	BaselineFile         string
}

type ciIdentity struct {
	BuildID       string `json:"build_id"`
	BuildInputsID string `json:"build_inputs_id"`
}

func ciHash(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func ciRelative(root, name string) (string, error) {
	if filepath.IsAbs(name) {
		var err error
		name, err = filepath.Rel(root, name)
		if err != nil || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("CI input %q is outside the source root", name)
		}
	}
	full := filepath.Join(root, name)
	clean, err := filepath.Rel(root, full)
	if err != nil || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("CI input %q is outside the source root", name)
	}
	return filepath.ToSlash(clean), nil
}

func ciFile(root, name string) (string, []byte, error) {
	clean, err := ciRelative(root, name)
	if err != nil {
		return "", nil, err
	}
	full := filepath.Join(root, filepath.FromSlash(clean))
	data, err := os.ReadFile(full)
	if err != nil {
		return "", nil, fmt.Errorf("read CI input %s: %w", clean, err)
	}
	return clean, data, nil
}

func ciSharedInputs(root string) (map[string]string, error) {
	paths := []string{"go.mod", "go.sum"}
	engine := filepath.Join(root, "src", "autolib-builder")
	entries, err := os.ReadDir(engine)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && ((strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")) || strings.HasSuffix(name, ".cmake")) {
			paths = append(paths, filepath.ToSlash(filepath.Join("src", "autolib-builder", name)))
		}
	}
	slices.Sort(paths)
	inputs := make(map[string]string, len(paths))
	for _, path := range paths {
		name, data, err := ciFile(root, path)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		inputs[name] = hex.EncodeToString(digest[:])
	}
	return inputs, nil
}

func ciModuleConfiguration(root string, module *moduleDefinition) (string, error) {
	normalized := *module
	normalized.Path = ""
	normalized.Patches = slices.Clone(module.Patches)
	for i := range normalized.Patches {
		name, err := ciRelative(root, normalized.Patches[i].File)
		if err != nil {
			return "", err
		}
		normalized.Patches[i].File = name
	}
	normalized.Steps = slices.Clone(module.Steps)
	for i := range normalized.Steps {
		normalized.Steps[i].Depends = slices.Clone(module.Steps[i].Depends)
		for j, dependency := range normalized.Steps[i].Depends {
			if !strings.Contains(dependency, "<") {
				name, err := ciRelative(root, dependency)
				if err != nil {
					return "", err
				}
				normalized.Steps[i].Depends[j] = name
			}
		}
		if normalized.Steps[i].WorkingDirectory != "" && !strings.Contains(normalized.Steps[i].WorkingDirectory, "<") {
			name, err := ciRelative(root, normalized.Steps[i].WorkingDirectory)
			if err != nil {
				return "", err
			}
			normalized.Steps[i].WorkingDirectory = name
		}
	}
	data, err := json.Marshal(normalized)
	return string(data), err
}

func ciModuleFiles(root string, module *moduleDefinition) (map[string]string, error) {
	paths := []string{}
	for _, patch := range module.Patches {
		paths = append(paths, patch.File)
	}
	for _, step := range module.Steps {
		for _, dependency := range step.Depends {
			if !strings.Contains(dependency, "<") {
				paths = append(paths, dependency)
			}
		}
	}
	slices.Sort(paths)
	inputs := map[string]string{}
	for _, path := range paths {
		name, data, err := ciFile(root, path)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		inputs[name] = hex.EncodeToString(digest[:])
	}
	return inputs, nil
}

func ciBuildContext(o ciBuildInputs, universal bool) map[string]any {
	result := map[string]any{"release": "Release"}
	if !universal {
		result["os"] = strings.ToLower(o.OS)
		result["arch"] = normalizeArchitecture(o.Arch)
		result["generator"] = o.Context.Generator
		result["platform"] = o.Context.Platform
		result["toolset"] = o.Context.Toolset
		result["instance"] = o.Context.Instance
		result["multi_config"] = o.Context.MultiConfig
		if strings.EqualFold(o.OS, "windows") {
			runtime := "MultiThreadedDLL"
			if o.Context.MSVCRuntimeLibrary != nil {
				runtime = *o.Context.MSVCRuntimeLibrary
			}
			result["msvc_runtime_library"] = runtime
		}
		defaults := o.Context.CMakeDefaults
		if defaults == nil {
			defaults = map[string]string{}
		}
		result["cmake_defaults"] = defaults
	}
	return result
}

func computeBuildIdentities(o ciBuildInputs, modules []*moduleDefinition) (map[string]ciIdentity, error) {
	root, err := filepath.Abs(o.SourceRoot)
	if err != nil {
		return nil, err
	}
	baselineName, baseline, err := ciFile(root, o.BaselineFile)
	if err != nil {
		return nil, err
	}
	shared, err := ciSharedInputs(root)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*moduleDefinition, len(modules))
	for _, module := range modules {
		byName[module.Name] = module
	}
	inputs := make(map[string]string, len(modules))
	identities := make(map[string]ciIdentity, len(modules))
	visiting := map[string]bool{}
	var build func(*moduleDefinition) error
	build = func(module *moduleDefinition) error {
		if _, ok := identities[module.Name]; ok {
			return nil
		}
		if visiting[module.Name] {
			return fmt.Errorf("module dependency cycle involving %s", module.Name)
		}
		visiting[module.Name] = true
		for _, dependency := range module.Depends {
			dep := byName[dependency]
			if dep == nil {
				return fmt.Errorf("module %s is missing dependency %s", module.Name, dependency)
			}
			if err := build(dep); err != nil {
				return err
			}
		}
		config, err := ciModuleConfiguration(root, module)
		if err != nil {
			return err
		}
		files, err := ciModuleFiles(root, module)
		if err != nil {
			return err
		}
		universal := module.PlatformIndependent
		base := map[string]any{"module_config": config, "files": files, "shared": shared, "baseline_name": baselineName, "baseline": string(baseline), "context": ciBuildContext(o, universal)}
		dependencyInputs := map[string]string{}
		for _, dependency := range module.Depends {
			dependencyInputs[dependency] = inputs[dependency]
		}
		base["dependencies"] = dependencyInputs
		inputID, err := ciHash(base)
		if err != nil {
			return err
		}
		inputs[module.Name] = inputID
		adapterPath := filepath.Join(filepath.Dir(module.Path), "autolib-import.cmake")
		adapterName, adapter, err := ciFile(root, adapterPath)
		if err != nil {
			return nilCIInputError(module.Name, err)
		}
		buildID, err := ciHash(map[string]any{"inputs": inputID, "adapter_name": adapterName, "adapter": string(adapter)})
		if err != nil {
			return err
		}
		identities[module.Name] = ciIdentity{BuildID: buildID, BuildInputsID: inputID}
		delete(visiting, module.Name)
		return nil
	}
	for _, module := range modules {
		if err := build(module); err != nil {
			return nil, err
		}
	}
	return identities, nil
}

func nilCIInputError(module string, err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("module %s has no autolib-import.cmake adapter", module)
	}
	return err
}
