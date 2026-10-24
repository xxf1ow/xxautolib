package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func publishTestIdentity(t *testing.T) (string, ciRunIdentity) {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "master"}, {"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v\n%s", err, output)
		}
	}
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(output))
	return root, ciRunIdentity{Repository: "xxf1ow/xxautolib", Ref: "refs/heads/master", SHA: sha, CheckoutSHA: sha, RunID: "123", Attempt: "1"}
}

func TestCIPublishAuthorization(t *testing.T) {
	root, identity := publishTestIdentity(t)
	for _, change := range []string{"valid", "topic", "tag", "repository", "checkout", "actual-head"} {
		t.Run(change, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes++
					http.Error(w, "unexpected write", 500)
					return
				}
				if r.URL.Path == "/repos/xxf1ow/xxautolib" {
					fmt.Fprint(w, `{"full_name":"xxf1ow/xxautolib","default_branch":"master"}`)
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			client := &githubClient{BaseURL: server.URL, UploadURL: server.URL, Token: "secret", HTTP: server.Client()}
			run := identity
			switch change {
			case "topic":
				run.Ref = "refs/heads/topic"
			case "tag":
				run.Ref = "refs/tags/master"
			case "repository":
				run.Repository = "elsewhere/repository"
			case "checkout":
				run.CheckoutSHA = strings.Repeat("f", 40)
			case "actual-head":
				run.CheckoutSHA, run.SHA = strings.Repeat("f", 40), strings.Repeat("f", 40)
			}
			auth, err := authorizeCIWrite(context.Background(), client, root, run)
			if change == "valid" {
				if err != nil || auth == nil {
					t.Fatalf("valid identity rejected: %v", err)
				}
			} else if err == nil || auth != nil {
				t.Fatalf("invalid identity authorized: %v", err)
			}
			if writes != 0 {
				t.Fatal("authorization wrote remotely")
			}
			if _, err := initCIRelease(context.Background(), client, nil); err == nil || writes != 0 {
				t.Fatal("nil authorization performed a write")
			}
		})
	}
}

func TestCIReplaceAsset(t *testing.T) {
	root, identity := publishTestIdentity(t)
	for _, scenario := range []string{"replace", "upload-fails", "wrong-digest", "response-interrupted", "bad-local-digest", "invalid-label", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			data := []byte("verified archive bytes")
			name := "queue-any-any.tar.zst"
			file := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(file, data, 0644); err != nil {
				t.Fatal(err)
			}
			label, _ := json.Marshal(ciAssetLabel{BuildID: "build-1", Commit: identity.SHA})
			old := githubAsset{ID: 7, Name: name, State: "uploaded", Label: string(label), Digest: "sha256:" + cacheTestDigest([]byte("old"))}
			other := githubAsset{ID: 8, Name: "lz4-linux-aarch64.tar.zst", State: "uploaded", Label: "untouched", Digest: "sha256:" + strings.Repeat("a", 64)}
			assets := []githubAsset{old, other}
			if scenario == "duplicate" {
				assets = append(assets, old)
			}
			var writes []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes = append(writes, r.Method+" "+r.URL.Path)
				}
				switch {
				case r.Method == "GET" && r.URL.Path == "/repos/xxf1ow/xxautolib":
					fmt.Fprint(w, `{"full_name":"xxf1ow/xxautolib","default_branch":"master"}`)
				case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/releases/42/assets"):
					json.NewEncoder(w).Encode(assets)
				case r.Method == "DELETE" && strings.HasSuffix(r.URL.Path, "/releases/assets/7"):
					assets = []githubAsset{other}
					w.WriteHeader(204)
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/releases/42/assets"):
					body, _ := io.ReadAll(r.Body)
					if !bytes.Equal(body, data) || r.URL.Query().Get("name") != name || r.URL.Query().Get("label") != string(label) {
						t.Errorf("upload changed payload/name/label: %s", r.URL)
					}
					if scenario == "upload-fails" {
						http.Error(w, "secret", 500)
						return
					}
					asset := githubAsset{ID: 9, Name: name, State: "uploaded", Label: string(label), Digest: "sha256:" + cacheTestDigest(data)}
					assets = append(assets, asset)
					if scenario == "response-interrupted" {
						w.WriteHeader(201)
						fmt.Fprint(w, `{"id":`)
						return
					}
					if scenario == "wrong-digest" {
						asset.Digest = "sha256:" + strings.Repeat("b", 64)
					}
					w.WriteHeader(201)
					json.NewEncoder(w).Encode(asset)
				default:
					http.Error(w, "unexpected request", 500)
				}
			}))
			defer server.Close()
			client := &githubClient{BaseURL: server.URL, UploadURL: server.URL, Token: "secret", HTTP: server.Client()}
			auth, err := authorizeCIWrite(context.Background(), client, root, identity)
			if err != nil {
				t.Fatal(err)
			}
			o := ciUploadOptions{File: file, Name: name, Label: string(label), SHA256: cacheTestDigest(data)}
			if scenario == "bad-local-digest" {
				o.SHA256 = strings.Repeat("0", 64)
			}
			if scenario == "invalid-label" {
				o.Label = `{}`
			}
			release := githubRelease{ID: 42, TagName: "autolib-release", AssetsURL: server.URL + "/repos/xxf1ow/xxautolib/releases/42/assets"}
			got, err := uploadCIAsset(context.Background(), client, auth, release, o)
			if scenario == "replace" {
				if err != nil || got.ID != 9 {
					t.Fatalf("replace failed: %+v %v", got, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("expected safe failure: %v", err)
			}
			if scenario == "bad-local-digest" || scenario == "invalid-label" || scenario == "duplicate" {
				if len(writes) != 0 {
					t.Fatalf("invalid input wrote: %v", writes)
				}
			} else if len(writes) != 2 || writes[0] != "DELETE /repos/xxf1ow/xxautolib/releases/assets/7" || writes[1] != "POST /repos/xxf1ow/xxautolib/releases/42/assets" {
				t.Fatalf("unexpected writes: %v", writes)
			}
			otherUnchanged := false
			for _, asset := range assets {
				if asset.ID == 8 && asset == other {
					otherUnchanged = true
				}
			}
			if !otherUnchanged {
				t.Fatalf("unselected asset changed: %+v", assets)
			}
			if scenario == "response-interrupted" {
				current, err := client.releaseAssets(context.Background(), release)
				if err != nil {
					t.Fatal(err)
				}
				asset, reason, err := selectCICacheAsset(current, ciSlot{Name: "queue", OS: "any", Arch: "any"}, "build-1")
				if err != nil || reason != "" || asset.ID != 9 {
					t.Fatalf("explicit rerun cannot use completed asset: %+v %s %v", asset, reason, err)
				}
			}
		})
	}
}

func TestCIInitCLI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init"}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "--source-root") {
		t.Fatalf("missing init input accepted or command absent: %d %s", code, stderr.String())
	}
	root, identity := publishTestIdentity(t)
	for _, scenario := range []string{"missing-tag", "existing-tag", "existing-release", "api-error"} {
		t.Run(scenario, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == "/repos/xxf1ow/xxautolib":
					fmt.Fprint(w, `{"full_name":"xxf1ow/xxautolib","default_branch":"master"}`)
				case r.Method == "GET" && strings.Contains(r.URL.Path, "/releases/tags/"):
					if scenario == "existing-release" {
						fmt.Fprint(w, `{"id":42,"tag_name":"autolib-release","assets_url":"`+serverURL(r)+`/repos/xxf1ow/xxautolib/releases/42/assets"}`)
					} else if scenario == "api-error" {
						http.Error(w, "secret", 403)
					} else {
						http.NotFound(w, r)
					}
				case r.Method == "GET" && strings.Contains(r.URL.Path, "/git/ref/tags/"):
					if scenario == "existing-tag" {
						fmt.Fprint(w, `{"ref":"refs/tags/autolib-release","object":{"sha":"`+strings.Repeat("a", 40)+`"}}`)
					} else {
						http.NotFound(w, r)
					}
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/releases"):
					writes++
					var body struct {
						Tag        string `json:"tag_name"`
						SHA        string `json:"target_commitish"`
						Name       string `json:"name"`
						Draft      bool   `json:"draft"`
						Prerelease bool   `json:"prerelease"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Tag != "autolib-release" || body.SHA != identity.SHA || body.Draft {
						t.Errorf("wrong initialization: %+v %v", body, err)
					}
					w.WriteHeader(201)
					fmt.Fprint(w, `{"id":42,"tag_name":"autolib-release","assets_url":"`+serverURL(r)+`/repos/xxf1ow/xxautolib/releases/42/assets"}`)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer server.Close()
			client := &githubClient{BaseURL: server.URL, UploadURL: server.URL, Token: "secret", HTTP: server.Client()}
			auth, err := authorizeCIWrite(context.Background(), client, root, identity)
			if err != nil {
				t.Fatal(err)
			}
			release, err := initCIRelease(context.Background(), client, auth)
			wantWrites := 1
			if scenario == "existing-release" || scenario == "api-error" {
				wantWrites = 0
			}
			if scenario == "api-error" {
				if err == nil {
					t.Fatal("API error treated as missing")
				}
			} else if err != nil || release.ID != 42 {
				t.Fatalf("init failed: %+v %v", release, err)
			}
			if writes != wantWrites {
				t.Fatalf("write count=%d want %d", writes, wantWrites)
			}
			if scenario == "existing-release" {
				identityFile := filepath.Join(t.TempDir(), "run.json")
				data, _ := json.Marshal(identity)
				if err := os.WriteFile(identityFile, data, 0644); err != nil {
					t.Fatal(err)
				}
				var out, diagnostic bytes.Buffer
				code := runCIInit([]string{"--source-root", root, "--run-identity", identityFile}, &out, &diagnostic, client)
				var got githubRelease
				if code != 0 || json.Unmarshal(out.Bytes(), &got) != nil || got.ID != 42 || writes != 0 {
					t.Fatalf("init CLI failed: %d %s %s", code, out.String(), diagnostic.String())
				}
			}
		})
	}
}
