package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func localGraphFixture(t *testing.T) prepareOptions {
	t.Helper()
	root := t.TempDir()
	return prepareOptions{LocalRoot: filepath.Join(root, "local"), WorkRoot: filepath.Join(root, "work"), ResultPath: filepath.Join(root, "result.cmake"), OS: "linux", Arch: "aarch64"}
}

func graphArchive(t *testing.T, root string, id packageIdentity, dependencies string) string {
	t.Helper()
	header := strings.Replace(identityHeader(id), `set(AUTOLIB_DEPENDENCIES "")`, `set(AUTOLIB_DEPENDENCIES "`+dependencies+`")`, 1)
	archive := testArchive(t, []archiveMember{{Name: id.Name + "/autolib-import.cmake", Body: header}, {Name: id.Name + "/data", Body: "installed"}})
	return putTestFile(t, filepath.Join(root, id.Name+"-"+id.OS+"-"+id.Arch+".tar.zst"), readTestFile(t, archive))
}

func graphSlot(o prepareOptions, name string) string {
	return filepath.Join(o.LocalRoot, name)
}

func TestLocalCandidatePriority(t *testing.T) {
	for _, kind := range []string{"directory wins", "bad directory", "wrong platform directory", "old nested directory", "bad archive", "archive directory", "wrong identity"} {
		t.Run(kind, func(t *testing.T) {
			o := localGraphFixture(t)
			o.Packages = []string{"demo"}
			id := packageIdentity{"demo", o.OS, o.Arch}
			archive := graphArchive(t, o.LocalRoot, id, "")
			switch kind {
			case "directory wins":
				putTestFile(t, filepath.Join(graphSlot(o, "demo"), "autolib-import.cmake"), identityHeader(id))
				putTestFile(t, archive, "broken archive")
			case "bad directory":
				putTestFile(t, filepath.Join(graphSlot(o, "demo"), "autolib-import.cmake"), "invalid")
			case "wrong platform directory":
				putTestFile(t, filepath.Join(graphSlot(o, "demo"), "autolib-import.cmake"), identityHeader(packageIdentity{"demo", "windows", "x86_64"}))
			case "old nested directory":
				putTestFile(t, filepath.Join(o.LocalRoot, "packages/linux-aarch64/demo/autolib-import.cmake"), "invalid")
			case "bad archive":
				putTestFile(t, archive, "invalid")
			case "archive directory":
				if err := os.Rename(archive, archive+"-away"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(archive, 0755); err != nil {
					t.Fatal(err)
				}
			case "wrong identity":
				wrong := testArchive(t, []archiveMember{{Name: "demo/autolib-import.cmake", Body: identityHeader(packageIdentity{"other", o.OS, o.Arch})}})
				putTestFile(t, archive, readTestFile(t, wrong))
			}
			_, err := prepare(o)
			if (err == nil) != (kind == "directory wins" || kind == "old nested directory") {
				t.Fatalf("%s: %v", kind, err)
			}
			if kind == "wrong identity" {
				if _, err := os.Stat(graphSlot(o, "demo")); !os.IsNotExist(err) {
					t.Fatal("failed identity check polluted the final directory", err)
				}
			}
			if kind == "directory wins" {
				if readTestFile(t, archive) != "broken archive" {
					t.Fatal("shadowed archive changed")
				}
			}
		})
	}
}

func TestPrepareDependencyOrder(t *testing.T) {
	o := localGraphFixture(t)
	o.Packages = []string{"app", "tool", "app"}
	for _, node := range [][2]string{{"app", "beta;alpha;beta"}, {"alpha", "core"}, {"beta", "core"}, {"tool", "core"}, {"core", ""}} {
		graphArchive(t, o.LocalRoot, packageIdentity{node[0], o.OS, o.Arch}, node[1])
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
	entries, err := os.ReadDir(filepath.Dir(graphSlot(o, "core")))
	if err != nil || len(entries) != 10 {
		t.Fatalf("%v %v", entries, err)
	}
	for _, p := range got {
		if err := os.Rename(filepath.Join(o.LocalRoot, p.Name+"-linux-aarch64.tar.zst"), filepath.Join(o.LocalRoot, p.Name+"-away")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := prepare(o); err != nil {
		t.Fatal("read-only reuse:", err)
	}
}

func TestPrepareDependencyCycle(t *testing.T) {
	o := localGraphFixture(t)
	o.Packages = []string{"a", "later"}
	for _, node := range [][2]string{{"a", "b"}, {"b", "a"}, {"later", ""}} {
		graphArchive(t, o.LocalRoot, packageIdentity{node[0], o.OS, o.Arch}, node[1])
	}
	for range 2 {
		if _, err := prepare(o); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("expected cycle: %v", err)
		}
		for _, name := range []string{"a", "b"} {
			if readTestFile(t, filepath.Join(graphSlot(o, name), "data")) != "installed" {
				t.Fatal(name)
			}
		}
		if _, err := os.Stat(graphSlot(o, "later")); !os.IsNotExist(err) {
			t.Fatal("visited later root", err)
		}
		if _, err := os.Stat(o.ResultPath); !os.IsNotExist(err) {
			t.Fatal("committed failed result", err)
		}
	}
	t.Run("case collision", func(t *testing.T) {
		o := localGraphFixture(t)
		o.Packages = []string{"app"}
		graphArchive(t, o.LocalRoot, packageIdentity{"app", o.OS, o.Arch}, "App")
		if _, err := prepare(o); err == nil || !strings.Contains(err.Error(), "case") {
			t.Fatalf("%v", err)
		}
	})
}

func TestUniversalLocalSlots(t *testing.T) {
	o := localGraphFixture(t)
	o.Packages = []string{"queue"}
	id := packageIdentity{"queue", "any", "any"}
	graphArchive(t, o.LocalRoot, id, "")
	first, err := prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	o.OS, o.Arch = "windows", "x86_64"
	second, err := prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Directory != second[0].Directory || first[0].Directory != filepath.Join(o.LocalRoot, "queue") {
		t.Fatal("universal installation was not shared")
	}
	for _, p := range []preparedPackage{first[0], second[0]} {
		m, err := readPackageMetadata(p.Directory)
		if err != nil || m.Identity != id {
			t.Fatalf("%+v %v", m, err)
		}
	}
	o.OS, o.Arch = "linux", "x86_64"
	graphArchive(t, o.LocalRoot, packageIdentity{"queue", "windows", "aarch64"}, "")
	if _, err := prepare(o); err != nil {
		t.Fatal("unrelated platform conflicted:", err)
	}
	graphArchive(t, o.LocalRoot, packageIdentity{"queue", "linux", "aarch64"}, "")
	// Only a missing directory consults competing archive slots.
	o.Arch = "aarch64"
	if err := os.Rename(first[0].Directory, first[0].Directory+"-away"); err != nil {
		t.Fatal(err)
	}
	if _, err := prepare(o); err == nil {
		t.Fatal("accepted universal/platform archive ambiguity")
	}
}

func TestPrepareArchiveFailure(t *testing.T) {
	for _, kind := range []string{"corrupt", "truncated", "no checksum"} {
		t.Run(kind, func(t *testing.T) {
			o := localGraphFixture(t)
			o.Packages = []string{"sentinel", "demo"}
			graphArchive(t, o.LocalRoot, packageIdentity{"sentinel", o.OS, o.Arch}, "")
			archive := graphArchive(t, o.LocalRoot, packageIdentity{"demo", o.OS, o.Arch}, "")
			data := []byte(readTestFile(t, archive))
			if kind == "corrupt" {
				data[len(data)-1] ^= 1
			}
			if kind == "truncated" {
				data = data[:len(data)-1]
			}
			if kind == "no checksum" {
				d, err := zstd.NewReader(nil)
				if err != nil {
					t.Fatal(err)
				}
				plain, err := d.DecodeAll(data, nil)
				d.Close()
				if err != nil {
					t.Fatal(err)
				}
				e, err := zstd.NewWriter(nil, zstd.WithEncoderCRC(false))
				if err != nil {
					t.Fatal(err)
				}
				data = e.EncodeAll(plain, nil)
				e.Close()
			}
			putTestFile(t, archive, string(data))
			_, err := prepare(o)
			if (err == nil) != (kind == "no checksum") {
				t.Fatalf("%v", err)
			}
			if readTestFile(t, filepath.Join(graphSlot(o, "sentinel"), "data")) != "installed" {
				t.Fatal("damaged successful package")
			}
			entries, err := os.ReadDir(filepath.Dir(graphSlot(o, "demo")))
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") {
					t.Fatal("staging leaked", e.Name())
				}
			}
			if kind != "no checksum" {
				if _, err := os.Stat(graphSlot(o, "demo")); !os.IsNotExist(err) {
					t.Fatal("committed corrupt package", err)
				}
			}
		})
	}
}
