package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	for _, kind := range []string{"complete", "exact refs", "missing tool", "missing descriptor"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			install := filepath.Join(root, "installed packages")
			putTestFile(t, filepath.Join(root, "src/autolib.cmake"), readTestFile(t, "../autolib.cmake"))
			putTestFile(t, filepath.Join(install, "autolib.cmake"), readTestFile(t, "../autolib.cmake"))
			roots := "flann;queue"
			if kind == "exact refs" {
				roots = "flann@1.0;queue@1.0"
			}
			putTestFile(t, filepath.Join(root, "tests/autolib/CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(installed_fixture NONE)
if(NOT ROOT_PACKAGES STREQUAL "`+roots+`")
    message(FATAL_ERROR "driver lost local-only exact roots")
endif()
set(CMAKE_SYSTEM_NAME Linux)
set(CMAKE_SYSTEM_PROCESSOR aarch64)
include("${AUTOLIB_ENTRY}")
add_library(app INTERFACE)
autolib_import(TARGET app INTERFACE ${ROOT_PACKAGES})
get_filename_component(entry_root "${AUTOLIB_ENTRY}" DIRECTORY)
if(NOT AUTOLIB_PACKAGE_DIR_flann STREQUAL "${entry_root}/flann/1.0")
    message(FATAL_ERROR "driver did not use the original installation")
endif()
file(WRITE "${CMAKE_BINARY_DIR}/prepared-state.cmake" "set(AUTOLIB_FLANN_RUNTIME_DIR [==[${AUTOLIB_PACKAGE_DIR_flann}/lib]==])\n")
enable_testing()
add_test(NAME installed COMMAND "${CMAKE_COMMAND}" -E true)
`)
			for _, name := range []string{"queue", "lz4", "flann"} {
				id := packageIdentity{name, "1.0", "linux", "aarch64"}
				if name == "queue" {
					id.OS, id.Arch = "any", "any"
				}
				header := identityHeader(id)
				if name == "flann" {
					header = strings.Replace(header, `AUTOLIB_DEPENDENCIES ""`, `AUTOLIB_DEPENDENCIES "lz4@1.0"`, 1)
				}
				putTestFile(t, filepath.Join(install, name, "1.0", "autolib-import.cmake"), header+"add_library(autolib::"+name+" INTERFACE IMPORTED GLOBAL)\n")
				versionedLatest(t, install, name, "1.0")
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
				if err := os.Remove(filepath.Join(install, "flann/1.0/autolib-import.cmake")); err != nil {
					t.Fatal(err)
				}
			}
			generator := "Ninja"
			if runtime.GOOS == "linux" {
				generator = "Unix Makefiles"
			}
			args := []string{"-DSOURCE_ROOT=" + root, "-DINSTALL_ROOT=" + install, "-DVALIDATION_ROOT=" + filepath.Join(root, "build/validation"), "-DGENERATOR=" + generator, "-P", driver}
			if kind == "exact refs" {
				args = append([]string{"-DROOT_PACKAGES=" + roots}, args...)
			}
			installBefore := sourceSnapshot(t, install)
			sourceBefore := sourceSnapshot(t, filepath.Join(root, "src"))
			out, err := exec.Command("cmake", args...).CombinedOutput()
			complete := kind == "complete" || kind == "exact refs"
			if (err == nil) != complete {
				t.Fatalf("%s: %v\n%s", kind, err, out)
			}
			if !reflect.DeepEqual(installBefore, sourceSnapshot(t, install)) || !reflect.DeepEqual(sourceBefore, sourceSnapshot(t, filepath.Join(root, "src"))) {
				t.Fatal("driver modified the producer installation or source entry directory")
			}
			if complete {
				runs, _ := filepath.Glob(filepath.Join(root, "build/validation/run-*"))
				if len(runs) != 1 || !strings.Contains(readTestFile(t, filepath.Join(runs[0], "test.log")), "100% tests passed") {
					t.Fatal("driver did not execute CTest")
				}
				if configure := readTestFile(t, filepath.Join(runs[0], "configure.log")); strings.Contains(configure, "Ignoring extra path") {
					t.Fatalf("driver split list arguments into extra command-line paths:\n%s", configure)
				}
			} else if !strings.Contains(string(out), "importer") {
				t.Fatalf("missing installation did not fail through consumption: %s", out)
			}
		})
	}
}
