package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

func readModuleDescription(ctx context.Context, builderFile, sourceRoot, systemName, architecture string, modules []string) (moduleDescription, error) {
	if strings.TrimSpace(builderFile) == "" {
		return moduleDescription{}, fmt.Errorf("builder file cannot be empty")
	}
	if strings.TrimSpace(sourceRoot) == "" || len(modules) == 0 {
		return moduleDescription{}, fmt.Errorf("source root and requested modules are required")
	}
	args := []string{"describe", "--source-root", sourceRoot, "--system-name", systemName, "--architecture", architecture}
	for _, module := range modules {
		args = append(args, "--module", module)
	}
	cmd := exec.CommandContext(ctx, builderFile, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return moduleDescription{}, fmt.Errorf("builder describe failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	var description moduleDescription
	if err := decoder.Decode(&description); err != nil {
		return moduleDescription{}, fmt.Errorf("decode builder describe output: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return moduleDescription{}, fmt.Errorf("builder describe output contains multiple JSON values")
		}
		return moduleDescription{}, fmt.Errorf("builder describe output has trailing data: %w", err)
	}
	if err := validateModuleDescription(description, systemName, architecture, modules); err != nil {
		return moduleDescription{}, err
	}
	return description, nil
}

func validateModuleDescription(description moduleDescription, systemName, architecture string, requested []string) error {
	if description.SystemName != systemName || description.Architecture != architecture {
		return fmt.Errorf("builder described %s/%s, requested %s/%s", description.SystemName, description.Architecture, systemName, architecture)
	}
	if description.Modules == nil || description.GoModuleFiles == nil || description.BuilderFiles == nil {
		return fmt.Errorf("builder describe output is missing required arrays")
	}
	byName := make(map[string]describedModule, len(description.Modules))
	for _, module := range description.Modules {
		var configuration map[string]json.RawMessage
		configurationErr := json.Unmarshal(module.Configuration, &configuration)
		if module.Name == "" || module.Depends == nil || module.SystemDependencies == nil || len(module.Configuration) == 0 || configurationErr != nil || configuration == nil || module.InputFiles == nil {
			return fmt.Errorf("builder describe output has incomplete module description %q", module.Name)
		}
		if _, exists := byName[module.Name]; exists {
			return fmt.Errorf("builder describe output repeats module %q", module.Name)
		}
		byName[module.Name] = module
	}
	for _, module := range description.Modules {
		for _, dependency := range module.Depends {
			if _, ok := byName[dependency]; !ok {
				return fmt.Errorf("module %s is missing dependency description %s", module.Name, dependency)
			}
		}
	}
	for _, name := range requested {
		if _, ok := byName[name]; !ok {
			return fmt.Errorf("builder describe output is missing requested module %q", name)
		}
	}
	reachable := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if reachable[name] {
			return
		}
		reachable[name] = true
		for _, dependency := range byName[name].Depends {
			visit(dependency)
		}
	}
	for _, name := range requested {
		visit(name)
	}
	if len(reachable) != len(byName) {
		return fmt.Errorf("builder describe output contains modules outside the requested dependency closure")
	}
	for _, paths := range [][]string{description.GoModuleFiles, description.BuilderFiles} {
		if err := validateDescriptionPaths(paths); err != nil {
			return err
		}
	}
	for _, module := range description.Modules {
		if err := validateDescriptionPaths(module.InputFiles); err != nil {
			return err
		}
		if module.AdapterFile != "" && slices.Contains(module.InputFiles, module.AdapterFile) {
			return fmt.Errorf("module %s adapter file must not be included in input_files", module.Name)
		}
		if module.AdapterFile != "" {
			if err := validateDescriptionPaths([]string{module.AdapterFile}); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateDescriptionPaths(paths []string) error {
	seen := map[string]bool{}
	for _, name := range paths {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(name)))
		if name == "" || filepath.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(name, "\\") || seen[name] {
			return fmt.Errorf("invalid builder describe path %q", name)
		}
		seen[name] = true
	}
	if !slices.IsSorted(paths) {
		return fmt.Errorf("builder describe paths are not sorted")
	}
	return nil
}
