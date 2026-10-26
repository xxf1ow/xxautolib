package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var sourceScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

func normalizeLocalSource(uri string) (string, error) {
	if uri == "" {
		return "", fmt.Errorf("local source must not be empty")
	}
	if scheme := sourceScheme.FindString(uri); len(scheme) > 2 {
		return "", fmt.Errorf("only local source directories are supported: %q", uri)
	}
	absolute, err := filepath.Abs(uri)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Clean(absolute)), nil
}

func packageDirectory(localRoot string, id packageIdentity) string {
	return filepath.Join(localRoot, id.Name)
}

func selectLocalArchive(localRoot string, target packageIdentity) (archivePath string, archiveIdentity packageIdentity, err error) {
	for _, id := range []packageIdentity{target, {Name: target.Name, OS: "any", Arch: "any"}} {
		candidate := filepath.Join(localRoot, id.Name+"-"+id.OS+"-"+id.Arch+".tar.zst")
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", packageIdentity{}, err
		}
		if !info.Mode().IsRegular() {
			return "", packageIdentity{}, fmt.Errorf("package archive must be a regular file: %s", candidate)
		}
		if archivePath != "" {
			return "", packageIdentity{}, fmt.Errorf("ambiguous package archives: %s and %s", archivePath, candidate)
		}
		archivePath, archiveIdentity = candidate, id
	}
	return archivePath, archiveIdentity, nil
}

func readPackageMetadata(directory string) (metadata packageMetadata, err error) {
	id := &metadata.Identity
	var dependencies string
	path := filepath.Join(directory, "autolib-import.cmake")
	defer func() {
		if err != nil {
			err = fmt.Errorf("package metadata %s: expected fixed name/os/arch/dependencies header: %w", path, err)
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return metadata, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for _, field := range []struct {
		key   string
		value *string
	}{{"AUTOLIB_PACKAGE_NAME", &id.Name}, {"AUTOLIB_OS", &id.OS}, {"AUTOLIB_ARCH", &id.Arch}, {"AUTOLIB_DEPENDENCIES", &dependencies}} {
		if !scanner.Scan() {
			return metadata, fmt.Errorf("missing %s: %v", field.key, scanner.Err())
		}
		prefix := `set(` + field.key + ` "`
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, `")`) {
			return metadata, fmt.Errorf("invalid %s line", field.key)
		}
		*field.value = strings.TrimSuffix(strings.TrimPrefix(line, prefix), `")`)
	}
	if err := validateIdentity(*id); err != nil {
		return metadata, err
	}
	if dependencies != "" {
		metadata.Dependencies = strings.Split(dependencies, ";")
		for _, dependency := range metadata.Dependencies {
			if err := validatePackageName(dependency); err != nil {
				return metadata, err
			}
		}
		slices.Sort(metadata.Dependencies)
		metadata.Dependencies = slices.Compact(metadata.Dependencies)
	}
	return metadata, nil
}
