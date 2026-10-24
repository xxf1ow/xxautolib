package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func releaseFixture(t *testing.T) packageOptions {
	t.Helper()
	o := stageFixture(t, "queue")
	definitionFile(t, o.SourceRoot, "lz4", "cmake", testSource)
	putFile(t, filepath.Join(o.SourceRoot, "src/dependencies/lz4/autolib-import.cmake"), "# lz4\n")
	putFile(t, filepath.Join(o.InstallRoot, "lz4/lib/lz4.lib"), "library bytes")
	tool := putFile(t, filepath.Join(o.SourceRoot, "build/tool.exe"), "actual importer bytes")
	return packageOptions{SourceRoot: o.SourceRoot, InstallRoot: o.InstallRoot, OutputRoot: filepath.Join(o.SourceRoot, "build/release"), OS: o.OS, Arch: o.Arch, HostOS: "windows", HostArch: "x86_64", ImporterFile: tool, CMakeCommand: o.CMakeCommand, Modules: []string{"queue", "lz4", "queue"}}
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
	if len(entries) != 3 {
		t.Fatalf("expected only tool and two archives, got %v", entries)
	}
	for _, asset := range []string{"autolib-importer-windows-x86_64.exe", "lz4-windows-x86_64.tar.zst", "queue-windows-x86_64.tar.zst"} {
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
	requireFile(t, filepath.Join(o.InstallRoot, "queue/include/input.h"), "unchanged installation\n")
}

func TestLocalReleasePreservesInputs(t *testing.T) {
	o := releaseFixture(t)
	definition, err := loadDefinition(filepath.Join(o.SourceRoot, "src/dependencies/queue/module.toml"), "Windows", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	packageOS, packageArch := packagePlatform(definition, o.OS, o.Arch)
	if packageOS != o.OS || packageArch != o.Arch {
		t.Fatalf("undeclared package became universal: %s/%s", packageOS, packageArch)
	}
	putFile(t, filepath.Join(o.OutputRoot, "unrelated"), "preserve")
	for range 2 {
		if err := packageRelease(o); err != nil {
			t.Fatal(err)
		}
		requireFile(t, filepath.Join(o.OutputRoot, "unrelated"), "preserve")
	}
	old := readFile(t, filepath.Join(o.OutputRoot, "queue-windows-x86_64.tar.zst"))
	o.Modules = append(o.Modules, "missing")
	if err := packageRelease(o); err == nil {
		t.Fatal("published missing package")
	}
	requireFile(t, filepath.Join(o.OutputRoot, "queue-windows-x86_64.tar.zst"), old)
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
	for _, tc := range []string{"outside build", "overlap", "empty modules", "missing importer"} {
		t.Run(tc, func(t *testing.T) {
			o := releaseFixture(t)
			switch tc {
			case "outside build":
				o.OutputRoot = filepath.Join(o.SourceRoot, "release")
			case "overlap":
				o.InstallRoot = filepath.Join(o.SourceRoot, "build")
				o.OutputRoot = filepath.Join(o.InstallRoot, "release")
			case "empty modules":
				o.Modules = nil
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
