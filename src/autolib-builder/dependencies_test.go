package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (f *fixture) child(name, body, metadata, subdir string, depends []string, files map[string]string) *moduleDefinition {
	f.t.Helper()
	if files == nil {
		files = map[string]string{}
	}
	files[filepath.ToSlash(filepath.Join(subdir, "CMakeLists.txt"))] = "cmake_minimum_required(VERSION 3.20)\nproject(child CXX)\n" + body + "\nfile(WRITE \"${CMAKE_BINARY_DIR}/marker\" \"installed\")\ninstall(FILES \"${CMAKE_BINARY_DIR}/marker\" DESTINATION .)\n"
	repo := f.repository(name, files)
	definition := metadata + "[source]\n" + repo
	if subdir != "" {
		definition += "[cmake]\nSOURCE_SUBDIR='" + subdir + "'\n"
	}
	m := f.definition(name, "cmake", definition)
	m.Depends = depends
	return m
}

func TestDirectDependencies(t *testing.T) {
	t.Setenv("PKG_CONFIG_PATH", "existing-pc-path")
	f := newFixture(t)
	shared := f.child("shared", "", "pkgconfig_dir='lib/pkgconfig'\n", "", nil, nil)
	dep := f.child("dep", "install(FILES dep.h DESTINATION include)", "config_dir='lib/cmake/Dep'\npackage_name='Dep'\npkgconfig_dir='lib/pkgconfig'\n", "", []string{"shared"}, map[string]string{"dep.h": "#define DEP 17\n"})
	sibling := f.child("sibling", "", "pkgconfig_dir='lib/pkgconfig'\n", "", nil, nil)
	caller := putFile(t, filepath.Join(f.root, "caller include.cmake"), "file(WRITE \"${CMAKE_BINARY_DIR}/caller\" \"caller preserved\")\n")
	leaf := f.child("leaf", `if(NOT EXISTS "${Dep_DIR}/../../../marker")
message(FATAL_ERROR "dependency not installed")
endif()
file(WRITE "${CMAKE_BINARY_DIR}/paths" "${CMAKE_PREFIX_PATH}|${Dep_DIR}|$ENV{PKG_CONFIG_PATH}")
file(WRITE "${CMAKE_BINARY_DIR}/args" "${ONE}|${MANY}|${CMAKE_CXX_STANDARD}")
add_library(check STATIC check.cpp)
install(FILES "${CMAKE_BINARY_DIR}/paths" "${CMAKE_BINARY_DIR}/args" "${CMAKE_BINARY_DIR}/caller" DESTINATION .)
`, "", "sub", []string{"dep"}, map[string]string{"sub/check.cpp": "#include <dep.h>\nint value() { return DEP; }\n"})
	leaf.Cache["CMAKE_PREFIX_PATH"] = []string{"caller path"}
	leaf.Cache["CMAKE_PROJECT_INCLUDE"] = filepath.ToSlash(caller)
	leaf.Cache["ONE"] = "a;b"
	leaf.Cache["MANY"] = []string{"first path", "second path"}
	f.project([]*moduleDefinition{shared, dep, sibling, leaf}, "")
	f.build()
	prefix := filepath.ToSlash(f.installed("dep", ""))
	separator := ":"
	if f.context.SystemName == "Windows" {
		separator = ";"
	}
	requireFile(t, f.installed("leaf", "paths"), prefix+";caller path|"+prefix+"/lib/cmake/Dep|"+prefix+"/lib/pkgconfig"+separator+"existing-pc-path")
	requireFile(t, f.installed("leaf", "args"), "a;b|first path;second path|17")
	requireFile(t, f.installed("leaf", "caller"), "caller preserved")
	if os.Getenv("PKG_CONFIG_PATH") != "existing-pc-path" {
		t.Fatal("environment changed")
	}
	fail := f.child("fail", "message(FATAL_ERROR \"intentional child failure\")", "", "", []string{"dep"}, nil)
	f.project([]*moduleDefinition{shared, dep, fail}, "")
	out, stderr, err := f.command(f.context.CMakeCommand, "--build", f.context.BuildRoot, "--config", "Release")
	if err == nil || !strings.Contains(out+stderr, "intentional child failure") {
		t.Fatalf("%v %s%s", err, out, stderr)
	}
	t.Run("cached dependency order", func(t *testing.T) {
		f := newFixture(t)
		dep := f.child("dep", "", "", "", nil, nil)
		cached := f.definition("cached", "copy", "sentinel='marker'\nconfig_dir='lib/cmake/Cached'\n"+testSource+"[[copy]]\nfiles=['unused']\ndestination='.'\n")
		cached.Depends = []string{"dep"}
		cached.Resources["unavailable"] = sourceDefinition{GitRepository: "unavailable", GitTag: "missing"}
		putFile(t, f.installed("cached", "marker"), "cached")
		leaf := f.child("leaf", "if(NOT EXISTS \""+filepath.ToSlash(f.installed("dep", "marker"))+"\")\nmessage(FATAL_ERROR \"cached dependency order lost\")\nendif()", "", "", []string{"cached"}, nil)
		f.project([]*moduleDefinition{dep, cached, leaf}, "")
		f.build()
		requireFile(t, f.installed("leaf", "marker"), "installed")
		if _, err := os.Stat(filepath.Join(f.root, "git/_resources/cached/unavailable")); !os.IsNotExist(err) {
			t.Fatal("cached resource work executed")
		}
	})
}
