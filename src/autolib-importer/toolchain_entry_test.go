package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCMakeConsumerToolchainBaseline(t *testing.T) {
	f := newEntryFixture(t, true, "toolchain")
	calls := filepath.Join(f.root, "calls.jsonl")
	host := map[string]string{"linux": "Linux", "windows": "Windows", "darwin": "Darwin"}[runtime.GOOS]
	body := filepath.Join(f.source, "CMakeLists.txt")
	headers := filepath.Join(f.source, "headers", "features.h")

	configure := func(t *testing.T, system, language, settings string, want ...string) {
		t.Helper()
		if err := os.Remove(calls); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		putTestFile(t, body, fmt.Sprintf(`cmake_minimum_required(VERSION 3.20)
project(consume LANGUAGES %s)
set(CMAKE_SYSTEM_NAME %s)
set(CMAKE_SYSTEM_PROCESSOR arm64)
%s
include(autolib.cmake)
add_library(consumer INTERFACE)
autolib_import(TARGET consumer INTERFACE demo)
`, language, system, settings))
		out, err := f.configure("-B", filepath.Join(f.root, "probe-"+system+"-"+language), "-DCMAKE_SYSTEM_NAME="+host)
		if len(want) == 0 {
			if err != nil {
				t.Fatalf("configure compatible project: %v\n%s", err, out)
			}
			if _, err := os.Stat(calls); err != nil {
				t.Fatalf("compatible project did not invoke importer: %v", err)
			}
			return
		}
		if err == nil {
			t.Errorf("configure accepted a toolchain below the baseline:\n%s", out)
		}
		for _, part := range want {
			if !strings.Contains(out, part) {
				t.Errorf("expected diagnostic %q:\n%s", part, out)
			}
		}
		if _, err := os.Stat(calls); !os.IsNotExist(err) {
			t.Errorf("importer invocation log exists or cannot be checked: %v", err)
		}
	}

	t.Run("Windows", func(t *testing.T) {
		for _, tc := range []struct {
			name, language, setup string
			want                  []string
		}{
			{"MSVC C too old", "C", "set(CMAKE_C_COMPILER_ID MSVC)\nset(CMAKE_C_COMPILER_VERSION 19.43.10000)", []string{"MSVC", "19.43.10000", "19.44", "17.14"}},
			{"MSVC C++ exact", "CXX", "set(CMAKE_CXX_COMPILER_ID MSVC)\nset(CMAKE_CXX_COMPILER_VERSION 19.44)", nil},
			{"MSVC C++ newer", "CXX", "set(CMAKE_CXX_COMPILER_ID MSVC)\nset(CMAKE_CXX_COMPILER_VERSION 19.50)", nil},
			{"Clang with old MSVC ABI", "CXX", "set(CMAKE_CXX_COMPILER_ID Clang)\nset(CMAKE_CXX_SIMULATE_ID MSVC)\nset(CMAKE_CXX_SIMULATE_VERSION 19.43)", []string{"Clang", "19.43", "19.44"}},
			{"Clang with current MSVC ABI", "CXX", "set(CMAKE_CXX_COMPILER_ID Clang)\nset(CMAKE_CXX_SIMULATE_ID MSVC)\nset(CMAKE_CXX_SIMULATE_VERSION 19.44)", nil},
			{"Clang with GNU ABI", "CXX", "set(CMAKE_CXX_COMPILER_ID Clang)\nset(CMAKE_CXX_SIMULATE_ID GNU)", nil},
			{"no enabled compiler", "NONE", "", nil},
		} {
			t.Run(tc.name, func(t *testing.T) { configure(t, "Windows", tc.language, tc.setup, tc.want...) })
		}
	})

	t.Run("Linux", func(t *testing.T) {
		const settings = `set(CMAKE_C_FLAGS "${CMAKE_C_FLAGS} -I\"${CMAKE_CURRENT_SOURCE_DIR}/headers\"")`
		putTestFile(t, headers, "#define __GLIBC__ 2\n#define __GLIBC_MINOR__ 31\n")
		configure(t, "Linux", "C", settings)
		putTestFile(t, headers, "#define __GLIBC__ 2\n#define __GLIBC_MINOR__ 30\n")
		configure(t, "Linux", "C", settings, "glibc", "2.30", "2.31")
	})
	t.Run("Linux Release flags", func(t *testing.T) {
		putTestFile(t, headers, `#define __GLIBC__ 2
#ifdef AUTOLIB_TEST_OLD_GLIBC
#define __GLIBC_MINOR__ 30
#else
#define __GLIBC_MINOR__ 31
#endif
`)
		settings := `set(CMAKE_BUILD_TYPE Release)
set(CMAKE_C_FLAGS "${CMAKE_C_FLAGS} -I\"${CMAKE_CURRENT_SOURCE_DIR}/headers\"")
set(CMAKE_C_FLAGS_RELEASE "${CMAKE_C_FLAGS_RELEASE} -DAUTOLIB_TEST_OLD_GLIBC")`
		configure(t, "Linux", "C", settings, "glibc", "2.30", "2.31")
	})

	t.Run("Darwin", func(t *testing.T) {
		configure(t, "Darwin", "C", "set(CMAKE_OSX_DEPLOYMENT_TARGET 15.0)")
		configure(t, "Darwin", "C", "set(CMAKE_OSX_DEPLOYMENT_TARGET 14.0)", "14.0", "15.0", "CMAKE_OSX_DEPLOYMENT_TARGET")
		settings := `set(CMAKE_OSX_DEPLOYMENT_TARGET "")
set(CMAKE_C_FLAGS "${CMAKE_C_FLAGS} -D__ENVIRONMENT_MAC_OS_X_VERSION_MIN_REQUIRED__=140000")`
		if runtime.GOOS == "darwin" {
			settings = `set(CMAKE_OSX_DEPLOYMENT_TARGET "")
set(CMAKE_C_FLAGS "${CMAKE_C_FLAGS} -mmacosx-version-min=14.0")`
		}
		configure(t, "Darwin", "C", settings, "14.0", "15.0", "CMAKE_OSX_DEPLOYMENT_TARGET")
	})
}
