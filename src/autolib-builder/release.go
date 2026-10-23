package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

type packageOptions struct {
	SourceRoot, InstallRoot, OutputRoot, OS, Arch, HostOS, HostArch, ImporterFile, CMakeCommand string
	Modules                                                                                     []string
}

type releaseImporter struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Asset  string `json:"asset"`
	SHA256 string `json:"sha256"`
}

type releasePackage struct {
	Name   string `json:"name"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Asset  string `json:"asset"`
	SHA256 string `json:"sha256"`
}

type releaseManifest struct {
	Importers []releaseImporter `json:"importers"`
	Packages  []releasePackage  `json:"packages"`
}

func packageRelease(o packageOptions) error {
	if err := validatePackagePlatform(o.OS, o.Arch); err != nil {
		return err
	}
	if err := validatePackagePlatform(o.HostOS, o.HostArch); err != nil {
		return err
	}
	if o.SourceRoot == "" || o.InstallRoot == "" || o.OutputRoot == "" || o.ImporterFile == "" || o.CMakeCommand == "" || len(o.Modules) == 0 {
		return fmt.Errorf("package requires all paths and at least one module")
	}
	source, err := resolvePackagePath(o.SourceRoot)
	if err != nil {
		return err
	}
	output, err := resolvePackagePath(o.OutputRoot)
	if err != nil {
		return err
	}
	install, err := resolvePackagePath(o.InstallRoot)
	if err != nil {
		return err
	}
	if !pathWithin(filepath.Join(source, "build"), output) {
		return fmt.Errorf("release output must be inside %s/build: %s", source, output)
	}
	if pathWithin(install, output) || pathWithin(output, install) {
		return fmt.Errorf("release output overlaps installation: %s", output)
	}
	toolInfo, err := os.Stat(o.ImporterFile)
	if err != nil {
		return err
	}
	if !toolInfo.Mode().IsRegular() {
		return fmt.Errorf("importer must be a regular file: %s", o.ImporterFile)
	}
	names := []string{}
	for _, n := range o.Modules {
		if _, err := name(n, "package name"); err != nil {
			return err
		}
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	if err := os.MkdirAll(filepath.Join(source, "build"), 0755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(filepath.Join(source, "build"), "package-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	toolAsset := fmt.Sprintf("autolib-importer-%s-%s", o.HostOS, o.HostArch)
	if o.HostOS == "windows" {
		toolAsset += ".exe"
	}
	if err := copyPackageFile(o.ImporterFile, filepath.Join(work, toolAsset), 0755); err != nil {
		return err
	}
	digest, err := releaseDigest(filepath.Join(work, toolAsset))
	if err != nil {
		return err
	}
	m := releaseManifest{Importers: []releaseImporter{{OS: o.HostOS, Arch: o.HostArch, Asset: toolAsset, SHA256: digest}}, Packages: []releasePackage{}}
	assets := []string{toolAsset}
	for _, n := range names {
		module, err := loadDefinition(filepath.Join(source, "src/dependencies", n, "module.toml"), map[string]string{"windows": "Windows", "linux": "Linux"}[o.OS], o.Arch)
		if err != nil {
			return err
		}
		packageOS, packageArch := packagePlatform(module, o.OS, o.Arch)
		directory, err := stagePackage(stageOptions{SourceRoot: source, InstallRoot: install, StageRoot: filepath.Join(work, "staging"), Name: n, OS: o.OS, Arch: o.Arch, CMakeCommand: o.CMakeCommand})
		if err != nil {
			return err
		}
		asset := fmt.Sprintf("%s-%s-%s.tar.zst", n, packageOS, packageArch)
		if err := writePackageArchive(directory, filepath.Join(work, asset)); err != nil {
			return err
		}
		digest, err := releaseDigest(filepath.Join(work, asset))
		if err != nil {
			return err
		}
		m.Packages = append(m.Packages, releasePackage{Name: n, OS: packageOS, Arch: packageArch, Asset: asset, SHA256: digest})
		assets = append(assets, asset)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "manifest.json"), append(data, '\n'), 0644); err != nil {
		return err
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		return err
	}
	for _, asset := range append(assets, "manifest.json") {
		if err := os.Rename(filepath.Join(work, asset), filepath.Join(output, asset)); err != nil {
			return fmt.Errorf("publish %s: %w", asset, err)
		}
	}
	return nil
}

// Resolve existing ancestors so output containment also accounts for directory links.
func resolvePackagePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	ancestor := absolute
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return "", fmt.Errorf("no existing ancestor: %s", absolute)
		}
		ancestor = next
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(ancestor, absolute)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, rel), nil
}

func releaseDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func runPackage(args []string, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	flags := flag.NewFlagSet("package", flag.ContinueOnError)
	flags.SetOutput(stderr)
	o := packageOptions{}
	for _, field := range []struct {
		key   string
		value *string
	}{{"source-root", &o.SourceRoot}, {"install-root", &o.InstallRoot}, {"output-root", &o.OutputRoot}, {"os", &o.OS}, {"arch", &o.Arch}, {"host-os", &o.HostOS}, {"host-arch", &o.HostArch}, {"importer-file", &o.ImporterFile}, {"cmake-command", &o.CMakeCommand}} {
		flags.StringVar(field.value, field.key, "", field.key)
	}
	var modules repeatedFlag
	flags.Var(&modules, "module", "selected package (repeatable)")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		return fail(fmt.Errorf("package: unexpected positional argument %q", flags.Arg(0)))
	}
	o.Modules = modules
	if err := packageRelease(o); err != nil {
		return fail(err)
	}
	return 0
}
