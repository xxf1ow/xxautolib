package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestCMakeGiteeEnumerationReuse(t *testing.T) {
	for _, recoverFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed-first=%v", recoverFailure), func(t *testing.T) {
			f := newEntryFixture(t, false, "")
			tool := readTestFile(t, f.importerArchive(f.toolAsset, readTestFile(t, f.tool)))
			assets := []remoteAssetFixture{{id: 1, name: f.toolAsset + ".tar.xz", data: []byte(tool)}}
			for i, name := range []string{"demo", "dep", "extra"} {
				dependencies := ""
				adapter := "if(TARGET autolib::" + name + ")\nreturn()\nendif()\nadd_library(autolib::" + name + " INTERFACE IMPORTED GLOBAL)\nset_target_properties(autolib::" + name + " PROPERTIES INTERFACE_INCLUDE_DIRECTORIES \"${CMAKE_CURRENT_LIST_DIR}/include\")\n"
				header := "#pragma once\ninline int " + name + "_value() { return 1; }\n"
				if name == "demo" {
					dependencies = "dep"
					adapter += "set_target_properties(autolib::demo PROPERTIES INTERFACE_LINK_LIBRARIES autolib::dep)\n"
					header = "#pragma once\n#include <dep.h>\ninline int demo_value() { return dep_value() + 40; }\n"
				}
				prefix := strings.Replace(identityHeader(packageIdentity{name, "any", "any"}), `set(AUTOLIB_DEPENDENCIES "")`, `set(AUTOLIB_DEPENDENCIES "`+dependencies+`")`, 1)
				archive := testArchive(t, []archiveMember{{Name: name + "/autolib-import.cmake", Body: prefix + adapter}, {Name: name + "/include/" + name + ".h", Body: header}})
				assets = append(assets, remoteAssetFixture{id: i + 2, name: name + "-any-any.tar.zst", data: []byte(readTestFile(t, archive))})
			}
			for _, name := range []string{f.toolAsset, "demo-linux-aarch64.tar.zst"} {
				if err := os.Remove(filepath.Join(f.release, name)); err != nil {
					t.Fatal(err)
				}
			}
			putTestFile(t, filepath.Join(f.source, "main.cpp"), "#include <demo.h>\n#include <extra.h>\nint main() { return demo_value() + extra_value() == 42 ? 0 : 1; }\n")
			putTestFile(t, filepath.Join(f.source, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(gitee_consumer LANGUAGES CXX)
set(AUTOLIB_LOCAL_ROOT "${FIXTURE_RELEASE}")
include(autolib.cmake)
add_executable(consumer main.cpp)
autolib_import(TARGET consumer PRIVATE demo)
if(NOT AUTOLIB_PREPARED_PACKAGES STREQUAL "dep;demo")
 message(FATAL_ERROR incorrect_transitive_closure)
endif()
autolib_import(TARGET consumer PRIVATE extra)
`)
			if runtime.GOOS == "windows" {
				f.generator = "Visual Studio 17 2022"
			}
			var mu sync.Mutex
			counts := map[string]int{}
			fail := recoverFailure
			ca := entryHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				counts[r.URL.Path]++
				if r.Header.Get("Authorization") != "" {
					t.Error("consumer sent credentials")
				}
				switch r.URL.Path {
				case "/api/v5/repos/owner/repo/releases/tags/v1":
					files := map[string]any{}
					for _, a := range assets {
						files[a.name] = map[string]any{"sha256": fmt.Sprintf("%x", sha256.Sum256(a.data)), "gitee_asset_id": a.id}
					}
					index, _ := json.Marshal(map[string]any{"files": files})
					json.NewEncoder(w).Encode(map[string]any{"id": 42, "tag_name": "v1", "body": "<!-- autolib-gitee-sync:start -->\n```json\n" + string(index) + "\n```\n<!-- autolib-gitee-sync:end -->"})
				case "/api/v5/repos/owner/repo/releases/42/attach_files":
					page := []map[string]any{}
					for i, a := range assets {
						if (r.URL.Query().Get("page") == "1") == (i == 3) {
							continue
						}
						page = append(page, map[string]any{"id": a.id, "name": a.name, "size": len(a.data), "browser_download_url": fmt.Sprintf("https://gitee.com/download/%d", a.id)})
					}
					if r.URL.Query().Get("page") == "1" {
						for len(page) < 100 {
							page = append(page, map[string]any{"name": fmt.Sprintf("unused-%d", len(page))})
						}
					}
					json.NewEncoder(w).Encode(page)
				default:
					for _, a := range assets {
						if r.URL.Path == fmt.Sprintf("/download/%d", a.id) {
							if fail && a.id == 2 {
								fmt.Fprint(w, "changed package")
								return
							}
							w.Write(a.data)
							return
						}
					}
					w.WriteHeader(404)
				}
			}))
			args := []string{"-DAUTOLIB_REMOTE_ROOT=https://gitee.com/owner/repo/releases/download/v1", "-DCMAKE_TLS_CAINFO=" + filepath.ToSlash(ca), "-DCMAKE_SYSTEM_NAME=" + map[string]string{"windows": "Windows", "linux": "Linux"}[runtime.GOOS], "-DCMAKE_SYSTEM_PROCESSOR=" + map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]}
			if recoverFailure {
				if out, err := f.configure(args...); err == nil {
					t.Fatalf("accepted changed package: %s", out)
				}
				if _, err := os.Stat(filepath.Join(f.release, "demo")); !os.IsNotExist(err) {
					t.Fatal("committed bad package", err)
				}
				mu.Lock()
				fail = false
				mu.Unlock()
			}
			if out, err := f.configure(args...); err != nil {
				t.Fatalf("configure: %v\n%s", err, out)
			}
			build := filepath.Join(f.root, "consumer-build")
			if out, err := exec.Command(f.cmake, "--build", build, "--config", "Release").CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, out)
			}
			program := filepath.Join(build, "consumer")
			if runtime.GOOS == "windows" {
				program = filepath.Join(build, "Release", "consumer.exe")
			}
			if out, err := exec.Command(program).CombinedOutput(); err != nil {
				t.Fatalf("consumer: %v\n%s", err, out)
			}
			mu.Lock()
			enumerations := 1
			if recoverFailure {
				enumerations = 2
			}
			if counts["/api/v5/repos/owner/repo/releases/tags/v1"] != enumerations || counts["/api/v5/repos/owner/repo/releases/42/attach_files"] != 2*enumerations || counts["/download/1"] != 1 || counts["/download/2"] != enumerations || counts["/download/3"] != 1 || counts["/download/4"] != 1 {
				t.Error("enumeration/download reuse", counts)
			}
			before := fmt.Sprint(counts)
			mu.Unlock()
			if out, err := f.configure(args...); err != nil {
				t.Fatalf("local reconfigure: %v\n%s", err, out)
			}
			mu.Lock()
			after := fmt.Sprint(counts)
			mu.Unlock()
			if before != after {
				t.Fatalf("local cache used network: %s -> %s", before, after)
			}
		})
	}
}
