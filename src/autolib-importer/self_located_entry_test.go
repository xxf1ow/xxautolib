package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestImporterRetiredOptions(t *testing.T) {
	for _, option := range []string{"local-root", "work-root", "cmake-command"} {
		t.Run(option, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{"prepare", "--" + option + "=unused"}, &stdout, &stderr)
			if code == 0 || !strings.Contains(stderr.String(), "flag provided but not defined") {
				t.Fatalf("retired option accepted: code=%d stderr=%s", code, stderr.String())
			}
		})
	}
}

func TestSelfLocatedEntry(t *testing.T) {
	for _, kind := range []string{"complete", "missing tool", "missing root", "missing dependency", "damaged root"} {
		for _, variables := range []string{"unset", "normal", "cache", "environment"} {
			t.Run(kind+"/"+variables, func(t *testing.T) {
				f := versionedEntryFixture(t, false)
				cache := entryCache(f)
				copyEntryTool(t, f, cache)
				cmakeEntryPackage(t, cache, "demo", "1.0", "leaf@2", "")
				cmakeEntryPackage(t, cache, "leaf", "2", "", "")
				setup := ""
				switch variables {
				case "normal":
					setup = "set(AUTOLIB_LOCAL_ROOT [==[" + filepath.ToSlash(cache) + "]==])\nset(AUTOLIB_REMOTE_ROOT https://gitee.com/owner/repo/releases/download/v1)\n"
				case "cache":
					setup = "set(AUTOLIB_LOCAL_ROOT [==[" + filepath.ToSlash(cache) + "]==] CACHE STRING unused FORCE)\nset(AUTOLIB_REMOTE_ROOT https://gitee.com/owner/repo/releases/download/v1 CACHE STRING unused FORCE)\n"
				case "environment":
					t.Setenv("AUTOLIB_LOCAL_ROOT", cache)
					t.Setenv("AUTOLIB_REMOTE_ROOT", "https://gitee.com/owner/repo/releases/download/v1")
				}
				checks := `if(NOT AUTOLIB_PREPARED_PACKAGES STREQUAL "leaf;demo" OR NOT AUTOLIB_PACKAGE_DIR_demo STREQUAL [==[` + filepath.ToSlash(cache) + `/demo/1.0]==])
message(FATAL_ERROR "entry did not consume its precise same-root closure")
endif()
`
				versionedEntryConsumer(f, setup, "demo@1.0", checks)
				// The include happens in a function; subsequent imports use saved entry properties.
				consumer := filepath.Join(f.source, "CMakeLists.txt")
				body := readTestFile(t, consumer)
				body = strings.Replace(body, "include(", "function(load_entry)\ninclude(", 1)
				body = strings.Replace(body, "add_library(consumer", "endfunction()\nload_entry()\nadd_library(consumer", 1)
				putTestFile(t, consumer, body)
				switch kind {
				case "missing tool":
					if err := os.Remove(filepath.Join(cache, f.toolAsset)); err != nil {
						t.Fatal(err)
					}
				case "missing root":
					if err := os.Rename(filepath.Join(cache, "demo/1.0"), filepath.Join(cache, "demo/hidden")); err != nil {
						t.Fatal(err)
					}
				case "missing dependency":
					if err := os.Rename(filepath.Join(cache, "leaf/2"), filepath.Join(cache, "leaf/hidden")); err != nil {
						t.Fatal(err)
					}
				case "damaged root":
					putTestFile(t, filepath.Join(cache, "demo/1.0/autolib-import.cmake"), "broken")
				}
				before := sourceSnapshot(t, cache)
				trace := filepath.Join(f.root, "git.trace")
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(503) }))
				defer server.Close()
				t.Setenv("HTTPS_PROXY", server.URL)
				t.Setenv("https_proxy", server.URL)
				t.Setenv("NO_PROXY", "")
				t.Setenv("no_proxy", "")
				t.Setenv("GIT_TRACE", trace)
				t.Setenv("PATH", t.TempDir())
				out, err := f.configure()
				if (err == nil) != (kind == "complete") {
					t.Fatalf("configure: %v\n%s", err, out)
				}
				if kind != "complete" && !strings.Contains(out, "importer") {
					t.Fatalf("missing input diagnostic: %s", out)
				}
				if !slices.Equal(before, sourceSnapshot(t, cache)) {
					t.Fatal("entry changed installed inputs")
				}
				if _, err := os.Stat(trace); !os.IsNotExist(err) {
					t.Fatalf("local entry invoked Git: %v", err)
				}
				if requests.Load() != 0 {
					t.Fatalf("local entry performed %d HTTP requests", requests.Load())
				}
				if strings.Contains(out, "download failed") || strings.Contains(out, "metadata snapshot") {
					t.Fatalf("local entry attempted remote bootstrap: %s", out)
				}
			})
		}
	}
}
