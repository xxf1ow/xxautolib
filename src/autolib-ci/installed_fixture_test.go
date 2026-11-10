package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type installedFixture struct {
	options  ciRunOptions
	plan     ciPlan
	identity ciRunIdentity
}

func transportIdentity(t *testing.T, root string) ciRunIdentity {
	t.Helper()
	for _, args := range [][]string{{"init"}, {"symbolic-ref", "HEAD", "refs/heads/master"}, {"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, output)
		}
	}
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(head))
	return ciRunIdentity{Repository: "xxf1ow/xxautolib", Ref: "refs/heads/master", SHA: sha, CheckoutSHA: sha, RunID: "123", Attempt: "1"}
}

func transportSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		data := ""
		if info.Mode().IsRegular() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			data = string(body)
		} else if info.Mode()&os.ModeSymlink != 0 {
			data, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		out[path] = info.Mode().String() + info.ModTime().String() + data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func transportInputFixture(t *testing.T) installedFixture {
	t.Helper()
	root := t.TempDir()
	identity := transportIdentity(t, root)
	platform := runtime.GOOS + "-" + map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	work := filepath.Join(root, "build", "ci", "123-1", platform)
	f := installedFixture{identity: identity, options: ciRunOptions{Repository: ciGitHubRepository, SourceRoot: root, Platform: platform, WorkRoot: work, PlanFile: filepath.Join(root, "plan.json"), RunIdentityFile: filepath.Join(root, "run.json")}}
	f.plan = ciPlan{Publishers: map[string]string{"queue@1": platform}, Platforms: []ciPlatformPlan{{ID: platform, Roots: []string{"queue@1"}, Description: moduleDescription{Roots: []string{"queue@1"}, Modules: []describedModule{{Name: "queue", Version: "1", Target: "fixture-queue", Depends: []string{}, PackagePlatform: "any-any"}}}, Layers: [][]string{{"queue@1"}}}}}
	result := map[string]any{"run": identity, "platform": platform, "packages": []any{map[string]any{"slot": map[string]string{"name": "queue", "version": "1", "os": "any", "arch": "any"}, "built": true, "verified": true, "status": "stored"}}, "roots": []any{map[string]any{"root": "queue@1", "verified": true}}, "importer": map[string]any{"file": ciImporterExecutable(platform), "verified": true, "status": "stored"}, "complete": true, "processed": true, "error": ""}
	entry := "set(_autolib_release_root \"\")\n# installed fixture\n"
	writeCIFixture(t, root, "src/autolib.cmake", entry)
	writeCIFixture(t, work, "install/autolib.cmake", entry)
	result["entry"] = map[string]any{"file": "autolib.cmake", "sha256": testDigest([]byte(entry)), "verified": true}
	for file, value := range map[string]any{f.options.PlanFile: f.plan, f.options.RunIdentityFile: identity, filepath.Join(work, "results", platform+".json"): result} {
		if err := writeCIJSON(file, value); err != nil {
			t.Fatal(err)
		}
	}
	writeCIFixture(t, work, "install/queue/package.toml", "latest='1'\n")
	writeCIFixture(t, work, "install/queue/1/header.h", "fixture header\n")
	writeCIFixture(t, work, "install/queue/1/autolib-import.cmake", "set(AUTOLIB_PACKAGE_NAME \"queue\")\nset(AUTOLIB_PACKAGE_VERSION \"1\")\nset(AUTOLIB_OS \"any\")\nset(AUTOLIB_ARCH \"any\")\nset(AUTOLIB_DEPENDENCIES \"\")\n")
	fixup, err := os.ReadFile("../autolib-importer/fixup-metadata.cmake")
	if err != nil {
		t.Fatal(err)
	}
	writeCIFixture(t, root, "src/autolib-importer/fixup-metadata.cmake", string(fixup))
	builder, err := os.ReadFile(discoveryBuilder(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"build/tools", "tools"} {
		path := dir + "/autolib-builder" + executableSuffix()
		writeCIFixture(t, work, path, string(builder))
		if err := os.Chmod(filepath.Join(work, path), 0755); err != nil {
			t.Fatal(err)
		}
	}
	f.options.CMakeCommand, err = exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	writeCIFixture(t, work, "install/"+ciImporterExecutable(platform), "bare verified host tool\n")
	if err := os.Chmod(filepath.Join(work, "install", ciImporterExecutable(platform)), 0755); err != nil {
		t.Fatal(err)
	}
	return f
}

func installedCIFixture(t *testing.T) installedFixture {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("requires native Unix Makefiles fixture")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(repository, "build", "ci-test")
	os.MkdirAll(base, 0755)
	root, err := os.MkdirTemp(base, "installed fixture 中文 ")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	for _, file := range []string{"CMakeLists.txt", "go.mod", "go.sum", ".github/ci-tools.json", "src/autolib.cmake", "tests/autolib/run-installed.cmake", "tests/ci/jobserver/Makefile", "tests/ci/jobserver/child.mk", "tests/ci/jobserver/worker.cmake"} {
		data, err := os.ReadFile(filepath.Join(repository, file))
		if err != nil {
			t.Fatal(err)
		}
		writeCIFixture(t, root, file, string(data))
	}
	for _, directory := range []string{"src/autolib-builder", "src/autolib-importer", "src/internal/packagemetadata"} {
		entries, err := os.ReadDir(filepath.Join(repository, directory))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			file := filepath.Join(directory, entry.Name())
			data, err := os.ReadFile(filepath.Join(repository, file))
			if err != nil {
				t.Fatal(err)
			}
			writeCIFixture(t, root, file, string(data))
		}
	}
	if err := os.Symlink(filepath.Join(repository, ".tools"), filepath.Join(root, ".tools")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	for _, item := range []struct{ name, version, depends string }{{"dep", "1", ""}, {"queue", "1", ""}, {"eigen", "3", "dep@1"}, {"eigen", "5", "dep@1"}} {
		name, version := item.name, item.version
		recipe := "type='copy'\npackage_platforms=['any-any']\n"
		if item.depends != "" {
			recipe += "depends=['" + item.depends + "']\n"
		}
		recipe += "[source]\nGIT_REPOSITORY='local-fixture'\nGIT_TAG='0123456789012345678901234567890123456789'\n[[copy]]\nfiles=['header.h']\ndestination='include'\n"
		writeCIFixture(t, root, "src/dependencies/"+name+"/"+version+"/module.toml", recipe)
		writeCIFixture(t, root, "src/dependencies/"+name+"/package.toml", "latest='"+map[string]string{"dep": "1", "queue": "1", "eigen": "3"}[name]+"'\n")
		adapter := "add_library(autolib::" + name + " INTERFACE IMPORTED GLOBAL)\nset_target_properties(autolib::" + name + " PROPERTIES INTERFACE_INCLUDE_DIRECTORIES \"${CMAKE_CURRENT_LIST_DIR}/include\" INTERFACE_COMPILE_DEFINITIONS \"FIXTURE_" + strings.ToUpper(name) + "=" + version + "\")\n"
		if item.depends != "" {
			adapter += "target_link_libraries(autolib::eigen INTERFACE autolib::dep)\n"
		}
		writeCIFixture(t, root, "src/dependencies/"+name+"/"+version+"/autolib-import.cmake", adapter)
		writeCIFixture(t, root, "git/"+name+"/"+version+"/header.h", "#pragma once\n")
		command := exec.Command("git", "init", filepath.Join(root, "git", name, version))
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("local upstream: %v %s", err, out)
		}
	}
	writeCIFixture(t, root, "tests/autolib/CMakeLists.txt", `cmake_minimum_required(VERSION 3.20)
project(installed_fixture CXX)
enable_testing()
include("${AUTOLIB_ENTRY}")
add_executable(autolib_smoke smoke.cpp)
autolib_import(TARGET autolib_smoke PRIVATE ${ROOT_PACKAGES})
if(ROOT_PACKAGES STREQUAL "eigen@3")
  target_compile_definitions(autolib_smoke PRIVATE EXPECTED_EIGEN=3)
elseif(ROOT_PACKAGES STREQUAL "eigen@5")
  target_compile_definitions(autolib_smoke PRIVATE EXPECTED_EIGEN=5)
endif()
file(WRITE "${CMAKE_BINARY_DIR}/prepared-state.cmake" "")
file(WRITE "${CMAKE_BINARY_DIR}/link-inputs-Release.txt" "")
add_test(NAME exact_consumer COMMAND autolib_smoke)
`)
	writeCIFixture(t, root, "tests/autolib/smoke.cpp", `#include <cstdio>
int main() {
#ifdef EXPECTED_EIGEN
 static_assert(FIXTURE_EIGEN==EXPECTED_EIGEN);
 static_assert(FIXTURE_DEP==1);
 std::printf("eigen=%d dependency=%d\n", FIXTURE_EIGEN,FIXTURE_DEP);
#else
 static_assert(FIXTURE_QUEUE==1);
 std::puts("queue=1");
#endif
 return 0;
}
`)
	identity := transportIdentity(t, root)
	platform := runtime.GOOS + "-" + map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	builder := filepath.Join(root, "tools", "builder")
	command := exec.Command("cmake", "-P", filepath.Join(repository, "src/autolib-builder/go-sdk.cmake"), "--", "build", "-mod=readonly", "-trimpath", "-o", builder, "./src/autolib-builder")
	command.Dir = repository
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("builder: %v %s", err, out)
	}
	modules := "eigen@3,eigen@5,queue@1"
	plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Platforms: &platform, Modules: &modules})
	if err != nil {
		t.Fatal(err)
	}
	makeProgram, err := exec.LookPath("make")
	if err != nil {
		t.Fatal(err)
	}
	cmakeCommand, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "build", "ci", "123-1", platform)
	f := installedFixture{plan: plan, identity: identity, options: ciRunOptions{Repository: ciGitHubRepository, SourceRoot: root, PlanFile: filepath.Join(root, "plan.json"), RunIdentityFile: filepath.Join(root, "run.json"), Platform: platform, WorkRoot: work, ImporterFile: filepath.Join(work, "install", ciImporterExecutable(platform)), CMakeCommand: cmakeCommand, MakeProgram: makeProgram, Budget: 2}}
	if err := writeCIJSON(f.options.PlanFile, plan); err != nil {
		t.Fatal(err)
	}
	if err := writeCIJSON(f.options.RunIdentityFile, identity); err != nil {
		t.Fatal(err)
	}
	return f
}
