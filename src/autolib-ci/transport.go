package main

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ciTransportPackage struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	OS           string   `json:"os"`
	Arch         string   `json:"arch"`
	Dependencies []string `json:"dependencies"`
	Publish      bool     `json:"publish"`
}

type ciTransport struct {
	Run      ciRunIdentity        `json:"run"`
	Platform string               `json:"platform"`
	Packages []ciTransportPackage `json:"packages"`
	Entry    string               `json:"entry"`
	Assets   []ciTransportAsset   `json:"assets"`
}

type ciTransportAsset struct {
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Size     int64   `json:"size"`
	SHA256   string  `json:"sha256"`
	Package  *ciSlot `json:"package,omitempty"`
	Platform string  `json:"platform,omitempty"`
}

type ciTransportMember struct {
	path, name string
	info       os.FileInfo
}

func writeCITransport(ctx context.Context, o ciRunOptions, output string) error {
	plan, run, platform, source, work, err := loadCIWork(ctx, o)
	if err != nil {
		return err
	}
	var result ciPlatformResult
	if err := decodeStrictJSONFile(filepath.Join(work, "results", o.Platform+".json"), &result); err != nil {
		return err
	}
	selected := plan
	selected.Platforms = []ciPlatformPlan{*platform}
	if err := finalizeCI(selected, o.Repository, run, []ciPlatformResult{result}); err != nil {
		return err
	}
	install := filepath.Join(work, "install")
	info, err := os.Lstat(install)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("transport installation root must be a directory, not a link")
	}
	entry, err := verifyCIEntry(source, install)
	if err != nil {
		return err
	}
	if entry != result.Entry {
		return errors.New("transport entry does not match verified result")
	}
	manifest := ciTransport{Run: run, Platform: o.Platform, Packages: []ciTransportPackage{}, Entry: "autolib.cmake", Assets: []ciTransportAsset{}}
	var members []ciTransportMember
	add := func(path, name string) error {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("transport member must be a regular file: %s", path)
		}
		members = append(members, ciTransportMember{path: path, name: name, info: info})
		return nil
	}
	var modules []string
	packages := map[string]bool{}
	for _, module := range platform.Description.Modules {
		slot := ciModuleSlot(module, o.Platform)
		publish := module.PackagePlatform != "any-any" || plan.Publisher == o.Platform
		manifest.Packages = append(manifest.Packages, ciTransportPackage{Name: slot.Name, Version: slot.Version, OS: slot.OS, Arch: slot.Arch, Dependencies: module.Depends, Publish: publish})
		if publish {
			modules = append(modules, moduleIdentity(module))
			name := fmt.Sprintf("%s-%s-%s-%s.tar.zst", slot.Name, slot.Version, slot.OS, slot.Arch)
			manifest.Assets = append(manifest.Assets, ciTransportAsset{Kind: "library", Name: name, Path: "assets/" + name, Package: &slot})
		}
		packageRoot := filepath.Join(install, module.Name)
		packageInfo, err := os.Lstat(packageRoot)
		if err != nil {
			return err
		}
		if !packageInfo.IsDir() {
			return fmt.Errorf("package root must be a directory, not a link: %s", packageRoot)
		}
		if !packages[module.Name] {
			candidate := filepath.Join(packageRoot, "package.toml")
			info, err := os.Lstat(candidate)
			if err != nil {
				return fmt.Errorf("package candidate %s: %w", candidate, err)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("package candidate must be a regular file: %s", candidate)
			}
			// Candidates are the builder's original bytes, shared by every selected version.
			if err := add(candidate, "candidates/"+module.Name+"/package.toml"); err != nil {
				return err
			}
			packages[module.Name] = true
		}
		versionRoot := filepath.Join(packageRoot, module.Version)
		info, err := os.Lstat(versionRoot)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("package version root must be a directory: %s", versionRoot)
		}
	}
	importer := filepath.Join(install, ciImporterExecutable(o.Platform))
	info, err = os.Lstat(importer)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("transport importer must be a regular file")
	}
	if err := add(filepath.Join(install, "autolib.cmake"), manifest.Entry); err != nil {
		return err
	}
	if strings.TrimSpace(output) == "" {
		return errors.New("transport --output is required")
	}
	absolute, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	absolute, err = resolveExistingPrefix(absolute)
	if err != nil {
		return err
	}
	if err := requirePathWithin(work, absolute, "transport output"); err != nil {
		return err
	}
	if requirePathWithin(install, absolute, "transport output") == nil {
		return errors.New("transport output must not be inside installation")
	}
	if o.CMakeCommand == "" {
		return errors.New("transport --cmake-command is required")
	}
	packaged, err := os.MkdirTemp(work, "packaged-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(packaged)
	osName, arch, _ := strings.Cut(o.Platform, "-")
	builder := filepath.Join(work, "build", "tools", "autolib-builder")
	if len(platform.Description.Modules) == 0 {
		builder = filepath.Join(work, "tools", "autolib-builder")
	}
	if osName == "windows" {
		builder += ".exe"
	}
	args := []string{"package", "--source-root", source, "--install-root", install, "--output-root", packaged, "--os", osName, "--arch", arch, "--host-os", osName, "--host-arch", arch, "--importer-file", importer, "--cmake-command", o.CMakeCommand}
	for _, module := range modules {
		args = append(args, "--module", module)
	}
	if err := ciCommand(ctx, builder, args, source, nil, filepath.Join(work, "logs", "package.log")); err != nil {
		return err
	}
	toolName := ciImporterExecutable(o.Platform) + ".tar.xz"
	manifest.Assets = append(manifest.Assets, ciTransportAsset{Kind: "importer", Name: toolName, Path: "assets/" + toolName, Platform: o.Platform})
	for i := range manifest.Assets {
		asset := &manifest.Assets[i]
		path := filepath.Join(packaged, asset.Name)
		if err := add(path, asset.Path); err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		digest := sha256.New()
		size, copyErr := io.Copy(digest, input)
		if err := errors.Join(copyErr, input.Close()); err != nil {
			return err
		}
		asset.Size, asset.SHA256 = size, fmt.Sprintf("%x", digest.Sum(nil))
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	archive := tar.NewWriter(file)
	success := false
	defer func() {
		archive.Close()
		file.Close()
		if !success {
			os.Remove(absolute)
		}
	}()
	for _, metadata := range []struct {
		name  string
		value any
	}{{"transport.json", manifest}, {"plan.json", plan}, {"result.json", result}} {
		data, err := json.MarshalIndent(metadata.value, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if err := archive.WriteHeader(&tar.Header{Name: metadata.name, Mode: 0644, Typeflag: tar.TypeReg, Size: int64(len(data))}); err != nil {
			return err
		}
		if _, err := archive.Write(data); err != nil {
			return err
		}
	}
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(member.info, "")
		if err != nil {
			return err
		}
		header.Name = member.name
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if member.info.Mode().IsRegular() {
			input, err := os.Open(member.path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(archive, input)
			if err := errors.Join(copyErr, input.Close()); err != nil {
				return err
			}
		}
	}
	if err := errors.Join(archive.Close(), file.Close()); err != nil {
		return err
	}
	success = true
	return nil
}

func runCITransport(args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	flags := flag.NewFlagSet("transport", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var o ciRunOptions
	var output string
	for _, item := range []struct {
		key   string
		value *string
	}{{"repository", &o.Repository}, {"source-root", &o.SourceRoot}, {"plan", &o.PlanFile}, {"run-identity", &o.RunIdentityFile}, {"platform", &o.Platform}, {"work-root", &o.WorkRoot}, {"output", &output}, {"cmake-command", &o.CMakeCommand}} {
		flags.StringVar(item.value, item.key, "", item.key)
	}
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 || o.PlanFile == "" || output == "" || o.CMakeCommand == "" {
		return fail(errors.New("transport requires --source-root, --plan, --run-identity, --platform, --work-root, --cmake-command and --output"))
	}
	if err := writeCITransport(context.Background(), o, output); err != nil {
		return fail(err)
	}
	fmt.Fprintln(stdout, output)
	return 0
}
