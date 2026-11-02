package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCMakeGiteeSnapshotReuse(t *testing.T) {
	for _, recoverFailure := range []bool{false, true} {
		name := "clean"
		if recoverFailure {
			name = "retry"
		}
		t.Run(name, func(t *testing.T) {
			e := remoteEntry(t, "gitee.com")
			f := e.f
			toolAsset := f.toolAsset + ".tar.xz"
			e.asset(t, "autolib-importer/"+toolAsset, readTestFile(t, f.importerArchive(f.toolAsset, readTestFile(t, f.tool))))
			want := map[string]int{toolAsset: 1}
			for _, name := range []string{"demo", "dep", "extra"} {
				dependencies := ""
				adapter := "if(TARGET autolib::" + name + ")\nreturn()\nendif()\nadd_library(autolib::" + name + " INTERFACE IMPORTED GLOBAL)\nset_target_properties(autolib::" + name + " PROPERTIES INTERFACE_INCLUDE_DIRECTORIES \"${CMAKE_CURRENT_LIST_DIR}/include\")\n"
				header := "#pragma once\ninline int " + name + "_value() { return 1; }\n"
				if name == "demo" {
					dependencies = "dep@1.0"
					adapter += "set_target_properties(autolib::demo PROPERTIES INTERFACE_LINK_LIBRARIES autolib::dep)\n"
					header = "#pragma once\n#include <dep.h>\ninline int demo_value() { return dep_value() + 40; }\n"
				}
				prefix := strings.Replace(identityHeader(packageIdentity{name, "1.0", "any", "any"}), `set(AUTOLIB_DEPENDENCIES "")`, `set(AUTOLIB_DEPENDENCIES "`+dependencies+`")`, 1)
				archive := testArchive(t, []archiveMember{{Name: name + "/1.0/autolib-import.cmake", Body: prefix + adapter}, {Name: name + "/1.0/include/" + name + ".h", Body: header}})
				asset := name + "-1.0-any-any.tar.zst"
				e.asset(t, name+"/1.0/"+asset, readTestFile(t, archive))
				versionedLatest(t, e.repo, name, "1.0")
				want[asset] = 1
			}
			demo := "demo-1.0-any-any.tar.zst"
			validDemo := e.assets[demo]
			putTestFile(t, filepath.Join(f.source, "main.cpp"), "#include <demo.h>\n#include <extra.h>\nint main() { return demo_value() + extra_value() == 42 ? 0 : 1; }\n")
			putTestFile(t, filepath.Join(f.source, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(gitee_consumer LANGUAGES CXX)
set(AUTOLIB_LOCAL_ROOT "")
set(AUTOLIB_REMOTE_ROOT "https://gitee.com/owner/repo/releases/download/v1")
include([==[`+filepath.ToSlash(filepath.Join(entryCache(f), "autolib.cmake"))+`]==])
add_executable(consumer main.cpp)
autolib_import(TARGET consumer PRIVATE demo)
if(NOT AUTOLIB_PREPARED_PACKAGES STREQUAL "dep;demo" OR NOT AUTOLIB_PACKAGE_VERSION_dep STREQUAL "1.0")
 message(FATAL_ERROR incorrect_transitive_closure)
endif()
autolib_import(TARGET consumer PRIVATE extra)
`)
			if runtime.GOOS == "windows" {
				f.generator = "Visual Studio 17 2022"
			}
			args := []string{"-DCMAKE_SYSTEM_NAME=" + map[string]string{"windows": "Windows", "linux": "Linux"}[runtime.GOOS], "-DCMAKE_SYSTEM_PROCESSOR=" + map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]}
			clones := 1
			if recoverFailure {
				e.assets[demo] = "changed package"
				if out, err := e.configure(t, args...); err == nil || !strings.Contains(out, "SHA256 mismatch") {
					t.Fatalf("changed package rejection: %v\n%s", err, out)
				}
				if _, err := os.Stat(filepath.Join(entryCache(f), "demo/1.0")); !os.IsNotExist(err) {
					t.Fatal("committed bad package", err)
				}
				requireNoPackageStaging(t, entryCache(f))
				e.mu.Lock()
				e.assets[demo] = validDemo
				e.mu.Unlock()
				clones = 2
				want[demo] = 2
			}
			if out, err := e.configure(t, args...); err != nil {
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
			e.counts(t, clones, want)
			if out, err := e.configure(t, args...); err != nil {
				t.Fatalf("local reconfigure: %v\n%s", err, out)
			}
			e.counts(t, clones, want)
		})
	}
}
