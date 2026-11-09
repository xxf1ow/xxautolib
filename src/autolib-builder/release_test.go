package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func releaseFixture(t *testing.T) packageOptions {
	t.Helper()
	o := stageFixture(t, "queue")
	putInstalledRelease(t, o.InstallRoot, "lz4", "1.0", "windows", "x86_64", "")
	putFile(t, filepath.Join(o.InstallRoot, "lz4/1.0/lib/lz4.lib"), "library bytes")
	putFile(t, filepath.Join(o.InstallRoot, "queue/package.toml"), "latest='1.0'\n")
	putFile(t, filepath.Join(o.InstallRoot, "lz4/package.toml"), "latest='1.0'\n")
	tool := putFile(t, filepath.Join(o.SourceRoot, "build/tool.exe"), "actual importer bytes")
	return packageOptions{SourceRoot: o.SourceRoot, InstallRoot: o.InstallRoot, OutputRoot: filepath.Join(o.SourceRoot, "build/release"), OS: o.OS, Arch: o.Arch, HostOS: "windows", HostArch: "x86_64", ImporterFile: tool, CMakeCommand: o.CMakeCommand, Modules: []string{"queue", "lz4", "queue"}}
}

func putInstalledRelease(t *testing.T, root, name, version, system, arch, dependencies string) string {
	t.Helper()
	header := fmt.Sprintf("set(AUTOLIB_PACKAGE_NAME %q)\nset(AUTOLIB_PACKAGE_VERSION %q)\nset(AUTOLIB_OS %q)\nset(AUTOLIB_ARCH %q)\nset(AUTOLIB_DEPENDENCIES %q)\n", name, version, system, arch, dependencies)
	putFile(t, filepath.Join(root, name, version, "autolib-import.cmake"), header+"# installed adapter\n")
	putFile(t, filepath.Join(root, name, version, "include/input.h"), name+"@"+version)
	return header
}

func releaseArchiveMembers(t *testing.T, archive, root string) map[string]string {
	t.Helper()
	input, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	z, err := zstd.NewReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	tr := tar.NewReader(z)
	members := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name != root+"/" && !strings.HasPrefix(h.Name, root+"/") {
			t.Fatalf("member outside exact root: %s", h.Name)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		members[h.Name] = string(body)
	}
	return members
}

func requireReleaseDigest(t *testing.T, archive string) {
	t.Helper()
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := os.ReadFile(archive + ".sha256")
	if err != nil || string(digest) != fmt.Sprintf("%x\n", sha256.Sum256(data)) {
		t.Fatalf("digest does not match final archive bytes: %q, %v", digest, err)
	}
}

func releaseSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	entries := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var content string
		if info.Mode()&os.ModeSymlink != 0 {
			content, err = os.Readlink(path)
		} else if info.Mode().IsRegular() {
			var data []byte
			data, err = os.ReadFile(path)
			content = fmt.Sprintf("%x", sha256.Sum256(data))
		}
		if err != nil {
			return err
		}
		entries[path] = fmt.Sprintf("%s %d %s", info.Mode(), info.ModTime().UnixNano(), content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestVersionedLocalRelease(t *testing.T) {
	o := releaseFixture(t)
	o.OS, o.Arch, o.HostOS = "linux", "x86_64", "linux"
	o.Modules = []string{"eigen@3.4.0", "eigen", "leaf@1", "eigen@3.4.0"}
	headers := map[string]string{}
	for _, version := range []string{"3.4.0", "5.0.1"} {
		headers[version] = putInstalledRelease(t, o.InstallRoot, "eigen", version, "linux", "x86_64", "leaf@1")
	}
	putInstalledRelease(t, o.InstallRoot, "leaf", "1", "any", "any", "")
	putFile(t, filepath.Join(o.InstallRoot, "eigen/package.toml"), "latest='5.0.1'\n")
	if err := packageRelease(o); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"3.4.0", "5.0.1"} {
		archive := filepath.Join(o.OutputRoot, "eigen-"+version+"-linux-x86_64.tar.zst")
		requireReleaseDigest(t, archive)
		members := releaseArchiveMembers(t, archive, "eigen/"+version)
		if members["eigen/"+version+"/autolib-import.cmake"] != headers[version]+"# installed adapter\n" || members["eigen/"+version+"/include/input.h"] != "eigen@"+version {
			t.Fatalf("wrong installed bytes: %v", members)
		}
	}
	leaf := filepath.Join(o.OutputRoot, "leaf-1-any-any.tar.zst")
	requireReleaseDigest(t, leaf)
	releaseArchiveMembers(t, leaf, "leaf/1")
	if _, err := os.Stat(filepath.Join(o.OutputRoot, "leaf/package.toml")); !os.IsNotExist(err) {
		t.Fatalf("invented latest: %v", err)
	}
	requireFile(t, filepath.Join(o.OutputRoot, "eigen/package.toml"), "latest='5.0.1'\n")
	tool := filepath.Join(o.OutputRoot, "autolib-importer-linux-x86_64.tar.xz")
	requireReleaseDigest(t, tool)
	listing, err := exec.Command(o.CMakeCommand, "-E", "tar", "tf", tool).CombinedOutput()
	if err != nil || strings.TrimSpace(string(listing)) != "autolib-importer-linux-x86_64" {
		t.Fatalf("tool is not a single bare program: %s %v", listing, err)
	}
	extracted := t.TempDir()
	cmd := exec.Command(o.CMakeCommand, "-E", "tar", "xJf", tool)
	cmd.Dir = extracted
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	requireFile(t, filepath.Join(extracted, "autolib-importer-linux-x86_64"), "actual importer bytes")
	o.OutputRoot = filepath.Join(o.SourceRoot, "build/old-only")
	o.Modules = []string{"eigen@3.4.0", "leaf@1"}
	if err := packageRelease(o); err != nil {
		t.Fatal(err)
	}
	requireFile(t, filepath.Join(o.OutputRoot, "eigen/package.toml"), "latest='5.0.1'\n")
	requireFile(t, filepath.Join(o.InstallRoot, "eigen/package.toml"), "latest='5.0.1'\n")
	if _, err := os.Stat(filepath.Join(o.OutputRoot, "eigen-5.0.1-linux-x86_64.tar.zst")); !os.IsNotExist(err) {
		t.Fatalf("unselected latest was packed: %v", err)
	}
}

func TestVersionedReleaseUsesInstalledIdentity(t *testing.T) {
	o := releaseFixture(t)
	o.Modules = []string{"queue@1.0"}
	putFile(t, filepath.Join(o.SourceRoot, "src/dependencies/leaf/package.toml"), "latest='2'\n")
	if err := packageRelease(o); err != nil {
		t.Fatal(err)
	}
	before := releaseArchiveMembers(t, filepath.Join(o.OutputRoot, "queue-1.0-windows-x86_64.tar.zst"), "queue/1.0")
	if !strings.Contains(before["queue/1.0/autolib-import.cmake"], "set(AUTOLIB_DEPENDENCIES \"leaf@1\")\n") {
		t.Fatal("source latest changed installed dependencies")
	}
	if err := os.RemoveAll(filepath.Join(o.SourceRoot, "src/dependencies")); err != nil {
		t.Fatal(err)
	}
	if err := packageRelease(o); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(o.OutputRoot, "queue-1.0-windows-x86_64.tar.zst")
	members := releaseArchiveMembers(t, archive, "queue/1.0")
	if !strings.Contains(members["queue/1.0/autolib-import.cmake"], "set(AUTOLIB_DEPENDENCIES \"leaf@1\")\n") {
		t.Fatal("frozen dependency changed")
	}
	entries, err := os.ReadDir(o.OutputRoot)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	want := []string{"autolib-importer-windows-x86_64.exe.tar.xz", "autolib-importer-windows-x86_64.exe.tar.xz.sha256", "queue", "queue-1.0-windows-x86_64.tar.zst", "queue-1.0-windows-x86_64.tar.zst.sha256"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("unexpected release entries: %v", names)
	}
	for _, invalid := range []string{"missing", "incomplete", "wrong version", "bare dependency"} {
		t.Run(invalid, func(t *testing.T) {
			o := releaseFixture(t)
			o.Modules = []string{"queue@1.0"}
			path := filepath.Join(o.InstallRoot, "queue/1.0/autolib-import.cmake")
			body := readFile(t, path)
			switch invalid {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "incomplete":
				putFile(t, path, incompleteConsumption)
			case "wrong version":
				putFile(t, path, strings.Replace(body, `"1.0"`, `"2.0"`, 1))
			case "bare dependency":
				putFile(t, path, strings.Replace(body, `"leaf@1"`, `"leaf"`, 1))
			}
			if err := packageRelease(o); err == nil {
				t.Fatal("packed invalid installation")
			}
			if _, err := os.Stat(o.OutputRoot); !os.IsNotExist(err) {
				t.Fatalf("published failed release: %v", err)
			}
		})
	}
}

func TestLocalPackageAssets(t *testing.T) {
	o := releaseFixture(t)
	if err := packageRelease(o); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(o.OutputRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 8 {
		t.Fatalf("expected tool and package archives, digests and pointers, got %v", entries)
	}
	for _, asset := range []string{"autolib-importer-windows-x86_64.exe.tar.xz", "lz4-1.0-windows-x86_64.tar.zst", "queue-1.0-windows-x86_64.tar.zst"} {
		if _, err := os.Stat(filepath.Join(o.OutputRoot, asset)); err != nil {
			t.Fatal(err)
		}
	}
	putFile(t, filepath.Join(o.OutputRoot, "unrelated"), "preserve")
	putFile(t, filepath.Join(o.OutputRoot, "manifest.json"), "user-owned old file")
	o.Modules = append(o.Modules, "missing")
	if err := packageRelease(o); err == nil {
		t.Fatal("accepted missing input")
	}
	requireFile(t, filepath.Join(o.OutputRoot, "unrelated"), "preserve")
	requireFile(t, filepath.Join(o.OutputRoot, "manifest.json"), "user-owned old file")
	requireFile(t, filepath.Join(o.InstallRoot, "queue/1.0/include/input.h"), "unchanged installation\n")
}

func TestLocalReleasePreservesInputs(t *testing.T) {
	o := releaseFixture(t)
	before := releaseSnapshot(t, o.InstallRoot)
	putFile(t, filepath.Join(o.OutputRoot, "unrelated"), "preserve")
	putFile(t, filepath.Join(o.OutputRoot, "other/package.toml"), "latest='keep'\n")
	putFile(t, filepath.Join(o.OutputRoot, "queue-0.9-windows-x86_64.tar.zst"), "older archive")
	for range 2 {
		if err := packageRelease(o); err != nil {
			t.Fatal(err)
		}
		requireFile(t, filepath.Join(o.OutputRoot, "unrelated"), "preserve")
		requireFile(t, filepath.Join(o.OutputRoot, "other/package.toml"), "latest='keep'\n")
		requireFile(t, filepath.Join(o.OutputRoot, "queue-0.9-windows-x86_64.tar.zst"), "older archive")
		if after := releaseSnapshot(t, o.InstallRoot); !reflect.DeepEqual(before, after) {
			t.Fatal("installation entries, bytes or metadata changed")
		}
	}
	old := readFile(t, filepath.Join(o.OutputRoot, "queue-1.0-windows-x86_64.tar.zst"))
	o.Modules = append(o.Modules, "missing")
	if err := packageRelease(o); err == nil {
		t.Fatal("published missing package")
	}
	requireFile(t, filepath.Join(o.OutputRoot, "queue-1.0-windows-x86_64.tar.zst"), old)
	if after := releaseSnapshot(t, o.InstallRoot); !reflect.DeepEqual(before, after) {
		t.Fatal("failed release changed installation")
	}
	t.Run("symlink output overlaps input", func(t *testing.T) {
		o := releaseFixture(t)
		if err := os.Symlink(o.InstallRoot, o.OutputRoot); err != nil {
			t.Skipf("host cannot create directory symlink: %v", err)
		}
		if err := packageRelease(o); err == nil {
			t.Fatal("release followed output symlink into installation")
		}
		if _, err := os.Stat(filepath.Join(o.InstallRoot, "manifest.json")); !os.IsNotExist(err) {
			t.Fatal("installation modified")
		}
	})
	for _, tc := range []string{"outside build", "overlap", "missing importer"} {
		t.Run(tc, func(t *testing.T) {
			o := releaseFixture(t)
			switch tc {
			case "outside build":
				o.OutputRoot = filepath.Join(o.SourceRoot, "release")
			case "overlap":
				o.InstallRoot = filepath.Join(o.SourceRoot, "build")
				o.OutputRoot = filepath.Join(o.InstallRoot, "release")
			case "missing importer":
				o.ImporterFile = filepath.Join(o.SourceRoot, "missing")
			}
			if err := packageRelease(o); err == nil {
				t.Fatal("accepted invalid release")
			}
			if _, err := os.Stat(filepath.Join(o.OutputRoot, "manifest.json")); !os.IsNotExist(err) {
				t.Fatalf("unexpected manifest: %v", err)
			}
		})
	}
}

func TestVersionedReleaseArchiveCollision(t *testing.T) {
	for _, tc := range []struct {
		label, version string
		existing       bool
	}{
		{"selected references", "bar-1", false},
		{"selected case aliases", "BAR-1", false},
		{"existing other identity", "bar-1", true},
	} {
		t.Run(tc.label, func(t *testing.T) {
			o := releaseFixture(t)
			o.OS, o.Arch, o.HostOS = "linux", "x86_64", "linux"
			putInstalledRelease(t, o.InstallRoot, "foo", tc.version, "linux", "x86_64", "")
			putInstalledRelease(t, o.InstallRoot, "foo-bar", "1", "linux", "x86_64", "")
			o.Modules = []string{"foo@" + tc.version, "foo-bar@1"}
			if tc.existing {
				o.Modules = []string{"foo-bar@1"}
				if err := packageRelease(o); err != nil {
					t.Fatal(err)
				}
				o.Modules = []string{"foo@" + tc.version}
			}
			putFile(t, filepath.Join(o.OutputRoot, "unrelated/package.toml"), "latest='keep'\n")
			before := releaseSnapshot(t, o.OutputRoot)
			err := packageRelease(o)
			if err == nil {
				t.Error("accepted two package identities for one archive name")
			}
			if after := releaseSnapshot(t, o.OutputRoot); !reflect.DeepEqual(before, after) {
				t.Fatal("archive collision changed existing release entries, bytes or metadata")
			}
		})
	}
}

func packageArguments(o packageOptions) []string {
	args := []string{"package"}
	for _, pair := range [][2]string{{"source-root", o.SourceRoot}, {"install-root", o.InstallRoot}, {"output-root", o.OutputRoot}, {"os", o.OS}, {"arch", o.Arch}, {"host-os", o.HostOS}, {"host-arch", o.HostArch}, {"importer-file", o.ImporterFile}, {"cmake-command", o.CMakeCommand}} {
		args = append(args, "--"+pair[0]+"="+pair[1])
	}
	for _, name := range o.Modules {
		args = append(args, "--module="+name)
	}
	return args
}

func TestPackageCLI(t *testing.T) {
	o := releaseFixture(t)
	args := packageArguments(o)
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("%d: %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatal(stdout.String())
	}
	badArgs := [][]string{{"package"}, append(append([]string{}, args...), "extra"), append(append([]string{}, args...), "--unknown=x")}
	for i := 1; i <= 9; i++ {
		badArgs = append(badArgs, append(append([]string{}, args[:i]...), args[i+1:]...))
		badArgs = append(badArgs, append(append([]string{}, args...), strings.SplitN(args[i], "=", 2)[0]+"="))
	}
	for _, bad := range badArgs {
		stderr.Reset()
		if code := run(bad, &stdout, &stderr); code == 0 || stderr.Len() == 0 {
			t.Fatalf("accepted %q: %d %q", bad, code, stderr.String())
		}
	}
}

func TestDarwinPackageIdentity(t *testing.T) {
	o := releaseFixture(t)
	o.OS, o.Arch, o.HostOS, o.HostArch = "darwin", "aarch64", "darwin", "aarch64"
	putInstalledRelease(t, o.InstallRoot, "lz4", "1.10.0", "darwin", "aarch64", "")
	putInstalledRelease(t, o.InstallRoot, "queue", "45db9a8b4431", "any", "any", "")
	o.Modules = []string{"lz4@1.10.0", "queue@45db9a8b4431"}
	before := releaseSnapshot(t, o.InstallRoot)
	if err := packageRelease(o); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"autolib-importer-darwin-aarch64.tar.xz", "lz4-1.10.0-darwin-aarch64.tar.zst", "queue-45db9a8b4431-any-any.tar.zst"} {
		requireReleaseDigest(t, filepath.Join(o.OutputRoot, name))
	}
	if !reflect.DeepEqual(before, releaseSnapshot(t, o.InstallRoot)) {
		t.Fatal("package mutated installation")
	}
	o.Arch = "x86_64"
	if err := packageRelease(o); err == nil {
		t.Fatal("Intel Darwin accepted")
	}
	f := newFixture(t)
	f.context.SystemName = "Darwin"
	f.context.Architecture = "aarch64"
	definitionFile(t, f.root, "unsupported", "copy", "package_platforms=['linux-aarch64']\npackage_platforms=['any-any']\n"+testSource+"\n[[copy]]\nfiles=['x.h']\ndestination='include'\n")
	if err := generate(f.context, []string{"unsupported"}); err == nil {
		t.Fatal("platform independent bypassed support")
	}
	if _, err := os.Stat(filepath.Join(f.context.BuildRoot, "generated")); !os.IsNotExist(err) {
		t.Fatal("generated unsupported target")
	}
}
