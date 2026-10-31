package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGiteePrepare(t *testing.T) {
	for _, kind := range []string{"closure", "shared-snapshot", "digest", "missing-digest", "mixed", "download-error", "corrupt", "wrong-platform"} {
		t.Run(kind, func(t *testing.T) {
			f := newRemotePrepareFixture(t, "gitee.com")
			o := f.options
			digest := filepath.Join(f.root, "flann/1.0", f.flann+".sha256")
			switch kind {
			case "digest":
				f.assets[f.flann] = "changed"
			case "missing-digest":
				if err := os.Remove(digest); err != nil {
					t.Fatal(err)
				}
			case "mixed":
				putTestFile(t, filepath.Join(f.root, "flann/1.0/flann-1.0-any-any.tar.zst.sha256"), strings.Repeat("a", 64)+"\n")
			case "download-error":
				f.status[f.flann] = 404
			case "corrupt":
				f.assets[f.flann] = "invalid archive"
				f.digest(t, "flann", "1.0", f.flann)
			case "wrong-platform":
				archive := testArchive(t, []archiveMember{{Name: "flann/1.0/autolib-import.cmake", Body: identityHeader(packageIdentity{"flann", "1.0", "windows", "x86_64"})}})
				f.assets[f.flann] = readTestFile(t, archive)
				f.digest(t, "flann", "1.0", f.flann)
			}
			if kind == "shared-snapshot" {
				o.Packages = []string{"queue"}
			}
			putTestFile(t, o.ResultPath, "previous")
			got, err := prepare(o)
			if kind != "closure" && kind != "shared-snapshot" {
				if err == nil {
					t.Fatal("accepted bad source")
				}
				requireNoResult(t, o)
				requireNoPackageStaging(t, o.CacheRoot)
				if _, err := os.Stat(filepath.Join(o.CacheRoot, "flann/1.0")); !os.IsNotExist(err) {
					t.Fatal("committed unverified package", err)
				}
				want := map[string]int{f.flann: 1}
				if kind == "missing-digest" || kind == "mixed" {
					want = map[string]int{}
				}
				f.requireRequests(t, want)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "shared-snapshot" {
				o.Packages = []string{"flann"}
				more, nextErr := prepare(o)
				if nextErr != nil {
					t.Fatal(nextErr)
				}
				got = append(got, more...)
			}
			var names []string
			for _, p := range got {
				names = append(names, p.Name)
				if readTestFile(t, filepath.Join(p.Directory, "data")) != "archive" {
					t.Fatal("wrong downloaded bytes", p)
				}
			}
			expected := []string{"lz4", "flann", "queue"}
			if kind == "shared-snapshot" {
				expected = []string{"queue", "lz4", "flann"}
			}
			if !slices.Equal(names, expected) {
				t.Fatal(names)
			}
			want := map[string]int{f.flann: 1, f.lz4: 1, f.queue: 1}
			f.requireRequests(t, want)
			o.MetadataRoot = filepath.Join(f.root, "missing-but-unused")
			if _, err := prepare(o); err != nil {
				t.Fatal(err)
			}
			f.requireRequests(t, want)
			if strings.Contains(readTestFile(t, o.ResultPath), "previous") {
				t.Fatal("result not prepared")
			}
		})
	}
}
