package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubProtocol(t *testing.T) {
	digestText := cacheTestDigest([]byte("archive bytes"))
	var requests []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/repos/xxf1ow/xxautolib/releases/tags/autolib-release":
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected release method: %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":42,"tag_name":"autolib-release","assets_url":"`+serverURL(r)+`/repos/xxf1ow/xxautolib/releases/42/assets"}`)
		case "/repos/xxf1ow/xxautolib/releases/42/assets":
			if r.URL.Query().Get("page") == "1" || r.URL.Query().Get("page") == "" {
				w.Header().Set("Link", `<http://`+r.Host+r.URL.Path+`?per_page=100&page=2>; rel="next"`)
				fmt.Fprint(w, `[{"id":1,"name":"lz4-linux-x86_64.tar.zst","state":"uploaded","label":"producer","digest":"sha256:`+digestText+`"}]`)
			} else {
				fmt.Fprint(w, `[{"id":2,"name":"flann-linux-x86_64.tar.zst","state":"uploaded","digest":"sha256:`+digestText+`"},{"id":3,"name":"queue-any-any.tar.zst","state":"uploaded","digest":"sha256:`+digestText+`"}]`)
			}
		case "/repos/xxf1ow/xxautolib/releases/assets/3":
			fmt.Fprint(w, "archive bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &githubClient{BaseURL: server.URL, UploadURL: server.URL, Token: "must-not-appear", HTTP: server.Client()}
	release, found, err := client.release(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("release fixture was not found")
	}
	assets, err := client.releaseAssets(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 3 {
		t.Fatalf("pagination returned %d assets, want 3", len(assets))
	}
	if assets[0].State != "uploaded" || assets[0].Label != "producer" {
		t.Fatalf("lost asset facts: %+v", assets[0])
	}
	if digest, err := client.downloadAsset(context.Background(), assets[2], filepath.Join(t.TempDir(), "asset")); err != nil || digest != digestText {
		t.Fatalf("download by ID failed: %q %v", digest, err)
	}
	if len(requests) < 3 || !strings.Contains(strings.Join(requests, "\n"), "page=2") {
		t.Fatalf("release assets were not fully paginated: %v", requests)
	}
	for _, request := range requests {
		if strings.Contains(request, "upload") {
			t.Fatalf("read-only cache lookup called an upload endpoint: %s", request)
		}
	}

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			failure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "token must-not-appear", status)
			}))
			defer failure.Close()
			failing := &githubClient{BaseURL: failure.URL, Token: "must-not-appear", HTTP: failure.Client()}
			_, _, err := failing.release(context.Background())
			if err == nil || strings.Contains(err.Error(), "must-not-appear") {
				t.Fatalf("unexpected error or credential disclosure: %v", err)
			}
		})
	}

	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer missing.Close()
	_, found, err = (&githubClient{BaseURL: missing.URL, HTTP: missing.Client()}).release(context.Background())
	if err != nil || found {
		t.Fatalf("missing tagged release must be a miss, found=%v err=%v", found, err)
	}
	t.Run("malformed response", func(t *testing.T) {
		broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{broken`) }))
		defer broken.Close()
		if _, _, err := (&githubClient{BaseURL: broken.URL, HTTP: broken.Client()}).release(context.Background()); err == nil {
			t.Fatal("malformed API response became an empty cache")
		}
	})

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	_, _, err = (&githubClient{BaseURL: closedURL}).release(context.Background())
	if err == nil || strings.Contains(err.Error(), "must-not-appear") {
		t.Fatalf("network error was not returned safely: %v", err)
	}
}

func serverURL(r *http.Request) string { return "http://" + r.Host }
