package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type cacheGitFixture struct {
	store                     *ciCacheGitStore
	remote, seed, defaultHead string
	status                    int
}

func cacheGitCommand(t *testing.T, directory string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", directory}, args...)...)
	cmd.Env = append(ciSafeEnvironment(nil), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v %s", args[0], err, out)
	}
	return strings.TrimSpace(string(out))
}

func cacheGitTestFixture(t *testing.T) *cacheGitFixture {
	t.Helper()
	directory := t.TempDir()
	f := &cacheGitFixture{remote: filepath.Join(directory, "remote.git"), seed: filepath.Join(directory, "seed")}
	if err := os.Mkdir(f.seed, 0755); err != nil {
		t.Fatal(err)
	}
	cacheGitCommand(t, directory, "init", "--bare", f.remote)
	cacheGitCommand(t, f.seed, "init", "-b", "main")
	os.WriteFile(filepath.Join(f.seed, ".autolib-repository.json"), []byte(`{"type":"xxautolib-artifacts","schema":2,"upstream":"https://github.com/fixture/source"}`), 0644)
	cacheGitCommand(t, f.seed, "add", ".")
	cacheGitCommand(t, f.seed, "commit", "-m", "identity")
	cacheGitCommand(t, f.seed, "remote", "add", "origin", f.remote)
	cacheGitCommand(t, f.seed, "push", "origin", "main")
	f.defaultHead = cacheGitCommand(t, f.seed, "rev-parse", "HEAD")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token secret&%$!" {
			t.Error("incorrect Gitee credential")
			w.WriteHeader(401)
			return
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		switch r.URL.Path {
		case "/api/v5/user":
			fmt.Fprint(w, `{"id":7,"login":"token-user"}`)
		case "/api/v5/repos/fixture/mirror":
			fmt.Fprint(w, `{"id":9,"full_name":"fixture/mirror","private":true,"default_branch":"main","permission":{"push":true}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	s, err := newCICacheGitStore("fixture/source", "fixture/mirror", "secret&%$!", filepath.Join(directory, "work"))
	if err != nil {
		t.Fatal(err)
	}
	s.APIBase = server.URL + "/api/v5"
	s.URL = f.remote
	f.store = s
	return f
}

func (f *cacheGitFixture) setTree(t *testing.T, files map[string]string) {
	t.Helper()
	cacheGitCommand(t, f.seed, "checkout", "--orphan", "cache-tree")
	cacheGitCommand(t, f.seed, "rm", "-rf", ".")
	for name, body := range files {
		p := filepath.Join(f.seed, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cacheGitCommand(t, f.seed, "add", ".")
	cacheGitCommand(t, f.seed, "commit", "-m", "cache")
	cacheGitCommand(t, f.seed, "push", "origin", "HEAD:refs/heads/autolib-cache")
}

const cacheGitMarker = `{"type":"xxautolib-build-cache","schema":1,"upstream":"https://github.com/fixture/source"}`

func TestCICacheGitIdentity(t *testing.T) {
	ctx := context.Background()
	t.Run("initialize independent root", func(t *testing.T) {
		f := cacheGitTestFixture(t)
		s, err := f.store.Snapshot(ctx, true)
		if err != nil {
			t.Fatal(err)
		}
		if s.Head == "" || len(s.Records) != 0 {
			t.Fatalf("invalid initial snapshot: %+v", s)
		}
		if got := cacheGitCommand(t, f.remote, "rev-list", "--parents", "-1", s.Head); got != s.Head {
			t.Fatal("cache branch inherited a parent")
		}
		if got := cacheGitCommand(t, f.remote, "rev-parse", "main"); got != f.defaultHead {
			t.Fatal("default branch changed")
		}
		if _, err = f.store.Snapshot(ctx, false); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("raw records and malformed item", func(t *testing.T) {
		f := cacheGitTestFixture(t)
		raw := "{ \"schema\": 1 }\n"
		name := "probe-1-linux-x86_64.tar.zst.publication.json"
		f.setTree(t, map[string]string{".autolib-cache.json": cacheGitMarker, name: raw, "autolib-importer-windows-x86_64.exe.tar.xz.publication.json": "broken"})
		s, err := f.store.Snapshot(ctx, false)
		if err != nil || s.Head == "" || !bytes.Equal(s.Records[name], []byte(raw)) || len(s.Records) != 2 {
			t.Fatalf("raw records lost: %+v %v", s, err)
		}
	})
	for _, kind := range []string{"upstream", "schema", "extra", "directory", "case", "symlink", "gitlink"} {
		t.Run(kind, func(t *testing.T) {
			f := cacheGitTestFixture(t)
			files := map[string]string{".autolib-cache.json": cacheGitMarker}
			switch kind {
			case "upstream":
				files[".autolib-cache.json"] = strings.ReplaceAll(cacheGitMarker, "fixture/source", "other/source")
			case "schema":
				files[".autolib-cache.json"] = strings.ReplaceAll(cacheGitMarker, `"schema":1`, `"schema":true`)
			case "extra":
				files["README.md"] = "unknown"
			case "directory":
				files["nested/probe-1-linux-x86_64.tar.zst.publication.json"] = "{}"
			case "case":
				files["probe-1-linux-x86_64.tar.zst.publication.json"] = "{}"
			}
			if kind == "symlink" {
				p := filepath.Join(f.seed, "link")
				if err := os.Symlink(".autolib-cache.json", p); err != nil {
					t.Skip(err)
				}
			}
			f.setTree(t, files)
			if kind == "case" {
				blob := cacheGitCommand(t, f.seed, "rev-parse", "HEAD:probe-1-linux-x86_64.tar.zst.publication.json")
				cacheGitCommand(t, f.seed, "update-index", "--add", "--cacheinfo", "100644,"+blob+",Probe-1-linux-x86_64.tar.zst.publication.json")
				cacheGitCommand(t, f.seed, "commit", "-m", "collision")
				cacheGitCommand(t, f.seed, "push", "origin", "HEAD:autolib-cache")
			}
			if kind == "gitlink" {
				cacheGitCommand(t, f.seed, "update-index", "--add", "--cacheinfo", "160000,"+f.defaultHead+",probe-1-linux-x86_64.tar.zst.publication.json")
				cacheGitCommand(t, f.seed, "commit", "-m", "gitlink")
				cacheGitCommand(t, f.seed, "push", "origin", "HEAD:autolib-cache")
			}
			before := cacheGitCommand(t, f.remote, "rev-parse", "autolib-cache")
			if _, err := f.store.Snapshot(ctx, true); err == nil {
				t.Fatal("unsafe existing cache tree accepted")
			}
			if got := cacheGitCommand(t, f.remote, "rev-parse", "autolib-cache"); got != before {
				t.Fatal("unsafe tree was overwritten")
			}
		})
	}
	for _, status := range []int{401, 403, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := cacheGitTestFixture(t)
			f.status = status
			if _, err := f.store.Snapshot(ctx, true); err == nil {
				t.Fatal("API failure accepted")
			}
			if refs := cacheGitCommand(t, f.remote, "for-each-ref", "refs/heads/autolib-cache"); refs != "" {
				t.Fatal("branch created after API failure")
			}
		})
	}
	t.Run("transport is not missing branch", func(t *testing.T) {
		f := cacheGitTestFixture(t)
		f.store.URL = filepath.Join(t.TempDir(), "absent.git")
		if _, err := f.store.Snapshot(ctx, true); err == nil {
			t.Fatal("transport failure accepted")
		}
	})
}

func TestCICacheGitCredentials(t *testing.T) {
	f := cacheGitTestFixture(t)
	if _, err := f.store.Snapshot(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if f.store.Login != "token-user" {
		t.Fatal("token login was inferred from repository owner")
	}
	t.Setenv("AUTOLIB_CACHE_GIT_TOKEN", "secret&%$!")
	t.Setenv("AUTOLIB_CACHE_GIT_LOGIN", "token-user")
	t.Setenv("AUTOLIB_CACHE_ASKPASS", "1")
	for prompt, want := range map[string]string{"Username for 'https://gitee.com':": "token-user\n", "Password for 'https://token-user@gitee.com':": "secret&%$!\n"} {
		var out bytes.Buffer
		if runCICacheGitAskpass([]string{prompt}, &out) != 0 || out.String() != want {
			t.Fatal("askpass changed credential bytes")
		}
	}
	for _, prompt := range []string{"Password for 'https://gitee.com.attacker':", "Username for 'https://gitee.com.attacker':"} {
		var secret bytes.Buffer
		if runCICacheGitAskpass([]string{prompt}, &secret) == 0 || secret.Len() != 0 {
			t.Fatal("askpass answered a different host")
		}
	}
	for _, value := range ciSafeEnvironment(nil) {
		if strings.HasPrefix(value, "AUTOLIB_CACHE_") || strings.Contains(value, "secret&%$!") {
			t.Fatal("askpass secret leaked into ordinary subprocess")
		}
	}
	t.Setenv("AUTOLIB_CACHE_ASKPASS", "")
	var out bytes.Buffer
	if runCICacheGitAskpass([]string{"Password for 'https://gitee.com':"}, &out) == 0 || out.Len() != 0 {
		t.Fatal("askpass exposed secret outside dedicated process")
	}
	directory := filepath.Join(t.TempDir(), "路径 with spaces")
	os.MkdirAll(directory, 0755)
	binary := filepath.Join(directory, "ci")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command(filepath.Join(os.Getenv("GOROOT"), "bin", "go"), "build", "-mod=readonly", "-p=1", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build askpass CLI: %v %s", err, output)
	}
	cmd := exec.Command("git", "-c", "credential.helper=", "credential", "fill")
	cmd.Stdin = strings.NewReader("protocol=https\nhost=gitee.com\n\n")
	cmd.Env = append(ciSafeEnvironment(nil), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS="+binary, "AUTOLIB_CACHE_ASKPASS=1", "AUTOLIB_CACHE_GIT_LOGIN=token-user", "AUTOLIB_CACHE_GIT_TOKEN=secret&%$!")
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "password=secret&%$!\n") || !strings.Contains(string(output), "username=token-user\n") {
		t.Fatalf("Git askpass round trip failed: %v", err)
	}
	// API responses may be hostile; identity failure must not print token-bearing text.
	f.status = 403
	_, err = f.store.Snapshot(context.Background(), true)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("authentication failure leaked credentials")
	}
}

func cacheGitSibling(t *testing.T, s *ciCacheGitStore) *ciCacheGitStore {
	t.Helper()
	other, err := newCICacheGitStore(s.SourceRepository, s.Repository, s.Token, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other.URL = s.URL
	other.APIBase = s.APIBase
	return other
}

func cacheGitShim() {
	args := os.Args[1:]
	isPush := false
	for _, arg := range args {
		if arg == "push" {
			isPush = true
			break
		}
	}
	mode := os.Getenv("AUTOLIB_CI_GIT_FAULT")
	if isPush {
		counter := os.Getenv("AUTOLIB_CI_GIT_COUNT")
		f, _ := os.OpenFile(counter, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if f != nil {
			f.WriteString("push\n")
			f.Close()
		}
		if mode == "reject" {
			os.Exit(1)
		}
		if mode == "auth" {
			fmt.Fprintln(os.Stderr, "Authentication failed")
			os.Exit(1)
		}
		if mode == "advance" {
			path := os.Getenv("AUTOLIB_CI_GIT_HEADS")
			data, _ := os.ReadFile(path)
			heads := strings.Fields(string(data))
			if len(heads) > 0 {
				cmd := exec.Command(os.Getenv("AUTOLIB_CI_REAL_GIT"), "--git-dir", os.Getenv("AUTOLIB_CI_GIT_REMOTE"), "update-ref", "refs/heads/autolib-cache", heads[0])
				if err := cmd.Run(); err != nil {
					os.Exit(2)
				}
				os.WriteFile(path, []byte(strings.Join(heads[1:], "\n")), 0600)
			}
		}
	}
	cmd := exec.Command(os.Getenv("AUTOLIB_CI_REAL_GIT"), args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var status *exec.ExitError
		if errors.As(err, &status) {
			os.Exit(status.ExitCode())
		}
		os.Exit(2)
	}
	if isPush && mode == "lost" {
		os.Exit(1)
	}
	os.Exit(0)
}

func cacheGitFault(t *testing.T, mode, remote string, heads []string) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(directory, name), data, 0755)
	count := filepath.Join(directory, "count")
	headfile := filepath.Join(directory, "heads")
	os.WriteFile(headfile, []byte(strings.Join(heads, "\n")), 0600)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AUTOLIB_CI_CACHE_GIT_SHIM", "1")
	t.Setenv("AUTOLIB_CI_REAL_GIT", real)
	t.Setenv("AUTOLIB_CI_GIT_FAULT", mode)
	t.Setenv("AUTOLIB_CI_GIT_COUNT", count)
	t.Setenv("AUTOLIB_CI_GIT_HEADS", headfile)
	t.Setenv("AUTOLIB_CI_GIT_REMOTE", remote)
	return count
}

func TestCICacheGitUpdate(t *testing.T) {
	ctx := context.Background()
	name := "probe-1-linux-x86_64.tar.zst.publication.json"
	record := localCacheFixture(t).Record
	raw, _ := json.Marshal(record)
	changed := record
	changed.Fingerprint = strings.Repeat("b", 64)
	different, _ := json.Marshal(changed)
	t.Run("independent work copies", func(t *testing.T) {
		f := cacheGitTestFixture(t)
		base, err := f.store.Snapshot(ctx, true)
		if err != nil {
			t.Fatal(err)
		}
		other := cacheGitSibling(t, f.store)
		nameB := "probe-2-linux-x86_64.tar.zst.publication.json"
		nameC := "probe-3-linux-x86_64.tar.zst.publication.json"
		if _, err = f.store.Update(ctx, base.Head, nameC, raw); err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.Update(ctx, base.Head, name, raw); err != nil {
			t.Fatal(err)
		}
		if _, err = other.Update(ctx, base.Head, nameB, different); err != nil {
			t.Fatal(err)
		}
		third := cacheGitSibling(t, f.store)
		result, err := third.Snapshot(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string][]byte{name: raw, nameB: different, nameC: raw} {
			if !bytes.Equal(result.Records[key], want) {
				t.Fatalf("concurrent record lost: %s", key)
			}
		}
		// Formatting differences must not create another commit.
		pretty, _ := json.MarshalIndent(record, "", "  ")
		same, err := other.Update(ctx, base.Head, name, pretty)
		if err != nil || same.Head != result.Head {
			t.Fatal("same semantic record changed history", err)
		}
		if _, err = other.Update(ctx, base.Head, name, different); err == nil {
			t.Fatal("conflicting selected record overwritten")
		}
		current, err := third.Snapshot(ctx, false)
		if err != nil || !bytes.Equal(current.Records[name], raw) {
			t.Fatal("conflict changed remote", err)
		}
		deleted, err := other.Update(ctx, current.Head, name, nil)
		if err != nil {
			t.Fatal(err)
		}
		again, err := other.Update(ctx, current.Head, name, nil)
		if err != nil || again.Head != deleted.Head {
			t.Fatal("confirmed deletion not idempotent", err)
		}
	})
	for _, mode := range []string{"lost", "reject", "auth", "advance", "advance-once"} {
		t.Run(mode, func(t *testing.T) {
			f := cacheGitTestFixture(t)
			base, err := f.store.Snapshot(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			heads := []string{}
			parent := base.Head
			if strings.HasPrefix(mode, "advance") {
				for i := 0; i < 3; i++ {
					head, err := f.store.commitFile(ctx, parent, "other-1-linux-x86_64.tar.zst.publication.json", []byte(fmt.Sprint(i)))
					if err != nil {
						t.Fatal(err)
					}
					cacheGitCommand(t, f.remote, "fetch", f.store.directory, head)
					heads = append(heads, head)
					parent = head
				}
				if mode == "advance-once" {
					heads = heads[:1]
				}
			}
			fault := mode
			if mode == "advance-once" {
				fault = "advance"
			}
			count := cacheGitFault(t, fault, f.remote, heads)
			got, err := f.store.Update(ctx, base.Head, name, raw)
			if mode == "lost" || mode == "advance-once" {
				if err != nil || !bytes.Equal(got.Records[name], raw) {
					t.Fatal("confirmed record rejected", err)
				}
			} else {
				if err == nil {
					t.Fatal("unconfirmed record accepted")
				}
				remote, e := f.store.readBranch(ctx)
				if e != nil || remote.Records[name] != nil {
					t.Fatal("failed operation changed selected record", e)
				}
			}
			data, _ := os.ReadFile(count)
			pushes := strings.Count(string(data), "push\n")
			if pushes > 3 || (mode == "auth" && pushes != 1) || (mode == "advance" && pushes != 3) {
				t.Fatalf("unexpected push budget: %d", pushes)
			}
			if mode == "advance-once" {
				if got.Records["other-1-linux-x86_64.tar.zst.publication.json"] == nil {
					t.Fatal("replay lost other writer")
				}
			}
		})
	}
	t.Run("cancel", func(t *testing.T) {
		f := cacheGitTestFixture(t)
		base, err := f.store.Snapshot(ctx, true)
		if err != nil {
			t.Fatal(err)
		}
		c, cancel := context.WithCancel(ctx)
		cancel()
		if _, err = f.store.Update(c, base.Head, name, raw); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
	})
}
