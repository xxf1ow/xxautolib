package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestPackageMetadata(t *testing.T) {
	head := "set(AUTOLIB_PACKAGE_NAME \"flann\")\nset(AUTOLIB_PACKAGE_VERSION \"1.0\")\nset(AUTOLIB_OS \"linux\")\nset(AUTOLIB_ARCH \"aarch64\")\nset(AUTOLIB_DEPENDENCIES \"zlib@1.3.1;lz4@1.10.0;zlib@1.3.1\")\n"
	for _, tc := range []struct {
		name, data string
		valid      bool
	}{
		{"valid", head + "message(FATAL_ERROR \"not executed\")\n", true},
		{"crlf", strings.ReplaceAll(head, "\n", "\r\n"), true},
		{"three lines", strings.Join(strings.Split(head, "\n")[:3], "\n"), false},
		{"order", strings.Replace(head, "AUTOLIB_OS", "AUTOLIB_ARCH", 1), false},
		{"expression", strings.Replace(head, "zlib@1.3.1;lz4@1.10.0;zlib@1.3.1", "${packages}", 1), false},
		{"empty item", strings.Replace(head, "zlib@1.3.1;lz4@1.10.0;zlib@1.3.1", "lz4@1.10.0;;zlib@1.3.1", 1), false},
		{"half universal", strings.Replace(head, "linux", "any", 1), false},
		{"reserved", strings.Replace(head, "flann", "CON.txt", 1), false},
		{"trailing dot", strings.Replace(head, "flann", "flann.", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "flann", "1.0")
			putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), tc.data)
			got, err := readPackageMetadata(dir)
			if !tc.valid {
				if err == nil {
					t.Fatal("accepted invalid metadata")
				}
				return
			}
			if err != nil || got.Identity != (packageIdentity{"flann", "1.0", "linux", "aarch64"}) || !slices.Equal(got.Dependencies, []string{"lz4@1.10.0", "zlib@1.3.1"}) {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

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
	a := packageDirectory(want, packageIdentity{Name: "lz4", Version: "1.0", OS: "linux", Arch: "aarch64"})
	c := packageDirectory(want, packageIdentity{Name: "lz4", Version: "1.0", OS: "windows", Arch: "x86_64"})
	if a != c || a != filepath.Join(want, "lz4", "1.0") {
		t.Fatal(a, c)
	}
}

func TestPackageIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lz4", "1.0")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "autolib-import.cmake")
	head := identityHeader(packageIdentity{"lz4", "1.0", "linux", "aarch64"})
	if err := os.WriteFile(path, []byte(head+"message(FATAL_ERROR \"must not execute\")\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readPackageMetadata(dir)
	if err != nil || got.Identity != (packageIdentity{Name: "lz4", Version: "1.0", OS: "linux", Arch: "aarch64"}) {
		t.Fatalf("%#v %v", got, err)
	}
	for _, data := range []string{"", strings.Replace(head, "lz4", "../escape", 1), strings.Replace(head, "linux", "any", 1), strings.Replace(head, "set(AUTOLIB_OS", "set(OTHER", 1), strings.Replace(head, `"lz4"`, `"${execute}"`, 1)} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := readPackageMetadata(dir); err == nil || !strings.Contains(err.Error(), dir) {
			t.Fatalf("accepted invalid header %q: %v", data, err)
		}
	}
}
