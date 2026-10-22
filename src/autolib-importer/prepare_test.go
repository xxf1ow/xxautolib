package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPrepareLocal(t *testing.T) {
	o, _ := localReleaseFixture(t, "queue", "lz4")
	before := readTestFile(t, filepath.Join(o.URI, "manifest.json"))
	o.Packages = []string{"queue", "lz4", "queue"}
	got, err := prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "queue" || got[1].Name != "lz4" {
		t.Fatal(got)
	}
	for _, p := range got {
		if readTestFile(t, filepath.Join(p.Directory, "data")) != "installed" {
			t.Fatal(p)
		}
	}
	if readTestFile(t, filepath.Join(o.URI, "manifest.json")) != before {
		t.Fatal("source changed")
	}
	if _, err := os.Stat(o.ResultPath); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareReuse(t *testing.T) {
	o, m := localReleaseFixture(t, "queue", "lz4")
	first, err := prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(first[0].Directory, "data"), "local authority")
	for _, asset := range []string{"manifest.json", m.Packages[0].Asset, m.Packages[1].Asset} {
		if err := os.Rename(filepath.Join(o.URI, asset), filepath.Join(o.URI, asset+"-away")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := prepare(o); err != nil {
		t.Fatal("reuse read missing source:", err)
	}
	if err := os.RemoveAll(first[1].Directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(o.URI, m.Packages[1].Asset+"-away"), filepath.Join(o.URI, m.Packages[1].Asset)); err != nil {
		t.Fatal(err)
	}
	explicit := filepath.Join(t.TempDir(), "current.json")
	writeTestManifest(t, explicit, m)
	o.ManifestPath = explicit
	if _, err := prepare(o); err != nil {
		t.Fatal("failed explicit manifest reuse:", err)
	}
	if readTestFile(t, filepath.Join(first[0].Directory, "data")) != "local authority" {
		t.Fatal("existing package replaced")
	}
	if err := os.RemoveAll(first[1].Directory); err != nil {
		t.Fatal(err)
	}
	o.ManifestPath = ""
	if _, err := prepare(o); err == nil {
		t.Fatal("used a historical manifest for missing package")
	}
}

func TestPrepareFailures(t *testing.T) {
	o, m := localReleaseFixture(t, "queue", "lz4")
	putTestFile(t, o.ResultPath, "previous result")
	m.Packages[1].SHA256 = strings.Repeat("0", 64)
	writeTestManifest(t, filepath.Join(o.URI, "manifest.json"), m)
	if _, err := prepare(o); err == nil {
		t.Fatal("accepted checksum mismatch")
	}
	source, err := normalizeLocalSource(o.URI)
	if err != nil {
		t.Fatal(err)
	}
	first := packageDirectory(o.WorkRoot, source, packageIdentity{Name: "queue", OS: o.OS, Arch: o.Arch})
	if readTestFile(t, filepath.Join(first, "data")) != "installed" || readTestFile(t, o.ResultPath) != "previous result" {
		t.Fatal("failed prepare damaged data")
	}
	data, err := os.ReadFile(filepath.Join(o.URI, m.Packages[1].Asset))
	if err != nil {
		t.Fatal(err)
	}
	m.Packages[1].SHA256 = testDigest(data)
	writeTestManifest(t, filepath.Join(o.URI, "manifest.json"), m)
	if _, err := prepare(o); err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(first, "autolib-import.cmake"), identityHeader(packageIdentity{Name: "wrong", OS: o.OS, Arch: o.Arch}))
	if _, err := prepare(o); err == nil {
		t.Fatal("replaced mismatched existing package")
	}
	if id, err := readPackageIdentity(first); err != nil || id.Name != "wrong" {
		t.Fatal("existing package changed", err)
	}
	t.Run("no network", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(404) }))
		defer server.Close()
		t.Setenv("HTTP_PROXY", server.URL)
		t.Setenv("HTTPS_PROXY", server.URL)
		o, m := localReleaseFixture(t, "demo")
		for _, uri := range []string{server.URL, "https://127.0.0.1:1/release"} {
			bad := o
			bad.URI = uri
			if _, err := prepare(bad); err == nil {
				t.Fatal("accepted network source")
			}
		}
		m.Packages[0].Asset = server.URL + "/archive"
		writeTestManifest(t, filepath.Join(o.URI, "manifest.json"), m)
		if _, err := prepare(o); err == nil {
			t.Fatal("accepted URL asset")
		}
		m.Packages[0].Asset = "missing.tar.zst"
		writeTestManifest(t, filepath.Join(o.URI, "manifest.json"), m)
		if _, err := prepare(o); err == nil {
			t.Fatal("accepted missing local archive")
		}
		if requests.Load() != 0 {
			t.Fatal("local mode made network requests")
		}
	})
}

func TestPreparedResult(t *testing.T) {
	root := t.TempDir()
	want := filepath.ToSlash(filepath.Join(root, "中文 空格;${not_a_variable} ]=] ]==]"))
	result := filepath.Join(root, "result.cmake")
	if err := writePreparedResult(result, []preparedPackage{{Name: "demo", Directory: want}}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "observed")
	script := putTestFile(t, filepath.Join(root, "check.cmake"), "include(\""+filepath.ToSlash(result)+"\")\nfile(WRITE \""+filepath.ToSlash(out)+"\" \"${AUTOLIB_PACKAGE_DIR_demo}\")\nif(NOT AUTOLIB_PREPARED_PACKAGES STREQUAL \"demo\")\nmessage(FATAL_ERROR \"wrong packages\")\nendif()\n")
	if output, err := exec.Command("cmake", "-P", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if got := readTestFile(t, out); got != want {
		t.Fatalf("%q != %q", got, want)
	}
}
