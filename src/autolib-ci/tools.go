package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

func readModuleDescription(ctx context.Context, builderFile, sourceRoot, systemName, architecture string, modules []string) (moduleDescription, error) {
	if strings.TrimSpace(builderFile) == "" {
		return moduleDescription{}, fmt.Errorf("builder file cannot be empty")
	}
	if strings.TrimSpace(sourceRoot) == "" {
		return moduleDescription{}, fmt.Errorf("source root is required")
	}
	args := []string{"describe", "--source-root", sourceRoot, "--system-name", systemName, "--architecture", architecture}
	for _, module := range modules {
		args = append(args, "--module", module)
	}
	if len(modules) == 0 {
		args = append(args, "--discover")
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
	if description.Roots == nil || description.RequestedRoots == nil || description.FilteredRoots == nil || description.Modules == nil || description.GoModuleFiles == nil || description.BuilderFiles == nil {
		return fmt.Errorf("builder describe output is missing required arrays")
	}
	byName := make(map[string]describedModule, len(description.Modules))
	for _, module := range description.Modules {
		var configuration map[string]json.RawMessage
		configurationErr := json.Unmarshal(module.Configuration, &configuration)
		if module.Name == "" || module.Version == "" || module.Target == "" || module.Depends == nil || module.SystemDependencies == nil || len(module.Configuration) == 0 || configurationErr != nil || configuration == nil || module.InputFiles == nil {
			return fmt.Errorf("builder describe output has incomplete module description %q", module.Name)
		}
		identity := moduleIdentity(module)
		if _, exists := byName[identity]; exists {
			return fmt.Errorf("builder describe output repeats module %q", identity)
		}
		byName[identity] = module
	}
	for _, module := range description.Modules {
		for _, dependency := range module.Depends {
			if _, ok := byName[dependency]; !ok {
				return fmt.Errorf("module %s is missing dependency description %s", module.Name, dependency)
			}
		}
	}
	rootSet := map[string]bool{}
	for _, root := range description.Roots {
		if _, ok := byName[root]; !ok || rootSet[root] {
			return fmt.Errorf("builder describe output has invalid root %q", root)
		}
		rootSet[root] = true
	}
	requestedSet := map[string]bool{}
	validReference := func(value string) bool {
		name, version, found := strings.Cut(value, "@")
		return found && ciModuleName.MatchString(name) && ciModuleName.MatchString(version)
	}
	for _, root := range description.RequestedRoots {
		if !validReference(root) || requestedSet[root] {
			return fmt.Errorf("builder describe output has invalid requested root %q", root)
		}
		requestedSet[root] = true
	}
	for root := range rootSet {
		if !requestedSet[root] {
			return fmt.Errorf("effective root %s was not requested", root)
		}
	}
	partition := maps.Clone(rootSet)
	for _, reason := range description.FilteredRoots {
		if !requestedSet[reason.Root] || partition[reason.Root] || len(reason.Chain) == 0 || reason.Chain[0] != reason.Root ||
			(reason.Code != "unsupported_platform" && reason.Code != "unsupported_dependency_platform") ||
			(reason.Code == "unsupported_platform" && len(reason.Chain) != 1) || (reason.Code == "unsupported_dependency_platform" && len(reason.Chain) < 2) {
			return fmt.Errorf("builder describe output has invalid platform filter for %q", reason.Root)
		}
		seen := map[string]bool{}
		for _, ref := range reason.Chain {
			if !validReference(ref) || seen[ref] {
				return fmt.Errorf("invalid filter dependency chain for %s", reason.Root)
			}
			seen[ref] = true
		}
		partition[reason.Root] = true
	}
	if len(partition) != len(requestedSet) {
		return fmt.Errorf("builder describe output omits a requested root result")
	}
	for _, request := range requested {
		found := false
		for _, root := range description.RequestedRoots {
			if request == root || request == strings.SplitN(root, "@", 2)[0] {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("builder describe output is missing requested module %q", request)
		}
	}
	if len(requested) > 0 {
		for root := range requestedSet {
			if !slices.Contains(requested, root) && !slices.Contains(requested, strings.SplitN(root, "@", 2)[0]) {
				return fmt.Errorf("builder described unrequested root %s", root)
			}
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
	for _, name := range description.Roots {
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
		if name == "" || path.IsAbs(name) || filepath.IsAbs(name) || path.Clean(name) != name || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(name, "\\") || seen[name] {
			return fmt.Errorf("invalid builder describe path %q", name)
		}
		seen[name] = true
	}
	if !slices.IsSorted(paths) {
		return fmt.Errorf("builder describe paths are not sorted")
	}
	return nil
}
