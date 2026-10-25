package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

var ciPlatforms = []string{"windows-x86_64", "linux-x86_64", "linux-aarch64"}
var ciModules = []string{"queue", "lz4", "flann"}

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
	selected, err := ciList(moduleSelection, ciModules, ciModules, "module")
	if err != nil {
		return ciPlan{}, err
	}
	plan := ciPlan{Platforms: []ciPlatformPlan{}, RequestedModules: selected}
	for _, platform := range platforms {
		systemName, architecture, err := ciTarget(platform)
		if err != nil {
			return ciPlan{}, err
		}
		description, err := readModuleDescription(ctx, o.BuilderFile, o.SourceRoot, systemName, architecture, selected)
		if err != nil {
			return ciPlan{}, fmt.Errorf("platform %s: %w", platform, err)
		}
		for _, module := range description.Modules {
			if !slices.Contains(ciModules, module.Name) {
				return ciPlan{}, fmt.Errorf("module %q is outside the supported CI closure", module.Name)
			}
		}
		layers, err := ciLayers(description.Modules)
		if err != nil {
			return ciPlan{}, err
		}
		plan.Platforms = append(plan.Platforms, ciPlatformPlan{ID: platform, Description: description, Layers: layers, SystemDependencies: ciSystemDependencies(description.Modules)})
	}
	if slices.Contains(selected, "queue") {
		for _, preferred := range []string{"linux-x86_64", "linux-aarch64", "windows-x86_64"} {
			if slices.Contains(platforms, preferred) {
				plan.Publisher = preferred
				break
			}
		}
	}
	return plan, nil
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
