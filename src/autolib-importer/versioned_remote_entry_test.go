package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type versionedRemoteEntry struct {
	f                     *entryFixture
	repo, trace, ca, host string
	mu                    sync.Mutex
	assets                map[string]string
	requests              map[string]int
	offline               bool
}

func remoteEntry(t *testing.T, host string) *versionedRemoteEntry {
	t.Helper()
	f := versionedEntryFixture(t, false)
	e := &versionedRemoteEntry{f: f, host: host, trace: filepath.Join(f.root, "git.trace"), assets: map[string]string{}, requests: map[string]int{}}
	e.repo = snapshotRepository(t, metadataRepositoryCase{Files: map[string]string{".autolib-repository.json": `{"type":"xxautolib-artifacts","schema":1,"upstream":"https://github.com/xxf1ow/xxautolib"}`}})
	redirect := "url.file://" + filepath.ToSlash(e.repo) + ".insteadOf=https://" + host + "/owner/repo.git"
	t.Setenv("GIT_CONFIG_PARAMETERS", "'"+strings.ReplaceAll(redirect, "'", "'\\''")+"'")
	t.Setenv("GIT_TRACE", e.trace)
	e.ca = entryHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		defer e.mu.Unlock()
		key := r.Host + r.URL.Path
		e.requests[key]++
		if r.Host != host || !strings.HasPrefix(r.URL.Path, "/owner/repo/releases/download/v1/") || strings.Contains(r.URL.Path, "/api/") || strings.Contains(r.URL.Path, "/contents/") {
			t.Errorf("forbidden request: %s%s", r.Host, r.URL)
			w.WriteHeader(500)
			return
		}
		body, ok := e.assets[filepath.Base(r.URL.Path)]
		if !ok {
			w.WriteHeader(404)
			return
		}
		fmt.Fprint(w, body)
	}))
	return e
}

func (e *versionedRemoteEntry) asset(t *testing.T, path, body string) {
	t.Helper()
	e.assets[filepath.Base(path)] = body
	putTestFile(t, filepath.Join(e.repo, filepath.FromSlash(path)+".sha256"), fmt.Sprintf("%x\n", sha256.Sum256([]byte(body))))
}

func (e *versionedRemoteEntry) pkg(t *testing.T, name, version, dependencies string) string {
	t.Helper()
	prefix := name + "/" + version + "/"
	header := strings.Replace(identityHeader(packageIdentity{name, version, "linux", "aarch64"}), `AUTOLIB_DEPENDENCIES ""`, `AUTOLIB_DEPENDENCIES "`+dependencies+`"`, 1)
	body := header + "if(NOT TARGET autolib::" + name + ")\nadd_library(autolib::" + name + " INTERFACE IMPORTED GLOBAL)\nendif()\n"
	archive := testArchive(t, []archiveMember{{Name: prefix + "autolib-import.cmake", Body: body}, {Name: prefix + "data", Body: "archive"}})
	asset := name + "-" + version + "-linux-aarch64.tar.zst"
	e.asset(t, prefix+asset, readTestFile(t, archive))
	versionedLatest(t, e.repo, name, version)
	return asset
}

func (e *versionedRemoteEntry) consumer(t *testing.T, packages, tail string) {
	t.Helper()
	versionedEntryConsumer(e.f, "set(AUTOLIB_LOCAL_ROOT \"\")\n", packages, tail)
	path := filepath.Join(e.f.source, "CMakeLists.txt")
	putTestFile(t, path, strings.Replace(readTestFile(t, path), `set(AUTOLIB_REMOTE_ROOT "")`, `set(AUTOLIB_REMOTE_ROOT "https://`+e.host+`/owner/repo/releases/download/v1")`, 1))
}

func (e *versionedRemoteEntry) configure(t *testing.T) (string, error) {
	t.Helper()
	snapshotGit(t, e.repo, "add", ".")
	snapshotGit(t, e.repo, "commit", "--allow-empty", "-m", "entry assets")
	if e.offline {
		t.Setenv("PATH", t.TempDir())
	}
	return e.f.configure("-DCMAKE_TLS_CAINFO=" + filepath.ToSlash(e.ca))
}

func (e *versionedRemoteEntry) counts(t *testing.T, clones int, want map[string]int) {
	t.Helper()
	if _, err := os.Stat(e.trace); err == nil {
		if c, trees := snapshotTraceCount(t, e.trace, " clone "), snapshotTraceCount(t, e.trace, " ls-tree "); c != clones || trees != clones {
			t.Errorf("clone=%d tree=%d want=%d", c, trees, clones)
		}
	} else if clones != 0 {
		t.Errorf("missing Git trace: %v", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.requests) != len(want) {
		t.Errorf("requests %v want %v", e.requests, want)
	}
	for asset, n := range want {
		if e.requests[e.host+"/owner/repo/releases/download/v1/"+asset] != n {
			t.Errorf("requests %v want %v", e.requests, want)
		}
	}
}

func TestVersionedRemoteBootstrap(t *testing.T) {
	for _, host := range []string{"github.com", "gitee.com"} {
		for _, kind := range []string{"empty cache", "bad digest", "missing digest", "invalid digest", "bad archive", "multiple members", "untrusted certificate", "bad local tool"} {
			t.Run(host+"/"+kind, func(t *testing.T) {
				e := remoteEntry(t, host)
				f := e.f
				marker := filepath.Join(f.root, "unverified-tool")
				t.Setenv("AUTOLIB_TEST_EXEC_LOG", marker)
				tool := readTestFile(t, f.importerArchive(f.toolAsset, readTestFile(t, f.tool)))
				if kind == "bad digest" {
					tool = readTestFile(t, f.importerArchive(f.toolAsset, readTestFile(t, buildEntryTool(t, true))))
				}
				if kind == "bad archive" {
					tool = "broken archive"
				}
				if kind == "multiple members" {
					tool = readTestFile(t, testArchive(t, []archiveMember{{Name: f.toolAsset, Body: readTestFile(t, f.tool)}, {Name: "extra", Body: "invalid"}}))
				}
				toolAsset := f.toolAsset + ".tar.xz"
				e.asset(t, "tools/"+toolAsset, tool)
				digest := filepath.Join(e.repo, "tools", toolAsset+".sha256")
				if kind == "bad digest" {
					putTestFile(t, digest, strings.Repeat("0", 64)+"\n")
				}
				if kind == "invalid digest" {
					putTestFile(t, digest, "invalid\n")
				}
				if kind == "missing digest" {
					if err := os.Remove(digest); err != nil {
						t.Fatal(err)
					}
				}
				pkg := e.pkg(t, "demo", "1.0", "")
				e.consumer(t, "demo@1.0", "")
				if kind == "untrusted certificate" {
					e.ca = putTestFile(t, filepath.Join(f.root, "wrong-ca"), "not a CA")
				}
				if kind == "bad local tool" {
					putTestFile(t, filepath.Join(entryCache(f), f.toolAsset), "not executable")
				}
				out, err := e.configure(t)
				if (err == nil) != (kind == "empty cache") {
					t.Fatalf("configure: %v\n%s", err, out)
				}
				if _, err := os.Lstat(marker); !os.IsNotExist(err) {
					t.Fatalf("unverified tool executed: %v", err)
				}
				want := map[string]int{}
				clones := 1
				if kind == "empty cache" {
					if got := readTestFile(t, filepath.Join(entryCache(f), "demo/1.0/data")); got != "archive" {
						t.Fatalf("wrong remote bytes: %q", got)
					}
					want[pkg] = 1
				}
				if kind == "bad local tool" {
					clones = 0
				} else if kind != "missing digest" && kind != "invalid digest" && kind != "untrusted certificate" {
					want[toolAsset] = 1
				}
				e.counts(t, clones, want)
			})
		}
	}
}

func TestVersionedRemoteResume(t *testing.T) {
	for _, kind := range []string{"cached root missing dependency", "tool and package snapshot", "append package", "upstream changes", "local complete", "bad local package", "deleted snapshot", "damaged snapshot"} {
		t.Run(kind, func(t *testing.T) {
			e := remoteEntry(t, "gitee.com")
			f := e.f
			pkg := e.pkg(t, "demo", "1.0", "")
			toolAsset := f.toolAsset + ".tar.xz"
			e.asset(t, "tools/"+toolAsset, readTestFile(t, f.importerArchive(f.toolAsset, readTestFile(t, f.tool))))
			tail := ""
			want := map[string]int{pkg: 1}
			clones := 1
			if kind != "tool and package snapshot" {
				copyEntryTool(t, f, entryCache(f))
			} else {
				want[toolAsset] = 1
			}
			if kind == "cached root missing dependency" {
				cmakeEntryPackage(t, entryCache(f), "demo", "1.0", "leaf@2", "")
				want = map[string]int{e.pkg(t, "leaf", "2", ""): 1}
			}
			if kind == "append package" || kind == "upstream changes" || kind == "deleted snapshot" || kind == "damaged snapshot" {
				other := e.pkg(t, "extra", "2", "")
				want[other] = 1
				tail = "autolib_import(TARGET consumer INTERFACE extra)\n"
				if kind == "upstream changes" {
					update := putTestFile(t, filepath.Join(f.root, "update.cmake"), fmt.Sprintf(`file(WRITE [==[%s/extra/package.toml]==] [==[latest = "999"
]==])
execute_process(COMMAND git -C [==[%s]==] -c user.name=fixture -c user.email=fixture@example.invalid commit -am changed RESULT_VARIABLE status)
if(NOT status STREQUAL "0")
message(FATAL_ERROR update_failed)
endif()
`, filepath.ToSlash(e.repo), filepath.ToSlash(e.repo)))
					tail = "include([==[" + filepath.ToSlash(update) + "]==])\n" + tail
				}
				if kind == "deleted snapshot" || kind == "damaged snapshot" {
					mutation := `file(REMOVE_RECURSE "${snapshot}")`
					if kind == "damaged snapshot" {
						mutation = `file(WRITE "${snapshot}/extra/package.toml" "latest = []")`
					}
					tail = `get_property(snapshot GLOBAL PROPERTY _AUTOLIB_METADATA_ROOT)
file(REAL_PATH "${snapshot}" checked)
file(REAL_PATH "${CMAKE_BINARY_DIR}/autolib" work)
string(FIND "${checked}" "${work}/.metadata-" prefix)
if(NOT prefix EQUAL 0)
message(FATAL_ERROR unsafe_snapshot_path)
endif()
` + mutation + "\n" + tail
					delete(want, other)
				}
			}
			if kind == "local complete" || kind == "bad local package" {
				e.offline = true
				cmakeEntryPackage(t, entryCache(f), "demo", "1.0", "", "")
				want, clones = map[string]int{}, 0
				if kind == "bad local package" {
					putTestFile(t, filepath.Join(entryCache(f), "demo/1.0/autolib-import.cmake"), "broken")
				}
			}
			e.consumer(t, "demo@1.0", tail)
			out, err := e.configure(t)
			failure := kind == "bad local package" || kind == "deleted snapshot" || kind == "damaged snapshot"
			if (err != nil) != failure {
				t.Fatalf("configure: %v\n%s", err, out)
			}
			if !failure && kind != "local complete" && kind != "cached root missing dependency" {
				if got := readTestFile(t, filepath.Join(entryCache(f), "demo/1.0/data")); got != "archive" {
					t.Fatalf("wrong remote bytes: %q", got)
				}
			}
			if kind == "cached root missing dependency" {
				if got := readTestFile(t, filepath.Join(entryCache(f), "leaf/2/data")); got != "archive" {
					t.Fatalf("wrong dependency bytes: %q", got)
				}
			}
			e.counts(t, clones, want)
		})
	}
}
