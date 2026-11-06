package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type moduleRef struct {
	Name, Version string
}

func moduleTarget(ref moduleRef) string {
	return fmt.Sprintf("_al_pkg_%x_%x", ref.Name, ref.Version)
}

func (r moduleRef) String() string {
	if r.Version == "" {
		return r.Name
	}
	return r.Name + "@" + r.Version
}

func parseModuleRef(value string) (moduleRef, error) {
	parts := strings.Split(value, "@")
	if len(parts) > 2 {
		return moduleRef{}, fmt.Errorf("invalid module reference %q: expected name or name@version", value)
	}
	packageName, err := name(parts[0], "module reference")
	if err != nil {
		return moduleRef{}, err
	}
	ref := moduleRef{Name: packageName}
	if len(parts) == 2 {
		if err := validatePackageVersion(parts[1]); err != nil {
			return moduleRef{}, err
		}
		ref.Version = parts[1]
	}
	return ref, nil
}

func validatePackageVersion(value string) error {
	if !targetName.MatchString(value) || strings.HasSuffix(value, ".") {
		return fmt.Errorf("version: invalid package version %q", value)
	}
	stem, _, _ := strings.Cut(strings.ToUpper(value), ".")
	device := slices.Contains([]string{"CON", "PRN", "AUX", "NUL"}, stem)
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
		device = true
	}
	if device || strings.EqualFold(value, "package.toml") {
		return fmt.Errorf("version: reserved package version %q", value)
	}
	return nil
}

func exactEntry(directory, value string, wantDirectory bool) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != value {
			continue
		}
		if wantDirectory && entry.IsDir() {
			return nil
		}
		if !wantDirectory {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				return nil
			}
		}
		return fmt.Errorf("%s: unexpected file type", filepath.Join(directory, value))
	}
	return fmt.Errorf("%s: exact path entry not found", filepath.Join(directory, value))
}

func versionDirectories(directory string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	var versions []os.DirEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		key := strings.ToLower(entry.Name())
		if previous, ok := seen[key]; ok {
			return nil, fmt.Errorf("%s: case-colliding version directories %q and %q", directory, previous, entry.Name())
		}
		seen[key] = entry.Name()
		versions = append(versions, entry)
	}
	return versions, nil
}

func validateArtifactVersionPath(path string) error {
	directory, requested := filepath.Dir(path), filepath.Base(path)
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	seen := map[string]string{}
	for _, entry := range entries {
		key := strings.ToLower(entry.Name())
		if previous, ok := seen[key]; ok {
			return fmt.Errorf("%s: case-colliding version entries %q and %q; preserve them and resolve the collision manually", directory, previous, entry.Name())
		}
		seen[key] = entry.Name()
		if entry.Name() != requested && strings.EqualFold(entry.Name(), requested) {
			return fmt.Errorf("%s: case alias %q does not match exact version entry %q; preserve it and resolve the spelling manually", directory, entry.Name(), requested)
		}
	}
	return nil
}

func validateSourceVersionPaths(module *moduleDefinition, sourceRoot string) error {
	if module.Source != nil && module.Source.GitRepository != "" {
		if err := validateArtifactVersionPath(filepath.Join(sourceRoot, "git", module.Name, module.Version)); err != nil {
			return err
		}
	}
	for _, resource := range module.Resources {
		if resource.GitRepository != "" {
			return validateArtifactVersionPath(filepath.Join(sourceRoot, "git", "_resources", module.Name, module.Version))
		}
	}
	return nil
}

func discoverModuleRefs(sourceRoot string) ([]moduleRef, error) {
	dependencies := filepath.Join(sourceRoot, "src", "dependencies")
	packages, err := os.ReadDir(dependencies)
	if err != nil {
		return nil, err
	}
	refs := []moduleRef{}
	for _, entry := range packages {
		if !entry.IsDir() {
			continue
		}
		directory := filepath.Join(dependencies, entry.Name())
		versions, err := versionDirectories(directory)
		if err != nil {
			return nil, err
		}
		for _, version := range versions {
			recipeDirectory := filepath.Join(directory, version.Name())
			files, err := os.ReadDir(recipeDirectory)
			if err != nil {
				return nil, err
			}
			for _, file := range files {
				if file.Name() != "module.toml" {
					continue
				}
				ref, err := parseModuleRef(entry.Name() + "@" + version.Name())
				if err != nil {
					return nil, fmt.Errorf("%s: %w", recipeDirectory, err)
				}
				if err := exactEntry(recipeDirectory, "module.toml", false); err != nil {
					return nil, err
				}
				refs = append(refs, ref)
			}
		}
	}
	slices.SortFunc(refs, func(a, b moduleRef) int {
		if order := strings.Compare(a.Name, b.Name); order != 0 {
			return order
		}
		return strings.Compare(a.Version, b.Version)
	})
	return refs, nil
}

func resolveModuleRef(sourceRoot string, request moduleRef) (moduleRef, error) {
	if _, err := name(request.Name, "module reference"); err != nil {
		return moduleRef{}, err
	}
	if request.Version != "" {
		if err := validatePackageVersion(request.Version); err != nil {
			return moduleRef{}, err
		}
	}
	dependencies := filepath.Join(sourceRoot, "src", "dependencies")
	if err := exactEntry(dependencies, request.Name, true); err != nil {
		return moduleRef{}, err
	}
	directory := filepath.Join(dependencies, request.Name)
	if _, err := versionDirectories(directory); err != nil {
		return moduleRef{}, err
	}
	latest, err := packageLatest(directory)
	if err != nil {
		return moduleRef{}, err
	}
	if request.Version == "" {
		request.Version = latest
	}
	if err := exactEntry(directory, request.Version, true); err != nil {
		return moduleRef{}, err
	}
	if err := exactEntry(filepath.Join(directory, request.Version), "module.toml", false); err != nil {
		return moduleRef{}, err
	}
	return request, nil
}

func packageLatest(directory string) (string, error) {
	path := filepath.Join(directory, "package.toml")
	if err := exactEntry(directory, "package.toml", false); err != nil {
		return "", err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var raw map[string]any
	if err := toml.Unmarshal(content, &raw); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	metadata, err := table(raw, path, "latest")
	if err != nil {
		return "", err
	}
	latest, err := text(metadata["latest"], path+".latest")
	if err != nil {
		return "", err
	}
	if err := validatePackageVersion(latest); err != nil {
		return "", err
	}
	if err := exactEntry(directory, latest, true); err != nil {
		return "", err
	}
	if err := exactEntry(filepath.Join(directory, latest), "module.toml", false); err != nil {
		return "", err
	}
	return latest, nil
}
