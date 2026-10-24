package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPrepareLocal(t *testing.T) {
	o := localReleaseFixture(t, "queue", "lz4")
	o.Packages = append(o.Packages, "queue")
	got, err := prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "queue" || got[1].Name != "lz4" {
		t.Fatal(got)
	}
	for _, p := range got {
		if readTestFile(t, filepath.Join(p.Directory, "data")) != "installed" {
			t.Fatal(p)
		}
	}
}
func TestPrepareReuse(t *testing.T) {
	o := localReleaseFixture(t, "demo")
	got, err := prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(got[0].Directory, "data"), "local authority")
	if err := os.Rename(filepath.Join(o.LocalRoot, "demo-linux-aarch64.tar.zst"), filepath.Join(o.LocalRoot, "archive-away")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepare(o); err != nil {
		t.Fatal(err)
	}
	if readTestFile(t, filepath.Join(got[0].Directory, "data")) != "local authority" {
		t.Fatal("overwrote directory")
	}
	putTestFile(t, filepath.Join(got[0].Directory, "autolib-import.cmake"), identityHeader(packageIdentity{"wrong", o.OS, o.Arch}))
	if _, err := prepare(o); err == nil {
		t.Fatal("accepted wrong directory")
	}
}
func TestPrepareFailures(t *testing.T) {
	o := localReleaseFixture(t, "demo")
	if _, err := prepare(o); err != nil {
		t.Fatal(err)
	}
	o.Packages = []string{"missing"}
	putTestFile(t, o.ResultPath, "previous result")
	if _, err := prepare(o); err == nil {
		t.Fatal("accepted missing package")
	}
	if readTestFile(t, o.ResultPath) != "previous result" {
		t.Fatal("failed prepare wrote result")
	}
	o.RemoteRoot = "https://example.invalid/release"
	if _, err := prepare(o); err == nil {
		t.Fatal("enabled remote prematurely")
	}
	o.RemoteRoot = ""
	o.LocalRoot = "https://example.invalid/release"
	if _, err := prepare(o); err == nil {
		t.Fatal("accepted URL as local root")
	}
}

func TestPreparedResult(t *testing.T) {
	root := t.TempDir()
	want := filepath.ToSlash(filepath.Join(root, "中文 空格;${not_a_variable} ]=] ]==]"))
	result := filepath.Join(root, "result.cmake")
	if err := writePreparedResult(result, []preparedPackage{{Name: "demo", Directory: want}}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "observed")
	script := putTestFile(t, filepath.Join(root, "check.cmake"), "include(\""+filepath.ToSlash(result)+"\")\nfile(WRITE \""+filepath.ToSlash(out)+"\" \"${AUTOLIB_PACKAGE_DIR_demo}\")\nif(NOT AUTOLIB_PREPARED_PACKAGES STREQUAL \"demo\")\nmessage(FATAL_ERROR \"wrong packages\")\nendif()\n")
	if output, err := exec.Command("cmake", "-P", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if got := readTestFile(t, out); got != want {
		t.Fatalf("%q != %q", got, want)
	}
}
