package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLocalSource(t *testing.T) {
	want, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	got, err := normalizeLocalSource("./folder/..")
	if err != nil || got != filepath.ToSlash(want) {
		t.Fatalf("%q %v", got, err)
	}
	for _, uri := range []string{"", "https://host/root", "http://host/root", "file:///root", "ssh://host/root"} {
		if _, err := normalizeLocalSource(uri); err == nil {
			t.Fatalf("accepted %q", uri)
		}
	}
	if runtime.GOOS == "windows" {
		for input, want := range map[string]string{`C:\release\folder\..`: `C:/release`, `\\host\share\folder\..`: `//host/share/`} {
			got, err := normalizeLocalSource(input)
			if err != nil || strings.TrimSuffix(got, "/") != strings.TrimSuffix(want, "/") {
				t.Fatalf("%q: %q %v", input, got, err)
			}
		}
	}
	if got := sourceKey("/tmp/release"); got != "52c4bf853c6fb38d1cba37946f606eec34ae56a4662b1ba434f4a1e3f2bfaf1b" {
		t.Fatal(got)
	}
	a := packageDirectory(want, "source A", packageIdentity{Name: "lz4", OS: "linux", Arch: "aarch64"})
	b := packageDirectory(want, "source B", packageIdentity{Name: "lz4", OS: "linux", Arch: "aarch64"})
	c := packageDirectory(want, "source A", packageIdentity{Name: "lz4", OS: "windows", Arch: "x86_64"})
	if a == b || a == c || filepath.Base(a) != "lz4" || filepath.Base(filepath.Dir(a)) != "linux-aarch64" {
		t.Fatal(a, b, c)
	}
}

func TestPackageIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "autolib-import.cmake")
	head := "set(AUTOLIB_PACKAGE_NAME \"lz4\")\nset(AUTOLIB_OS \"linux\")\nset(AUTOLIB_ARCH \"aarch64\")\n"
	if err := os.WriteFile(path, []byte(head+"message(FATAL_ERROR \"must not execute\")\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readPackageIdentity(dir)
	if err != nil || got != (packageIdentity{Name: "lz4", OS: "linux", Arch: "aarch64"}) {
		t.Fatalf("%#v %v", got, err)
	}
	for _, data := range []string{"", strings.Replace(head, "lz4", "../escape", 1), strings.Replace(head, "linux", "any", 1), strings.Replace(head, "set(AUTOLIB_OS", "set(OTHER", 1), strings.Replace(head, `"lz4"`, `"${execute}"`, 1)} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := readPackageIdentity(dir); err == nil || !strings.Contains(err.Error(), dir) {
			t.Fatalf("accepted invalid header %q: %v", data, err)
		}
	}
}
