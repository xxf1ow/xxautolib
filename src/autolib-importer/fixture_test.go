package main

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func localReleaseFixture(t *testing.T, names ...string) prepareOptions {
	t.Helper()
	o := localGraphFixture(t)
	o.Packages = names
	for _, name := range names {
		graphInstalled(t, o.CacheRoot, packageIdentity{name, "1.0", o.OS, o.Arch}, "")
	}
	return o
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func putTestFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

type archiveMember struct {
	Name, Body, Link string
	Kind             byte
}

func identityHeader(id packageIdentity) string {
	return "set(AUTOLIB_PACKAGE_NAME \"" + id.Name + "\")\nset(AUTOLIB_PACKAGE_VERSION \"" + id.Version + "\")\nset(AUTOLIB_OS \"" + id.OS + "\")\nset(AUTOLIB_ARCH \"" + id.Arch + "\")\nset(AUTOLIB_DEPENDENCIES \"\")\n"
}

func testArchive(t *testing.T, members []archiveMember) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.tar.zst")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zstd.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(z)
	for _, m := range members {
		kind := m.Kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		h := &tar.Header{Name: m.Name, Mode: 0755, Typeflag: kind, Linkname: m.Link}
		if kind == tar.TypeReg {
			h.Size = int64(len(m.Body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			if _, err := tw.Write([]byte(m.Body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, closer := range []interface{ Close() error }{tw, z, f} {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func installTestMembers(t *testing.T, root string, members []archiveMember) {
	t.Helper()
	for _, m := range members {
		if m.Kind == tar.TypeDir {
			if err := os.MkdirAll(filepath.Join(root, m.Name), 0755); err != nil {
				t.Fatal(err)
			}
		} else {
			putTestFile(t, filepath.Join(root, m.Name), m.Body)
		}
	}
}
func injectEntryRemote(t *testing.T, path, remote string) {
	t.Helper()
	body := readTestFile(t, path)
	// Fixtures inject only a copy of the source entry, matching release preparation.
	body = strings.Replace(body, `set(_autolib_release_root "")`, `set(_autolib_release_root "`+remote+`")`, 1)
	putTestFile(t, path, body)
}
