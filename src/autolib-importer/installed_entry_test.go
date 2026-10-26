package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstalledValidationDriver(t *testing.T) {
	tool := buildEntryTool(t, false)
	driver, err := filepath.Abs("../../tests/autolib/run-installed.cmake")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"complete", "missing tool", "missing descriptor"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			install := filepath.Join(root, "installed packages")
			putTestFile(t, filepath.Join(root, "src/autolib.cmake"), readTestFile(t, "../autolib.cmake"))
			putTestFile(t, filepath.Join(root, "tests/autolib/CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(installed_fixture NONE)
if(NOT ROOT_PACKAGES STREQUAL "flann;queue" OR NOT AUTOLIB_REMOTE_ROOT STREQUAL "" OR DEFINED INSTALL_ROOT)
    message(FATAL_ERROR "driver lost local-only roots or rejected the permitted producer prefix")
endif()
set(CMAKE_SYSTEM_NAME Linux)
set(CMAKE_SYSTEM_PROCESSOR aarch64)
set(AUTOLIB_LOCAL_ROOT "${RELEASE_ROOT}")
include("${AUTOLIB_ENTRY}")
add_library(app INTERFACE)
autolib_import(TARGET app INTERFACE ${ROOT_PACKAGES})
if(NOT AUTOLIB_PACKAGE_DIR_flann STREQUAL "`+filepath.ToSlash(install)+`/flann")
    message(FATAL_ERROR "driver did not consume the original installation")
endif()
file(WRITE "${CMAKE_BINARY_DIR}/prepared-root.txt" "${RELEASE_ROOT}")
enable_testing()
add_test(NAME installed COMMAND "${CMAKE_COMMAND}" -E true)
`)
			for _, name := range []string{"queue", "lz4", "flann"} {
				id := packageIdentity{name, "linux", "aarch64"}
				if name == "queue" {
					id.OS, id.Arch = "any", "any"
				}
				header := identityHeader(id)
				if name == "flann" {
					header = strings.Replace(header, `AUTOLIB_DEPENDENCIES ""`, `AUTOLIB_DEPENDENCIES "lz4"`, 1)
				}
				putTestFile(t, filepath.Join(install, name, "autolib-import.cmake"), header+"add_library(autolib::"+name+" INTERFACE IMPORTED GLOBAL)\n")
			}
			if kind != "missing tool" {
				asset := "autolib-importer-" + runtime.GOOS + "-" + map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
				if runtime.GOOS == "windows" {
					asset += ".exe"
				}
				path := putTestFile(t, filepath.Join(install, asset), readTestFile(t, tool))
				if err := os.Chmod(path, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "missing descriptor" {
				if err := os.Remove(filepath.Join(install, "flann/autolib-import.cmake")); err != nil {
					t.Fatal(err)
				}
			}
			generator := "Ninja"
			if runtime.GOOS == "linux" {
				generator = "Unix Makefiles"
			}
			args := []string{"-DSOURCE_ROOT=" + root, "-DINSTALL_ROOT=" + install, "-DVALIDATION_ROOT=" + filepath.Join(root, "build/validation"), "-DGENERATOR=" + generator, "-P", driver}
			out, err := exec.Command("cmake", args...).CombinedOutput()
			if (err == nil) != (kind == "complete") {
				t.Fatalf("%s: %v\n%s", kind, err, out)
			}
			if kind == "complete" {
				runs, _ := filepath.Glob(filepath.Join(root, "build/validation/run-*"))
				if len(runs) != 1 || !strings.Contains(readTestFile(t, filepath.Join(runs[0], "test.log")), "100% tests passed") {
					t.Fatal("driver did not execute CTest")
				}
			} else if !strings.Contains(string(out), "importer") {
				t.Fatalf("missing installation did not fail through consumption: %s", out)
			}
		})
	}
}
