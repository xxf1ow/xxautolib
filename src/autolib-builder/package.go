package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/klauspost/compress/zstd"
)

type stageOptions struct {
	SourceRoot, InstallRoot, StageRoot, Name, OS, Arch, CMakeCommand string
}

func validatePackageName(value string) error {
	stem := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	reserved := slices.Contains([]string{"CON", "PRN", "AUX", "NUL"}, stem) ||
		(len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9')
	if !targetName.MatchString(value) || strings.HasSuffix(value, ".") || reserved {
		return fmt.Errorf("invalid package name %q", value)
	}
	return nil
}

func validatePackagePlatform(system, arch string) error {
	if (system != "windows" && system != "linux") || (arch != "x86_64" && arch != "aarch64") {
		return fmt.Errorf("unsupported package platform %s/%s", system, arch)
	}
	return nil
}

func packagePlatform(m *moduleDefinition, system, arch string) (string, string) {
	if m.PlatformIndependent {
		return "any", "any"
	}
	return system, arch
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && filepath.IsLocal(rel)
}

func safePackageLink(root, path string) (string, error) {
	link, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(link) || strings.Contains(link, ":") {
		return "", fmt.Errorf("%s: package link must be relative", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !pathWithin(root, resolved) {
		return "", fmt.Errorf("%s: link leaves package root %s", path, root)
	}
	return filepath.ToSlash(link), nil
}

func copyPackageTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
			return os.MkdirAll(target, 0755)
		case info.Mode().IsRegular():
			return copyPackageFile(path, target, info.Mode().Perm())
		case info.Mode()&os.ModeSymlink != 0:
			link, err := safePackageLink(source, path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return fmt.Errorf("%s: unsupported package file type %s", path, info.Mode())
		}
	})
}

func copyPackageFile(source, destination string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

func stagePackage(o stageOptions) (staged string, err error) {
	if err = validatePackageName(o.Name); err != nil {
		return "", err
	}
	if err = validatePackagePlatform(o.OS, o.Arch); err != nil {
		return "", err
	}
	if o.SourceRoot == "" || o.InstallRoot == "" || o.StageRoot == "" || o.CMakeCommand == "" {
		return "", fmt.Errorf("staging requires source, install, stage and CMake paths")
	}
	sourceRoot, err := filepath.Abs(o.SourceRoot)
	if err != nil {
		return "", err
	}
	installRoot, err := filepath.EvalSymlinks(o.InstallRoot)
	if err != nil {
		return "", err
	}
	installRoot, err = filepath.Abs(installRoot)
	if err != nil {
		return "", err
	}
	stageRoot, err := resolvePackagePath(o.StageRoot)
	if err != nil {
		return "", err
	}
	if pathWithin(installRoot, stageRoot) || pathWithin(stageRoot, installRoot) {
		return "", fmt.Errorf("stage %s overlaps installation %s", stageRoot, installRoot)
	}
	system := map[string]string{"windows": "Windows", "linux": "Linux"}[o.OS]
	module, err := loadDefinition(filepath.Join(sourceRoot, "src/dependencies", o.Name, "module.toml"), system, o.Arch)
	if err != nil {
		return "", err
	}
	adapter, err := os.ReadFile(filepath.Join(filepath.Dir(module.Path), "autolib-import.cmake"))
	if err != nil {
		return "", err
	}
	input := filepath.Join(installRoot, o.Name)
	info, err := os.Lstat(input)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("installation must be a directory: %s", input)
	}
	metadata, err := readConsumptionMetadata(filepath.Join(input, "autolib-import.cmake"))
	if err != nil {
		return "", err
	}
	packageOS, packageArch := packagePlatform(module, o.OS, o.Arch)
	dependencies := slices.Clone(module.Depends)
	slices.Sort(dependencies)
	dependencies = slices.Compact(dependencies)
	if metadata.Name != o.Name || metadata.OS != packageOS || metadata.Arch != packageArch || !slices.Equal(metadata.Dependencies, dependencies) {
		return "", fmt.Errorf("%s: installed consumption identity or dependencies do not match module declaration", input)
	}
	if module.PlatformIndependent {
		if err = validateUniversalPackage(module, installRoot, adapter); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(stageRoot, 0755); err != nil {
		return "", err
	}
	staged = filepath.Join(stageRoot, o.Name)
	if err := os.Mkdir(staged, 0755); err != nil {
		return "", err
	}
	owned := staged
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(owned))
		}
	}()
	if err = copyPackageTree(input, staged); err != nil {
		return "", err
	}
	allowed := append([]string{o.Name}, module.Depends...)
	if err = checkProducerReferences(staged, installRoot, allowed); err != nil {
		return "", err
	}
	cmd := exec.Command(o.CMakeCommand, "-DPACKAGE_DIR="+filepath.ToSlash(staged), "-DPRODUCER_PREFIX="+filepath.ToSlash(installRoot), "-P", filepath.Join(sourceRoot, "src/autolib-builder/fixup-metadata.cmake"))
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("fixup %s: %w\n%s", staged, runErr, output)
	}
	return staged, nil
}

func validateUniversalPackage(module *moduleDefinition, installRoot string, adapter []byte) error {
	if module.Kind != "copy" || len(adapter) == 0 {
		return fmt.Errorf("universal package %s requires copy installation and a nonempty adapter", module.Name)
	}
	sourceRoot := filepath.Clean(filepath.Join(filepath.Dir(module.Path), "../../.."))
	want := map[string]string{}
	for _, mapping := range module.Copies {
		if mapping.Destination != "include" && !strings.HasPrefix(filepath.ToSlash(mapping.Destination), "include/") {
			return fmt.Errorf("universal package %s must copy its payload under include/", module.Name)
		}
		source := filepath.Join(sourceRoot, "git", module.Name)
		if mapping.Resource != "" {
			source = filepath.Join(sourceRoot, "git", "_resources", module.Name, mapping.Resource)
		}
		for _, file := range mapping.Files {
			want[filepath.Join(mapping.Destination, filepath.Base(file))] = filepath.Join(source, file)
		}
		for _, directory := range mapping.Directories {
			input := filepath.Join(source, directory)
			if err := filepath.WalkDir(input, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				if !entry.Type().IsRegular() {
					return fmt.Errorf("universal package source must be a regular file: %s", path)
				}
				rel, err := filepath.Rel(input, path)
				if err != nil {
					return err
				}
				want[filepath.Join(mapping.Destination, rel)] = path
				return nil
			}); err != nil {
				return err
			}
		}
	}
	if len(want) == 0 {
		return fmt.Errorf("universal package %s has no source payload", module.Name)
	}
	actual := map[string]bool{}
	root := filepath.Join(installRoot, module.Name)
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("universal package file must be regular: %s", path)
		}
		if rel == "autolib-import.cmake" {
			return nil
		}
		actual[rel] = true
		return nil
	}); err != nil {
		return err
	}
	if len(actual) != len(want) {
		return fmt.Errorf("universal package %s installation differs from its copy mappings", module.Name)
	}
	for path, source := range want {
		if !actual[path] {
			return fmt.Errorf("universal package %s installation is missing %s", module.Name, path)
		}
		installed := filepath.Join(root, filepath.FromSlash(path))
		sourceBytes, err := os.ReadFile(source)
		if err != nil {
			return fmt.Errorf("universal package source %s: %w", source, err)
		}
		installedBytes, err := os.ReadFile(installed)
		if err != nil {
			return err
		}
		if !bytes.Equal(sourceBytes, installedBytes) {
			return fmt.Errorf("universal package %s file differs from its source: %s", module.Name, path)
		}
	}
	return nil
}

func checkProducerReferences(root, prefix string, allowed []string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
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
		text := strings.ReplaceAll(string(data), `\`, "/")
		prefix := filepath.ToSlash(prefix)
		for rest := text; ; {
			i := strings.Index(rest, prefix)
			if i < 0 {
				return nil
			}
			rest = rest[i+len(prefix):]
			if !strings.HasPrefix(rest, "/") {
				return fmt.Errorf("%s: unhandled producer path at %s%s", path, prefix, rest)
			}
			component := strings.FieldsFunc(rest[1:], func(r rune) bool { return strings.ContainsRune("/\"' \t\r\n;)", r) })
			if len(component) == 0 || !slices.Contains(allowed, component[0]) {
				return fmt.Errorf("%s: producer reference must name this package or a declared sibling", path)
			}
		}
	})
}

func writePackageArchive(packageDir, destination string) (err error) {
	packageDir, err = filepath.Abs(packageDir)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	if pathWithin(packageDir, destination) {
		return fmt.Errorf("archive output is inside package: %s", destination)
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".archive-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	z, err := zstd.NewWriter(file, zstd.WithEncoderCRC(true))
	if err != nil {
		file.Close()
		return err
	}
	tw := tar.NewWriter(z)
	err = filepath.WalkDir(packageDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = safePackageLink(packageDir, path)
			if err != nil {
				return err
			}
		} else if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("%s: unsupported archive file type", path)
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Dir(packageDir), path)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			input, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, input)
			return errors.Join(err, input.Close())
		}
		return nil
	})
	err = errors.Join(err, tw.Close(), z.Close(), file.Close())
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), destination)
}
