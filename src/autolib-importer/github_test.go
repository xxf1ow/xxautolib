package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const testReleaseURL = "https://github.com/owner/repo/releases/tag/v1"

// Preserve production URL routing and TLS verification; only the socket destination changes.
func githubTestClient(t *testing.T, handler http.Handler) *http.Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

func TestGitHubSource(t *testing.T) {
	for _, tc := range []struct{ url, tag string }{
		{testReleaseURL, "v1"},
		{"https://github.com/owner/repo/releases/tag/release%2Fstable", "release/stable"},
		{"http://github.com/owner/repo/releases/tag/v1", ""},
		{"https://github.com:443/owner/repo/releases/tag/v1", ""},
		{"https://user@github.com/owner/repo/releases/tag/v1", ""},
		{"https://github.com/owner/repo/releases/latest", ""},
		{"https://github.com/owner/repo/releases/tag/v1?x=y", ""},
		{"https://github.com/owner/repo/releases/tag/v1#fragment", ""},
		{"https://github.com/owner/repo/releases/tag/", ""},
		{"https://gitee.com/owner/repo/releases/tag/v1", ""},
	} {
		t.Run(tc.url, func(t *testing.T) {
			got, err := parseGitHubSource(tc.url)
			if (err == nil) != (tc.tag != "") {
				t.Fatalf("source %+v, error %v", got, err)
			}
			if tc.tag != "" && (got.Owner != "owner" || got.Repository != "repo" || got.Tag != tc.tag) {
				t.Fatal(got)
			}
		})
	}
}

func TestGitHubSnapshot(t *testing.T) {
	for _, kind := range []string{"paged", "empty", "prerelease", "draft", "missing field", "wrong tag", "api error", "invalid json"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "" {
					t.Error("consumer sent credentials")
				}
				if r.URL.Path == "/repos/owner/repo/releases/tags/v1" {
					if kind == "api error" {
						w.WriteHeader(403)
						return
					}
					if kind == "invalid json" {
						fmt.Fprint(w, "{")
						return
					}
					release := map[string]any{"id": 42, "tag_name": "v1", "draft": kind == "draft", "prerelease": kind == "prerelease", "assets_url": "https://api.github.com/repos/owner/repo/releases/42/assets"}
					if kind == "missing field" {
						delete(release, "draft")
					}
					if kind == "wrong tag" {
						release["tag_name"] = "other"
					}
					json.NewEncoder(w).Encode(release)
					return
				}
				if r.URL.Path != "/repos/owner/repo/releases/42/assets" || r.URL.Query().Get("per_page") != "100" {
					t.Error("wrong assets request", r.URL)
					w.WriteHeader(404)
					return
				}
				assets := []map[string]any{}
				count := 1
				if kind == "empty" {
					count = 0
				}
				if kind == "paged" && r.URL.Query().Get("page") == "1" {
					count = 100
				}
				for i := 0; i < count; i++ {
					assets = append(assets, map[string]any{"name": fmt.Sprintf("unused-%d", i)})
				}
				json.NewEncoder(w).Encode(assets)
			}))
			source, err := parseGitHubSource(testReleaseURL)
			if err != nil {
				t.Fatal(err)
			}
			c := &githubSourceClient{Source: source, HTTP: client}
			file := filepath.Join(t.TempDir(), "responses.json")
			snapshot, err := loadGitHubSnapshot(context.Background(), c, file)
			valid := kind == "paged" || kind == "empty" || kind == "prerelease"
			if (err == nil) != valid {
				t.Fatalf("snapshot error: %v", err)
			}
			if !valid {
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatal("failed enumeration committed response")
				}
				return
			}
			wantPages, wantCalls := 1, int32(2)
			if kind == "paged" {
				wantPages, wantCalls = 2, 3
			}
			if len(snapshot.Pages) != wantPages || calls.Load() != wantCalls {
				t.Fatalf("pages %d calls %d", len(snapshot.Pages), calls.Load())
			}
			if _, err := loadGitHubSnapshot(context.Background(), c, file); err != nil || calls.Load() != wantCalls {
				t.Fatalf("response not reused: %v, %d", err, calls.Load())
			}
			body, _ := json.Marshal(snapshot)
			var missingSource map[string]json.RawMessage
			json.Unmarshal(body, &missingSource)
			delete(missingSource, "source")
			body, _ = json.Marshal(missingSource)
			putTestFile(t, file, string(body))
			if _, err := loadGitHubSnapshot(context.Background(), c, file); err == nil || calls.Load() != wantCalls {
				t.Fatalf("response without source accepted or refetched: %v", err)
			}
			for _, mutate := range []func(*githubSnapshot){
				func(s *githubSnapshot) { s.Source = "https://github.com/other/repo/releases/tag/v1" },
				func(s *githubSnapshot) { s.Pages = nil },
				func(s *githubSnapshot) { s.Pages = []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(`[]`)} },
				func(s *githubSnapshot) { s.Pages = []json.RawMessage{json.RawMessage(`null`)} },
			} {
				bad := snapshot
				mutate(&bad)
				body, _ := json.Marshal(bad)
				putTestFile(t, file, string(body))
				if _, err := loadGitHubSnapshot(context.Background(), c, file); err == nil || calls.Load() != wantCalls {
					t.Fatalf("invalid response accepted or refetched: %v", err)
				}
			}
		})
	}
}

func assetSnapshot(t *testing.T, assets ...map[string]any) githubSnapshot {
	t.Helper()
	page, err := json.Marshal(assets)
	if err != nil {
		t.Fatal(err)
	}
	return githubSnapshot{Pages: []json.RawMessage{page}}
}

func TestGitHubAssetSelection(t *testing.T) {
	good := func(name string) map[string]any {
		return map[string]any{"id": 7, "name": name, "state": "uploaded", "digest": "sha256:" + strings.Repeat("A", 64)}
	}
	names := []string{"demo-linux-aarch64.tar.zst", "demo-any-any.tar.zst"}
	for _, kind := range []string{"valid", "unrelated invalid", "missing", "duplicate", "mixed", "state", "id", "missing digest", "algorithm", "hex", "length"} {
		t.Run(kind, func(t *testing.T) {
			a := good(names[0])
			assets := []map[string]any{a}
			switch kind {
			case "unrelated invalid":
				assets = append(assets, map[string]any{"name": "unrelated", "id": "wrong", "digest": nil})
			case "missing":
				assets = []map[string]any{}
			case "duplicate":
				assets = append(assets, good(names[0]))
			case "mixed":
				assets = append(assets, good(names[1]))
			case "state":
				a["state"] = "new"
			case "id":
				a["id"] = 0
			case "missing digest":
				delete(a, "digest")
			case "algorithm":
				a["digest"] = "SHA256:" + strings.Repeat("a", 64)
			case "hex":
				a["digest"] = "sha256:" + strings.Repeat("z", 64)
			case "length":
				a["digest"] = "sha256:abcd"
			}
			got, err := selectGitHubAsset(assetSnapshot(t, assets...), names)
			if (err == nil) != (kind == "valid" || kind == "unrelated invalid") {
				t.Fatalf("asset %+v, error %v", got, err)
			}
		})
	}
}

func TestGitHubDownload(t *testing.T) {
	for _, kind := range []string{"direct", "redirect", "bad certificate", "downgrade", "deleted", "replaced", "mismatch", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			var calls, insecureCalls atomic.Int32
			insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { insecureCalls.Add(1); fmt.Fprint(w, "payload") }))
			defer insecure.Close()
			client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/repos/owner/repo/releases/assets/7" {
					if r.Header.Get("Accept") != "application/octet-stream" {
						t.Error("missing binary Accept")
					}
					switch kind {
					case "redirect":
						http.Redirect(w, r, "https://objects.example/content", 302)
						return
					case "downgrade":
						http.Redirect(w, r, insecure.URL, 302)
						return
					case "deleted", "replaced":
						w.WriteHeader(404)
						return
					case "mismatch":
						fmt.Fprint(w, "changed")
						return
					case "truncated":
						w.Header().Set("Content-Length", "100")
						fmt.Fprint(w, "part")
						return
					}
				} else if r.URL.Path != "/content" {
					t.Error("unexpected request", r.URL)
				}
				fmt.Fprint(w, "payload")
			}))
			if kind == "bad certificate" {
				transport := client.Transport.(*http.Transport).Clone()
				transport.TLSClientConfig = nil
				client.Transport = transport
				defer transport.CloseIdleConnections()
			}
			source, _ := parseGitHubSource(testReleaseURL)
			a := githubAsset{ID: 7, Name: "demo", State: "uploaded", Digest: fmt.Sprintf("sha256:%X", sha256.Sum256([]byte("payload")))}
			file := filepath.Join(t.TempDir(), "download")
			err := downloadGitHubAsset(context.Background(), &githubSourceClient{Source: source, HTTP: client}, a, file)
			valid := kind == "direct" || kind == "redirect"
			if (err == nil) != valid {
				t.Fatalf("download: %v", err)
			}
			if valid && readTestFile(t, file) != "payload" {
				t.Fatal("wrong content")
			}
			if !valid {
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatal("failed download left file")
				}
			}
			if insecureCalls.Load() != 0 {
				t.Fatal("followed HTTP downgrade")
			}
			want := int32(1)
			if kind == "redirect" {
				want = 2
			}
			if kind == "bad certificate" {
				want = 0
			}
			if calls.Load() != want {
				t.Fatalf("unexpected retries: got %d, want %d", calls.Load(), want)
			}
		})
	}
}
