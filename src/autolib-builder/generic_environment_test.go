package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenericInstall(t *testing.T) {
	f := newFixture(t)
	f.context.InstallRoot = filepath.Join(f.root, "isolated install")
	depSource := f.repository("dep", map[string]string{"dep.h": "dependency header"})
	appSource := f.repository("app", map[string]string{"app.h": "application header"})
	dep := f.definition("dep", "copy", "[source]\n"+depSource+"[[copy]]\nfiles=['dep.h']\ndestination='include'\n")
	app := f.definition("app", "copy", "[source]\n"+appSource+"[[copy]]\nfiles=['app.h']\ndestination='include'\n")
	app.Depends = []string{"dep"}
	generated, err := renderModule(app, f.context, []*moduleDefinition{dep})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(generated, filepath.ToSlash(filepath.Join(f.context.InstallRoot, "app"))) || !strings.Contains(generated, "[[dep]]") || strings.Contains(generated, "3rdparty") {
		t.Fatalf("generated install/dependency prefix did not use isolated root:\n%s", generated)
	}
	f.project([]*moduleDefinition{dep, app}, "")
	f.build()
	requireFile(t, f.installed("dep", "include/dep.h"), "dependency header")
	requireFile(t, f.installed("app", "include/app.h"), "application header")
	if _, err := os.Stat(filepath.Join(f.root, "3rdparty")); !os.IsNotExist(err) {
		t.Fatalf("default installation root was also written: %v", err)
	}
	entry := newEntryFixture(t)
	installRoot := filepath.Join(entry.root, "isolated entry install")
	t.Setenv("AUTOLIB_INSTALL_ROOT", installRoot)
	entry.must(entry.context.GitExecutable, "init", entry.root)
	entry.must(entry.context.GitExecutable, "-C", entry.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture")
	module := entry.copyModule("demo")
	putFile(t, module.Path, "sentinel='include/x.h'\n"+readFile(t, module.Path))
	entry.mustEntry("-DBUILD_LIST=demo")
	if !strings.Contains(readFile(t, filepath.Join(entry.context.BuildRoot, "generated", "demo.cmake")), filepath.ToSlash(filepath.Join(installRoot, "demo", "include", "x.h"))) {
		t.Fatal("AUTOLIB_INSTALL_ROOT did not reach the root builder sentinel path")
	}
	entry.build()
	requireFile(t, filepath.Join(installRoot, "demo", "include", "x.h"), "demo")
}

func TestMakePropagation(t *testing.T) {
	f := newFixture(t)
	outerGenerator, outerPlatform, outerToolset := f.context.Generator, f.context.Platform, f.context.Toolset
	module := f.definition("demo", "cmake", testSource)
	f.context.Generator = "Unix Makefiles"
	f.context.SystemName = "Linux"
	f.context.Platform, f.context.Toolset = "", ""
	f.context.MakeProgram = "/opt/ci-tools/bin/make"
	f.context.CMakeDefaults = map[string]string{"CMAKE_INSTALL_LIBDIR": "lib64"}
	generated, err := renderModule(module, f.context, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(generated, "CMAKE_MAKE_PROGRAM:STRING=/opt/ci-tools/bin/make") || !strings.Contains(generated, "CMAKE_INSTALL_LIBDIR:STRING=lib64") {
		t.Fatalf("Unix Makefiles child did not receive make/libdir override:\n%s", generated)
	}
	module.Cache["CMAKE_INSTALL_LIBDIR"] = "module-lib"
	generated, err = renderModule(module, f.context, nil)
	if err != nil || !strings.Contains(generated, "CMAKE_INSTALL_LIBDIR:STRING=module-lib") {
		t.Fatalf("module libdir did not override parent value: %v\n%s", err, generated)
	}
	delete(module.Cache, "CMAKE_INSTALL_LIBDIR")
	dep := f.definition("make-dep", "cmake", testSource)
	module.Depends = []string{"make-dep"}
	depGenerated, err := renderModule(dep, f.context, nil)
	if err != nil {
		t.Fatal(err)
	}
	generated, err = renderModule(module, f.context, []*moduleDefinition{dep})
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(f.root, "modules.cmake"), depGenerated+"\n"+generated)
	putFile(t, filepath.Join(f.root, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(fixture NONE)\ninclude(ExternalProject)\ninclude(modules.cmake)\n")
	args := []string{f.context.CMakeCommand, "-S", f.root, "-B", f.context.BuildRoot, "-G", outerGenerator}
	if outerPlatform != "" {
		args = append(args, "-A", outerPlatform, "-T", outerToolset)
	}
	f.must(args...)
	setup := readFile(t, filepath.Join(f.context.BuildRoot, "generated", "demo-dependency-setup.cmake"))
	if !strings.Contains(setup, "-DCMAKE_MAKE_PROGRAM:STRING=/opt/ci-tools/bin/make") {
		t.Fatalf("generated ExternalProject child configure omitted pinned Make program:\n%s", setup)
	}
	f.context.Generator, f.context.SystemName = "Visual Studio 17 2022", "Windows"
	f.context.Platform, f.context.Toolset = "x64", "v143,host=x64"
	f.context.MakeProgram = "/must/not/be/forwarded/make"
	generated, err = renderModule(module, f.context, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(generated, "CMAKE_MAKE_PROGRAM") || strings.Contains(generated, "must/not/be/forwarded") {
		t.Fatalf("Windows generator received Make argument:\n%s", generated)
	}
	f.context.MakeProgram, f.context.CMakeDefaults = "", nil
	f.context.Generator, f.context.SystemName = "Unix Makefiles", "Linux"
	f.context.Platform, f.context.Toolset = "", ""
	generated, err = renderModule(module, f.context, nil)
	if err != nil || !strings.Contains(generated, "CMAKE_INSTALL_LIBDIR:STRING=lib") {
		t.Fatalf("default libdir missing: %v\n%s", err, generated)
	}
}
