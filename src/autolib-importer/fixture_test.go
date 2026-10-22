package main

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func localReleaseFixture(t *testing.T, names ...string) (prepareOptions, manifest) {
	t.Helper()
	root := t.TempDir()
	o := prepareOptions{URI: filepath.Join(root, "release"), WorkRoot: filepath.Join(root, "work"), ResultPath: filepath.Join(root, "result.cmake"), OS: "linux", Arch: "aarch64", Packages: names}
	m := manifest{Importers: []importerRecord{}, Packages: []packageRecord{}}
	for _, name := range names {
		id := packageIdentity{Name: name, OS: o.OS, Arch: o.Arch}
		a := testArchive(t, []archiveMember{{Name: name + "/autolib-import.cmake", Body: identityHeader(id) + "# adapter\n"}, {Name: name + "/data", Body: "installed"}})
		data, err := os.ReadFile(a)
		if err != nil {
			t.Fatal(err)
		}
		asset := "custom-" + name + ".tar.zst"
		putTestFile(t, filepath.Join(o.URI, asset), string(data))
		h := sha256.Sum256(data)
		m.Packages = append(m.Packages, packageRecord{Name: name, OS: o.OS, Arch: o.Arch, Asset: asset, SHA256: hex.EncodeToString(h[:])})
	}
	writeTestManifest(t, filepath.Join(o.URI, "manifest.json"), m)
	return o, m
}

func writeTestManifest(t *testing.T, path string, m manifest) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	putTestFile(t, path, string(data))
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
	return "set(AUTOLIB_PACKAGE_NAME \"" + id.Name + "\")\nset(AUTOLIB_OS \"" + id.OS + "\")\nset(AUTOLIB_ARCH \"" + id.Arch + "\")\n"
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
