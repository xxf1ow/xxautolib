package main

import (
	"archive/tar"
	"bufio"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/pelletier/go-toml/v2"
)

type packageOptions struct {
	AssetKind                                                                                                   string
	SourceRoot, InstallRoot, ProducerPrefix, OutputRoot, OS, Arch, HostOS, HostArch, ImporterFile, CMakeCommand string
	Modules                                                                                                     []string
}

func packageRelease(o packageOptions) error {
	if o.AssetKind == "" {
		o.AssetKind = "all"
	}
	if o.AssetKind != "all" && o.AssetKind != "library" && o.AssetKind != "importer" {
		return fmt.Errorf("asset-kind must be all, library or importer")
	}
	if o.AssetKind == "library" && len(o.Modules) == 0 {
		return fmt.Errorf("library packaging requires explicit modules")
	}
	if o.AssetKind == "importer" && len(o.Modules) != 0 {
		return fmt.Errorf("importer packaging does not accept modules")
	}
	if err := validatePackagePlatform(o.OS, o.Arch); err != nil {
		return err
	}
	if o.AssetKind != "library" {
		if err := validatePackagePlatform(o.HostOS, o.HostArch); err != nil {
			return err
		}
	}
	if o.SourceRoot == "" || o.InstallRoot == "" || o.OutputRoot == "" || o.CMakeCommand == "" || (o.AssetKind != "library" && o.ImporterFile == "") {
		return fmt.Errorf("package requires source, install, output and CMake paths, and importer for tool assets")
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
	if o.AssetKind != "library" {
		toolInfo, err := os.Stat(o.ImporterFile)
		if err != nil {
			return err
		}
		if !toolInfo.Mode().IsRegular() {
			return fmt.Errorf("importer must be a regular file: %s", o.ImporterFile)
		}
	}
	refs := []moduleRef{}
	pointers := map[string][]byte{}
	for _, value := range o.Modules {
		ref, err := parseModuleRef(value)
		if err != nil {
			return err
		}
		if err := exactEntry(install, ref.Name, true); err != nil {
			return err
		}
		latest, content, err := installedPackageLatest(install, ref.Name)
		if err != nil && (!os.IsNotExist(err) || ref.Version == "") {
			return err
		}
		if err == nil {
			pointers[ref.Name] = content
		}
		if ref.Version == "" {
			ref.Version = latest
		}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	slices.SortFunc(refs, func(a, b moduleRef) int { return strings.Compare(a.String(), b.String()) })
	if err := os.MkdirAll(filepath.Join(source, "build"), 0755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(filepath.Join(source, "build"), "package-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	assets := []string{}
	if o.AssetKind != "library" {
		toolAsset := fmt.Sprintf("autolib-importer-%s-%s", o.HostOS, o.HostArch)
		if o.HostOS == "windows" {
			toolAsset += ".exe"
		}
		toolDirectory := filepath.Join(work, "tool")
		if err := os.Mkdir(toolDirectory, 0755); err != nil {
			return err
		}
		if err := copyPackageFile(o.ImporterFile, filepath.Join(toolDirectory, toolAsset), 0755); err != nil {
			return err
		}
		toolArchive := toolAsset + ".tar.xz"
		cmd := exec.Command(o.CMakeCommand, "-E", "tar", "cJf", filepath.Join(work, toolArchive), "--format=gnutar", toolAsset)
		cmd.Dir = toolDirectory
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("archive importer: %w\n%s", err, output)
		}
		assets = append(assets, toolArchive)
	}
	archiveRefs := map[string]moduleRef{}
	for _, ref := range refs {
		directory, err := stagePackage(stageOptions{SourceRoot: source, InstallRoot: install, ProducerPrefix: o.ProducerPrefix, StageRoot: filepath.Join(work, "staging"), Name: ref.Name, Version: ref.Version, OS: o.OS, Arch: o.Arch, CMakeCommand: o.CMakeCommand})
		if err != nil {
			return err
		}
		metadata, err := readConsumptionMetadata(filepath.Join(directory, "autolib-import.cmake"))
		if err != nil {
			return err
		}
		asset := fmt.Sprintf("%s-%s-%s-%s.tar.zst", ref.Name, ref.Version, metadata.OS, metadata.Arch)
		archiveKey := strings.ToLower(asset)
		if previous, ok := archiveRefs[archiveKey]; ok && previous != ref {
			return fmt.Errorf("archive %s: package references %s and %s have the same output name", asset, previous, ref)
		}
		archiveRefs[archiveKey] = ref
		if err := checkExistingReleaseArchive(filepath.Join(output, asset), metadata); err != nil {
			return err
		}
		if err := writePackageArchive(directory, filepath.Join(work, asset), ref); err != nil {
			return err
		}
		assets = append(assets, asset)
	}
	for _, asset := range slices.Clone(assets) {
		if err := writeArchiveDigest(filepath.Join(work, asset)); err != nil {
			return err
		}
		assets = append(assets, asset+".sha256")
	}
	for name, content := range pointers {
		if err := os.MkdirAll(filepath.Join(work, name), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(work, name, "package.toml"), content, 0644); err != nil {
			return err
		}
		assets = append(assets, filepath.Join(name, "package.toml"))
		if info, err := os.Lstat(filepath.Join(output, name)); err == nil && !info.IsDir() {
			return fmt.Errorf("release package metadata parent must be a directory: %s", filepath.Join(output, name))
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		return err
	}
	for _, asset := range assets {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(output, asset)), 0755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(work, asset), filepath.Join(output, asset)); err != nil {
			return fmt.Errorf("publish %s: %w", asset, err)
		}
	}
	return nil
}

func checkExistingReleaseArchive(path string, metadata consumptionMetadata) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s: existing archive must be a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	z, err := zstd.NewReader(file)
	if err != nil {
		return fmt.Errorf("%s: cannot identify existing archive: %w", path, err)
	}
	defer z.Close()
	tr := tar.NewReader(z)
	root := metadata.Name + "/" + metadata.Version
	found := false
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: cannot identify existing archive: %w", path, err)
		}
		member := strings.TrimSuffix(header.Name, "/")
		if member != root && !strings.HasPrefix(member, root+"/") {
			return fmt.Errorf("%s: existing archive member %q does not belong to %s@%s", path, header.Name, metadata.Name, metadata.Version)
		}
		if header.Name != root+"/autolib-import.cmake" {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return fmt.Errorf("%s: existing archive identity must be a regular file", path)
		}
		scanner := bufio.NewScanner(tr)
		for _, field := range []struct{ key, value string }{{"AUTOLIB_PACKAGE_NAME", metadata.Name}, {"AUTOLIB_PACKAGE_VERSION", metadata.Version}, {"AUTOLIB_OS", metadata.OS}, {"AUTOLIB_ARCH", metadata.Arch}} {
			if !scanner.Scan() || scanner.Text() != fmt.Sprintf("set(%s %q)", field.key, field.value) {
				return fmt.Errorf("%s: existing archive identity does not match %s@%s %s/%s", path, metadata.Name, metadata.Version, metadata.OS, metadata.Arch)
			}
		}
		found = true
	}
	if _, err := io.Copy(io.Discard, z); err != nil {
		return fmt.Errorf("%s: cannot read existing archive: %w", path, err)
	}
	if !found {
		return fmt.Errorf("%s: existing archive has no package identity", path)
	}
	return nil
}

func installedPackageLatest(root, name string) (string, []byte, error) {
	directory := filepath.Join(root, name)
	path := filepath.Join(directory, "package.toml")
	info, err := os.Lstat(path)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%s: expected a regular latest pointer", path)
	}
	if err := exactEntry(directory, "package.toml", false); err != nil {
		return "", nil, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var raw map[string]any
	if err := toml.Unmarshal(content, &raw); err != nil {
		return "", nil, fmt.Errorf("%s: %w", path, err)
	}
	latest, ok := raw["latest"].(string)
	if !ok || len(raw) != 1 {
		return "", nil, fmt.Errorf("%s: expected only latest as a version string", path)
	}
	if err := validatePackageVersion(latest); err != nil {
		return "", nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := validateArtifactVersionPath(filepath.Join(directory, latest)); err != nil {
		return "", nil, err
	}
	return latest, content, nil
}

func writeArchiveDigest(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.WriteFile(path+".sha256", []byte(fmt.Sprintf("%x\n", hash.Sum(nil))), 0644)
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

func runPackage(args []string, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	flags := flag.NewFlagSet("package", flag.ContinueOnError)
	flags.SetOutput(stderr)
	o := packageOptions{}
	flags.StringVar(&o.AssetKind, "asset-kind", "all", "all, library or importer")
	for _, field := range []struct {
		key   string
		value *string
	}{{"source-root", &o.SourceRoot}, {"install-root", &o.InstallRoot}, {"producer-prefix", &o.ProducerPrefix}, {"output-root", &o.OutputRoot}, {"os", &o.OS}, {"arch", &o.Arch}, {"host-os", &o.HostOS}, {"host-arch", &o.HostArch}, {"importer-file", &o.ImporterFile}, {"cmake-command", &o.CMakeCommand}} {
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
