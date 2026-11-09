package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVersionedInstallCoexistence(t *testing.T) {
	f := newEntryFixture(t)
	installRoot := filepath.Join(f.root, "versioned install")
	t.Setenv("AUTOLIB_INSTALL_ROOT", installRoot)
	for _, version := range []string{"3.4.0", "5.0.1"} {
		source := f.repository("eigen-"+version, map[string]string{
			"eigen-version.h": "#define FIXTURE_EIGEN_VERSION \"" + version + "\"\n",
			"CMakeLists.txt":  "cmake_minimum_required(VERSION 3.20)\nproject(eigen NONE)\ninstall(FILES eigen-version.h DESTINATION include)\n",
		})
		f.definitionVersion("eigen", version, "cmake", "sentinel='include/eigen-version.h'\n[source]\n"+source)
		putFile(t, filepath.Join(f.root, "src/dependencies/eigen", version, "autolib-import.cmake"), "# fixture adapter\n")
	}
	putFile(t, filepath.Join(f.root, "src/dependencies/eigen/package.toml"), "latest='9.9.9'\n")
	putFile(t, filepath.Join(f.root, "src/dependencies/eigen/9.9.9/module.toml"), "type='cmake'\npackage_platforms=['any-any']\n"+testSource)
	sentinel := putFile(t, filepath.Join(installRoot, "eigen/0.1/include/eigen-version.h"), "unrelated installed version\n")
	shared := f.repository("shared", map[string]string{"CMakeLists.txt": `cmake_minimum_required(VERSION 3.20)
project(shared NONE)
install(CODE "file(APPEND \"\${CMAKE_INSTALL_PREFIX}/install-count\" \"installed\\n\")")
`})
	f.definitionVersion("shared", "1", "cmake", "[source]\n"+shared)
	putFile(t, filepath.Join(f.root, "src/dependencies/shared/1/autolib-import.cmake"), "# fixture adapter\n")
	rootSource := f.repository("root", map[string]string{
		"probe.cpp": `#include <eigen-version.h>
#include <fstream>
int main(int argc, char** argv) {
    if (argc != 2) return 1;
    std::ofstream marker(argv[1], std::ios::binary);
    marker << "eigen=" << FIXTURE_EIGEN_VERSION;
    return marker ? 0 : 1;
}
`,
		"CMakeLists.txt": `cmake_minimum_required(VERSION 3.20)
project(root CXX)
add_executable(probe probe.cpp)
add_custom_command(OUTPUT "${CMAKE_BINARY_DIR}/marker"
    COMMAND "$<TARGET_FILE:probe>" "${CMAKE_BINARY_DIR}/marker"
    DEPENDS probe VERBATIM)
add_custom_target(marker ALL DEPENDS "${CMAKE_BINARY_DIR}/marker")
install(FILES "${CMAKE_BINARY_DIR}/marker" DESTINATION .)
`,
	})
	for _, root := range []struct{ name, version string }{{"left", "3.4.0"}, {"right", "5.0.1"}} {
		f.definitionVersion(root.name, "1", "cmake", "depends=['eigen@"+root.version+"','shared@1']\n[source]\n"+rootSource)
		putFile(t, filepath.Join(f.root, "src/dependencies", root.name, "1/autolib-import.cmake"), "# fixture adapter\n")
	}
	f.mustEntry("-DBUILD_LIST=left@1,right@1,left@1")
	f.build()
	requireFile(t, filepath.Join(installRoot, "left/1/marker"), "eigen=3.4.0")
	requireFile(t, filepath.Join(installRoot, "right/1/marker"), "eigen=5.0.1")
	requireFile(t, filepath.Join(installRoot, "shared/1/install-count"), "installed\n")
	requireFile(t, sentinel, "unrelated installed version\n")
	requireFile(t, filepath.Join(installRoot, "eigen/package.toml"), "latest='9.9.9'\n")
	if _, err := os.Stat(filepath.Join(installRoot, "eigen/9.9.9")); !os.IsNotExist(err) {
		t.Fatalf("unselected latest must remain uninstalled: %v", err)
	}
	for _, root := range []struct{ name, version string }{{"left", "3.4.0"}, {"right", "5.0.1"}} {
		metadata, err := readConsumptionMetadata(filepath.Join(installRoot, root.name, "1/autolib-import.cmake"))
		if err != nil || metadata.Name != root.name || metadata.Version != "1" || !reflect.DeepEqual(metadata.Dependencies, []string{"eigen@" + root.version, "shared@1"}) {
			t.Fatalf("wrong installed root identity: %+v, %v", metadata, err)
		}
	}
	f.build()
	requireFile(t, filepath.Join(installRoot, "shared/1/install-count"), "installed\n")
}

func TestVersionedBuildLocations(t *testing.T) {
	f := newEntryFixture(t)
	f.context.BuilderFile = filepath.Join(f.root, "builder")
	f.must(goExecutable(), "build", "-o", f.context.BuilderFile, ".")
	main := f.repository("main", map[string]string{"main.h": "main"})
	extra := f.repository("extra", map[string]string{"extra.h": "extra"})
	for _, version := range []string{"a+b", "a_b", "a-b"} {
		f.definitionVersion("demo", version, "copy", "sentinel='include/main.h'\n[source]\n"+main+"[resources.extra]\n"+extra+"[[copy]]\nfiles=['main.h']\ndestination='include'\n[[copy]]\nresource='extra'\nfiles=['extra.h']\ndestination='include'\n")
	}
	if err := generate(f.context, nil); err != nil {
		t.Fatal(err)
	}
	manifest := readFile(t, filepath.Join(f.context.BuildRoot, "generated/inputs.cmake"))
	untouched := putFile(t, filepath.Join(f.root, "3rdparty/untouched/package.toml"), "unrelated metadata\n")
	putFile(t, filepath.Join(f.root, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(fixture NONE)\ninclude(ExternalProject)\n"+manifest+"\nforeach(module IN LISTS XXAUTOLIB_GENERATED_MODULES)\ninclude(\"${module}\")\nendforeach()\n")
	f.configure()
	f.build()
	requireFile(t, filepath.Join(f.root, "3rdparty/demo/package.toml"), "latest='a-b'\n")
	requireFile(t, untouched, "unrelated metadata\n")
	metadataBefore, _ := os.Stat(filepath.Join(f.root, "3rdparty/demo/package.toml"))
	for version, target := range map[string]string{"a+b": "_al_pkg_64656d6f_612b62", "a_b": "_al_pkg_64656d6f_615f62", "a-b": "_al_pkg_64656d6f_612d62"} {
		for _, path := range []string{"git/demo/" + version + "/main.h", "git/_resources/demo/" + version + "/extra/extra.h", "build/generated/demo/" + version + ".cmake", "build/ep/demo/" + version + "/stamp/" + target + "-install", "3rdparty/demo/" + version + "/include/main.h", "3rdparty/demo/" + version + "/include/extra.h"} {
			if _, err := os.Stat(filepath.Join(f.root, path)); err != nil {
				t.Error(path, err)
			}
		}
	}
	putFile(t, filepath.Join(f.root, "3rdparty/demo/a+b/include/main.h"), "cached only a+b")
	f.configure()
	f.build()
	metadataAfter, _ := os.Stat(filepath.Join(f.root, "3rdparty/demo/package.toml"))
	if !metadataBefore.ModTime().Equal(metadataAfter.ModTime()) {
		t.Fatal("source-only reuse rewrote package metadata")
	}
	requireFile(t, filepath.Join(f.root, "3rdparty/demo/a+b/include/main.h"), "cached only a+b")
	requireFile(t, filepath.Join(f.root, "3rdparty/demo/a_b/include/main.h"), "main")
}

func TestVersionedDependencyWiring(t *testing.T) {
	f := newFixture(t)
	t.Setenv("PKG_CONFIG_PATH", "existing-pc")
	depSource := f.repository("dep", map[string]string{"dep.h": "#define DEP 17\n", "CMakeLists.txt": "cmake_minimum_required(VERSION 3.20)\nproject(dep NONE)\ninstall(FILES dep.h DESTINATION include)\ninstall(DIRECTORY DESTINATION lib/cmake/Dep)\ninstall(DIRECTORY DESTINATION lib/pkgconfig)\n"})
	for _, version := range []string{"1", "2"} {
		f.definitionVersion("dep", version, "cmake", "config_dir='lib/cmake/Dep'\npackage_name='Dep'\npkgconfig_dir='lib/pkgconfig'\n[source]\n"+depSource)
	}
	leafSource := f.repository("leaf", map[string]string{"check.cpp": "#include <dep.h>\nint value() { return DEP; }\n", "CMakeLists.txt": `cmake_minimum_required(VERSION 3.20)
project(leaf CXX)
file(WRITE "${CMAKE_BINARY_DIR}/paths" "${CMAKE_PREFIX_PATH}|${Dep_DIR}|$ENV{PKG_CONFIG_PATH}")
add_library(check STATIC check.cpp)
install(FILES "${CMAKE_BINARY_DIR}/paths" DESTINATION .)
`})
	for _, version := range []string{"1", "2"} {
		f.definitionVersion("leaf", version, "cmake", "depends=['dep@"+version+"']\n[source]\n"+leafSource)
	}
	modules, err := loadModuleClosure(f.root, []string{"leaf@1", "leaf@2"}, f.context.SystemName, f.context.Architecture)
	if err != nil {
		t.Fatal(err)
	}
	f.project(modules, "")
	f.build()
	separator := ":"
	if f.context.SystemName == "Windows" {
		separator = ";"
	}
	for _, version := range []string{"1", "2"} {
		prefix := filepath.ToSlash(filepath.Join(f.root, "3rdparty/dep", version))
		requireFile(t, filepath.Join(f.root, "3rdparty/leaf", version, "paths"), prefix+"|"+prefix+"/lib/cmake/Dep|"+prefix+"/lib/pkgconfig"+separator+"existing-pc")
	}
}

func TestVersionedConsumptionFreeze(t *testing.T) {
	f := newEntryFixture(t)
	if f.context.SystemName != "Darwin" {
		f.context.Architecture = "x86_64"
	}
	source := f.repository("leaf", map[string]string{"leaf.h": "leaf"})
	f.definitionVersion("eigen", "3.4.0", "cmake", testSource)
	f.definitionVersion("eigen", "5.0.1", "cmake", testSource)
	f.definitionVersion("leaf", "1", "copy", "depends=['eigen@3.4.0']\n[source]\n"+source+"[[copy]]\nfiles=['leaf.h']\ndestination='include'\n")
	putFile(t, filepath.Join(f.root, "src/dependencies/eigen/package.toml"), "latest='3.4.0'\n")
	for _, ref := range []string{"eigen/3.4.0", "eigen/5.0.1", "leaf/1"} {
		putFile(t, filepath.Join(f.root, "src/dependencies", ref, "autolib-import.cmake"), "# adapter\n")
	}
	modules, err := loadModuleClosure(f.root, []string{"leaf@1"}, f.context.SystemName, f.context.Architecture)
	if err != nil {
		t.Fatal(err)
	}
	f.context.BuilderFile = filepath.Join(f.root, "builder")
	f.must(goExecutable(), "build", "-o", f.context.BuilderFile, ".")
	putFile(t, filepath.Join(f.root, "3rdparty/eigen/3.4.0/autolib-import.cmake"), "set(AUTOLIB_PACKAGE_NAME \"eigen\")\nset(AUTOLIB_PACKAGE_VERSION \"3.4.0\")\nset(AUTOLIB_OS \""+strings.ToLower(f.context.SystemName)+"\")\nset(AUTOLIB_ARCH \""+normalizeArchitecture(f.context.Architecture)+"\")\nset(AUTOLIB_DEPENDENCIES \"\")\n# adapter\n")
	modules[0].Sentinel = "autolib-import.cmake"
	f.project(modules, "")
	putFile(t, filepath.Join(f.root, "src/dependencies/eigen/package.toml"), "latest='5.0.1'\n")
	f.build()
	output := filepath.Join(f.root, "3rdparty/leaf/1/autolib-import.cmake")
	metadata, err := readConsumptionMetadata(output)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "leaf" || metadata.Version != "1" || !reflect.DeepEqual(metadata.Dependencies, []string{"eigen@3.4.0"}) {
		t.Fatalf("installation reselected latest: %+v", metadata)
	}
	requireFile(t, filepath.Join(f.root, "3rdparty/eigen/package.toml"), "latest='5.0.1'\n")
	before, _ := os.Stat(output)
	f.build()
	after, _ := os.Stat(output)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("reuse rewrote unchanged metadata")
	}
	dep := filepath.Join(f.root, "3rdparty/eigen/3.4.0/autolib-import.cmake")
	putFile(t, filepath.Join(f.root, "3rdparty/eigen/5.0.1/autolib-import.cmake"), strings.Replace(readFile(t, dep), "\"3.4.0\"", "\"5.0.1\"", 1))
	baseArgs := []string{f.context.BuilderFile, "prepare-consumption", "--source-root=" + f.root, "--install-root=" + filepath.Join(f.root, "3rdparty"), "--name=leaf", "--version=1", "--os=" + strings.ToLower(f.context.SystemName), "--arch=" + normalizeArchitecture(f.context.Architecture)}
	for _, tc := range []struct {
		name         string
		dependencies []string
	}{
		{"missing", nil},
		{"bare", []string{"--dependency=eigen"}},
		{"mismatched declaration", []string{"--dependency=eigen@5.0.1"}},
		{"undeclared", []string{"--dependency=eigen@3.4.0", "--dependency=leaf@1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string{}, baseArgs...), tc.dependencies...)
			if _, _, err := f.command(append(args, "--phase=reuse")...); err == nil {
				t.Fatal("invalid frozen dependencies accepted")
			}
		})
	}
	args := append(append([]string{}, baseArgs...), "--dependency=eigen@3.4.0")
	f.must(append(append([]string{}, args...), "--dependency=eigen@3.4.0", "--phase=reuse")...)
	withoutVersion := []string{}
	for _, arg := range args {
		if arg != "--version=1" {
			withoutVersion = append(withoutVersion, arg)
		}
	}
	if _, _, err := f.command(append(withoutVersion, "--phase=reuse")...); err == nil {
		t.Fatal("missing --version accepted")
	}
	putFile(t, dep, strings.Replace(readFile(t, dep), "\"3.4.0\"", "\"5.0.1\"", 1))
	f.must(append(args, "--phase=begin")...)
	if _, _, err := f.command(append(args, "--phase=installed")...); err == nil {
		t.Fatal("wrong dependency version accepted")
	}
	requireFile(t, output, incompleteConsumption)
}

func TestVersionedUniversalSource(t *testing.T) {
	f := newFixture(t)
	for _, version := range []string{"1", "2"} {
		f.definitionVersion("headers", version, "copy", "package_platforms=['any-any']\n"+testSource+"[[copy]]\nfiles=['input.h']\ndestination='include'\n")
		putFile(t, filepath.Join(f.root, "src/dependencies/headers", version, "autolib-import.cmake"), "# adapter\n")
		putFile(t, filepath.Join(f.root, "git/headers", version, "input.h"), version)
		output := putFile(t, filepath.Join(f.root, "3rdparty/headers", version, "include/input.h"), version)
		o := consumptionOptions{SourceRoot: f.root, InstallRoot: filepath.Join(f.root, "3rdparty"), Name: "headers", Version: version, OS: "windows", Arch: "x86_64", Phase: "reuse"}
		if err := prepareConsumption(o); err != nil {
			t.Fatal(err)
		}
		metadata, err := readConsumptionMetadata(filepath.Join(filepath.Dir(filepath.Dir(output)), "autolib-import.cmake"))
		if err != nil || metadata.Version != version || metadata.OS != "any" || metadata.Arch != "any" {
			t.Fatalf("wrong universal identity: %+v, %v", metadata, err)
		}
	}
	putFile(t, filepath.Join(f.root, "3rdparty/headers/1/include/input.h"), "2")
	if err := prepareConsumption(consumptionOptions{SourceRoot: f.root, InstallRoot: filepath.Join(f.root, "3rdparty"), Name: "headers", Version: "1", OS: "linux", Arch: "aarch64", Phase: "reuse"}); err == nil || !strings.Contains(err.Error(), "differs from its source") {
		t.Fatalf("universal validation used the other version: %v", err)
	}
}

func TestVersionedConsumptionReuseIdentity(t *testing.T) {
	f := newEntryFixture(t)
	if f.context.SystemName != "Darwin" {
		f.context.Architecture = "x86_64"
	}
	f.context.BuilderFile = filepath.Join(f.root, "builder")
	f.must(goExecutable(), "build", "-o", f.context.BuilderFile, ".")
	for _, version := range []string{"1", "2"} {
		source := f.repository("dep"+version, map[string]string{"dep.h": "dependency " + version})
		f.definitionVersion("dep", version, "copy", "sentinel='include/dep.h'\n[source]\n"+source+"[[copy]]\nfiles=['dep.h']\ndestination='include'\n")
		putFile(t, filepath.Join(f.root, "src/dependencies/dep", version, "autolib-import.cmake"), "# dep adapter\n")
	}
	source := f.repository("leaf", map[string]string{"CMakeLists.txt": `cmake_minimum_required(VERSION 3.20)
project(leaf NONE)
file(READ "${CMAKE_PREFIX_PATH}/include/dep.h" dependency)
file(WRITE "${CMAKE_BINARY_DIR}/payload" "${dependency}")
install(FILES "${CMAKE_BINARY_DIR}/payload" DESTINATION .)
`})
	for _, version := range []string{"1", "2"} {
		f.definitionVersion("leaf", version, "cmake", "depends=['dep@"+version+"']\nsentinel='payload'\n[source]\n"+source)
	}
	adapter := putFile(t, filepath.Join(f.root, "src/dependencies/leaf/1/autolib-import.cmake"), "# original adapter\n")
	depLatest := putFile(t, filepath.Join(f.root, "src/dependencies/dep/package.toml"), "latest='1'\n")
	leafLatest := putFile(t, filepath.Join(f.root, "src/dependencies/leaf/package.toml"), "latest='1'\n")
	if err := generate(f.context, []string{"leaf@1"}); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(f.root, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(fixture NONE)\ninclude(ExternalProject)\ninclude(build/generated/inputs.cmake)\nforeach(module IN LISTS XXAUTOLIB_GENERATED_MODULES)\ninclude(\"${module}\")\nendforeach()\n")
	f.configure()
	f.build()
	output := filepath.Join(f.root, "3rdparty/leaf/1/autolib-import.cmake")
	before := readFile(t, output)
	requireFile(t, filepath.Join(f.root, "3rdparty/leaf/1/payload"), "dependency 1")
	putFile(t, depLatest, "latest='2'\n")
	leafRecipe := filepath.Join(f.root, "src/dependencies/leaf/1/module.toml")
	putFile(t, leafRecipe, strings.Replace(readFile(t, leafRecipe), "dep@1", "dep@2", 1))
	putFile(t, leafLatest, "latest='2'\n")
	putFile(t, adapter, "# changed adapter\n")
	if err := generate(f.context, []string{"leaf@1"}); err != nil {
		t.Fatal(err)
	}
	f.configure()
	out, stderr, err := f.command(f.context.CMakeCommand, "--build", f.context.BuildRoot, "--config", "Release")
	if err == nil {
		t.Error("sentinel reuse accepted changed exact dependencies")
	} else if !strings.Contains(out+stderr, "rebuild") {
		t.Errorf("dependency identity failure must request rebuild: %s%s", out, stderr)
	}
	requireFile(t, output, before)
	requireFile(t, filepath.Join(f.root, "3rdparty/leaf/1/payload"), "dependency 1")
	requireFile(t, filepath.Join(f.root, "3rdparty/leaf/package.toml"), "latest='1'\n")
	requireFile(t, filepath.Join(f.root, "3rdparty/dep/2/include/dep.h"), "dependency 2")
}

func TestVersionedClosureRoots(t *testing.T) {
	root := t.TempDir()
	definitionVersionFile(t, root, "eigen", "3.4.0", "cmake", testSource)
	definitionVersionFile(t, root, "eigen", "5.0.1", "cmake", testSource)
	definitionVersionFile(t, root, "left", "1", "cmake", "depends=['eigen@3.4.0']\n"+testSource)
	definitionVersionFile(t, root, "right", "1", "cmake", "depends=['eigen@5.0.1']\n"+testSource)
	modules, err := loadModuleClosure(root, []string{"left@1", "right@1", "left@1"}, "Linux", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	refs := []string{}
	for _, module := range modules {
		refs = append(refs, module.Name+"@"+module.Version)
	}
	if !reflect.DeepEqual(refs, []string{"eigen@3.4.0", "left@1", "eigen@5.0.1", "right@1"}) {
		t.Fatalf("independent roots must retain both versions with stable exact deduplication: %v", refs)
	}
	definitionVersionFile(t, root, "peer", "1", "cmake", "depends=['eigen@3.4.0']\n"+testSource)
	modules, err = loadModuleClosure(root, []string{"left@1", "peer@1"}, "Linux", "x86_64")
	if err != nil || len(modules) != 3 {
		t.Fatalf("shared exact dependency must appear once: %+v, %v", modules, err)
	}
	definitionVersionFile(t, root, "diamond", "1", "cmake", "depends=['left@1','right@1']\n"+testSource)
	for _, roots := range [][]string{{"diamond@1"}, {"left@1", "right@1", "diamond@1"}} {
		_, err := loadModuleClosure(root, roots, "Linux", "x86_64")
		for _, part := range []string{"diamond@1", "eigen@3.4.0", "eigen@5.0.1", "diamond@1 -> left@1 -> eigen@3.4.0", "diamond@1 -> right@1 -> eigen@5.0.1"} {
			if err == nil || !strings.Contains(err.Error(), part) {
				t.Fatalf("conflict must identify root, versions and both chains (%v): %v", roots, err)
			}
		}
	}
}

func TestVersionedClosureFreeze(t *testing.T) {
	root := t.TempDir()
	definitionVersionFile(t, root, "eigen", "3.4.0", "cmake", testSource)
	definitionVersionFile(t, root, "eigen", "5.0.1", "cmake", testSource)
	definitionVersionFile(t, root, "app", "1", "cmake", "depends=['eigen@3.4.0']\n"+testSource)
	metadata := filepath.Join(root, "src", "dependencies", "eigen", "package.toml")
	putFile(t, metadata, "latest='3.4.0'\n")
	modules, err := loadModuleClosure(root, []string{"app"}, "Linux", "x86_64")
	if err != nil || len(modules) != 2 || !reflect.DeepEqual(modules[1].Depends, []string{"eigen@3.4.0"}) {
		t.Fatalf("pinned dependencies must retain their declared version: %+v, %v", modules, err)
	}
	putFile(t, metadata, "latest='5.0.1'\n")
	modules, err = loadModuleClosure(root, []string{"app"}, "Linux", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(modules[1].Depends, []string{"eigen@3.4.0"}) {
		t.Fatal("a resolved dependency changed after latest changed")
	}
	definitionVersionFile(t, root, "app", "1", "cmake", "depends=['eigen@5.0.1']\n"+testSource)
	modules, err = loadModuleClosure(root, []string{"app"}, "Linux", "x86_64")
	if err != nil || !reflect.DeepEqual(modules[len(modules)-1].Depends, []string{"eigen@5.0.1"}) {
		t.Fatalf("an explicit dependency update must select the new version: %+v, %v", modules, err)
	}
	modules, err = loadModuleClosure(root, nil, "Linux", "x86_64")
	if err != nil || len(modules) != 3 {
		t.Fatalf("empty selection must discover every recipe version: %+v, %v", modules, err)
	}
}

func TestVersionedDescribe(t *testing.T) {
	var outputs [][]byte
	for range 2 {
		root := t.TempDir()
		describeRootInputs(t, root)
		definitionVersionFile(t, root, "eigen", "3.4.0", "cmake", testSource)
		definitionVersionFile(t, root, "app", "1", "cmake", "depends=['eigen@3.4.0']\n"+testSource)
		putFile(t, filepath.Join(root, "src/dependencies/app/1/autolib-import.cmake"), "# adapter\n")
		var stdout, stderr bytes.Buffer
		code := run([]string{"describe", "--source-root", root, "--system-name", "Linux", "--architecture", "x86_64", "--module", "app@1"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("describe failed: %d: %s", code, stderr.String())
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		var modules []map[string]json.RawMessage
		if err := json.Unmarshal(raw["modules"], &modules); err != nil {
			t.Fatal(err)
		}
		if len(modules) != 2 {
			t.Fatalf("wrong closure: %s", stdout.String())
		}
		app := modules[1]
		for key, want := range map[string]string{"name": `"app"`, "version": `"1"`, "depends": `["eigen@3.4.0"]`, "adapter_file": `"src/dependencies/app/1/autolib-import.cmake"`} {
			var compact bytes.Buffer
			if err := json.Compact(&compact, app[key]); err != nil || compact.String() != want {
				t.Fatalf("%s: got %s, want %s (%v)", key, app[key], want, err)
			}
		}
		var inputs []string
		if err := json.Unmarshal(app["input_files"], &inputs); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(inputs, []string{"src/dependencies/app/1/autolib-import.cmake", "src/dependencies/app/1/module.toml", "src/dependencies/app/package.toml"}) {
			t.Fatalf("missing recipe, adapter or latest metadata inputs: %v", inputs)
		}
		outputs = append(outputs, bytes.Clone(stdout.Bytes()))
	}
	if !bytes.Equal(outputs[0], outputs[1]) {
		t.Fatalf("checkout path affected description:\n%s\n%s", outputs[0], outputs[1])
	}
}
