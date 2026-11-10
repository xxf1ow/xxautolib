package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type sourceCatalog struct {
	Packages map[string]sourcePackage `json:"packages"`
}
type sourcePackage struct {
	Latest   string                   `json:"latest"`
	Error    string                   `json:"error"`
	Versions map[string]sourceVersion `json:"versions"`
}
type sourceVersion struct {
	Platforms []string `json:"platforms"`
	Error     string   `json:"error"`
}

func catalogBoundary(root, path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%w: %s", errDescriptionBoundary, path)
	}
	return nil
}

func describeCatalog(sourceRoot string) (sourceCatalog, error) {
	result := sourceCatalog{Packages: map[string]sourcePackage{}}
	if strings.TrimSpace(sourceRoot) == "" {
		return result, fmt.Errorf("catalog: source root is required")
	}
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return result, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return result, err
	}
	directory := filepath.Join(root, "src/dependencies")
	if err := catalogBoundary(root, directory); err != nil {
		return result, err
	}
	packages, err := os.ReadDir(directory)
	if err != nil {
		return result, err
	}
	if err := validateCatalogCase(packages, directory); err != nil {
		return result, err
	}
	for _, entry := range packages {
		path := filepath.Join(directory, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			return result, fmt.Errorf("catalog requires ordinary package directories: %s", path)
		}
		if !entry.IsDir() {
			continue
		}
		p := sourcePackage{}
		if err := validatePackageName(entry.Name()); err != nil {
			p.Error = err.Error()
			result.Packages[entry.Name()] = p
			continue
		}

		entries, listErr := os.ReadDir(path)
		if listErr == nil {
			for _, child := range entries {
				if child.Type()&os.ModeSymlink != 0 {
					return result, fmt.Errorf("catalog requires ordinary package entries: %s", filepath.Join(path, child.Name()))
				}
			}
		}
		versions, err := versionDirectories(path)
		if err != nil {
			p.Error = err.Error()
			result.Packages[entry.Name()] = p
			continue
		}
		p.Versions = map[string]sourceVersion{}
		if err := catalogBoundary(root, filepath.Join(path, "package.toml")); err != nil {
			return result, err
		}
		p.Latest, err = packageLatest(path)
		if err != nil {
			p.Error = err.Error()
		}
		for _, version := range versions {
			recipe := filepath.Join(path, version.Name(), "module.toml")
			if err := catalogBoundary(root, recipe); err != nil {
				return result, err
			}
			if _, err := os.Lstat(recipe); os.IsNotExist(err) {
				continue
			}
			v := sourceVersion{}
			definition, err := readDefinition(recipe, "Linux", "x86_64", false)
			if err != nil {
				v.Error = err.Error()
			} else {
				v.Platforms = definition.PackagePlatforms
			}
			p.Versions[version.Name()] = v
		}
		result.Packages[entry.Name()] = p
	}
	return result, nil
}

func runCatalog(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("catalog", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("source-root", "", "repository source root")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "catalog: unexpected positional arguments")
		return 1
	}
	catalog, err := describeCatalog(*root)
	if err == nil {
		err = json.NewEncoder(stdout).Encode(catalog)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
