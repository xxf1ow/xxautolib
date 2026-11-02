package main

import (
	"archive/tar"
	"context"
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
	Run            ciRunIdentity        `json:"run"`
	Platform       string               `json:"platform"`
	ProducerPrefix string               `json:"producer_prefix"`
	Packages       []ciTransportPackage `json:"packages"`
	Importer       string               `json:"importer"`
}

type ciTransportMember struct {
	path, name string
	info       os.FileInfo
	link       string
}

func writeCITransport(ctx context.Context, o ciRunOptions, output string) error {
	plan, run, platform, _, work, err := loadCIWork(ctx, o)
	if err != nil {
		return err
	}
	var result ciPlatformResult
	if err := decodeStrictJSONFile(filepath.Join(work, "results", o.Platform+".json"), &result); err != nil {
		return err
	}
	selected := plan
	selected.Platforms = []ciPlatformPlan{*platform}
	if err := finalizeCI(selected, run, []ciPlatformResult{result}); err != nil {
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
	manifest := ciTransport{Run: run, Platform: o.Platform, ProducerPrefix: install, Packages: []ciTransportPackage{}, Importer: "install/" + ciImporterExecutable(o.Platform)}
	var members []ciTransportMember
	add := func(path, name, boundary string) error {
		return filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			relative, err := filepath.Rel(path, current)
			if err != nil {
				return err
			}
			memberName := name
			if relative != "." {
				memberName += "/" + filepath.ToSlash(relative)
			}
			info, err := os.Lstat(current)
			if err != nil {
				return err
			}
			member := ciTransportMember{path: current, name: memberName, info: info}
			switch {
			case info.Mode().IsRegular(), info.IsDir():
			case info.Mode()&os.ModeSymlink != 0:
				link, err := os.Readlink(current)
				if err != nil {
					return err
				}
				if filepath.IsAbs(link) || strings.Contains(link, `\`) {
					return fmt.Errorf("transport link %s must be relative", current)
				}
				target := filepath.Clean(filepath.Join(filepath.Dir(current), link))
				if err := requirePathWithin(boundary, target, "transport link"); err != nil {
					return err
				}
				resolved, err := filepath.EvalSymlinks(current)
				if err != nil {
					return fmt.Errorf("resolve transport link %s: %w", current, err)
				}
				if err := requirePathWithin(boundary, resolved, "transport link target"); err != nil {
					return err
				}
				member.link = filepath.ToSlash(link)
			default:
				return fmt.Errorf("unsupported transport object %s (%s)", current, info.Mode())
			}
			members = append(members, member)
			return nil
		})
	}
	packages := map[string]bool{}
	for _, module := range platform.Description.Modules {
		slot := ciModuleSlot(module, o.Platform)
		manifest.Packages = append(manifest.Packages, ciTransportPackage{Name: slot.Name, Version: slot.Version, OS: slot.OS, Arch: slot.Arch, Dependencies: module.Depends, Publish: !module.PlatformIndependent || plan.Publisher == o.Platform})
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
			if err := add(candidate, "install/"+module.Name+"/package.toml", packageRoot); err != nil {
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
		if err := add(versionRoot, "install/"+module.Name+"/"+module.Version, versionRoot); err != nil {
			return err
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
	if err := add(importer, manifest.Importer, install); err != nil {
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
	if err := archive.WriteHeader(&tar.Header{Name: "install/", Mode: 0755, Typeflag: tar.TypeDir}); err != nil {
		return err
	}
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(member.info, member.link)
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
	}{{"source-root", &o.SourceRoot}, {"plan", &o.PlanFile}, {"run-identity", &o.RunIdentityFile}, {"platform", &o.Platform}, {"work-root", &o.WorkRoot}, {"output", &output}} {
		flags.StringVar(item.value, item.key, "", item.key)
	}
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 || o.PlanFile == "" || output == "" {
		return fail(errors.New("transport requires --source-root, --plan, --run-identity, --platform, --work-root and --output"))
	}
	if err := writeCITransport(context.Background(), o, output); err != nil {
		return fail(err)
	}
	fmt.Fprintln(stdout, output)
	return 0
}
