package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const incompleteConsumption = "# AutoLib installation incomplete\n"

type consumptionOptions struct {
	SourceRoot, InstallRoot, Name, OS, Arch, Phase string
}

type consumptionMetadata struct {
	Name, OS, Arch string
	Dependencies   []string
}

func readConsumptionMetadata(path string) (metadata consumptionMetadata, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("consumption file %s: %w", path, err)
		}
	}()
	info, err := os.Lstat(path)
	if err != nil {
		return metadata, err
	}
	if !info.Mode().IsRegular() {
		return metadata, fmt.Errorf("expected a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return metadata, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var dependencies string
	for _, field := range []struct {
		key   string
		value *string
	}{{"AUTOLIB_PACKAGE_NAME", &metadata.Name}, {"AUTOLIB_OS", &metadata.OS}, {"AUTOLIB_ARCH", &metadata.Arch}, {"AUTOLIB_DEPENDENCIES", &dependencies}} {
		if !scanner.Scan() {
			return metadata, fmt.Errorf("missing %s: %v", field.key, scanner.Err())
		}
		prefix := `set(` + field.key + ` "`
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, `")`) {
			return metadata, fmt.Errorf("invalid %s; incomplete or invalid installation, prepare a successful source installation", field.key)
		}
		*field.value = strings.TrimSuffix(strings.TrimPrefix(line, prefix), `")`)
	}
	if err := validatePackageName(metadata.Name); err != nil {
		return metadata, err
	}
	if metadata.OS != "any" || metadata.Arch != "any" {
		if err := validatePackagePlatform(metadata.OS, metadata.Arch); err != nil {
			return metadata, err
		}
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

func prepareConsumption(o consumptionOptions) error {
	if o.SourceRoot == "" || o.InstallRoot == "" || !slices.Contains([]string{"begin", "installed", "reuse"}, o.Phase) {
		return fmt.Errorf("prepare-consumption requires source-root, install-root and phase begin, installed or reuse")
	}
	if err := validatePackageName(o.Name); err != nil {
		return err
	}
	if err := validatePackagePlatform(o.OS, o.Arch); err != nil {
		return err
	}
	system := map[string]string{"windows": "Windows", "linux": "Linux"}[o.OS]
	module, err := loadDefinition(filepath.Join(o.SourceRoot, "src/dependencies", o.Name, "module.toml"), system, o.Arch)
	if err != nil {
		return err
	}
	adapterPath := filepath.Join(filepath.Dir(module.Path), "autolib-import.cmake")
	adapter, err := os.ReadFile(adapterPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", adapterPath, err)
	}
	packageOS, packageArch := packagePlatform(module, o.OS, o.Arch)
	output := filepath.Join(o.InstallRoot, o.Name, "autolib-import.cmake")
	old, err := os.ReadFile(output)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %w", output, err)
	}
	if err == nil && !(o.Phase != "reuse" && bytes.Equal(old, []byte(incompleteConsumption))) {
		metadata, err := readConsumptionMetadata(output)
		if err != nil {
			return err
		}
		if metadata.Name != o.Name || metadata.OS != packageOS || metadata.Arch != packageArch {
			return fmt.Errorf("%s: existing package identity %s/%s/%s does not match %s/%s/%s", output, metadata.Name, metadata.OS, metadata.Arch, o.Name, packageOS, packageArch)
		}
	}
	if o.Phase == "begin" {
		_, err := writeIfChanged(output, incompleteConsumption)
		return err
	}
	info, err := os.Lstat(filepath.Dir(output))
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("installation must be a directory: %s", filepath.Dir(output))
	}
	dependencies := slices.Clone(module.Depends)
	slices.Sort(dependencies)
	dependencies = slices.Compact(dependencies)
	for _, dependency := range dependencies {
		if err := validatePackageName(dependency); err != nil {
			return err
		}
		adapter := filepath.Join(o.SourceRoot, "src/dependencies", dependency, "autolib-import.cmake")
		if _, err := os.Stat(adapter); err != nil {
			return fmt.Errorf("package %s: dependency %s has no readable adapter %s: %w", o.Name, dependency, adapter, err)
		}
		metadata, err := readConsumptionMetadata(filepath.Join(o.InstallRoot, dependency, "autolib-import.cmake"))
		if err != nil {
			return fmt.Errorf("package %s: dependency %s: %w", o.Name, dependency, err)
		}
		if metadata.Name != dependency || !((metadata.OS == o.OS && metadata.Arch == o.Arch) || (metadata.OS == "any" && metadata.Arch == "any")) {
			return fmt.Errorf("package %s: dependency %s identity does not match %s/%s", o.Name, dependency, o.OS, o.Arch)
		}
	}
	if module.PlatformIndependent {
		if err := validateUniversalPackage(module, o.InstallRoot, adapter); err != nil {
			return err
		}
	}
	header := fmt.Sprintf("set(AUTOLIB_PACKAGE_NAME %q)\nset(AUTOLIB_OS %q)\nset(AUTOLIB_ARCH %q)\nset(AUTOLIB_DEPENDENCIES %q)\n", o.Name, packageOS, packageArch, strings.Join(dependencies, ";"))
	_, err = writeIfChanged(output, header+string(adapter))
	return err
}

func runConsumption(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("prepare-consumption", flag.ContinueOnError)
	flags.SetOutput(stderr)
	o := consumptionOptions{}
	for _, field := range []struct {
		name  string
		value *string
	}{{"source-root", &o.SourceRoot}, {"install-root", &o.InstallRoot}, {"name", &o.Name}, {"os", &o.OS}, {"arch", &o.Arch}, {"phase", &o.Phase}} {
		flags.StringVar(field.value, field.name, "", field.name)
	}
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "prepare-consumption does not accept positional arguments")
		return 1
	}
	if err := prepareConsumption(o); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
