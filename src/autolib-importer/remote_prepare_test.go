package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type remoteAssetFixture struct {
	id             int
	name           string
	data, download []byte
	status         int
}

type remotePrepareFixture struct {
	options  prepareOptions
	assets   []remoteAssetFixture
	mu       sync.Mutex
	requests []string
}

func newRemotePrepareFixture(t *testing.T) *remotePrepareFixture {
	t.Helper()
	f := &remotePrepareFixture{options: localGraphFixture(t)}
	f.options.RemoteRoot = testReleaseURL
	f.options.GitHubResponse = filepath.Join(f.options.WorkRoot, "api.json")
	source := t.TempDir()
	for i, node := range [][4]string{{"flann", "linux", "aarch64", "lz4"}, {"lz4", "linux", "aarch64", ""}, {"queue", "any", "any", ""}} {
		path := graphArchive(t, source, packageIdentity{node[0], node[1], node[2]}, node[3])
		f.assets = append(f.assets, remoteAssetFixture{id: i + 1, name: filepath.Base(path), data: []byte(readTestFile(t, path))})
	}
	client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.String())
		f.mu.Unlock()
		switch r.URL.Path {
		case "/repos/owner/repo/releases/tags/v1":
			fmt.Fprint(w, `{"id":42,"tag_name":"v1","draft":false,"assets_url":"https://api.github.com/repos/owner/repo/releases/42/assets"}`)
		case "/repos/owner/repo/releases/42/assets":
			assets := []map[string]any{}
			for _, a := range f.assets {
				assets = append(assets, map[string]any{"id": a.id, "name": a.name, "state": "uploaded", "digest": fmt.Sprintf("sha256:%x", sha256.Sum256(a.data))})
			}
			json.NewEncoder(w).Encode(assets)
		default:
			for _, a := range f.assets {
				if r.URL.Path != fmt.Sprintf("/repos/owner/repo/releases/assets/%d", a.id) {
					continue
				}
				if a.status != 0 {
					w.WriteHeader(a.status)
					return
				}
				body := a.data
				if a.download != nil {
					body = a.download
				}
				w.Write(body)
				return
			}
			w.WriteHeader(404)
		}
	}))
	previous := http.DefaultTransport
	http.DefaultTransport = client.Transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	return f
}

func (f *remotePrepareFixture) count(part string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, request := range f.requests {
		if strings.Contains(request, part) {
			n++
		}
	}
	return n
}

func TestPrepareRemote(t *testing.T) {
	for _, kind := range []string{"shared", "existing root", "cycle"} {
		t.Run(kind, func(t *testing.T) {
			f := newRemotePrepareFixture(t)
			o := f.options
			o.Packages = []string{"flann", "queue", "flann"}
			if kind == "existing root" {
				putTestFile(t, filepath.Join(graphSlot(o, "flann"), "autolib-import.cmake"), strings.Replace(identityHeader(packageIdentity{"flann", o.OS, o.Arch}), `set(AUTOLIB_DEPENDENCIES "")`, `set(AUTOLIB_DEPENDENCIES "lz4")`, 1))
				putTestFile(t, filepath.Join(graphSlot(o, "flann"), "data"), "user content")
			}
			if kind == "cycle" {
				path := graphArchive(t, t.TempDir(), packageIdentity{"lz4", o.OS, o.Arch}, "flann")
				f.assets[1].data = []byte(readTestFile(t, path))
			}
			got, err := prepare(o)
			if kind == "cycle" {
				if err == nil || !strings.Contains(err.Error(), "cycle") || f.count("/assets/3") != 0 {
					t.Fatalf("cycle did not stop traversal: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, p := range got {
				names = append(names, p.Name)
			}
			if !slices.Equal(names, []string{"lz4", "flann", "queue"}) || f.count("/releases/tags/") != 1 || f.count("?per_page=") != 1 || f.count("/assets/2") != 1 || f.count("/assets/3") != 1 {
				t.Fatalf("closure %v, requests %v", names, f.requests)
			}
			wantFlann := 1
			if kind == "existing root" {
				wantFlann = 0
				if readTestFile(t, filepath.Join(graphSlot(o, "flann"), "data")) != "user content" {
					t.Fatal("changed local input")
				}
			}
			if f.count("/assets/1") != wantFlann {
				t.Fatal("downloaded existing/shared root more than once")
			}
			// A later request may add roots, but must reuse the complete API responses.
			o.Packages = []string{"queue"}
			if _, err := prepare(o); err != nil || f.count("") != wantFlann+4 {
				t.Fatalf("local reuse made requests: %v, %v", err, f.requests)
			}
		})
	}
}

func TestPrepareSourcePriority(t *testing.T) {
	for _, kind := range []string{"directory", "archive", "bad directory", "bad archive"} {
		t.Run(kind, func(t *testing.T) {
			f := newRemotePrepareFixture(t)
			o := f.options
			o.Packages = []string{"queue"}
			switch kind {
			case "directory":
				putTestFile(t, filepath.Join(graphSlot(o, "queue"), "autolib-import.cmake"), identityHeader(packageIdentity{"queue", "any", "any"}))
				o.RemoteRoot = "https://unavailable.invalid/release"
				o.GitHubResponse = putTestFile(t, filepath.Join(o.WorkRoot, "bad.json"), "invalid")
			case "archive":
				graphArchive(t, o.LocalRoot, packageIdentity{"queue", "any", "any"}, "")
			case "bad directory":
				putTestFile(t, filepath.Join(graphSlot(o, "queue"), "autolib-import.cmake"), "invalid")
			case "bad archive":
				putTestFile(t, filepath.Join(o.LocalRoot, "queue-any-any.tar.zst"), "invalid")
			}
			_, err := prepare(o)
			if (err == nil) != (kind == "directory" || kind == "archive") || f.count("") != 0 {
				t.Fatalf("priority %s: %v, %v", kind, err, f.requests)
			}
		})
	}
}

func TestPrepareRemoteFailureCleanup(t *testing.T) {
	for _, kind := range []string{"digest", "corrupt", "deleted", "replaced", "write failure", "response"} {
		t.Run(kind, func(t *testing.T) {
			f := newRemotePrepareFixture(t)
			o := f.options
			o.Packages = []string{"queue", "flann", "later"}
			putTestFile(t, o.ResultPath, "previous result")
			switch kind {
			case "digest":
				f.assets[0].download = []byte("changed")
			case "corrupt":
				f.assets[0].data = []byte("not an archive")
			case "deleted", "replaced":
				f.assets[0].status = 404
			case "write failure":
				o.Packages = []string{"queue"}
				putTestFile(t, filepath.Join(o.LocalRoot, "packages"), "not a directory")
			case "response":
				putTestFile(t, o.GitHubResponse, "{invalid")
			}
			if _, err := prepare(o); err == nil {
				t.Fatal("accepted remote failure")
			}
			if readTestFile(t, o.ResultPath) != "previous result" {
				t.Fatal("committed failed result")
			}
			if kind != "write failure" && kind != "response" {
				if readTestFile(t, filepath.Join(graphSlot(o, "queue"), "data")) != "installed" {
					t.Fatal("lost successful package")
				}
				if _, err := os.Stat(graphSlot(o, "flann")); !os.IsNotExist(err) {
					t.Fatal("committed failed package")
				}
			}
			if f.count("/assets/2") != 0 || f.count("/releases/tags/") > 1 {
				t.Fatal("continued traversal or retried enumeration")
			}
			filepath.WalkDir(o.WorkRoot, func(path string, d os.DirEntry, err error) error {
				if err == nil && d != nil && (strings.HasPrefix(d.Name(), ".download-") || strings.HasPrefix(d.Name(), ".extract-")) {
					t.Error("left partial download", path)
				}
				return nil
			})
		})
	}
}

func TestPrepareRemoteOnly(t *testing.T) {
	f := newRemotePrepareFixture(t)
	o := f.options
	o.LocalRoot = ""
	o.Packages = []string{"flann"}
	// A stale directory at the ordinary work path must not become an undeclared cache source.
	putTestFile(t, filepath.Join(o.WorkRoot, "packages", o.OS+"-"+o.Arch, "flann", "autolib-import.cmake"), "invalid")
	got, err := prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "lz4" || got[1].Name != "flann" || filepath.Dir(got[0].Directory) != filepath.Dir(got[1].Directory) {
		t.Fatal(got)
	}
	for _, p := range got {
		if !strings.HasPrefix(p.Directory, o.WorkRoot+string(filepath.Separator)) || readTestFile(t, filepath.Join(p.Directory, "data")) != "installed" {
			t.Fatal(p)
		}
	}
	if f.count("/assets/") != 2 {
		t.Fatal("used an undeclared local source")
	}
	// A standalone prepare can also enumerate once without a response-file argument.
	o.GitHubResponse = ""
	if _, err := prepare(o); err != nil {
		t.Fatal(err)
	}
	if f.count("/releases/tags/") != 2 {
		t.Fatal("remote-only reused cross-call API state")
	}
}

func TestPrepareRemoteResponseReuse(t *testing.T) {
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
	if f.count("/releases/tags/") != 1 || f.count("?per_page=") != 1 {
		t.Fatal("enumerated again", f.requests)
	}
	// A response from a different source is not an empty cache miss.
	client := &githubSourceClient{Source: githubSource{"other", "repo", "v1"}}
	if _, err := loadGitHubSnapshot(context.Background(), client, o.GitHubResponse); err == nil {
		t.Fatal("accepted mismatched source")
	}
}
