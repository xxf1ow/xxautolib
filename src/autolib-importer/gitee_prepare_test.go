package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGiteePrepare(t *testing.T) {
	for _, kind := range []string{"closure", "shared-snapshot", "paged", "digest", "missing-index", "missing-entry", "wrong-id", "duplicate", "mixed", "api-error", "download-error", "corrupt", "wrong-platform", "incomplete-page"} {
		t.Run(kind, func(t *testing.T) {
			o := localGraphFixture(t)
			o.RemoteRoot = "https://gitee.com/owner/repo/releases/download/v1"
			o.SourceResponse = filepath.Join(o.WorkRoot, "source.json")
			o.Packages = []string{"flann", "queue"}
			var assets []remoteAssetFixture
			for i, node := range [][4]string{{"flann", "linux", "aarch64", "lz4"}, {"lz4", "linux", "aarch64", ""}, {"queue", "any", "any", ""}} {
				path := graphArchive(t, t.TempDir(), packageIdentity{node[0], node[1], node[2]}, node[3])
				assets = append(assets, remoteAssetFixture{id: i + 1, name: filepath.Base(path), data: []byte(readTestFile(t, path))})
			}
			if kind == "corrupt" {
				assets[0].data = []byte("invalid archive")
			}
			requests := map[string]int{}
			client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests[r.URL.Path]++
				if r.Header.Get("Authorization") != "" {
					t.Error("consumer sent credentials")
				}
				switch r.URL.Path {
				case "/api/v5/repos/owner/repo/releases/tags/v1":
					if kind == "api-error" {
						w.WriteHeader(503)
						return
					}
					files := map[string]any{}
					for _, asset := range assets {
						id := asset.id
						if kind == "wrong-id" {
							id++
						}
						files[asset.name] = map[string]any{"sha256": fmt.Sprintf("%x", sha256.Sum256(asset.data)), "gitee_asset_id": id}
					}
					if kind == "missing-entry" {
						delete(files, assets[0].name)
					}
					encoded, _ := json.Marshal(map[string]any{"files": files})
					body := "<!-- autolib-gitee-sync:start -->\n```json\n" + string(encoded) + "\n```\n<!-- autolib-gitee-sync:end -->"
					if kind == "missing-index" {
						body = "description"
					}
					json.NewEncoder(w).Encode(map[string]any{"id": 42, "tag_name": "v1", "body": body})
				case "/api/v5/repos/owner/repo/releases/42/attach_files":
					if r.URL.Query().Get("per_page") != "100" {
						t.Error("missing pagination", r.URL)
					}
					page := []map[string]any{}
					if kind == "incomplete-page" && r.URL.Query().Get("page") == "2" {
						w.WriteHeader(503)
						return
					}
					if r.URL.Query().Get("page") == "1" {
						for _, asset := range assets {
							page = append(page, map[string]any{"id": asset.id, "name": asset.name, "size": len(asset.data), "browser_download_url": fmt.Sprintf("https://gitee.com/download/%d", asset.id)})
						}
						if kind == "duplicate" {
							page = append(page, page[0])
						}
						if kind == "mixed" {
							page = append(page, map[string]any{"id": 4, "name": "flann-any-any.tar.zst", "size": 1, "browser_download_url": "https://gitee.com/download/4"})
						}
						if kind == "paged" || kind == "incomplete-page" {
							for len(page) < 100 {
								page = append(page, map[string]any{"name": fmt.Sprintf("unused-%d", len(page))})
							}
						}
					}
					json.NewEncoder(w).Encode(page)
				default:
					for _, asset := range assets {
						if r.URL.Path == fmt.Sprintf("/download/%d", asset.id) {
							if kind == "download-error" {
								w.WriteHeader(404)
								return
							}
							if kind == "digest" {
								fmt.Fprint(w, "changed")
								return
							}
							w.Write(asset.data)
							return
						}
					}
					w.WriteHeader(404)
				}
			}))
			previous := http.DefaultTransport
			http.DefaultTransport = client.Transport
			t.Cleanup(func() { http.DefaultTransport = previous })
			if kind == "wrong-platform" {
				putTestFile(t, o.SourceResponse, `{"platform":"github","source":"https://gitee.com/owner/repo/releases/download/v1","release":{},"pages":[[]]}`)
			}
			if kind == "shared-snapshot" {
				o.Packages = []string{"queue"}
			}
			putTestFile(t, o.ResultPath, "previous")
			got, err := prepare(o)
			if kind != "closure" && kind != "paged" && kind != "shared-snapshot" {
				if err == nil {
					t.Fatal("accepted bad source")
				}
				if readTestFile(t, o.ResultPath) != "previous" {
					t.Fatal("committed failed result")
				}
				if _, err := os.Stat(graphSlot(o, "flann")); !os.IsNotExist(err) {
					t.Fatal("committed unverified package")
				}
				if kind == "wrong-platform" && len(requests) != 0 {
					t.Fatal("bad snapshot triggered network")
				}
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
			names := []string{}
			for _, p := range got {
				names = append(names, p.Name)
			}
			expected := []string{"lz4", "flann", "queue"}
			if kind == "shared-snapshot" {
				expected = []string{"queue", "lz4", "flann"}
			}
			if !slices.Equal(names, expected) {
				t.Fatal(names)
			}
			pages := 1
			if kind == "paged" {
				pages = 2
			}
			if requests["/api/v5/repos/owner/repo/releases/tags/v1"] != 1 || requests["/api/v5/repos/owner/repo/releases/42/attach_files"] != pages || requests["/download/1"] != 1 || requests["/download/2"] != 1 || requests["/download/3"] != 1 {
				t.Fatal(requests)
			}
			before := fmt.Sprint(requests)
			o.SourceResponse = putTestFile(t, filepath.Join(o.WorkRoot, "bad.json"), "invalid")
			if _, err := prepare(o); err != nil || fmt.Sprint(requests) != before {
				t.Fatalf("local reuse: %v %v", err, requests)
			}
			if strings.Contains(readTestFile(t, o.ResultPath), "previous") {
				t.Fatal("result not prepared")
			}
		})
	}
}
