package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

func loadModuleClosure(sourceRoot string, names []string, systemName, architecture string) ([]*moduleDefinition, error) {
	result := []*moduleDefinition{}
	complete := map[string]bool{}
	stack := []string{}
	var visit func(string) error
	visit = func(moduleName string) error {
		path := filepath.Join(sourceRoot, "src", "dependencies", moduleName, "module.toml")
		chain := strings.Join(append(slices.Clone(stack), moduleName), " -> ")
		if slices.Contains(stack, moduleName) {
			return fmt.Errorf("%s: dependency cycle: %s", path, chain)
		}
		if complete[moduleName] {
			return nil
		}
		if _, err := name(moduleName, "module name"); err != nil {
			return fmt.Errorf("%s: %w; dependency chain: %s", path, err, chain)
		}
		module, err := loadDefinition(path, systemName, architecture)
		if err != nil {
			return fmt.Errorf("%w; dependency chain: %s", err, chain)
		}
		stack = append(stack, moduleName)
		defer func() { stack = stack[:len(stack)-1] }()
		for _, dependency := range module.Depends {
			if err := visit(dependency); err != nil {
				return fmt.Errorf("%s: %w", module.Path, err)
			}
		}
		complete[moduleName] = true
		result = append(result, module)
		return nil
	}
	for _, moduleName := range names {
		if err := visit(moduleName); err != nil {
			return nil, err
		}
	}
	return result, nil
}
