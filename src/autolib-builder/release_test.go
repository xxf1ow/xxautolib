package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func TestLocalReleaseManifest(t *testing.T) {
	o := releaseFixture(t)
	putFile(t, filepath.Join(o.OutputRoot, "unrelated"), "preserve")
	for range 2 {
		if err := packageRelease(o); err != nil {
			t.Fatal(err)
		}
		var document map[string][]map[string]string
		if err := json.Unmarshal([]byte(readFile(t, filepath.Join(o.OutputRoot, "manifest.json"))), &document); err != nil {
			t.Fatal(err)
		}
		if len(document) != 2 || len(document["importers"]) != 1 || len(document["packages"]) != 2 {
			t.Fatal(document)
		}
		if document["packages"][0]["name"] != "lz4" || document["packages"][1]["name"] != "queue" {
			t.Fatal(document)
		}
		for kind, records := range document {
			for _, row := range records {
				wantFields := 4
				if kind == "packages" {
					wantFields = 5
				}
				if len(row) != wantFields || row["os"] != "windows" || row["arch"] != "x86_64" {
					t.Fatal(row)
				}
				data, err := os.ReadFile(filepath.Join(o.OutputRoot, row["asset"]))
				if err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(data)
				if row["sha256"] != hex.EncodeToString(digest[:]) {
					t.Fatal(row)
				}
			}
		}
		requireFile(t, filepath.Join(o.OutputRoot, "unrelated"), "preserve")
	}
	old := readFile(t, filepath.Join(o.OutputRoot, "manifest.json"))
	o.Modules = append(o.Modules, "missing")
	if err := packageRelease(o); err == nil {
		t.Fatal("published missing package")
	}
	requireFile(t, filepath.Join(o.OutputRoot, "manifest.json"), old)
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
