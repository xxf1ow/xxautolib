package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type prepareOptions struct {
	LocalRoot, RemoteRoot, WorkRoot, OS, Arch, ResultPath string
	GitHubResponse                                        string
	Packages                                              []string
}

// Writes to one local root must be serialized by the caller. Completed directories are authoritative.
func prepare(o prepareOptions) ([]preparedPackage, error) {
	if err := validatePlatform(o.OS, o.Arch); err != nil {
		return nil, err
	}
	if o.LocalRoot == "" && o.RemoteRoot == "" {
		return nil, fmt.Errorf("prepare requires a local or remote source")
	}
	if o.WorkRoot == "" || o.ResultPath == "" || len(o.Packages) == 0 {
		return nil, fmt.Errorf("prepare requires work-root, result and at least one package")
	}
	local := ""
	if o.LocalRoot != "" {
		var err error
		local, err = normalizeLocalSource(o.LocalRoot)
		if err != nil {
			return nil, err
		}
	}
	work, err := filepath.Abs(o.WorkRoot)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(local+work, ";") {
		return nil, fmt.Errorf("local-root and work-root must not contain semicolons")
	}
	for _, name := range o.Packages {
		if err := validatePackageName(name); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(work, 0755); err != nil {
		return nil, err
	}
	destinationRoot := local
	if local == "" {
		destinationRoot, err = os.MkdirTemp(work, ".prepare-")
		if err != nil {
			return nil, err
		}
	}
	var remote *githubSourceClient
	var snapshot githubSnapshot
	acquire := func(id packageIdentity, directory string) error {
		if o.RemoteRoot == "" {
			return fmt.Errorf("missing local package %s for %s/%s in %s; no remote source configured", id.Name, id.OS, id.Arch, local)
		}
		if remote == nil {
			source, err := parseGitHubSource(o.RemoteRoot)
			if err != nil {
				return err
			}
			remote = &githubSourceClient{Source: source}
			snapshot, err = loadGitHubSnapshot(context.Background(), remote, o.GitHubResponse)
			if err != nil {
				return err
			}
		}
		platformName := id.Name + "-" + id.OS + "-" + id.Arch + ".tar.zst"
		universalName := id.Name + "-any-any.tar.zst"
		asset, err := selectGitHubAsset(snapshot, []string{platformName, universalName})
		if err != nil {
			return err
		}
		identity := id
		if asset.Name == universalName {
			identity.OS, identity.Arch = "any", "any"
		}
		download, err := os.MkdirTemp(work, ".fetch-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(download)
		archive := filepath.Join(download, asset.Name)
		if err := downloadGitHubAsset(context.Background(), remote, asset, archive); err != nil {
			return err
		}
		return prepareMissingPackage(archive, directory, identity)
	}
	packages := []preparedPackage{}
	state := map[string]uint8{}
	spellings := map[string]string{}
	var visit func(string) error
	visit = func(name string) error {
		key := strings.ToLower(name)
		if previous, ok := spellings[key]; ok && previous != name {
			return fmt.Errorf("package name case collision: %s and %s", previous, name)
		}
		spellings[key] = name
		if state[name] == 1 {
			return fmt.Errorf("dependency cycle at package %s", name)
		}
		if state[name] == 2 {
			return nil
		}
		state[name] = 1
		id := packageIdentity{name, o.OS, o.Arch}
		directory := packageDirectory(destinationRoot, id)
		info, err := os.Lstat(directory)
		if os.IsNotExist(err) {
			var archive string
			var identity packageIdentity
			if local != "" {
				archive, identity, err = selectLocalArchive(local, id)
				if err != nil {
					return err
				}
			}
			if archive == "" {
				if err := acquire(id, directory); err != nil {
					return err
				}
			} else {
				if err := prepareMissingPackage(archive, directory, identity); err != nil {
					return err
				}
			}
		} else if err != nil {
			return err
		} else if !info.IsDir() {
			return fmt.Errorf("existing package is not a directory: %s", directory)
		}
		metadata, err := readPackageMetadata(directory)
		if err != nil {
			return err
		}
		if !matchesPackageIdentity(metadata.Identity, id) {
			return fmt.Errorf("existing package %s: expected identity %+v, got %+v; preserve or remove this directory before retrying", directory, id, metadata.Identity)
		}
		for _, dependency := range metadata.Dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[name] = 2
		packages = append(packages, preparedPackage{name, directory})
		return nil
	}
	for _, name := range o.Packages {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	if err := writePreparedResult(o.ResultPath, packages); err != nil {
		return nil, err
	}
	return packages, nil
}

func prepareMissingPackage(archivePath, directory string, id packageIdentity) error {
	parent := filepath.Dir(directory)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	staged, err := extractPackage(archivePath, parent, id)
	if err != nil {
		return fmt.Errorf("package %s: %w", archivePath, err)
	}
	defer os.RemoveAll(filepath.Dir(staged))
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
