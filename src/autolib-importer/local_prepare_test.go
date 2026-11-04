package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func localGraphFixture(t *testing.T) prepareOptions {
	t.Helper()
	root := t.TempDir()
	return prepareOptions{CacheRoot: filepath.Join(root, "cache"), ResultPath: filepath.Join(root, "result.cmake"), OS: "linux", Arch: "aarch64"}
}

func graphInstalled(t *testing.T, root string, id packageIdentity, dependencies string) {
	t.Helper()
	header := strings.Replace(identityHeader(id), `set(AUTOLIB_DEPENDENCIES "")`, `set(AUTOLIB_DEPENDENCIES "`+dependencies+`")`, 1)
	putTestFile(t, filepath.Join(root, id.Name, id.Version, "autolib-import.cmake"), header)
	putTestFile(t, filepath.Join(root, id.Name, id.Version, "data"), "installed")
	versionedLatest(t, root, id.Name, id.Version)
}

func graphSlot(o prepareOptions, name string) string {
	return filepath.Join(o.CacheRoot, name, "1.0")
}

func TestPrepareDependencyOrder(t *testing.T) {
	o := localGraphFixture(t)
	o.Packages = []string{"app", "tool", "app"}
	for _, node := range [][2]string{{"app", "beta@1.0;alpha@1.0;beta@1.0"}, {"alpha", "core@1.0"}, {"beta", "core@1.0"}, {"tool", "core@1.0"}, {"core", ""}} {
		graphInstalled(t, o.CacheRoot, packageIdentity{node[0], "1.0", o.OS, o.Arch}, node[1])
	}
	got, err := prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, p := range got {
		names = append(names, p.Name)
		if p.Directory != graphSlot(o, p.Name) {
			t.Fatal(p)
		}
	}
	if !slices.Equal(names, []string{"core", "alpha", "beta", "app", "tool"}) {
		t.Fatal(names)
	}
	entries, err := os.ReadDir(o.CacheRoot)
	if err != nil || len(entries) != 5 {
		t.Fatalf("%v %v", entries, err)
	}
	if _, err := prepare(o); err != nil {
		t.Fatal("read-only reuse:", err)
	}
}

func TestPrepareDependencyCycle(t *testing.T) {
	o := localGraphFixture(t)
	o.Packages = []string{"a", "later"}
	for _, node := range [][2]string{{"a", "b@1.0"}, {"b", "a@1.0"}, {"later", ""}} {
		graphInstalled(t, o.CacheRoot, packageIdentity{node[0], "1.0", o.OS, o.Arch}, node[1])
	}
	for range 2 {
		if _, err := prepare(o); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("expected cycle: %v", err)
		}
		if got := readTestFile(t, filepath.Join(graphSlot(o, "later"), "data")); got != "installed" {
			t.Fatal("changed later root")
		}
		if _, err := os.Stat(o.ResultPath); !os.IsNotExist(err) {
			t.Fatal("committed failed result", err)
		}
	}
	t.Run("case collision", func(t *testing.T) {
		o := localGraphFixture(t)
		o.Packages = []string{"app"}
		graphInstalled(t, o.CacheRoot, packageIdentity{"app", "1.0", o.OS, o.Arch}, "App@1.0")
		if _, err := prepare(o); err == nil || !strings.Contains(err.Error(), "case") {
			t.Fatalf("%v", err)
		}
	})
}

func TestUniversalLocalSlots(t *testing.T) {
	o := localGraphFixture(t)
	o.Packages = []string{"queue"}
	id := packageIdentity{"queue", "1.0", "any", "any"}
	graphInstalled(t, o.CacheRoot, id, "")
	first, err := prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	o.OS, o.Arch = "windows", "x86_64"
	second, err := prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Directory != second[0].Directory || first[0].Directory != filepath.Join(o.CacheRoot, "queue", "1.0") {
		t.Fatal("universal installation was not shared")
	}
	for _, p := range []preparedPackage{first[0], second[0]} {
		m, err := readPackageMetadata(p.Directory)
		if err != nil || m.Identity != id {
			t.Fatalf("%+v %v", m, err)
		}
	}
}
