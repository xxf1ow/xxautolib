package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

type prepareOptions struct {
	URI, WorkRoot, OS, Arch, ResultPath, ManifestPath string
	Packages                                          []string
}

func prepare(o prepareOptions) ([]preparedPackage, error) {
	if err := validatePlatform(o.OS, o.Arch); err != nil {
		return nil, err
	}
	if o.WorkRoot == "" || o.ResultPath == "" || len(o.Packages) == 0 {
		return nil, fmt.Errorf("prepare requires work-root, result and at least one package")
	}
	source, err := normalizeLocalSource(o.URI)
	if err != nil {
		return nil, err
	}
	work, err := filepath.Abs(o.WorkRoot)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, name := range o.Packages {
		if err := validateIdentity(packageIdentity{Name: name, OS: o.OS, Arch: o.Arch}); err != nil {
			return nil, err
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	packages := make([]preparedPackage, 0, len(names))
	var current manifest
	manifestRead := false
	for _, name := range names {
		id := packageIdentity{Name: name, OS: o.OS, Arch: o.Arch}
		directory := packageDirectory(work, source, id)
		info, statErr := os.Lstat(directory)
		if statErr == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("existing package is not a directory: %s", directory)
			}
			actual, err := readPackageIdentity(directory)
			if err != nil {
				return nil, err
			}
			if !matchesPackageIdentity(actual, id) {
				return nil, fmt.Errorf("existing package %s: expected identity %+v, got %+v; preserve or remove this cache before retrying", directory, id, actual)
			}
		} else {
			if !os.IsNotExist(statErr) {
				return nil, statErr
			}
			if !manifestRead {
				manifestPath := o.ManifestPath
				if manifestPath == "" {
					manifestPath = filepath.Join(source, "manifest.json")
				}
				current, err = readManifest(manifestPath)
				if err != nil {
					return nil, err
				}
				manifestRead = true
			}
			row, err := selectPackage(current, id)
			if err != nil {
				return nil, err
			}
			selectedIdentity := packageIdentity{Name: row.Name, OS: row.OS, Arch: row.Arch}
			if err := prepareMissingPackage(source, directory, row, selectedIdentity); err != nil {
				return nil, err
			}
		}
		packages = append(packages, preparedPackage{Name: name, Directory: directory})
	}
	if err := writePreparedResult(o.ResultPath, packages); err != nil {
		return nil, err
	}
	return packages, nil
}

func prepareMissingPackage(source, directory string, row packageRecord, id packageIdentity) error {
	parent := filepath.Dir(directory)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	archive, err := os.CreateTemp(parent, ".download-")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	asset := filepath.Join(source, row.Asset)
	input, err := os.Open(asset)
	if err != nil {
		archive.Close()
		return fmt.Errorf("local package %s: %w", asset, err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(archive, hash), input)
	if err := errors.Join(copyErr, input.Close(), archive.Close()); err != nil {
		return err
	}
	want, err := hex.DecodeString(row.SHA256)
	if err != nil || !bytes.Equal(want, hash.Sum(nil)) {
		return fmt.Errorf("package %s: SHA256 mismatch, expected %s, got %x", asset, row.SHA256, hash.Sum(nil))
	}
	staged, err := extractPackage(archive.Name(), parent, id)
	if err != nil {
		return fmt.Errorf("package %s: %w", asset, err)
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
