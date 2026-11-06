package main

import (
	"maps"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func darwinTestDefaults(f *fixture) map[string]string {
	f.t.Helper()
	return map[string]string{
		"CMAKE_OSX_ARCHITECTURES": "arm64", "CMAKE_OSX_DEPLOYMENT_TARGET": "15.0",
		"CMAKE_C_COMPILER":   strings.TrimSpace(f.must("xcrun", "--find", "clang")),
		"CMAKE_CXX_COMPILER": strings.TrimSpace(f.must("xcrun", "--find", "clang++")),
		"CMAKE_OSX_SYSROOT":  strings.TrimSpace(f.must("xcrun", "--sdk", "macosx", "--show-sdk-path")),
	}
}

func TestDarwinCMakeParameters(t *testing.T) {
	f := newFixture(t)
	m := f.definition("probe", "cmake", testSource)
	f.context.SystemName = "Darwin"
	f.context.Architecture = "aarch64"
	f.context.CMakeDefaults = map[string]string{"CMAKE_OSX_ARCHITECTURES": "arm64", "CMAKE_OSX_DEPLOYMENT_TARGET": "15.0", "CMAKE_C_COMPILER": "/tools 中文/clang", "CMAKE_CXX_COMPILER": "/tools 中文/clang++", "CMAKE_OSX_SYSROOT": "/SDK 中文/macOS.sdk", "CMAKE_CXX_STANDARD": "20"}
	text, err := renderModule(m, f.context, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range f.context.CMakeDefaults {
		if !strings.Contains(text, "-D"+k+":STRING="+v) {
			t.Errorf("missing %s", k)
		}
	}
	for _, tc := range []struct{ key, value string }{{"CMAKE_OSX_ARCHITECTURES", "x86_64"}, {"CMAKE_OSX_ARCHITECTURES", "arm64;x86_64"}, {"CMAKE_OSX_DEPLOYMENT_TARGET", "16.0"}, {"CMAKE_C_COMPILER", "/other/clang"}, {"CMAKE_CXX_COMPILER", "/other/clang++"}, {"CMAKE_OSX_SYSROOT", "/other.sdk"}} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			m.Cache = map[string]any{tc.key: tc.value}
			if _, err := renderModule(m, f.context, nil); err == nil {
				t.Fatal("conflicting recipe accepted")
			}
		})
	}
	m.Cache = nil
	for _, key := range []string{"CMAKE_OSX_ARCHITECTURES", "CMAKE_OSX_DEPLOYMENT_TARGET", "CMAKE_C_COMPILER", "CMAKE_CXX_COMPILER", "CMAKE_OSX_SYSROOT"} {
		saved := f.context.CMakeDefaults[key]
		delete(f.context.CMakeDefaults, key)
		if _, err := renderModule(m, f.context, nil); err == nil {
			t.Errorf("missing %s accepted", key)
		}
		f.context.CMakeDefaults[key] = saved
	}
	for _, system := range []string{"Windows", "Linux"} {
		f.context.SystemName = system
		m.Cache = map[string]any{"CMAKE_CXX_STANDARD": "23"}
		text, err := renderModule(m, f.context, nil)
		if err != nil || !strings.Contains(text, "-DCMAKE_CXX_STANDARD:STRING=23") {
			t.Fatalf("%s precedence: %v", system, err)
		}
	}
}

func TestDarwinEntryCompiler(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Darwin ARM64")
	}
	f := newEntryFixture(t)
	defaults := darwinTestDefaults(f)
	putFile(t, filepath.Join(f.root, "src/autolib.cmake"), readFile(t, "../autolib.cmake"))
	repo := f.repository("probe", map[string]string{
		"CMakeLists.txt": `cmake_minimum_required(VERSION 3.20)
project(probe C CXX)
file(WRITE "${CMAKE_BINARY_DIR}/identity" "${CMAKE_C_COMPILER_ID}|${CMAKE_CXX_COMPILER_ID}")
add_library(probe STATIC probe.c probe.cpp)
install(TARGETS probe ARCHIVE DESTINATION lib)
`, "probe.c": "int probe(void) {return 1;}\n", "probe.cpp": "int cpp_probe() {return 2;}\n"})
	f.definition("probe", "cmake", "[source]\n"+repo)
	// Both producer and child report the actual compiler identity after project().
	p := filepath.Join(f.root, "CMakeLists.txt")
	putFile(t, p, readFile(t, p)+"\nfile(WRITE \"${CMAKE_BINARY_DIR}/identity\" \"${CMAKE_C_COMPILER_ID}|${CMAKE_CXX_COMPILER_ID}\")\n")
	f.context.CMakeDefaults = defaults
	args := []string{"-DBUILD_LIST=probe"}
	for k, v := range defaults {
		args = append(args, "-D"+k+"="+v)
	}
	f.mustEntry(args...)
	f.build()
	for _, root := range []string{f.context.BuildRoot, filepath.Join(f.context.BuildRoot, "ep/probe/1.0/bin")} {
		requireFile(t, filepath.Join(root, "identity"), "AppleClang|AppleClang")
		cache := readFile(t, filepath.Join(root, "CMakeCache.txt"))
		for k, v := range defaults {
			found := false
			for _, line := range strings.Split(cache, "\n") {
				if strings.HasPrefix(line, k+":") && strings.HasSuffix(line, "="+v) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s lacks %s=%s", root, k, v)
			}
		}
		t.Logf("verified root/child parameters: %s", root)
	}
	for _, tc := range []struct{ key, value string }{{"CMAKE_OSX_ARCHITECTURES", "x86_64"}, {"CMAKE_OSX_ARCHITECTURES", "arm64;x86_64"}, {"CMAKE_OSX_DEPLOYMENT_TARGET", "16.0"}, {"CMAKE_C_COMPILER", "/usr/bin/false"}, {"CMAKE_CXX_COMPILER", "/usr/bin/false"}} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			bad := maps.Clone(defaults)
			bad[tc.key] = tc.value
			f.context.BuildRoot = filepath.Join(f.root, "rejected "+strings.ReplaceAll(tc.key+tc.value, "/", "_"))
			a := []string{"-DBUILD_LIST=probe"}
			for k, v := range bad {
				a = append(a, "-D"+k+"="+v)
			}
			out, err := f.entry(a...)
			if err == nil || !strings.Contains(out, "Darwin native build") {
				t.Fatalf("invalid input not rejected by native guard: %v\n%s", err, out)
			}
		})
	}
}
