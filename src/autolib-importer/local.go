package main

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Resolve existing parents without creating missing cache directories.
func resolveLocalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		info, err := os.Stat(resolved)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("local root is not a directory: %s", resolved)
		}
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if _, statErr := os.Lstat(absolute); statErr == nil {
		return "", err
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	parent := filepath.Dir(absolute)
	if parent == absolute {
		return "", err
	}
	resolved, err = resolveLocalPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(absolute)), nil
}

func packageCandidateExists(root string, id packageIdentity) (bool, error) {
	if err := exactPackageEntry(root, id.Name, true); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	parent := filepath.Join(root, id.Name)
	if err := validatePackageVersionEntries(parent, id.Version); err != nil {
		return false, err
	}
	if err := exactPackageEntry(parent, id.Version, true); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Only importer-owned staging trees may be made writable for removal; links are never followed.
func removePackageStaging(root string) error {
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm() | 0200
		if entry.IsDir() {
			mode |= 0500
		}
		return os.Chmod(path, mode)
	})
	return errors.Join(err, os.RemoveAll(root))
}

func parsePackageDigest(path string, data []byte) (string, error) {
	digest := strings.Trim(string(data), " \t\r\n")
	if len(digest) != 64 || strings.ToLower(digest) != digest {
		return "", fmt.Errorf("%s: expected one lowercase SHA256", path)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("%s: invalid SHA256", path)
	}
	return digest, nil
}

func writePackageLatest(root, name, version string) error {
	path := filepath.Join(root, name, "package.toml")
	if current, err := readPackageLatest(root, name); err == nil && current == version {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".latest-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := fmt.Fprintf(f, "latest = %q\n", version)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	// Windows Rename does not replace an existing file. Cache writers are serialized.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(f.Name(), path)
}

func packageDirectory(root string, id packageIdentity) string {
	return filepath.Join(root, id.Name, id.Version)
}

func readPackageMetadata(directory string) (metadata packageMetadata, err error) {
	id := &metadata.Identity
	var dependencies string
	path := filepath.Join(directory, "autolib-import.cmake")
	defer func() {
		if err != nil {
			err = fmt.Errorf("package metadata %s: expected fixed name/version/os/arch/dependencies header: %w", path, err)
		}
	}()
	if err := exactPackageEntry(directory, "autolib-import.cmake", false); err != nil {
		return metadata, err
	}
	f, err := os.Open(path)
	if err != nil {
		return metadata, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for _, field := range []struct {
		key   string
		value *string
	}{{"AUTOLIB_PACKAGE_NAME", &id.Name}, {"AUTOLIB_PACKAGE_VERSION", &id.Version}, {"AUTOLIB_OS", &id.OS}, {"AUTOLIB_ARCH", &id.Arch}, {"AUTOLIB_DEPENDENCIES", &dependencies}} {
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
	directory = filepath.Clean(directory)
	packagePath := filepath.Dir(directory)
	if filepath.Base(directory) != id.Version || filepath.Base(packagePath) != id.Name {
		return metadata, fmt.Errorf("directory must end in %s/%s", id.Name, id.Version)
	}
	if err := exactPackageEntry(filepath.Dir(packagePath), id.Name, true); err != nil {
		return metadata, err
	}
	if err := validatePackageVersionEntries(packagePath, id.Version); err != nil {
		return metadata, err
	}
	if err := exactPackageEntry(packagePath, id.Version, true); err != nil {
		return metadata, err
	}
	if dependencies != "" {
		metadata.Dependencies = strings.Split(dependencies, ";")
		for _, dependency := range metadata.Dependencies {
			ref, err := parsePackageRef(dependency)
			if err != nil || ref.Version == "" {
				return metadata, fmt.Errorf("dependency %q must be an exact name@version reference: %v", dependency, err)
			}
		}
		slices.Sort(metadata.Dependencies)
		metadata.Dependencies = slices.Compact(metadata.Dependencies)
	}
	return metadata, nil
}
