package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGenerationWrites(t *testing.T) {
	f := newEntryFixture(t)
	dep := definitionFile(t, f.root, "dep", "cmake", testSource+"[cmake.cache]\nVALUE='one'\n")
	definitionFile(t, f.root, "leaf", "cmake", "depends=['dep@1.0']\n"+testSource)
	definitionFile(t, f.root, "unused", "cmake", testSource)
	if err := generate(f.context, []string{"leaf"}); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(f.context.BuildRoot, "generated/inputs.cmake")
	before := readFile(t, manifest)
	stat, err := os.Stat(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"dep/1.0/module.toml", "leaf/1.0/module.toml", "dep/package.toml", "leaf/package.toml", "go.mod", "go.sum", "cmake.go", "apply_patch.cmake", "configure_dependencies.cmake"} {
		if !strings.Contains(before, part) {
			t.Fatalf("missing input %s", part)
		}
	}
	if strings.Contains(before, "unused/") {
		t.Fatal("unselected input")
	}
	if err := generate(f.context, []string{"leaf"}); err != nil {
		t.Fatal(err)
	}
	now, _ := os.Stat(manifest)
	if !stat.ModTime().Equal(now.ModTime()) {
		t.Fatal("unchanged output rewritten")
	}
	generated := filepath.Join(f.context.BuildRoot, "generated/dep/1.0.cmake")
	old := readFile(t, generated)
	putFile(t, dep, strings.ReplaceAll(readFile(t, dep), "VALUE='one'", "VALUE='two'"))
	if err := generate(f.context, []string{"leaf"}); err != nil {
		t.Fatal(err)
	}
	if readFile(t, generated) == old || !strings.Contains(readFile(t, generated), "VALUE:STRING=two") {
		t.Fatal("dependency change ignored")
	}
	putFile(t, dep, "name=[broken TOML")
	if err := generate(f.context, []string{"leaf"}); err == nil {
		t.Fatal("accepted malformed input")
	}
	if readFile(t, manifest) != before {
		t.Fatal("failed generation replaced inputs")
	}
	now, _ = os.Stat(manifest)
	if !stat.ModTime().Equal(now.ModTime()) {
		t.Fatal("failed generation rewrote inputs")
	}
	output := filepath.Join(f.root, "output.cmake")
	if changed, err := writeIfChanged(output, "one"); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if changed, err := writeIfChanged(output, "one"); err != nil || changed {
		t.Fatal(changed, err)
	}
	if changed, err := writeIfChanged(output, "two"); err != nil || !changed {
		t.Fatal(changed, err)
	}
}

func TestVersionedGeneratedSetupIsolation(t *testing.T) {
	f := newEntryFixture(t)
	depSource := f.repository("dep", map[string]string{"dep.h": "dependency"})
	f.definitionVersion("dep", "1", "copy", "[source]\n"+depSource+"[[copy]]\nfiles=['dep.h']\ndestination='include'\n")
	source := f.repository("demo", map[string]string{"input.h": "installed", "CMakeLists.txt": "cmake_minimum_required(VERSION 3.20)\nproject(demo NONE)\ninstall(FILES input.h DESTINATION include)\n"})
	f.definitionVersion("demo", "1", "cmake", "depends=['dep@1']\n[source]\n"+source)
	f.definitionVersion("demo", "1-dependency-setup", "cmake", "[source]\n"+source)
	f.definitionVersion("demo", "1.cmake", "cmake", "depends=['dep@1']\n[source]\n"+source)
	if err := generate(f.context, []string{"demo@1", "demo@1-dependency-setup", "demo@1.cmake"}); err != nil {
		t.Fatal(err)
	}
	modulePath := filepath.Join(f.context.BuildRoot, "generated/demo/1-dependency-setup.cmake")
	before := readFile(t, modulePath)
	putFile(t, filepath.Join(f.root, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(fixture NONE)\ninclude(ExternalProject)\ninclude(build/generated/inputs.cmake)\nforeach(module IN LISTS XXAUTOLIB_GENERATED_MODULES)\ninclude(\"${module}\")\nendforeach()\n")
	f.configure()
	if readFile(t, modulePath) != before {
		t.Error("dependency setup overwrote a version's module file")
	}
	f.configure()
	f.build()
	for _, version := range []string{"1", "1-dependency-setup", "1.cmake"} {
		requireFile(t, filepath.Join(f.root, "3rdparty/demo", version, "include/input.h"), "installed")
	}
	requireFile(t, modulePath, before)
}

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
	if !strings.Contains(generated, filepath.ToSlash(filepath.Join(f.context.InstallRoot, "app", "1.0"))) || !strings.Contains(generated, "[[_al_pkg_646570_312e30]]") || strings.Contains(generated, "3rdparty") {
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
	if !strings.Contains(readFile(t, filepath.Join(entry.context.BuildRoot, "generated", "demo", "1.0.cmake")), filepath.ToSlash(filepath.Join(installRoot, "demo", "1.0", "include", "x.h"))) {
		t.Fatal("AUTOLIB_INSTALL_ROOT did not reach the root builder sentinel path")
	}
	entry.build()
	requireFile(t, filepath.Join(installRoot, "demo", "1.0", "include", "x.h"), "demo")
}

func TestModuleClosureStable(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ name, depends string }{{"shared", "[]"}, {"left", "['shared']"}, {"right", "['shared']"}, {"leaf", "['left','right']"}, {"unused", "[]"}} {
		definitionFile(t, root, tc.name, "cmake", "depends="+tc.depends+"\n"+testSource)
	}
	modules, err := loadModuleClosure(root, []string{"leaf", "leaf"}, "Linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, m := range modules {
		names = append(names, m.Name)
	}
	if !reflect.DeepEqual(names, []string{"shared", "left", "right", "leaf"}) {
		t.Fatal(names)
	}
}

func TestModuleClosureErrors(t *testing.T) {
	for _, tc := range []struct{ left, right, chain string }{{"missing", "", "left@1.0 -> missing"}, {"left", "", "left@1.0 -> left@1.0"}, {"right", "left", "left@1.0 -> right@1.0 -> left@1.0"}} {
		t.Run(tc.chain, func(t *testing.T) {
			root := t.TempDir()
			definitionFile(t, root, "left", "cmake", "depends=['"+tc.left+"@1.0']\n"+testSource)
			if tc.right != "" {
				definitionFile(t, root, "right", "cmake", "depends=['"+tc.right+"@1.0']\n"+testSource)
			}
			_, err := loadModuleClosure(root, []string{"left"}, "Linux", "amd64")
			if err == nil || !strings.Contains(err.Error(), tc.chain) || !strings.Contains(err.Error(), "module.toml") || !strings.Contains(err.Error(), root) {
				t.Fatal(err)
			}
		})
	}
	if _, err := loadModuleClosure(t.TempDir(), []string{"../escape"}, "Linux", "amd64"); err == nil || !strings.Contains(err.Error(), "module reference") {
		t.Fatal(err)
	}
}
