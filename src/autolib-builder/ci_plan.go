package main

import (
	"fmt"
	"slices"
	"strings"
)

var ciPlatforms = []string{"windows-x86_64", "linux-x86_64", "linux-aarch64"}
var ciModules = []string{"queue", "lz4", "flann"}

type ciPlanOptions struct {
	SourceRoot         string
	Platforms, Modules *string
	Force              bool
}

type ciPlan struct {
	Platforms          []string            `json:"platforms"`
	Modules            []string            `json:"modules"`
	Layers             [][]string          `json:"layers"`
	Publisher          string              `json:"publisher"`
	Force              bool                `json:"force"`
	SystemDependencies map[string][]string `json:"system_dependencies"`
}

func ciList(value *string, fallback, allowed []string, label string) ([]string, error) {
	if value == nil {
		return slices.Clone(fallback), nil
	}
	parts := strings.Split(*value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
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

func planCI(o ciPlanOptions) (ciPlan, error) {
	if strings.TrimSpace(o.SourceRoot) == "" {
		return ciPlan{}, fmt.Errorf("source root cannot be empty")
	}
	platforms, err := ciList(o.Platforms, ciPlatforms, ciPlatforms, "platform")
	if err != nil {
		return ciPlan{}, err
	}
	selected, err := ciList(o.Modules, ciModules, ciModules, "module")
	if err != nil {
		return ciPlan{}, err
	}
	closure := []string{}
	definitions := map[string]*moduleDefinition{}
	for _, platform := range platforms {
		system, architecture, err := ciTarget(platform)
		if err != nil {
			return ciPlan{}, err
		}
		loaded, err := loadModuleClosure(o.SourceRoot, selected, system, architecture)
		if err != nil {
			return ciPlan{}, err
		}
		for _, definition := range loaded {
			if !slices.Contains(closure, definition.Name) {
				closure = append(closure, definition.Name)
			}
			definitions[definition.Name] = definition
		}
	}

	depth := map[string]int{}
	var moduleDepth func(string, map[string]bool) (int, error)
	moduleDepth = func(name string, visiting map[string]bool) (int, error) {
		if level, ok := depth[name]; ok {
			return level, nil
		}
		if visiting[name] {
			return 0, fmt.Errorf("module dependency cycle involving %s", name)
		}
		definition := definitions[name]
		if definition == nil {
			return 0, fmt.Errorf("module %q is missing from selected dependency closure", name)
		}
		visiting[name] = true
		level := 0
		for _, dependency := range definition.Depends {
			if !slices.Contains(closure, dependency) {
				continue
			}
			depLevel, err := moduleDepth(dependency, visiting)
			if err != nil {
				return 0, err
			}
			if depLevel+1 > level {
				level = depLevel + 1
			}
		}
		delete(visiting, name)
		depth[name] = level
		return level, nil
	}
	maxDepth := 0
	for _, name := range closure {
		level, err := moduleDepth(name, map[string]bool{})
		if err != nil {
			return ciPlan{}, err
		}
		if level > maxDepth {
			maxDepth = level
		}
	}
	layers := make([][]string, maxDepth+1)
	for _, name := range closure {
		layers[depth[name]] = append(layers[depth[name]], name)
	}
	for i := range layers {
		slices.Sort(layers[i])
	}

	modules := slices.Clone(closure)
	plan := ciPlan{Platforms: platforms, Modules: modules, Layers: layers, Force: o.Force, SystemDependencies: map[string][]string{}}
	if slices.Contains(modules, "queue") {
		for _, preferred := range []string{"linux-x86_64", "linux-aarch64", "windows-x86_64"} {
			if slices.Contains(platforms, preferred) {
				plan.Publisher = preferred
				break
			}
		}
	}
	return plan, nil
}
