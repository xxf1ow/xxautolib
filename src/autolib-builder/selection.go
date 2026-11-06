package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

var recipePlatforms = []string{"windows-x86_64", "linux-x86_64", "linux-aarch64", "darwin-aarch64"}

type filteredRoot struct {
	Root  string   `json:"root"`
	Code  string   `json:"code"`
	Chain []string `json:"chain"`
}

type moduleSelection struct {
	RequestedRoots, Roots []string
	Modules               []*moduleDefinition
	FilteredRoots         []filteredRoot
}

func buildPlatform(systemName, architecture string) (string, error) {
	platform := strings.ToLower(systemName) + "-" + normalizeArchitecture(architecture)
	if !slices.Contains(recipePlatforms, platform) {
		return "", fmt.Errorf("unsupported platform %s/%s", systemName, architecture)
	}
	return platform, nil
}

func selectModuleClosure(sourceRoot string, requests []string, systemName, architecture string, discover bool) (moduleSelection, error) {
	result := moduleSelection{RequestedRoots: []string{}, Roots: []string{}, Modules: []*moduleDefinition{}, FilteredRoots: []filteredRoot{}}
	platform, err := buildPlatform(systemName, architecture)
	if err != nil {
		return result, err
	}
	if discover {
		refs, err := discoverModuleRefs(sourceRoot)
		if err != nil {
			return result, err
		}
		for _, ref := range refs {
			requests = append(requests, ref.String())
		}
	}
	resolved := map[string]moduleRef{}
	resolve := func(value string) (moduleRef, error) {
		if ref, ok := resolved[value]; ok {
			return ref, nil
		}
		ref, err := parseModuleRef(value)
		if err != nil {
			return moduleRef{}, err
		}
		ref, err = resolveModuleRef(sourceRoot, ref)
		if err == nil {
			resolved[value], resolved[ref.String()] = ref, ref
		}
		return ref, err
	}
	definitions := map[string]*moduleDefinition{}
	merged := map[string]bool{}
	for _, request := range requests {
		root, err := resolve(request)
		if err != nil {
			return result, fmt.Errorf("root %s: %w", request, err)
		}
		if slices.Contains(result.RequestedRoots, root.String()) {
			continue
		}
		result.RequestedRoots = append(result.RequestedRoots, root.String())
		type selection struct {
			ref   moduleRef
			chain string
		}
		selected := map[string]selection{}
		complete := map[string]bool{}
		stack := []string{}
		closure := []*moduleDefinition{}
		var blocked []string
		var visit func(moduleRef) error
		visit = func(ref moduleRef) error {
			key := ref.String()
			chain := append(slices.Clone(stack), key)
			chainText := strings.Join(chain, " -> ")
			path := filepath.Join(sourceRoot, "src/dependencies", ref.Name, ref.Version, "module.toml")
			if previous, ok := selected[ref.Name]; ok && previous.ref.Version != ref.Version {
				return fmt.Errorf("root %s: conflicting dependency versions %s and %s; dependency chains: %s; %s", root, previous.ref, ref, previous.chain, chainText)
			}
			if slices.Contains(stack, key) {
				return fmt.Errorf("%s: dependency cycle: %s", path, chainText)
			}
			if complete[key] {
				return nil
			}
			selected[ref.Name] = selection{ref, chainText}
			module, ok := definitions[key]
			if !ok {
				module, err = readDefinition(path, systemName, architecture, false)
				if err != nil {
					return fmt.Errorf("%w; dependency chain: %s", err, chainText)
				}
				depends := []string{}
				for _, dependency := range module.Depends {
					dep, err := resolve(dependency)
					if err != nil {
						return fmt.Errorf("%s: %w; dependency chain: %s -> %s", path, err, chainText, dependency)
					}
					depends = append(depends, dep.String())
				}
				module.Depends = unique(depends)
				definitions[key] = module
			}
			if blocked == nil && !slices.Contains(module.PackagePlatforms, "any-any") && !slices.Contains(module.PackagePlatforms, platform) {
				blocked = chain
			}
			stack = append(stack, key)
			defer func() { stack = stack[:len(stack)-1] }()
			for _, dependency := range module.Depends {
				if err := visit(resolved[dependency]); err != nil {
					return err
				}
			}
			complete[key] = true
			closure = append(closure, module)
			return nil
		}
		if err := visit(root); err != nil {
			return result, err
		}
		if blocked != nil {
			code := "unsupported_dependency_platform"
			if len(blocked) == 1 {
				code = "unsupported_platform"
			}
			result.FilteredRoots = append(result.FilteredRoots, filteredRoot{root.String(), code, blocked})
			continue
		}
		result.Roots = append(result.Roots, root.String())
		for _, declaration := range closure {
			key := (moduleRef{declaration.Name, declaration.Version}).String()
			if merged[key] {
				continue
			}
			module, err := loadDefinition(declaration.Path, systemName, architecture)
			if err != nil {
				return result, err
			}
			module.Depends = declaration.Depends
			result.Modules = append(result.Modules, module)
			merged[key] = true
		}
	}
	return result, nil
}
