package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var ciPlatforms = []string{"windows-x86_64", "linux-x86_64", "linux-aarch64"}

func planCI(ctx context.Context, o ciPlanOptions) (ciPlan, error) {
	if strings.TrimSpace(o.SourceRoot) == "" || strings.TrimSpace(o.BuilderFile) == "" {
		return ciPlan{}, fmt.Errorf("source root and builder file are required")
	}
	platformSelection := o.Platforms
	if platformSelection != nil && strings.TrimSpace(*platformSelection) == "all" {
		platformSelection = nil
	}
	platforms, err := ciList(platformSelection, ciPlatforms, ciPlatforms, "platform")
	if err != nil {
		return ciPlan{}, err
	}
	moduleSelection := o.Modules
	if moduleSelection != nil && strings.TrimSpace(*moduleSelection) == "" {
		moduleSelection = nil
	}
	known, candidates, err := discoverCIModules(o.SourceRoot)
	if err != nil {
		return ciPlan{}, err
	}
	selected, err := ciList(moduleSelection, candidates, known, "module")
	if err != nil {
		return ciPlan{}, err
	}
	plan := ciPlan{Platforms: []ciPlatformPlan{}, RequestedModules: selected}
	if len(selected) == 0 {
		return plan, nil
	}
	for _, platform := range platforms {
		systemName, architecture, err := ciTarget(platform)
		if err != nil {
			return ciPlan{}, err
		}
		description, err := readModuleDescription(ctx, o.BuilderFile, o.SourceRoot, systemName, architecture, selected)
		if err != nil {
			return ciPlan{}, fmt.Errorf("platform %s: %w", platform, err)
		}
		description.Modules, err = effectiveCIModules(o.SourceRoot, description.Modules, selected)
		if err != nil {
			return ciPlan{}, fmt.Errorf("platform %s: %w", platform, err)
		}
		if len(description.Modules) == 0 {
			continue
		}
		layers, err := ciLayers(description.Modules)
		if err != nil {
			return ciPlan{}, err
		}
		plan.Platforms = append(plan.Platforms, ciPlatformPlan{ID: platform, Description: description, Layers: layers, SystemDependencies: ciSystemDependencies(description.Modules)})
	}
	for _, preferred := range []string{"linux-x86_64", "linux-aarch64", "windows-x86_64"} {
		for _, platform := range plan.Platforms {
			if platform.ID != preferred {
				continue
			}
			for _, module := range platform.Description.Modules {
				if module.PlatformIndependent {
					plan.Publisher = preferred
					return plan, nil
				}
			}
		}
	}
	return plan, nil
}

func readableCIAdapter(file string) (bool, error) {
	info, err := os.Stat(file)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("adapter %s must be a readable regular file", file)
	}
	f, err := os.Open(file)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	return true, nil
}

func discoverCIModules(root string) (known, candidates []string, err error) {
	directory := filepath.Join(root, "src", "dependencies")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, nil, err
	}
	known, candidates = []string{}, []string{}
	for _, entry := range entries {
		moduleDirectory := filepath.Join(directory, entry.Name())
		isDirectory := entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(moduleDirectory)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			isDirectory = info.IsDir()
		}
		if !isDirectory {
			continue
		}
		files, err := os.ReadDir(moduleDirectory)
		if err != nil {
			return nil, nil, err
		}
		if !slices.ContainsFunc(files, func(file os.DirEntry) bool { return file.Name() == "module.toml" }) {
			continue
		}
		name := entry.Name()
		known = append(known, name)
		ok, err := readableCIAdapter(filepath.Join(moduleDirectory, "autolib-import.cmake"))
		if err != nil {
			return nil, nil, err
		}
		if ok {
			candidates = append(candidates, name)
		}
	}
	return known, candidates, nil
}

func effectiveCIModules(root string, modules []describedModule, requested []string) ([]describedModule, error) {
	byName := map[string]describedModule{}
	eligible := map[string]bool{}
	for _, module := range modules {
		byName[module.Name] = module
		if module.AdapterFile != "" {
			ok, err := readableCIAdapter(filepath.Join(root, filepath.FromSlash(module.AdapterFile)))
			if err != nil {
				return nil, err
			}
			eligible[module.Name] = ok
		}
	}
	// Validate the complete described graph before excluding ineligible roots.
	if _, err := ciLayers(modules); err != nil {
		return nil, err
	}
	var qualifies func(string) bool
	qualifies = func(name string) bool {
		if !eligible[name] {
			return false
		}
		for _, dependency := range byName[name].Depends {
			if !qualifies(dependency) {
				return false
			}
		}
		return true
	}
	keep := map[string]bool{}
	var add func(string)
	add = func(name string) {
		if keep[name] {
			return
		}
		keep[name] = true
		for _, dependency := range byName[name].Depends {
			add(dependency)
		}
	}
	for _, name := range requested {
		if qualifies(name) {
			add(name)
		}
	}
	result := []describedModule{}
	for _, module := range modules {
		if keep[module.Name] {
			result = append(result, module)
		}
	}
	return result, nil
}

func ciList(value *string, fallback, allowed []string, label string) ([]string, error) {
	if value == nil {
		return slices.Clone(fallback), nil
	}
	result := []string{}
	for _, part := range strings.Split(*value, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			return nil, fmt.Errorf("%s contains an empty item", label)
		}
		if !slices.Contains(allowed, item) {
			return nil, fmt.Errorf("unknown %s %q", label, item)
		}
		if !slices.Contains(result, item) {
			result = append(result, item)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%s cannot be empty", label)
	}
	return result, nil
}

func ciTarget(platform string) (string, string, error) {
	switch platform {
	case "windows-x86_64":
		return "Windows", "x86_64", nil
	case "linux-x86_64":
		return "Linux", "x86_64", nil
	case "linux-aarch64":
		return "Linux", "aarch64", nil
	default:
		return "", "", fmt.Errorf("unsupported CI platform %q", platform)
	}
}

func ciLayers(modules []describedModule) ([][]string, error) {
	byName := make(map[string]describedModule, len(modules))
	for _, module := range modules {
		byName[module.Name] = module
	}
	depth := map[string]int{}
	visiting := map[string]bool{}
	var level func(string) (int, error)
	level = func(name string) (int, error) {
		if depthValue, ok := depth[name]; ok {
			return depthValue, nil
		}
		if visiting[name] {
			return 0, fmt.Errorf("module dependency cycle involving %s", name)
		}
		module, ok := byName[name]
		if !ok {
			return 0, fmt.Errorf("module %q is missing from selected dependency description", name)
		}
		visiting[name] = true
		depthValue := 0
		for _, dependency := range module.Depends {
			dependencyDepth, err := level(dependency)
			if err != nil {
				return 0, err
			}
			if dependencyDepth+1 > depthValue {
				depthValue = dependencyDepth + 1
			}
		}
		delete(visiting, name)
		depth[name] = depthValue
		return depthValue, nil
	}
	maxDepth := 0
	for name := range byName {
		depthValue, err := level(name)
		if err != nil {
			return nil, err
		}
		if depthValue > maxDepth {
			maxDepth = depthValue
		}
	}
	layers := make([][]string, maxDepth+1)
	for name, depthValue := range depth {
		layers[depthValue] = append(layers[depthValue], name)
	}
	for i := range layers {
		slices.Sort(layers[i])
	}
	return layers, nil
}

func ciSystemDependencies(modules []describedModule) map[string][]string {
	result := map[string][]string{"apt": {}, "choco": {}}
	for _, module := range modules {
		for manager, packages := range module.SystemDependencies {
			for _, name := range packages {
				if !slices.Contains(result[manager], name) {
					result[manager] = append(result[manager], name)
				}
			}
		}
	}
	for manager := range result {
		slices.Sort(result[manager])
	}
	return result
}
