package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestUniversalIdentity(t *testing.T) {
	actual := packageIdentity{Name: "queue", OS: "any", Arch: "any"}
	for _, requested := range []packageIdentity{
		{Name: "queue", OS: "windows", Arch: "x86_64"},
		{Name: "queue", OS: "linux", Arch: "x86_64"},
		{Name: "queue", OS: "linux", Arch: "aarch64"},
	} {
		if !matchesPackageIdentity(actual, requested) {
			t.Fatalf("universal identity did not match %+v", requested)
		}
	}
	for _, requested := range []packageIdentity{
		{Name: "other", OS: "linux", Arch: "x86_64"},
		{Name: "queue", OS: "linux", Arch: "mips"},
		{Name: "queue", OS: "linux", Arch: "any"},
	} {
		if matchesPackageIdentity(actual, requested) {
			t.Fatalf("universal identity matched unsupported request %+v", requested)
		}
	}
}

func TestUniversalEligibility(t *testing.T) {
	if err := validatePlatform("any", "any"); err == nil {
		t.Fatal("universal identity became a supported target platform")
	}
	if err := validateIdentity(packageIdentity{Name: "queue", OS: "any", Arch: "linux"}); err == nil {
		t.Fatal("accepted half-universal identity")
	}
	tool := importerRecord{OS: "any", Arch: "any", Asset: "tool", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	path := filepath.Join(t.TempDir(), "manifest.json")
	writeTestManifest(t, path, manifest{Importers: []importerRecord{tool}, Packages: []packageRecord{}})
	if _, err := readManifest(path); err == nil {
		t.Fatal("accepted universal importer identity")
	}
}

func TestUniversalOfflineCache(t *testing.T) {
	var requests atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer trap.Close()
	t.Setenv("HTTP_PROXY", trap.URL)
	t.Setenv("HTTPS_PROXY", trap.URL)
	root := t.TempDir()
	o := prepareOptions{URI: filepath.Join(root, "release"), WorkRoot: filepath.Join(root, "work"), ResultPath: filepath.Join(root, "result.cmake"), OS: "linux", Arch: "aarch64", Packages: []string{"queue"}}
	id := packageIdentity{Name: "queue", OS: "any", Arch: "any"}
	archive := testArchive(t, []archiveMember{{Name: "queue/autolib-import.cmake", Body: identityHeader(id) + "# adapter\n"}, {Name: "queue/include/queue/readerwriterqueue.h", Body: "fixed header"}})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	asset := "queue-any-any.tar.zst"
	putTestFile(t, filepath.Join(o.URI, asset), string(data))
	digest := testDigest(data)
	selected := packageRecord{Name: "queue", OS: "any", Arch: "any", Asset: asset, SHA256: digest}
	writeTestManifest(t, filepath.Join(o.URI, "manifest.json"), manifest{Importers: []importerRecord{}, Packages: []packageRecord{selected}})
	prepared, err := prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, filepath.Join(prepared[0].Directory, "autolib-import.cmake")); got != identityHeader(id)+"# adapter\n" {
		t.Fatalf("wrong cached identity: %q", got)
	}
	if err := os.Rename(o.URI, o.URI+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	if _, err := prepare(o); err != nil {
		t.Fatalf("offline cache reuse consulted unavailable source: %v", err)
	}
	if err := os.RemoveAll(prepared[0].Directory); err != nil {
		t.Fatal(err)
	}
	wrong := packageIdentity{Name: "queue", OS: "windows", Arch: "x86_64"}
	wrongArchive := testArchive(t, []archiveMember{{Name: "queue/autolib-import.cmake", Body: identityHeader(wrong)}, {Name: "queue/data", Body: "wrong platform"}})
	wrongBytes, err := os.ReadFile(wrongArchive)
	if err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(o.URI, asset), string(wrongBytes))
	selected.SHA256 = testDigest(wrongBytes)
	writeTestManifest(t, filepath.Join(o.URI, "manifest.json"), manifest{Importers: []importerRecord{}, Packages: []packageRecord{selected}})
	if _, err := prepare(o); err == nil {
		t.Fatal("accepted wrong-platform package identity")
	}
	if _, err := os.Stat(prepared[0].Directory); !os.IsNotExist(err) {
		t.Fatalf("failed import replaced destination unexpectedly: %v", err)
	}
	putTestFile(t, filepath.Join(prepared[0].Directory, "autolib-import.cmake"), identityHeader(wrong))
	putTestFile(t, filepath.Join(prepared[0].Directory, "local-change"), "preserve cache")
	if _, err := prepare(o); err == nil {
		t.Fatal("accepted mismatched existing cache")
	}
	if got := readTestFile(t, filepath.Join(prepared[0].Directory, "local-change")); got != "preserve cache" {
		t.Fatal("failed import changed existing cache")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("local import made %d network requests", got)
	}
}

func TestUniversalSlotConflict(t *testing.T) {
	base := packageRecord{Name: "queue", OS: "any", Arch: "any", Asset: "queue-any-any.tar.zst", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	for _, records := range [][]packageRecord{
		{{Name: "queue", OS: "any", Arch: "linux", Asset: "queue-any-linux.tar.zst", SHA256: base.SHA256}},
		{base, {Name: "queue", OS: "linux", Arch: "x86_64", Asset: "queue-linux-x86_64.tar.zst", SHA256: base.SHA256}},
	} {
		m := manifest{Importers: []importerRecord{}, Packages: records}
		path := filepath.Join(t.TempDir(), "manifest.json")
		writeTestManifest(t, path, m)
		if _, err := readManifest(path); err == nil {
			t.Fatalf("accepted conflicting package slots: %+v", records)
		}
	}
}
