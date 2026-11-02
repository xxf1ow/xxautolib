package main

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestPackageTransportedPrefix(t *testing.T) {
	for _, producer := range []string{`C:\producer space\install`, `C:/producer space/install`, "/producer space/install"} {
		t.Run(producer, func(t *testing.T) {
			o := releaseFixture(t)
			o.Modules = []string{"queue@1.0"}
			root := filepath.Join(o.InstallRoot, "queue/1.0")
			for _, extension := range []string{"cmake", "pc"} {
				putFile(t, filepath.Join(root, "lib/config."+extension), "prefix="+producer+"/queue/1.0\nslash="+strings.ReplaceAll(producer, `\`, "/")+"/leaf/1/include\n")
			}
			if runtime.GOOS == "linux" {
				if err := os.Symlink("include/input.h", filepath.Join(root, "relative-link")); err != nil {
					t.Fatal(err)
				}
			}
			before := releaseSnapshot(t, o.InstallRoot)
			args := []string{"--source-root=" + o.SourceRoot, "--install-root=" + o.InstallRoot, "--producer-prefix=" + producer, "--output-root=" + o.OutputRoot, "--os=" + o.OS, "--arch=" + o.Arch, "--host-os=" + o.HostOS, "--host-arch=" + o.HostArch, "--importer-file=" + o.ImporterFile, "--cmake-command=" + o.CMakeCommand, "--module=queue@1.0"}
			var stderr bytes.Buffer
			if code := runPackage(args, &stderr); code != 0 {
				t.Fatalf("package exit %d: %s", code, stderr.String())
			}
			archive := filepath.Join(o.OutputRoot, "queue-1.0-windows-x86_64.tar.zst")
			requireReleaseDigest(t, archive)
			members := releaseArchiveMembers(t, archive, "queue/1.0")
			for _, extension := range []string{"cmake", "pc"} {
				body := members["queue/1.0/lib/config."+extension]
				variable := "${CMAKE_CURRENT_LIST_DIR}"
				if extension == "pc" {
					variable = "${pcfiledir}"
				}
				want := "prefix=" + variable + "/../../../queue/1.0\nslash=" + variable + "/../../../leaf/1/include\n"
				if body != want {
					t.Fatalf("metadata not relocated: got %q want %q", body, want)
				}
			}
			if runtime.GOOS == "linux" {
				requireTransportLink(t, archive)
			}
			if !reflect.DeepEqual(before, releaseSnapshot(t, o.InstallRoot)) {
				t.Fatal("transport input content, type, permissions, link or mtime changed")
			}
		})
	}
}

func requireTransportLink(t *testing.T, archive string) {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := zstd.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	tr := tar.NewReader(z)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			t.Fatal("missing relative link")
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "queue/1.0/relative-link" {
			if h.Typeflag != tar.TypeSymlink || h.Linkname != "include/input.h" {
				t.Fatalf("wrong link: %+v", h)
			}
			return
		}
	}
}

func TestPackageTransportedIdentity(t *testing.T) {
	t.Run("tool only", func(t *testing.T) {
		o := releaseFixture(t)
		o.Modules = nil
		o.HostOS = "linux"
		before := releaseSnapshot(t, o.InstallRoot)
		if err := packageRelease(o); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(o.OutputRoot)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("tool-only selection produced other assets: %v", entries)
		}
		archive := filepath.Join(o.OutputRoot, "autolib-importer-linux-x86_64.tar.xz")
		requireReleaseDigest(t, archive)
		listing, err := exec.Command(o.CMakeCommand, "-E", "tar", "tf", archive).CombinedOutput()
		if err != nil || strings.TrimSpace(string(listing)) != "autolib-importer-linux-x86_64" {
			t.Fatalf("not one top-level program: %s %v", listing, err)
		}
		dir := t.TempDir()
		cmd := exec.Command(o.CMakeCommand, "-E", "tar", "xJf", archive)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		program := filepath.Join(dir, "autolib-importer-linux-x86_64")
		requireFile(t, program, "actual importer bytes")
		info, err := os.Stat(program)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "linux" && info.Mode().Perm()&0111 == 0 {
			t.Fatal("tool is not executable")
		}
		if !reflect.DeepEqual(before, releaseSnapshot(t, o.InstallRoot)) {
			t.Fatal("tool packaging mutated installation")
		}
	})
	for _, field := range []string{"version", "platform"} {
		t.Run(field, func(t *testing.T) {
			o := releaseFixture(t)
			o.Modules = []string{"queue@1.0"}
			path := filepath.Join(o.InstallRoot, "queue/1.0/autolib-import.cmake")
			old, replacement := `"1.0"`, `"2.0"`
			if field == "platform" {
				old, replacement = `"windows"`, `"linux"`
			}
			putFile(t, path, strings.Replace(readFile(t, path), old, replacement, 1))
			before := releaseSnapshot(t, o.InstallRoot)
			if err := packageRelease(o); err == nil {
				t.Fatal("accepted wrong transported identity")
			}
			if !reflect.DeepEqual(before, releaseSnapshot(t, o.InstallRoot)) {
				t.Fatal("rejected input changed")
			}
		})
	}
	t.Run("two versions", func(t *testing.T) {
		o := releaseFixture(t)
		o.Modules = []string{"eigen@3.4.0", "eigen@5.0.1"}
		for _, version := range []string{"3.4.0", "5.0.1"} {
			putInstalledRelease(t, o.InstallRoot, "eigen", version, "any", "any", "")
		}
		putFile(t, filepath.Join(o.InstallRoot, "eigen/package.toml"), "latest='3.4.0'\n")
		if err := packageRelease(o); err != nil {
			t.Fatal(err)
		}
		for _, version := range []string{"3.4.0", "5.0.1"} {
			archive := filepath.Join(o.OutputRoot, "eigen-"+version+"-any-any.tar.zst")
			requireReleaseDigest(t, archive)
			members := releaseArchiveMembers(t, archive, "eigen/"+version)
			if members["eigen/"+version+"/include/input.h"] != "eigen@"+version {
				t.Fatalf("version overwritten: %v", members)
			}
		}
		requireFile(t, filepath.Join(o.OutputRoot, "eigen/package.toml"), "latest='3.4.0'\n")
	})
}
