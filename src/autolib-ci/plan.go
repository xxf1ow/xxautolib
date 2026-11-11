package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var ciPlatforms = []string{"windows-x86_64", "linux-x86_64", "linux-aarch64", "darwin-aarch64"}

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
	toolOnly := moduleSelection == nil || *moduleSelection == ""
	selected := []string{}
	if !toolOnly && *moduleSelection != "all" {
		for _, value := range strings.Split(*moduleSelection, ",") {
			request := strings.TrimSpace(value)
			if request == "" {
				return ciPlan{}, fmt.Errorf("module contains an empty item")
			}
			if strings.EqualFold(request, "all") || strings.Split(request, "@")[0] == "autolib-importer" {
				return ciPlan{}, fmt.Errorf("invalid module selection %q; use exactly all or a library list", *moduleSelection)
			}
			if !slices.Contains(selected, request) {
				selected = append(selected, request)
			}
		}
	}
	plan := ciPlan{Platforms: []ciPlatformPlan{}, RequestedModules: []string{}, Publishers: map[string]string{}}
	for _, platform := range platforms {
		systemName, architecture, err := ciTarget(platform)
		if err != nil {
			return ciPlan{}, err
		}
		description := moduleDescription{SystemName: systemName, Architecture: architecture, Roots: []string{}, RequestedRoots: []string{}, FilteredRoots: []filteredRoot{}, FailedRoots: []failedRoot{}, Modules: []describedModule{}, GoModuleFiles: []string{}, BuilderFiles: []string{}}
		if !toolOnly {
			description, err = readModuleDescription(ctx, o.BuilderFile, o.SourceRoot, systemName, architecture, selected)
			if err != nil {
				return ciPlan{}, fmt.Errorf("platform %s: %w", platform, err)
			}
		}
		requested := slices.Clone(description.RequestedRoots)
		modules, roots, adapterReasons, err := effectiveCIModules(o.SourceRoot, description.Modules, description.Roots)
		if err != nil {
			return ciPlan{}, fmt.Errorf("platform %s: %w", platform, err)
		}
		description.Modules, description.Roots = modules, roots
		description.FilteredRoots = append(description.FilteredRoots, adapterReasons...)
		filtered := []string{}
		for _, reason := range description.FilteredRoots {
			filtered = append(filtered, reason.Root)
		}
		layers, err := ciLayers(description.Modules)
		if err != nil {
			return ciPlan{}, err
		}
		plan.Platforms = append(plan.Platforms, ciPlatformPlan{ID: platform, RequestedRoots: requested, FilteredRoots: filtered, FailedRoots: slices.Clone(description.FailedRoots), Roots: roots, Description: description, Layers: layers})
		for _, root := range roots {
			if !slices.Contains(plan.RequestedModules, root) {
				plan.RequestedModules = append(plan.RequestedModules, root)
			}
		}
	}
	plan.Publishers = ciPublishers(plan.Platforms)
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

func moduleIdentity(module describedModule) string { return module.Name + "@" + module.Version }

func effectiveCIModules(root string, modules []describedModule, requested []string) ([]describedModule, []string, []filteredRoot, error) {
	byName := map[string]describedModule{}
	eligible := map[string]bool{}
	for _, module := range modules {
		identity := moduleIdentity(module)
		byName[identity] = module
		if module.AdapterFile != "" {
			ok, err := readableCIAdapter(filepath.Join(root, filepath.FromSlash(module.AdapterFile)))
			if err != nil {
				return nil, nil, nil, err
			}
			eligible[identity] = ok
		}
	}
	// Validate the complete described graph before excluding ineligible roots.
	if _, err := ciLayers(modules); err != nil {
		return nil, nil, nil, err
	}
	var blocked func(string, []string) []string
	blocked = func(name string, chain []string) []string {
		chain = append(slices.Clone(chain), name)
		if !eligible[name] {
			return chain
		}
		for _, dependency := range byName[name].Depends {
			if reason := blocked(dependency, chain); reason != nil {
				return reason
			}
		}
		return nil
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
	roots := []string{}
	reasons := []filteredRoot{}
	for _, name := range requested {
		if chain := blocked(name, nil); chain != nil {
			reasons = append(reasons, filteredRoot{name, "missing_adapter", chain})
		} else {
			roots = append(roots, name)
			add(name)
		}
	}
	result := []describedModule{}
	for _, module := range modules {
		if keep[moduleIdentity(module)] {
			result = append(result, module)
		}
	}
	return result, roots, reasons, nil
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
	return result, nil
}

func ciTarget(platform string) (string, string, error) {
	switch platform {
	case "windows-x86_64":
		return "Windows", "x86_64", nil
	case "linux-x86_64":
		return "Linux", "x86_64", nil
	case "darwin-aarch64":
		return "Darwin", "aarch64", nil
	case "linux-aarch64":
		return "Linux", "aarch64", nil
	default:
		return "", "", fmt.Errorf("unsupported CI platform %q", platform)
	}
}

func ciLayers(modules []describedModule) ([][]string, error) {
	if len(modules) == 0 {
		return [][]string{}, nil
	}
	byName := make(map[string]describedModule, len(modules))
	for _, module := range modules {
		byName[moduleIdentity(module)] = module
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

func ciPublishers(platforms []ciPlatformPlan) map[string]string {
	publishers := map[string]string{}
	for _, preferred := range []string{"linux-x86_64", "linux-aarch64", "windows-x86_64", "darwin-aarch64"} {
		for _, platform := range platforms {
			if platform.ID != preferred {
				continue
			}
			for _, module := range platform.Description.Modules {
				key := moduleIdentity(module)
				if module.PackagePlatform == "any-any" && publishers[key] == "" {
					publishers[key] = preferred
				}
			}
		}
	}
	return publishers
}

func validateCIPublishers(plan ciPlan) error {
	if plan.Publishers == nil {
		return fmt.Errorf("CI plan requires publishers object")
	}
	expected := ciPublishers(plan.Platforms)
	if len(expected) != len(plan.Publishers) {
		return fmt.Errorf("CI publishers do not match exact universal packages")
	}
	for key, publisher := range expected {
		if plan.Publishers[key] != publisher {
			return fmt.Errorf("CI publisher for %s must be %s", key, publisher)
		}
	}
	for _, platform := range plan.Platforms {
		for _, module := range platform.Description.Modules {
			if module.PackagePlatform != "any-any" && module.PackagePlatform != platform.ID {
				return fmt.Errorf("invalid package_platform for %s on %s", moduleIdentity(module), platform.ID)
			}
		}
	}
	return nil
}
