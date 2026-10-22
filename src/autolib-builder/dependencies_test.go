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
	leaf := f.child("leaf", `if(NOT EXISTS "`+filepath.ToSlash(f.installed("dep", "marker"))+`")
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
		leaf := f.child("leaf", "if(NOT EXISTS \""+filepath.ToSlash(f.installed("dep", "marker"))+"\")\nmessage(FATAL_ERROR \"cached dependency order lost\")\nendif()\nif(NOT cached_DIR STREQUAL \""+filepath.ToSlash(f.installed("cached", "lib/cmake/Cached"))+"\")\nmessage(FATAL_ERROR \"cached metadata lost\")\nendif()", "", "", []string{"cached"}, nil)
		f.project([]*moduleDefinition{dep, cached, leaf}, "")
		f.build()
		requireFile(t, f.installed("leaf", "marker"), "installed")
		if _, err := os.Stat(filepath.Join(f.root, "git/_resources/cached/unavailable")); !os.IsNotExist(err) {
			t.Fatal("cached resource work executed")
		}
	})
}

func TestDependencyConfigurationIdentity(t *testing.T) {
	f := newFixture(t)
	dep := f.child("dep", "", "", "", nil, nil)
	leaf := f.child("leaf", "", "", "", []string{"dep"}, nil)
	crt := "MultiThreaded$<$<CONFIG:Debug>:Debug>DLL"
	context := f.context
	context.SystemName = "Windows"
	context.Generator = "Visual Studio 17 2022"
	context.Platform = "x64"
	context.Toolset = "v142,host=x64"
	context.Instance = "C:/VS instance"
	context.MultiConfig = true
	context.MSVCRuntimeLibrary = &crt
	for _, multi := range []bool{true, false} {
		if !multi {
			context.Generator = "Unix Makefiles"
			context.Platform, context.Toolset, context.Instance = "", "", ""
			context.MultiConfig = false
			context.MSVCRuntimeLibrary = nil
		}
		generated, err := renderModule(leaf, context, []*moduleDefinition{dep})
		if err != nil {
			t.Fatal(err)
		}
		putFile(t, filepath.Join(f.root, "modules.cmake"), generated)
		putFile(t, filepath.Join(f.root, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(fixture NONE)\ninclude(ExternalProject)\nadd_custom_target(dep)\ninclude(modules.cmake)\n")
		f.configure()
		script := putFile(t, filepath.Join(f.root, "inspect.cmake"), "include(\""+filepath.ToSlash(filepath.Join(f.context.BuildRoot, "generated/leaf-dependency-setup.cmake"))+"\")\nforeach(arg IN LISTS _al_command)\nmessage(\"ARG=${arg}\")\nendforeach()\n")
		out := f.must(f.context.CMakeCommand, "-P", script)
		if multi {
			for _, want := range []string{"ARG=Visual Studio 17 2022\n", "ARG=-A\n", "ARG=x64\n", "ARG=-T\n", "ARG=v142,host=x64\n", "ARG=-DCMAKE_GENERATOR_INSTANCE:INTERNAL=C:/VS instance\n", "ARG=-DCMAKE_MSVC_RUNTIME_LIBRARY:STRING=" + crt + "\n"} {
				if !strings.Contains(strings.ReplaceAll(out, "\r\n", "\n"), want) {
					t.Fatal(want, out)
				}
			}
			if strings.Contains(out, "CMAKE_BUILD_TYPE") {
				t.Fatal(out)
			}
		} else if !strings.Contains(out, "ARG=-DCMAKE_BUILD_TYPE:STRING=Release") || strings.Contains(out, "CMAKE_MSVC_RUNTIME_LIBRARY") {
			t.Fatal(out)
		}
	}
}
