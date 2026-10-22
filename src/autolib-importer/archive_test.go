package main

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractPackage(t *testing.T) {
	id := packageIdentity{Name: "lz4", OS: "linux", Arch: "aarch64"}
	large := strings.Repeat("stream bytes\n", 30000)
	members := []archiveMember{{Name: "lz4/", Kind: tar.TypeDir}, {Name: "lz4/autolib-import.cmake", Body: identityHeader(id)}, {Name: "lz4/lib/", Kind: tar.TypeDir}, {Name: "lz4/lib/", Kind: tar.TypeDir}, {Name: "lz4/lib/data", Body: large}, {Name: "lz4/empty"}}
	parent := t.TempDir()
	dir, err := extractPackage(testArchive(t, members), parent, id)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "lz4" {
		t.Fatal(dir)
	}
	if got := readTestFile(t, filepath.Join(dir, "lib/data")); got != large {
		t.Fatal("streamed data differs")
	}
	if got := readTestFile(t, filepath.Join(dir, "empty")); got != "" {
		t.Fatal(got)
	}
	for _, bad := range []packageIdentity{{Name: "other", OS: id.OS, Arch: id.Arch}, {Name: id.Name, OS: "windows", Arch: id.Arch}, {Name: id.Name, OS: id.OS, Arch: "x86_64"}} {
		if _, err := extractPackage(testArchive(t, members), parent, bad); err == nil {
			t.Fatalf("accepted wrong identity: %#v", bad)
		}
	}
	t.Run("internal links", func(t *testing.T) {
		probe := filepath.Join(t.TempDir(), "probe")
		if err := os.Symlink("target", probe); err != nil {
			t.Skipf("host cannot create symlinks: %v", err)
		}
		linked := append(append([]archiveMember{}, members...), archiveMember{Name: "lz4/lib/liblz4.so.1", Link: "data", Kind: tar.TypeSymlink}, archiveMember{Name: "lz4/lib/liblz4.so", Link: "liblz4.so.1", Kind: tar.TypeSymlink})
		dir, err := extractPackage(testArchive(t, linked), parent, id)
		if err != nil {
			t.Fatal(err)
		}
		if got := readTestFile(t, filepath.Join(dir, "lib/liblz4.so")); got != large {
			t.Fatal("internal link lost")
		}
		if link, err := os.Readlink(filepath.Join(dir, "lib/liblz4.so")); err != nil || link != "liblz4.so.1" {
			t.Fatalf("%q %v", link, err)
		}
	})
}

func TestRejectUnsafeArchive(t *testing.T) {
	id := packageIdentity{Name: "lz4", OS: "linux", Arch: "aarch64"}
	cases := map[string][]archiveMember{
		"parent escape":      {{Name: "lz4/../../outside/sentinel", Body: "overwritten"}},
		"absolute":           {{Name: "/outside/sentinel", Body: "overwritten"}},
		"drive":              {{Name: "C:/outside/sentinel", Body: "overwritten"}},
		"unc":                {{Name: `\\host\share\file`, Body: "overwritten"}},
		"extra top":          {{Name: "other/file", Body: "bad"}},
		"duplicate":          {{Name: "lz4/a", Body: "a"}, {Name: "lz4/sub/../a", Body: "b"}},
		"file directory":     {{Name: "lz4/a", Body: "a"}, {Name: "lz4/a/", Kind: tar.TypeDir}},
		"implicit directory": {{Name: "lz4/a/file", Body: "a"}, {Name: "lz4/a", Body: "b"}},
		"link ancestor":      {{Name: "lz4/a", Link: ".", Kind: tar.TypeSymlink}, {Name: "lz4/a/file", Body: "b"}},
		"late link ancestor": {{Name: "lz4/a/file", Body: "b"}, {Name: "lz4/a", Link: ".", Kind: tar.TypeSymlink}},
		"link escape":        {{Name: "lz4/a", Link: "../../outside/sentinel", Kind: tar.TypeSymlink}},
		"absolute link":      {{Name: "lz4/a", Link: "/outside/sentinel", Kind: tar.TypeSymlink}},
		"link cycle":         {{Name: "lz4/a", Link: "b", Kind: tar.TypeSymlink}, {Name: "lz4/b", Link: "a", Kind: tar.TypeSymlink}},
		"parent after link":  {{Name: "lz4/a", Link: ".", Kind: tar.TypeSymlink}, {Name: "lz4/b", Link: "a/../outside", Kind: tar.TypeSymlink}},
		"hardlink":           {{Name: "lz4/a", Link: "autolib-import.cmake", Kind: tar.TypeLink}},
		"device":             {{Name: "lz4/a", Kind: tar.TypeChar}},
		"fifo":               {{Name: "lz4/a", Kind: tar.TypeFifo}},
	}
	for label, members := range cases {
		t.Run(label, func(t *testing.T) {
			root := t.TempDir()
			sentinel := putTestFile(t, filepath.Join(root, "outside/sentinel"), "preserve")
			staging := filepath.Join(root, "staging")
			if err := os.Mkdir(staging, 0755); err != nil {
				t.Fatal(err)
			}
			members = append([]archiveMember{{Name: "lz4/autolib-import.cmake", Body: identityHeader(id)}}, members...)
			if _, err := extractPackage(testArchive(t, members), staging, id); err == nil {
				t.Fatal("accepted unsafe archive")
			}
			if got := readTestFile(t, sentinel); got != "preserve" {
				t.Fatal("outside data changed")
			}
			entries, err := os.ReadDir(staging)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed extraction left staging: %v %v", entries, err)
			}
		})
	}
}
