package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type remotePrepareFixture struct {
	*versionedRemoteFixture
	options           prepareOptions
	flann, lz4, queue string
}

func newRemotePrepareFixture(t *testing.T, hosts ...string) *remotePrepareFixture {
	t.Helper()
	remote := versionedRemote(t, hosts...)
	o, _ := versionedFixture(t, "flann", "queue")
	o.RemoteRoot = "https://" + remote.host + "/owner/repo/releases/download/v1"
	o.MetadataRoot = remote.root
	f := &remotePrepareFixture{versionedRemoteFixture: remote, options: o}
	f.flann = f.add(t, "flann", "1.0", "lz4@1.0")
	f.lz4 = f.add(t, "lz4", "1.0", "")
	archive := testArchive(t, []archiveMember{{Name: "queue/1.0/autolib-import.cmake", Body: identityHeader(packageIdentity{"queue", "1.0", "any", "any"})}, {Name: "queue/1.0/data", Body: "archive"}})
	f.queue = "queue-1.0-any-any.tar.zst"
	f.assets[f.queue] = readTestFile(t, archive)
	f.digest(t, "queue", "1.0", f.queue)
	versionedLatest(t, f.root, "queue", "1.0")
	return f
}

func (f *remotePrepareFixture) digest(t *testing.T, name, version, asset string) string {
	t.Helper()
	return putTestFile(t, filepath.Join(f.root, name, version, asset+".sha256"), fmt.Sprintf("%x\n", sha256.Sum256([]byte(f.assets[asset]))))
}

func TestPrepareRemote(t *testing.T) {
	for _, kind := range []string{"shared", "existing root", "cycle"} {
		t.Run(kind, func(t *testing.T) {
			f := newRemotePrepareFixture(t)
			o := f.options
			o.Packages = []string{"flann", "queue", "flann"}
			want := map[string]int{f.flann: 1, f.lz4: 1, f.queue: 1}
			if kind == "existing root" {
				versionedInstalled(t, o.CacheRoot, "flann", "1.0", "lz4@1.0", "user content")
				versionedLatest(t, o.CacheRoot, "flann", "1.0")
				delete(want, f.flann)
			}
			if kind == "cycle" {
				f.add(t, "lz4", "1.0", "flann@1.0")
			}
			got, err := prepare(o)
			if kind == "cycle" {
				if err == nil || !strings.Contains(err.Error(), "cycle") {
					t.Fatalf("cycle did not stop traversal: %v", err)
				}
				f.requireRequests(t, map[string]int{f.flann: 1, f.lz4: 1})
				requireNoResult(t, o)
				requireNoPackageStaging(t, o.CacheRoot)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, p := range got {
				names = append(names, p.Name)
				if p.Version != "1.0" {
					t.Fatal(p)
				}
			}
			if !slices.Equal(names, []string{"lz4", "flann", "queue"}) {
				t.Fatal(names)
			}
			if kind == "existing root" && readTestFile(t, filepath.Join(o.CacheRoot, "flann/1.0/data")) != "user content" {
				t.Fatal("changed cached input")
			}
			f.requireRequests(t, want)
			o.Packages = []string{"queue"}
			o.MetadataRoot = filepath.Join(f.root, "unused-missing-snapshot")
			if _, err := prepare(o); err != nil {
				t.Fatal(err)
			}
			f.requireRequests(t, want)
		})
	}
}

func TestPrepareSourcePriority(t *testing.T) {
	for _, kind := range []string{"directory", "archive", "bad directory", "bad archive"} {
		t.Run(kind, func(t *testing.T) {
			f := newRemotePrepareFixture(t)
			o := f.options
			o.Packages = []string{"queue@1.0"}
			switch kind {
			case "directory", "bad directory":
				dir := versionedInstalled(t, o.LocalRoot, "queue", "1.0", "", "user content")
				if kind == "bad directory" {
					putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), "invalid")
				}
			case "archive", "bad archive":
				archive := versionedArchive(t, o.LocalRoot, "queue", "1.0", "")
				if kind == "bad archive" {
					putTestFile(t, archive, "invalid")
				}
			}
			o.RemoteRoot, o.MetadataRoot = "invalid but unused", filepath.Join(f.root, "missing")
			before := sourceSnapshot(t, o.LocalRoot)
			_, err := prepare(o)
			if (err == nil) != (kind == "directory" || kind == "archive") {
				t.Fatalf("priority %s: %v", kind, err)
			}
			if !slices.Equal(before, sourceSnapshot(t, o.LocalRoot)) {
				t.Fatal("changed local source")
			}
			f.requireRequests(t, map[string]int{})
		})
	}
}

func TestPrepareRemoteFailureCleanup(t *testing.T) {
	for _, kind := range []string{"digest", "corrupt", "deleted", "replaced", "write failure", "snapshot"} {
		t.Run(kind, func(t *testing.T) {
			f := newRemotePrepareFixture(t)
			o := f.options
			o.Packages = []string{"queue", "flann", "later"}
			putTestFile(t, o.ResultPath, "previous result")
			want := map[string]int{f.queue: 1, f.flann: 1}
			switch kind {
			case "digest":
				f.assets[f.flann] = "changed"
			case "corrupt":
				f.assets[f.flann] = "not an archive"
				f.digest(t, "flann", "1.0", f.flann)
			case "deleted", "replaced":
				f.status[f.flann] = 404
			case "write failure":
				o.Packages = []string{"queue"}
				putTestFile(t, filepath.Join(o.CacheRoot, "queue"), "not a directory")
				want = map[string]int{}
			case "snapshot":
				o.MetadataRoot = putTestFile(t, filepath.Join(f.root, "invalid-snapshot"), "not a directory")
				want = map[string]int{}
			}
			if _, err := prepare(o); err == nil {
				t.Fatal("accepted remote failure")
			}
			requireNoResult(t, o)
			requireNoPackageStaging(t, o.CacheRoot)
			if kind != "write failure" && kind != "snapshot" {
				if readTestFile(t, filepath.Join(o.CacheRoot, "queue/1.0/data")) != "archive" {
					t.Fatal("lost successful package")
				}
				if _, err := os.Stat(filepath.Join(o.CacheRoot, "flann/1.0")); !os.IsNotExist(err) {
					t.Fatal("committed failed package", err)
				}
			}
			f.requireRequests(t, want)
		})
	}
}

func TestPrepareRemoteOnly(t *testing.T) {
	f := newRemotePrepareFixture(t)
	o := f.options
	o.LocalRoot = ""
	o.Packages = []string{"flann"}
	putTestFile(t, filepath.Join(o.WorkRoot, "flann/1.0/autolib-import.cmake"), "invalid undeclared cache")
	got, err := prepare(o)
	if err != nil || len(got) != 2 || got[0].Name != "lz4" || got[1].Name != "flann" {
		t.Fatalf("remote closure %+v: %v", got, err)
	}
	for _, p := range got {
		if !strings.HasPrefix(p.Directory, o.CacheRoot+string(filepath.Separator)) || readTestFile(t, filepath.Join(p.Directory, "data")) != "archive" {
			t.Fatal(p)
		}
	}
	o.MetadataRoot = ""
	if _, err := prepare(o); err != nil {
		t.Fatal(err)
	}
	f.requireRequests(t, map[string]int{f.flann: 1, f.lz4: 1})
}

func TestPrepareRemoteSnapshotReuse(t *testing.T) {
	f := newRemotePrepareFixture(t)
	o := f.options
	o.Packages = []string{"queue"}
	if _, err := prepare(o); err != nil {
		t.Fatal(err)
	}
	o.Packages = []string{"flann"}
	if _, err := prepare(o); err != nil {
		t.Fatal(err)
	}
	f.requireRequests(t, map[string]int{f.queue: 1, f.flann: 1, f.lz4: 1})
}
