package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type prepareOptions struct {
	RemoteRoot, CacheRoot, OS, Arch, ResultPath, MetadataRoot string
	Packages                                                  []string
}

var errMetadataRequired = errors.New("metadata snapshot required")

// Writes to one cache root must be serialized by the caller. Completed directories are authoritative.
func prepare(o prepareOptions) (packages []preparedPackage, err error) {
	if o.ResultPath != "" {
		if err := os.Remove(o.ResultPath); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	defer func() {
		if err != nil && o.ResultPath != "" {
			_ = os.Remove(o.ResultPath)
		}
	}()
	if err := validatePlatform(o.OS, o.Arch); err != nil {
		return nil, err
	}
	if o.CacheRoot == "" || o.ResultPath == "" || len(o.Packages) == 0 {
		return nil, fmt.Errorf("prepare requires cache-root, result and at least one package")
	}
	cache, err := resolveLocalPath(o.CacheRoot)
	if err != nil {
		return nil, err
	}
	cache = filepath.ToSlash(cache)
	if strings.Contains(cache, ";") {
		return nil, fmt.Errorf("cache-root must not contain semicolons")
	}
	requests := make([]packageRef, len(o.Packages))
	for i, value := range o.Packages {
		requests[i], err = parsePackageRef(value)
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(cache, 0755); err != nil {
		return nil, err
	}
	selected := map[string]packageRef{}
	chains := map[string]string{}
	state := map[string]uint8{}
	latest := map[string]string{}
	var remote metadataSource
	requireMetadata := func() error {
		if o.RemoteRoot == "" {
			return fmt.Errorf("missing package input: remote source is disabled")
		}
		if o.MetadataRoot == "" {
			return errMetadataRequired
		}
		info, err := os.Stat(o.MetadataRoot)
		if err != nil {
			return fmt.Errorf("metadata snapshot %s: %w", o.MetadataRoot, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("metadata snapshot is not a directory: %s", o.MetadataRoot)
		}
		remote, err = parseMetadataSource(o.RemoteRoot)
		return err
	}
	var visit func(packageRef, []string, bool) error
	visit = func(ref packageRef, chain []string, root bool) (visitErr error) {
		bare := ref.Version == ""
		if bare {
			version, err := readPackageLatest(cache, ref.Name)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err == nil {
				found, err := packageCandidateExists(cache, packageIdentity{Name: ref.Name, Version: version, OS: o.OS, Arch: o.Arch})
				if err != nil {
					return err
				}
				if found {
					ref.Version = version
				}
			}
			if ref.Version == "" {
				if err := requireMetadata(); err != nil {
					return err
				}
				ref.Version, err = readPackageLatest(o.MetadataRoot, ref.Name)
				if err != nil {
					return err
				}
			}
		}
		chain = append(append([]string{}, chain...), ref.String())
		path := strings.Join(chain, " -> ")
		key := strings.ToLower(ref.Name)
		if previous, ok := selected[key]; ok {
			if previous.Name != ref.Name {
				return fmt.Errorf("package name case collision: %s and %s", previous.Name, ref.Name)
			}
			if previous.Version != ref.Version {
				return fmt.Errorf("package version conflict: %s versus %s", chains[key], path)
			}
			if state[key] == 1 {
				return fmt.Errorf("dependency cycle: %s", path)
			}
		} else {
			selected[key], chains[key] = ref, path
		}
		if bare && root {
			latest[ref.Name] = ref.Version
		}
		if state[key] == 2 {
			return nil
		}
		state[key] = 1
		id := packageIdentity{Name: ref.Name, Version: ref.Version, OS: o.OS, Arch: o.Arch}
		directory := packageDirectory(cache, id)
		candidate := directory
		found, err := packageCandidateExists(cache, id)
		if err != nil {
			return err
		}
		if !found {
			if err := requireMetadata(); err != nil {
				return err
			}
			asset, identity, err := metadataPackageAsset(o.MetadataRoot, remote, id)
			if err != nil {
				return err
			}
			candidate, err = stageRemotePackage(cache, asset, identity)
			if err != nil {
				return err
			}
			defer func() {
				visitErr = errors.Join(visitErr, removePackageStaging(filepath.Dir(filepath.Dir(candidate))))
			}()
		}
		metadata, err := readPackageMetadata(candidate)
		if err != nil {
			return err
		}
		if !matchesPackageIdentity(metadata.Identity, id) {
			return fmt.Errorf("package %s: expected identity %+v, got %+v", directory, id, metadata.Identity)
		}
		for _, dependency := range metadata.Dependencies {
			dep, err := parsePackageRef(dependency)
			if err != nil {
				return err
			}
			if err := visit(dep, chain, false); err != nil {
				return err
			}
		}
		// A missing root stays staged until its dependencies succeed, so a stale latest cannot revive on failure.
		if !found {
			if err := commitPreparedPackage(candidate, directory); err != nil {
				return err
			}
		}
		state[key] = 2
		packages = append(packages, preparedPackage{Name: ref.Name, Version: ref.Version, Directory: directory})
		return nil
	}
	for _, ref := range requests {
		if err := visit(ref, nil, true); err != nil {
			return nil, err
		}
	}
	for _, p := range packages {
		if version, ok := latest[p.Name]; ok {
			if err := writePackageLatest(cache, p.Name, version); err != nil {
				return nil, err
			}
		}
	}
	if err := writePreparedResult(o.ResultPath, packages); err != nil {
		return nil, err
	}
	return packages, nil
}

func stageRemotePackage(cache string, asset releaseAsset, id packageIdentity) (candidate string, err error) {
	work, err := os.MkdirTemp(cache, ".remote-")
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, removePackageStaging(work)) }()
	archive := filepath.Join(work, asset.Name)
	if err := downloadReleaseAsset(context.Background(), nil, asset, archive); err != nil {
		return "", err
	}
	return extractPackage(archive, cache, id)
}

func commitPreparedPackage(staged, directory string) error {
	if err := os.MkdirAll(filepath.Dir(directory), 0755); err != nil {
		return err
	}
	if _, err := os.Lstat(directory); err == nil {
		return fmt.Errorf("package destination already exists: %s", directory)
	} else if !os.IsNotExist(err) {
		return err
	}
	info, err := os.Stat(staged)
	if err != nil {
		return err
	}
	// Moving a directory changes its parent entry, which requires owner write access on Linux.
	if err := os.Chmod(staged, info.Mode().Perm()|0200); err != nil {
		return err
	}
	if err := os.Rename(staged, directory); err != nil {
		return fmt.Errorf("commit package %s: %w", directory, err)
	}
	return os.Chmod(directory, info.Mode().Perm())
}
