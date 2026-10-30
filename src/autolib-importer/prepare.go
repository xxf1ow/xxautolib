package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type prepareOptions struct {
	LocalRoot, RemoteRoot, WorkRoot, CacheRoot, CMakeCommand, OS, Arch, ResultPath string
	SourceResponse                                                                 string
	Packages                                                                       []string
}

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
	if o.WorkRoot == "" || o.CacheRoot == "" || o.CMakeCommand == "" || o.ResultPath == "" || len(o.Packages) == 0 {
		return nil, fmt.Errorf("prepare requires work-root, cache-root, cmake-command, result and at least one package")
	}
	cache, err := resolveLocalPath(o.CacheRoot)
	if err != nil {
		return nil, err
	}
	cache = filepath.ToSlash(cache)
	local := ""
	if o.LocalRoot != "" {
		local, err = normalizeLocalSource(o.LocalRoot)
		if err != nil {
			return nil, err
		}
		if local == cache || (runtime.GOOS == "windows" && strings.EqualFold(local, cache)) {
			return nil, fmt.Errorf("local-root and cache-root must be different physical directories: %s", cache)
		}
		localInfo, localErr := os.Stat(local)
		cacheInfo, cacheErr := os.Stat(cache)
		if localErr == nil && cacheErr == nil && os.SameFile(localInfo, cacheInfo) {
			return nil, fmt.Errorf("local-root aliases cache-root: %s", cache)
		}
	}
	if strings.ContainsAny(local+cache+o.WorkRoot, ";") {
		return nil, fmt.Errorf("local-root, cache-root and work-root must not contain semicolons")
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
	var visit func(packageRef, []string, bool) error
	visit = func(ref packageRef, chain []string, root bool) error {
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
			if ref.Version == "" && local != "" {
				version, err := readPackageLatest(local, ref.Name)
				if err != nil && !os.IsNotExist(err) {
					return err
				}
				if err == nil {
					ref.Version = version
				}
			}
			if ref.Version == "" {
				return fmt.Errorf("missing local input for %s: no valid cache latest or local source latest; remote retrieval is not available", ref.Name)
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
			installed := false
			if local != "" {
				installed, err = packageCandidateExists(local, id)
				if err != nil {
					return err
				}
			}
			if installed {
				candidate, err = stageInstalledPackage(local, cache, id, o.CMakeCommand)
			} else {
				archive, identity := "", packageIdentity{}
				if local != "" {
					archive, identity, err = selectLocalArchive(local, id)
					if err != nil {
						return err
					}
				}
				if archive == "" {
					return fmt.Errorf("missing local package %s for %s/%s; remote retrieval is not available", ref, o.OS, o.Arch)
				}
				if err := verifyLocalDigest(archive); err != nil {
					return err
				}
				candidate, err = extractPackage(archive, cache, identity)
			}
			if err != nil {
				return err
			}
			defer os.RemoveAll(filepath.Dir(filepath.Dir(candidate)))
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

func commitPreparedPackage(staged, directory string) error {
	if err := os.MkdirAll(filepath.Dir(directory), 0755); err != nil {
		return err
	}
	if _, err := os.Lstat(directory); err == nil {
		return fmt.Errorf("package destination already exists: %s", directory)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staged, directory); err != nil {
		return fmt.Errorf("commit package %s: %w", directory, err)
	}
	return nil
}
