package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type restoreOptions struct {
	ArchivePath, CacheRoot, Name, Version, OS, Arch, SHA256, ResultPath string
	Size                                                                int64
	Dependencies                                                        []string
}

// Restore never replaces an existing candidate or changes a package's latest selection.
func restoreLocalPackage(o restoreOptions) (restored preparedPackage, err error) {
	id := packageIdentity{o.Name, o.Version, o.OS, o.Arch}
	if err := validateIdentity(id); err != nil {
		return restored, err
	}
	if o.ArchivePath == "" || o.CacheRoot == "" || o.ResultPath == "" || o.Size <= 0 || o.Size >= 2<<30 {
		return restored, fmt.Errorf("restore requires local archive, cache-root, result and size below 2 GiB")
	}
	if len(o.SHA256) != 64 {
		return restored, fmt.Errorf("restore requires archive SHA256")
	}
	if digest, err := parsePackageDigest("restore --sha256", []byte(o.SHA256)); err != nil || digest != o.SHA256 {
		return restored, fmt.Errorf("restore requires lowercase SHA256")
	}
	archive, err := filepath.Abs(o.ArchivePath)
	if err != nil {
		return restored, err
	}
	info, err := os.Lstat(archive)
	if err != nil {
		return restored, err
	}
	if !info.Mode().IsRegular() || info.Size() != o.Size {
		return restored, fmt.Errorf("restore archive size or file type mismatch: %s", archive)
	}
	input, err := os.Open(archive)
	if err != nil {
		return restored, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, input)
	if err := errors.Join(copyErr, input.Close()); err != nil {
		return restored, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != o.SHA256 {
		return restored, fmt.Errorf("restore archive SHA256 mismatch: %s", archive)
	}
	cache, err := resolveLocalPath(o.CacheRoot)
	if err != nil {
		return restored, err
	}
	if strings.Contains(cache, ";") {
		return restored, fmt.Errorf("cache-root must not contain semicolons")
	}
	result, err := filepath.Abs(o.ResultPath)
	if err != nil {
		return restored, err
	}
	if resultInfo, statErr := os.Stat(result); statErr == nil && os.SameFile(info, resultInfo) {
		return restored, fmt.Errorf("restore result would overwrite its source archive")
	}
	destination := packageDirectory(cache, id)
	rel, err := filepath.Rel(destination, result)
	if err != nil {
		return restored, err
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return restored, fmt.Errorf("restore result must be outside the package destination")
	}
	dependencies := slices.Clone(o.Dependencies)
	for _, value := range dependencies {
		ref, err := parsePackageRef(value)
		if err != nil || ref.Version == "" {
			return restored, fmt.Errorf("restore dependency must be exact: %q", value)
		}
	}
	slices.Sort(dependencies)
	dependencies = slices.Compact(dependencies)
	if err := os.MkdirAll(cache, 0755); err != nil {
		return restored, err
	}
	if exists, err := packageCandidateExists(cache, id); err != nil {
		return restored, err
	} else if exists {
		return restored, fmt.Errorf("package destination already exists: %s", destination)
	}
	staged, err := extractPackage(archive, cache, id)
	if err != nil {
		return restored, err
	}
	defer func() { err = errors.Join(err, removePackageStaging(filepath.Dir(filepath.Dir(staged)))) }()
	metadata, err := readPackageMetadata(staged)
	if err != nil {
		return restored, err
	}
	if !slices.Equal(metadata.Dependencies, dependencies) {
		return restored, fmt.Errorf("restored package %s@%s has different exact dependencies", id.Name, id.Version)
	}
	if err := commitPreparedPackage(staged, destination); err != nil {
		return restored, err
	}
	restored = preparedPackage{Name: id.Name, Version: id.Version, Directory: destination}
	if err := writePreparedResult(o.ResultPath, []preparedPackage{restored}); err != nil {
		return restored, err
	}
	return restored, nil
}

func runRestore(args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var o restoreOptions
	for _, field := range []struct {
		key   string
		value *string
	}{{"archive", &o.ArchivePath}, {"cache-root", &o.CacheRoot}, {"name", &o.Name}, {"version", &o.Version}, {"os", &o.OS}, {"arch", &o.Arch}, {"sha256", &o.SHA256}, {"result", &o.ResultPath}} {
		flags.StringVar(field.value, field.key, "", field.key)
	}
	flags.Int64Var(&o.Size, "size", 0, "expected archive size")
	var dependencies packageFlags
	flags.Var(&dependencies, "dependency", "exact dependency name@version (repeatable)")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		return fail(fmt.Errorf("restore: unexpected positional argument %q", flags.Arg(0)))
	}
	o.Dependencies = dependencies
	if _, err := restoreLocalPackage(o); err != nil {
		return fail(err)
	}
	return 0
}
