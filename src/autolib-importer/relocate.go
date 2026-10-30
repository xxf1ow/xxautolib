package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"xxautolib/src/internal/packagemetadata"
)

//go:embed fixup-metadata.cmake
var fixupMetadataScript []byte

// Only the copied version directory is writable; dependency identities come from its installed metadata.
func relocatePackage(packageDir, producerRoot, destinationRoot, cmakeCommand string, metadata packageMetadata) (err error) {
	if err := validateIdentity(metadata.Identity); err != nil {
		return err
	}
	if packageDir == "" || producerRoot == "" || destinationRoot == "" || cmakeCommand == "" {
		return fmt.Errorf("relocation requires package, producer, destination and CMake paths")
	}
	allowed := []string{metadata.Identity.Name + "/" + metadata.Identity.Version}
	for _, dependency := range metadata.Dependencies {
		ref, err := parsePackageRef(dependency)
		if err != nil || ref.Version == "" {
			return fmt.Errorf("dependency %q must be an exact name@version reference", dependency)
		}
		allowed = append(allowed, ref.Name+"/"+ref.Version)
	}
	if err := checkProducerReferences(packageDir, producerRoot, allowed); err != nil {
		return err
	}
	script, err := os.CreateTemp("", ".autolib-fixup-*.cmake")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.Remove(script.Name())) }()
	_, writeErr := script.Write(fixupMetadataScript)
	if err := errors.Join(writeErr, script.Close()); err != nil {
		return err
	}
	command := exec.Command(cmakeCommand, "-DPACKAGE_DIR="+filepath.ToSlash(packageDir), "-DPRODUCER_PREFIX="+filepath.ToSlash(producerRoot), "-DDESTINATION_ROOT="+filepath.ToSlash(destinationRoot), "-P", script.Name())
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("fixup %s: %w\n%s", packageDir, err, output)
	}
	return nil
}

func checkProducerReferences(root, prefix string, allowed []string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) || strings.Contains(link, ":") {
				return fmt.Errorf("%s: package link must be relative", path)
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, resolved)
			if err != nil || !filepath.IsLocal(rel) {
				return fmt.Errorf("%s: link leaves package root %s", path, root)
			}
		}
		if entry.IsDir() || (filepath.Ext(path) != ".cmake" && filepath.Ext(path) != ".pc") {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("metadata must be a regular file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := packagemetadata.CheckProducerReferences(string(data), prefix, allowed); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	})
}
