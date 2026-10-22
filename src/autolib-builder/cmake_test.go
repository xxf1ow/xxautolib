package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArgumentRoundTrip(t *testing.T) {
	f := newFixture(t)
	repo := f.repository("args", map[string]string{"x.h": "header"})
	m := f.definition("demo", "copy", "[source]\n"+repo+"[[copy]]\nfiles=['x.h']\ndestination='include'\n")
	capture := putFile(t, filepath.Join(f.root, "capture.cmake"), `file(MAKE_DIRECTORY "${OUT}")
math(EXPR last "${CMAKE_ARGC}-1")
foreach(i RANGE 4 ${last})
  file(WRITE "${OUT}/${i}.txt" "${CMAKE_ARGV${i}}")
endforeach()
`)
	want := []string{"中文 a b", `C:\a b\c`, "a;b", "${literal}", "__AL_LIST__", `a"b`}
	m.Steps = append(m.Steps, stepDefinition{Name: "capture", Dependees: []string{"build"}, Dependers: []string{"install"}, Commands: [][]string{append([]string{f.context.CMakeCommand, "-DOUT=<INSTALL_DIR>/capture", "-P", filepath.ToSlash(capture)}, want...)}})
	f.project([]*moduleDefinition{m}, "")
	f.build()
	for i, value := range want {
		requireFile(t, f.installed("demo", fmt.Sprintf("capture/%d.txt", 4+i)), value)
	}
	files, err := os.ReadDir(f.installed("demo", "capture"))
	if err != nil || len(files) != len(want) {
		t.Fatalf("capture count: %d, %v", len(files), err)
	}
}

func TestNativePipeline(t *testing.T) {
	t.Run("source subdir", func(t *testing.T) {
		f := newFixture(t)
		repo := f.repository("child", map[string]string{"public.h": "subdir header", "build/cmake/CMakeLists.txt": "cmake_minimum_required(VERSION 3.20)\nproject(child NONE)\ninstall(FILES ../../public.h DESTINATION include)\n"})
		m := f.definition("demo", "cmake", "[source]\n"+repo+"[cmake]\nSOURCE_SUBDIR='build/cmake'\n")
		if err := os.MkdirAll(filepath.Join(f.root, "git", "demo"), 0755); err != nil {
			t.Fatal(err)
		}
		f.project([]*moduleDefinition{m}, "")
		f.build()
		requireFile(t, f.installed("demo", "include/public.h"), "subdir header")
	})
	t.Run("resources and steps", func(t *testing.T) {
		f := newFixture(t)
		first := f.repository("first", map[string]string{"first.h": "first"})
		second := f.repository("second", map[string]string{"dir/second.h": "second"})
		m := f.definition("demo", "copy", "[resources.first]\n"+first+"[resources.second]\n"+second+"[[copy]]\nresource='first'\nfiles=['first.h']\ndestination='include'\n[[copy]]\nresource='second'\ndirectories=['.']\ndestination='.'\n")
		script := putFile(t, filepath.Join(f.root, "record.cmake"), "file(APPEND \"${LOG}\" \"${VALUE}\\n\")\n")
		log := filepath.ToSlash(filepath.Join(f.root, "sequence.txt"))
		commands := [][]string{}
		for _, v := range []string{"one", "two"} {
			commands = append(commands, []string{f.context.CMakeCommand, "-DLOG=" + log, "-DVALUE=" + v, "-P", filepath.ToSlash(script)})
		}
		m.Steps = append(m.Steps, stepDefinition{Name: "record", Dependees: []string{"build"}, Dependers: []string{"install"}, Depends: []string{filepath.ToSlash(script)}, Commands: commands})
		f.project([]*moduleDefinition{m}, "")
		f.build()
		requireFile(t, f.installed("demo", "include/first.h"), "first")
		requireFile(t, f.installed("demo", "dir/second.h"), "second")
		requireTextFile(t, log, "one\ntwo\n")
		secondBuild := f.build()
		if strings.ReplaceAll(readFile(t, log), "\r\n", "\n") != "one\ntwo\n" {
			t.Log(secondBuild)
		}
		requireTextFile(t, log, "one\ntwo\n")
		m.Copies[0].Files = []string{"missing.h"}
		f.project([]*moduleDefinition{m}, "")
		if _, _, err := f.command(f.context.CMakeCommand, "--build", f.context.BuildRoot, "--config", "Release"); err == nil {
			t.Fatal("missing copy succeeded")
		}
	})
}

func TestDefaultsAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		defaults map[string]string
		cache    map[string]any
		want     string
	}{
		{"engine", nil, nil, "3.5|17|ON|a;b|first path;second path"},
		{"parent", map[string]string{"CMAKE_CXX_STANDARD": "20", "CMAKE_POSITION_INDEPENDENT_CODE": "OFF"}, nil, "3.5|20|OFF|a;b|first path;second path"},
		{"module", map[string]string{"CMAKE_CXX_STANDARD": "20"}, map[string]any{"CMAKE_CXX_STANDARD": "23"}, "3.5|23|ON|a;b|first path;second path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			repo := f.repository("defaults", map[string]string{"CMakeLists.txt": `cmake_minimum_required(VERSION 3.20)
project(child NONE)
file(WRITE "${CMAKE_BINARY_DIR}/settings" "${CMAKE_POLICY_VERSION_MINIMUM}|${CMAKE_CXX_STANDARD}|${CMAKE_POSITION_INDEPENDENT_CODE}|${ONE}|${MANY}")
install(FILES "${CMAKE_BINARY_DIR}/settings" DESTINATION .)
`})
			m := f.definition("demo", "cmake", "[source]\n"+repo+"[cmake.cache]\nONE='a;b'\nMANY=['first path','second path']\n")
			for k, v := range tc.cache {
				m.Cache[k] = v
			}
			f.context.CMakeDefaults = tc.defaults
			f.project([]*moduleDefinition{m}, "")
			f.build()
			requireFile(t, f.installed("demo", "settings"), tc.want)
		})
	}
	f := newFixture(t)
	m := f.definition("demo", "cmake", testSource)
	actual := f.context
	crt := "MultiThreadedDLL"
	f.context.Generator = "Visual Studio 17 2022"
	f.context.Platform = "x64"
	f.context.Toolset = "v142,host=x64"
	f.context.Instance = "C:/VS instance"
	f.context.MSVCRuntimeLibrary = &crt
	f.context.MultiConfig = true
	generated, err := renderModule(m, f.context, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.context = actual
	putFile(t, filepath.Join(f.root, "modules.cmake"), generated)
	putFile(t, filepath.Join(f.root, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(fixture NONE)
include(ExternalProject)
include(modules.cmake)
foreach(key CMAKE_GENERATOR CMAKE_GENERATOR_PLATFORM CMAKE_GENERATOR_TOOLSET CMAKE_GENERATOR_INSTANCE CMAKE_ARGS BUILD_COMMAND INSTALL_COMMAND)
 get_property(value TARGET demo PROPERTY _EP_${key})
 file(WRITE "${CMAKE_BINARY_DIR}/${key}" "${value}")
endforeach()
`)
	f.configure()
	for key, want := range map[string]string{"CMAKE_GENERATOR": "Visual Studio 17 2022", "CMAKE_GENERATOR_PLATFORM": "x64", "CMAKE_GENERATOR_TOOLSET": "v142,host=x64", "CMAKE_GENERATOR_INSTANCE": "C:/VS instance"} {
		requireFile(t, filepath.Join(f.context.BuildRoot, key), want)
	}
	args := readFile(t, filepath.Join(f.context.BuildRoot, "CMAKE_ARGS"))
	if !strings.Contains(args, "CMAKE_MSVC_RUNTIME_LIBRARY:STRING=MultiThreadedDLL") || strings.Contains(args, "CMAKE_BUILD_TYPE") {
		t.Fatal(args)
	}
	for _, key := range []string{"BUILD_COMMAND", "INSTALL_COMMAND"} {
		if !strings.Contains(readFile(t, filepath.Join(f.context.BuildRoot, key)), "--config;Release") {
			t.Fatal(key)
		}
	}
	m.Cache["PYTHON_FOR_GENERATION"] = "<PYTHON_EXECUTABLE>"
	if _, err := renderModule(m, f.context, nil); err == nil || !strings.Contains(err.Error(), "PYTHON_EXECUTABLE") {
		t.Fatal(err)
	}
}

func TestPersistentInputs(t *testing.T) {
	f := newFixture(t)
	main := f.repository("main", map[string]string{"main.h": "original"})
	extra := f.repository("extra", map[string]string{"extra.h": "extra"})
	m := f.definition("demo", "copy", "[source]\n"+main+"[resources.extra]\n"+extra+"[[copy]]\nfiles=['main.h']\ndestination='first'\n[[copy]]\nresource='extra'\nfiles=['extra.h']\ndestination='second'\n")
	f.project([]*moduleDefinition{m}, "")
	f.build()
	putFile(t, filepath.Join(f.root, "git/demo/main.h"), "manual edit")
	putFile(t, filepath.Join(f.root, "git/_resources/demo/extra/extra.h"), "resource edit")
	m.Source.GitTag = "does-not-exist"
	r := m.Resources["extra"]
	r.GitTag = "does-not-exist"
	m.Resources["extra"] = r
	f.context.BuildRoot = filepath.Join(f.root, "fresh build")
	m.Copies[0].Destination = "fresh"
	m.Copies[1].Destination = "fresh"
	f.project([]*moduleDefinition{m}, "")
	f.build()
	requireFile(t, f.installed("demo", "fresh/main.h"), "manual edit")
	requireFile(t, f.installed("demo", "fresh/extra.h"), "resource edit")
	t.Run("git file", func(t *testing.T) {
		f := newFixture(t)
		m := f.definition("demo", "copy", testSource+"[[copy]]\nfiles=['x.h']\ndestination='include'\n")
		putFile(t, filepath.Join(f.root, "git/demo/.git"), "gitdir: unavailable")
		putFile(t, filepath.Join(f.root, "git/demo/x.h"), "local worktree")
		f.project([]*moduleDefinition{m}, "")
		f.build()
		requireFile(t, f.installed("demo", "include/x.h"), "local worktree")
	})
	for _, resource := range []bool{false, true} {
		t.Run(fmt.Sprint("hidden/", resource), func(t *testing.T) {
			f := newFixture(t)
			body := testSource + "[[copy]]\nfiles=['x.h']\ndestination='.'\n"
			local := filepath.Join(f.root, "git/demo")
			if resource {
				body = "[resources.extra]\nGIT_REPOSITORY='unused'\nGIT_TAG='fixed'\n[[copy]]\nresource='extra'\nfiles=['x.h']\ndestination='.'\n"
				local = filepath.Join(f.root, "git/_resources/demo/extra")
			}
			m := f.definition("demo", "copy", body)
			marker := putFile(t, filepath.Join(local, ".manual"), "preserve me")
			generated, err := renderModule(m, f.context, nil)
			if err != nil {
				t.Fatal(err)
			}
			putFile(t, filepath.Join(f.root, "modules.cmake"), generated)
			putFile(t, filepath.Join(f.root, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(fixture NONE)\ninclude(ExternalProject)\ninclude(modules.cmake)\n")
			out, stderr, err := f.command(f.context.CMakeCommand, "-S", f.root, "-B", f.context.BuildRoot)
			if err == nil || !strings.Contains(out+stderr, "nonempty directory") {
				t.Fatalf("%v %s%s", err, out, stderr)
			}
			requireFile(t, marker, "preserve me")
		})
	}
}

func TestSentinel(t *testing.T) {
	f := newFixture(t)
	m := f.definition("demo", "copy", "sentinel='include/x.h'\n"+testSource+"[resources.extra]\nGIT_REPOSITORY='unavailable'\nGIT_TAG='missing'\n[[copy]]\nfiles=['x.h']\ndestination='include'\n")
	sentinel := putFile(t, f.installed("demo", "include/x.h"), "cached")
	m.Patches = append(m.Patches, patchDefinition{File: filepath.Join(f.root, "must-not-apply.patch")})
	m.Steps = append(m.Steps, stepDefinition{Name: "must_not_run", Dependees: []string{"build"}, Commands: [][]string{{"unavailable-command"}}})
	f.project([]*moduleDefinition{m}, `if(TARGET _al_resource_4_demo_extra)
message(FATAL_ERROR "cached module contains work targets")
endif()
get_property(external TARGET demo PROPERTY _EP_IS_EXTERNAL_PROJECT)
if(external)
message(FATAL_ERROR "cached module is external")
endif()
`)
	f.build()
	requireFile(t, sentinel, "cached")
	if _, err := os.Stat(filepath.Join(f.root, "git")); !os.IsNotExist(err) {
		t.Fatal("cached module prepared source")
	}
	if err := os.Remove(sentinel); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := f.command(f.context.CMakeCommand, "-S", f.root, "-B", f.context.BuildRoot)
	if err == nil || !strings.Contains(out+stderr, "cached module contains work targets") {
		t.Fatalf("%v %s%s", err, out, stderr)
	}
}
