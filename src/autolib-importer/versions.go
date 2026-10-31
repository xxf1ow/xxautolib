package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type packageRef struct{ Name, Version string }

func (r packageRef) String() string {
	if r.Version == "" {
		return r.Name
	}
	return r.Name + "@" + r.Version
}

func parsePackageRef(value string) (packageRef, error) {
	parts := strings.Split(value, "@")
	if len(parts) > 2 {
		return packageRef{}, fmt.Errorf("invalid package reference %q: expected name or name@version", value)
	}
	if err := validatePackageName(parts[0]); err != nil {
		return packageRef{}, err
	}
	ref := packageRef{Name: parts[0]}
	if len(parts) == 2 {
		if err := validatePackageVersion(parts[1]); err != nil {
			return packageRef{}, err
		}
		ref.Version = parts[1]
	}
	return ref, nil
}

func validatePackageVersion(value string) error {
	if validatePackageName(value) != nil || strings.EqualFold(value, "package.toml") {
		return fmt.Errorf("invalid package version %q", value)
	}
	return nil
}

// Check directory entries explicitly so case-insensitive hosts do not accept aliases.
func exactPackageEntry(directory, name string, wantDirectory bool) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	found := false
	for _, entry := range entries {
		if entry.Name() != name {
			if strings.EqualFold(entry.Name(), name) {
				return fmt.Errorf("%s: case alias %q does not match exact entry %q", directory, entry.Name(), name)
			}
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if (wantDirectory && !info.IsDir()) || (!wantDirectory && !info.Mode().IsRegular()) {
			return fmt.Errorf("%s: unexpected file type", filepath.Join(directory, name))
		}
		found = true
	}
	if !found {
		return &os.PathError{Op: "find exact entry", Path: filepath.Join(directory, name), Err: os.ErrNotExist}
	}
	return nil
}

func validatePackageVersionEntries(directory, requested string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	seen := map[string]string{}
	for _, entry := range entries {
		if entry.Name() == "package.toml" {
			continue
		}
		key := strings.ToLower(entry.Name())
		if previous, ok := seen[key]; ok {
			return fmt.Errorf("%s: case-colliding version entries %q and %q", directory, previous, entry.Name())
		}
		seen[key] = entry.Name()
		if requested != entry.Name() && strings.EqualFold(requested, entry.Name()) {
			return fmt.Errorf("%s: case alias %q does not match exact version %q", directory, entry.Name(), requested)
		}
	}
	return nil
}

func readPackageLatest(root, name string) (string, error) {
	if err := validatePackageName(name); err != nil {
		return "", err
	}
	if err := exactPackageEntry(root, name, true); err != nil {
		return "", err
	}
	directory := filepath.Join(root, name)
	if err := exactPackageEntry(directory, "package.toml", false); err != nil {
		return "", err
	}
	path := filepath.Join(directory, "package.toml")
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var raw map[string]any
	if err := toml.Unmarshal(content, &raw); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	latest, ok := raw["latest"].(string)
	if !ok || len(raw) != 1 {
		return "", fmt.Errorf("%s: expected only latest as a version string", path)
	}
	if err := validatePackageVersion(latest); err != nil {
		return "", fmt.Errorf("%s.latest: %w", path, err)
	}
	if err := validatePackageVersionEntries(directory, latest); err != nil {
		return "", err
	}
	return latest, nil
}
