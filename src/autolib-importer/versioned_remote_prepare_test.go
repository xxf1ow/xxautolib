package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const versionedDownloadRoot = "https://github.com/owner/repo/releases/download/v1"

type versionedRemoteFixture struct {
	root     string
	host     string
	mu       sync.Mutex
	assets   map[string]string
	status   map[string]int
	requests map[string]int
}

func versionedRemote(t *testing.T, hosts ...string) *versionedRemoteFixture {
	t.Helper()
	host := "github.com"
	if len(hosts) != 0 {
		host = hosts[0]
	}
	f := &versionedRemoteFixture{host: host, root: t.TempDir(), assets: map[string]string{}, status: map[string]int{}, requests: map[string]int{}}
	client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests[r.URL.Path]++
		if r.Header.Get("Authorization") != "" {
			t.Error("consumer sent credentials")
		}
		if r.Host != host || !strings.HasPrefix(r.URL.Path, "/owner/repo/releases/download/v1/") {
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(500)
			return
		}
		name := filepath.Base(r.URL.Path)
		if status := f.status[name]; status != 0 {
			w.WriteHeader(status)
			return
		}
		body, ok := f.assets[name]
		if !ok {
			w.WriteHeader(404)
			return
		}
		fmt.Fprint(w, body)
	}))
	previous := http.DefaultTransport
	http.DefaultTransport = client.Transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	return f
}

func (f *versionedRemoteFixture) add(t *testing.T, name, version, dependencies string) string {
	t.Helper()
	archive := versionedArchive(t, t.TempDir(), name, version, dependencies)
	asset := filepath.Base(archive)
	f.assets[asset] = readTestFile(t, archive)
	putTestFile(t, filepath.Join(f.root, name, version, asset+".sha256"), fmt.Sprintf("%x\n", sha256.Sum256([]byte(f.assets[asset]))))
	versionedLatest(t, f.root, name, version)
	return asset
}

func (f *versionedRemoteFixture) requireRequests(t *testing.T, want map[string]int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != len(want) {
		t.Fatalf("requests %v, want %v", f.requests, want)
	}
	for name, count := range want {
		if f.requests["/owner/repo/releases/download/v1/"+name] != count {
			t.Fatalf("requests %v, want %v", f.requests, want)
		}
	}
}

func versionedRemoteRun(t *testing.T, o prepareOptions, metadata string, want int) {
	t.Helper()
	args := prepareArguments(o)
	if metadata != "" {
		args = append(args, "--metadata-root="+metadata)
	}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != want {
		t.Fatalf("CLI code %d, want %d: %s", code, want, stderr.String())
	}
}

func requireNoPackageStaging(t *testing.T, cache string) {
	t.Helper()
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatal("leaked staging", entry.Name())
		}
	}
}

func TestVersionedMetadataHandoff(t *testing.T) {
	for _, source := range []string{"installation", "archive", "cache"} {
		t.Run(source+" exact dependency", func(t *testing.T) {
			f := versionedRemote(t)
			o, cache := versionedFixture(t, "probe")
			o.RemoteRoot = versionedDownloadRoot
			root := o.LocalRoot
			if source == "cache" {
				root = cache
			}
			if source == "archive" {
				versionedArchive(t, root, "probe", "1", "dep@1")
			} else {
				versionedInstalled(t, root, "probe", "1", "dep@1", "root")
			}
			versionedLatest(t, root, "probe", "1")
			dep := f.add(t, "dep", "1", "")
			f.add(t, "dep", "2", "")
			putTestFile(t, o.ResultPath, "stale")
			_, err := prepare(o)
			if !errors.Is(err, errMetadataRequired) {
				t.Fatalf("expected metadata handoff: %v", err)
			}
			requireNoResult(t, o)
			requireNoPackageStaging(t, cache)
			f.requireRequests(t, map[string]int{})
			versionedRemoteRun(t, o, "", 2)
			versionedRemoteRun(t, o, f.root, 0)
			if got := readTestFile(t, o.ResultPath); !strings.Contains(got, "AUTOLIB_PACKAGE_VERSION_dep [[1]]") {
				t.Fatal(got)
			}
			f.requireRequests(t, map[string]int{dep: 1})
			if _, err := os.Stat(filepath.Join(cache, "dep/package.toml")); !os.IsNotExist(err) {
				t.Fatal("dependency changed latest", err)
			}
		})
	}
	t.Run("local complete ignores remote and snapshot", func(t *testing.T) {
		f := versionedRemote(t)
		o, _ := versionedFixture(t, "probe")
		o.RemoteRoot = "invalid but unused"
		versionedInstalled(t, o.LocalRoot, "probe", "1", "", "local")
		versionedLatest(t, o.LocalRoot, "probe", "1")
		versionedRemoteRun(t, o, filepath.Join(f.root, "missing"), 0)
		f.requireRequests(t, map[string]int{})
	})
	for _, kind := range []string{"disabled", "bad cache", "bad installation", "bad archive", "bad latest"} {
		t.Run(kind+" is not handoff", func(t *testing.T) {
			f := versionedRemote(t)
			o, cache := versionedFixture(t, "probe@1")
			o.RemoteRoot = versionedDownloadRoot
			switch kind {
			case "disabled":
				o.RemoteRoot = ""
			case "bad cache":
				putTestFile(t, filepath.Join(versionedInstalled(t, cache, "probe", "1", "", "keep"), "autolib-import.cmake"), "broken")
			case "bad installation":
				putTestFile(t, filepath.Join(versionedInstalled(t, o.LocalRoot, "probe", "1", "", "keep"), "autolib-import.cmake"), "broken")
			case "bad archive":
				putTestFile(t, versionedArchive(t, o.LocalRoot, "probe", "1", ""), "broken")
			case "bad latest":
				o.Packages = []string{"probe"}
				putTestFile(t, filepath.Join(o.LocalRoot, "probe/package.toml"), "latest = []")
			}
			putTestFile(t, o.ResultPath, "stale")
			versionedRemoteRun(t, o, "", 1)
			requireNoResult(t, o)
			f.requireRequests(t, map[string]int{})
		})
	}
}

func TestVersionedRemoteSelection(t *testing.T) {
	for _, kind := range []string{"remote bare", "missing local artifact", "no local fallback", "exact old version", "deleted cache target", "cached root only missing dependency"} {
		t.Run(kind, func(t *testing.T) {
			f := versionedRemote(t)
			o, cache := versionedFixture(t, "probe")
			o.RemoteRoot = versionedDownloadRoot
			root := f.add(t, "probe", "2", "dep@1")
			dep := f.add(t, "dep", "1", "")
			f.add(t, "dep", "9", "")
			want := map[string]int{root: 1, dep: 1}
			switch kind {
			case "missing local artifact", "no local fallback":
				versionedLatest(t, o.LocalRoot, "probe", "99")
				if kind == "no local fallback" {
					versionedInstalled(t, o.LocalRoot, "probe", "2", "", "local wrong selection")
				}
			case "exact old version":
				o.Packages = []string{"probe@1"}
				old := f.add(t, "probe", "1", "dep@1")
				versionedLatest(t, f.root, "probe", "2")
				versionedLatest(t, cache, "probe", "2")
				want = map[string]int{old: 1, dep: 1}
			case "deleted cache target":
				versionedLatest(t, cache, "probe", "1")
				versionedInstalled(t, cache, "probe", "1", "", "old")
				versionedRemoteRun(t, o, "", 0)
				if err := os.Rename(filepath.Join(cache, "probe/1"), filepath.Join(t.TempDir(), "removed")); err != nil {
					t.Fatal(err)
				}
			case "cached root only missing dependency":
				versionedInstalled(t, cache, "probe", "1", "dep@1", "keep")
				versionedLatest(t, cache, "probe", "1")
				want = map[string]int{dep: 1}
			}
			versionedRemoteRun(t, o, f.root, 0)
			f.requireRequests(t, want)
			latest := "2"
			if kind == "cached root only missing dependency" {
				latest = "1"
			}
			if got, err := readPackageLatest(cache, "probe"); err != nil || got != latest {
				t.Fatalf("latest %q: %v", got, err)
			}
			if kind == "no local fallback" && readTestFile(t, filepath.Join(cache, "probe/2/data")) != "archive" {
				t.Fatal("remote selection fell back to local")
			}
			requireNoPackageStaging(t, cache)
		})
	}
}

func TestVersionedRemoteFailure(t *testing.T) {
	for _, kind := range []string{"missing snapshot", "snapshot file", "missing latest", "bad latest", "missing digest", "bad digest", "digest mismatch", "404", "bad archive", "wrong identity", "missing dependency"} {
		t.Run(kind, func(t *testing.T) {
			f := versionedRemote(t)
			o, cache := versionedFixture(t, "probe")
			o.RemoteRoot = versionedDownloadRoot
			asset := f.add(t, "probe", "2", "")
			metadata := f.root
			digest := filepath.Join(f.root, "probe/2", asset+".sha256")
			switch kind {
			case "missing snapshot":
				metadata = filepath.Join(f.root, "missing")
			case "snapshot file":
				metadata = putTestFile(t, filepath.Join(f.root, "file"), "not directory")
			case "missing latest":
				if err := os.Remove(filepath.Join(f.root, "probe/package.toml")); err != nil {
					t.Fatal(err)
				}
			case "bad latest":
				putTestFile(t, filepath.Join(f.root, "probe/package.toml"), "latest = []")
			case "missing digest":
				if err := os.Remove(digest); err != nil {
					t.Fatal(err)
				}
			case "bad digest":
				putTestFile(t, digest, "bad")
			case "digest mismatch":
				f.assets[asset] = "wrong bytes"
			case "404":
				f.status[asset] = 404
			case "bad archive":
				f.assets[asset] = "bad archive"
				putTestFile(t, digest, fmt.Sprintf("%x\n", sha256.Sum256([]byte(f.assets[asset]))))
			case "wrong identity":
				archive := testArchive(t, []archiveMember{{Name: "probe/2/autolib-import.cmake", Body: identityHeader(packageIdentity{"probe", "2", "windows", "x86_64"})}})
				f.assets[asset] = readTestFile(t, archive)
				putTestFile(t, digest, fmt.Sprintf("%x\n", sha256.Sum256([]byte(f.assets[asset]))))
			case "missing dependency":
				f.add(t, "probe", "2", "absent@1")
			}
			versionedLatest(t, cache, "probe", "1")
			versionedInstalled(t, cache, "unrelated", "1", "", "keep")
			before := sourceSnapshot(t, filepath.Join(cache, "unrelated"))
			putTestFile(t, o.ResultPath, "stale")
			versionedRemoteRun(t, o, metadata, 1)
			requireNoResult(t, o)
			requireNoPackageStaging(t, cache)
			if got, err := readPackageLatest(cache, "probe"); err != nil || got != "1" {
				t.Fatal("failure changed latest", got, err)
			}
			if _, err := os.Stat(filepath.Join(cache, "probe/2")); !os.IsNotExist(err) {
				t.Fatal("failure committed root", err)
			}
			if got := sourceSnapshot(t, filepath.Join(cache, "unrelated")); strings.Join(got, "\n") != strings.Join(before, "\n") {
				t.Fatal("changed complete cache")
			}
			want := map[string]int{}
			if kind == "digest mismatch" || kind == "404" || kind == "bad archive" || kind == "wrong identity" || kind == "missing dependency" {
				want[asset] = 1
			}
			f.requireRequests(t, want)
		})
	}
}
