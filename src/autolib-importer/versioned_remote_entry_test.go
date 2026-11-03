package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type versionedRemoteEntry struct {
	f                     *entryFixture
	repo, trace, ca, host string
	mu                    sync.Mutex
	assets                map[string]string
	status                map[string]int
	redirects             map[string]string
	requests              map[string]int
	offline               bool
	apiRequests           int
	stopHTTP              func()
}

func remoteEntry(t *testing.T, host string) *versionedRemoteEntry {
	t.Helper()
	f := versionedEntryFixture(t, false)
	e := &versionedRemoteEntry{f: f, host: host, trace: filepath.Join(f.root, "git.trace"), assets: map[string]string{}, status: map[string]int{}, redirects: map[string]string{}, requests: map[string]int{}}
	e.repo = snapshotRepository(t, metadataRepositoryCase{Files: map[string]string{".autolib-repository.json": `{"type":"xxautolib-artifacts","schema":1,"upstream":"https://github.com/xxf1ow/xxautolib"}`}})
	redirect := "url.file://" + filepath.ToSlash(e.repo) + ".insteadOf=https://" + host + "/owner/repo.git"
	t.Setenv("GIT_CONFIG_PARAMETERS", "'"+strings.ReplaceAll(redirect, "'", "'\\''")+"'")
	t.Setenv("GIT_TRACE", e.trace)
	e.ca, e.stopHTTP = entryHTTPSProxyControl(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		defer e.mu.Unlock()
		key := r.Host + r.URL.Path
		e.requests[key]++
		if r.Header.Get("Authorization") != "" {
			t.Error("consumer sent credentials")
		}
		if r.Host != host || !strings.HasPrefix(r.URL.Path, "/owner/repo/releases/download/v1/") || strings.Contains(r.URL.Path, "/api/") || strings.Contains(r.URL.Path, "/contents/") {
			e.apiRequests++
			t.Errorf("forbidden request: %s%s", r.Host, r.URL)
			w.WriteHeader(500)
			return
		}
		name := filepath.Base(r.URL.Path)
		if status := e.status[name]; status != 0 {
			w.WriteHeader(status)
			return
		}
		if redirect := e.redirects[name]; redirect != "" {
			http.Redirect(w, r, redirect, http.StatusFound)
			return
		}
		body, ok := e.assets[name]
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

func (e *versionedRemoteEntry) configure(t *testing.T, extra ...string) (string, error) {
	t.Helper()
	snapshotGit(t, e.repo, "add", ".")
	snapshotGit(t, e.repo, "commit", "--allow-empty", "-m", "entry assets")
	if e.offline {
		t.Setenv("PATH", t.TempDir())
	}
	return e.f.configure(append([]string{"-DCMAKE_TLS_CAINFO=" + filepath.ToSlash(e.ca)}, extra...)...)
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
		for _, kind := range []string{"empty cache", "crlf digest", "padded digest", "embedded whitespace digest", "multiple digests", "bad digest", "missing digest", "invalid digest", "bad archive", "multiple members", "untrusted certificate", "bad local tool"} {
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
				e.asset(t, "autolib-importer/"+toolAsset, tool)
				digest := filepath.Join(e.repo, "autolib-importer", toolAsset+".sha256")
				success := slices.Contains([]string{"empty cache", "crlf digest", "padded digest"}, kind)
				sum := readTestFile(t, digest)
				switch kind {
				case "crlf digest":
					putTestFile(t, digest, strings.TrimSuffix(sum, "\n")+"\r\n")
				case "padded digest":
					putTestFile(t, digest, " \t\r\n"+sum+"\r\n\t ")
				case "embedded whitespace digest":
					putTestFile(t, digest, sum[:32]+" "+sum[32:])
				case "multiple digests":
					putTestFile(t, digest, sum+sum)
				}
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
				packageDigest := filepath.Join(e.repo, "demo", "1.0", pkg+".sha256")
				if kind == "crlf digest" {
					putTestFile(t, packageDigest, strings.TrimSuffix(readTestFile(t, packageDigest), "\n")+"\r\n")
				}
				if kind == "padded digest" {
					putTestFile(t, packageDigest, " \t\r\n"+readTestFile(t, packageDigest)+"\r\n\t ")
				}
				e.consumer(t, "demo@1.0", "")
				if kind == "untrusted certificate" {
					e.ca = putTestFile(t, filepath.Join(f.root, "wrong-ca"), "not a CA")
				}
				if kind == "bad local tool" {
					putTestFile(t, filepath.Join(entryCache(f), f.toolAsset), "not executable")
				}
				out, err := e.configure(t)
				if (err == nil) != success {
					t.Fatalf("configure: %v\n%s", err, out)
				}
				if _, err := os.Lstat(marker); !os.IsNotExist(err) {
					t.Fatalf("unverified tool executed: %v", err)
				}
				if !success && kind != "bad local tool" {
					if _, err := os.Lstat(filepath.Join(entryCache(f), f.toolAsset)); !os.IsNotExist(err) {
						t.Fatalf("failed bootstrap committed tool: %v", err)
					}
				}
				requireNoPackageStaging(t, entryCache(f))
				want := map[string]int{}
				clones := 1
				if success {
					if got := readTestFile(t, filepath.Join(entryCache(f), "demo/1.0/data")); got != "archive" {
						t.Fatalf("wrong remote bytes: %q", got)
					}
					want[pkg] = 1
				}
				if kind == "bad local tool" {
					clones = 0
				} else if !slices.Contains([]string{"missing digest", "invalid digest", "embedded whitespace digest", "multiple digests", "untrusted certificate"}, kind) {
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
			e.asset(t, "autolib-importer/"+toolAsset, readTestFile(t, f.importerArchive(f.toolAsset, readTestFile(t, f.tool))))
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

func TestVersionedRemoteExactThenBare(t *testing.T) {
	e := remoteEntry(t, "github.com")
	f := e.f
	cache := entryCache(f)
	copyEntryTool(t, f, cache)
	asset := e.pkg(t, "probe", "2", "")
	directory := filepath.Join(cache, "probe/2")
	checks := `if(NOT AUTOLIB_PACKAGE_VERSION_probe STREQUAL "2" OR NOT AUTOLIB_PACKAGE_DIR_probe STREQUAL [==[` + filepath.ToSlash(directory) + `]==])
message(FATAL_ERROR "unexpected probe identity or directory")
endif()
message(STATUS "selected_probe=${AUTOLIB_PACKAGE_VERSION_probe} directory=${AUTOLIB_PACKAGE_DIR_probe}")
`
	e.consumer(t, "probe@2", checks)
	if out, err := e.configure(t); err != nil {
		t.Fatalf("exact configure: %v\n%s", err, out)
	} else {
		t.Logf("exact configure:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(cache, "probe/package.toml")); !os.IsNotExist(err) {
		t.Fatalf("exact request created a selection record: %v", err)
	}
	before := sourceSnapshot(t, directory)
	e.counts(t, 1, map[string]int{asset: 1})
	t.Log("exact: clone=1 ls-tree=1 package_downloads=1 selection_record=absent")

	e.consumer(t, "probe", checks)
	if out, err := e.configure(t); err != nil {
		t.Fatalf("bare configure with exact cache: %v\n%s", err, out)
	} else {
		t.Logf("bare configure:\n%s", out)
	}
	if !slices.Equal(before, sourceSnapshot(t, directory)) {
		t.Fatal("bare request changed the existing version cache")
	}
	if got := readTestFile(t, filepath.Join(cache, "probe/package.toml")); got != "latest = \"2\"\n" {
		t.Fatalf("selection record = %q, want concrete version 2", got)
	}
	e.counts(t, 2, map[string]int{asset: 1})
	t.Log("bare: clone=2 ls-tree=2 package_downloads=1 selection_record=2 version_cache=unchanged")

	e.pkg(t, "probe", "3", "")
	if out, err := e.configure(t); err != nil {
		t.Fatalf("bare configure after remote latest changed: %v\n%s", err, out)
	} else {
		t.Logf("pinned configure:\n%s", out)
	}
	if !slices.Equal(before, sourceSnapshot(t, directory)) {
		t.Fatal("remote latest change modified the selected version cache")
	}
	if got := readTestFile(t, filepath.Join(cache, "probe/package.toml")); got != "latest = \"2\"\n" {
		t.Fatalf("remote latest changed the cached selection: %q", got)
	}
	for _, version := range []string{"3", "latest"} {
		if _, err := os.Lstat(filepath.Join(cache, "probe", version)); !os.IsNotExist(err) {
			t.Fatalf("unexpected cache version %s: %v", version, err)
		}
	}
	e.counts(t, 2, map[string]int{asset: 1})
	requireNoPackageStaging(t, cache)
	t.Log("pinned: remote_latest=3 selected=2 clone=2 ls-tree=2 package_downloads=1 Git_HTTP_delta=0 version_cache=unchanged")
}
