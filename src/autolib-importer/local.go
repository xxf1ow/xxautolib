package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	absolute, err := resolveLocalPath(uri)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Clean(absolute)), nil
}

// Resolve existing parents without creating missing source or cache directories.
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

func stageInstalledPackage(sourceRoot, cacheRoot string, id packageIdentity, cmake string) (staged string, err error) {
	source := packageDirectory(sourceRoot, id)
	metadata, err := readPackageMetadata(source)
	if err != nil {
		return "", err
	}
	if !matchesPackageIdentity(metadata.Identity, id) {
		return "", fmt.Errorf("installed package %s: expected identity %+v, got %+v", source, id, metadata.Identity)
	}
	work, err := os.MkdirTemp(cacheRoot, ".copy-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, removePackageStaging(work))
		}
	}()
	staged = packageDirectory(work, id)
	if err := copyInstalledPackage(source, staged); err != nil {
		return "", err
	}
	if err := relocatePackage(staged, sourceRoot, work, cmake, metadata); err != nil {
		return "", err
	}
	checked, err := readPackageMetadata(staged)
	if err != nil {
		return "", err
	}
	if checked.Identity != metadata.Identity || !slices.Equal(checked.Dependencies, metadata.Dependencies) {
		return "", fmt.Errorf("relocation changed package identity: %s", staged)
	}
	return staged, nil
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

func copyInstalledPackage(source, destination string) error {
	type directoryMode struct {
		path string
		mode fs.FileMode
	}
	var directories []directoryMode
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			directories = append(directories, directoryMode{target, info.Mode().Perm()})
			return os.MkdirAll(target, 0755)
		case info.Mode().IsRegular():
			input, err := os.Open(path)
			if err != nil {
				return err
			}
			defer input.Close()
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, input)
			return errors.Join(copyErr, output.Chmod(info.Mode().Perm()), output.Close())
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return fmt.Errorf("%s: unsupported package file type %s", path, info.Mode())
		}
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := os.Chmod(directories[i].path, directories[i].mode); err != nil {
			return err
		}
	}
	return nil
}

func verifyLocalDigest(archive string) error {
	path := archive + ".sha256"
	if err := exactPackageEntry(filepath.Dir(path), filepath.Base(path), false); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	digest := strings.TrimSuffix(string(data), "\n")
	if len(digest) != 64 || strings.ToLower(digest) != digest {
		return fmt.Errorf("%s: expected one lowercase SHA256", path)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("%s: invalid SHA256", path)
	}
	input, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer input.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return fmt.Errorf("%s: SHA256 mismatch", archive)
	}
	return nil
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

func packageDirectory(localRoot string, id packageIdentity) string {
	return filepath.Join(localRoot, id.Name, id.Version)
}

func selectLocalArchive(localRoot string, target packageIdentity) (archivePath string, archiveIdentity packageIdentity, err error) {
	if err := validateIdentity(target); err != nil {
		return "", packageIdentity{}, err
	}
	for _, id := range []packageIdentity{target, {Name: target.Name, Version: target.Version, OS: "any", Arch: "any"}} {
		name := id.Name + "-" + id.Version + "-" + id.OS + "-" + id.Arch + ".tar.zst"
		candidate := filepath.Join(localRoot, name)
		err := exactPackageEntry(localRoot, name, false)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", packageIdentity{}, err
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
