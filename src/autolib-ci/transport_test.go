package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestCIInstalledTransport(t *testing.T) {
	f := installedCIFixture(t)
	requests := 0
	original := http.DefaultTransport
	http.DefaultTransport = ciNoRemoteTransport{request: func() { requests++ }}
	t.Cleanup(func() { http.DefaultTransport = original })
	t.Cleanup(func() {
		if requests != 0 {
			t.Errorf("native build issued %d remote API requests", requests)
		}
	})
	for _, layer := range []string{"prepare", "layer0", "layer1", "verify"} {
		f.options.Layer = layer
		if err := runCILayer(context.Background(), f.options); err != nil {
			t.Fatalf("%s: %v", layer, err)
		}
	}
	var result ciPlatformResult
	if err := decodeStrictJSONFile(filepath.Join(f.options.WorkRoot, "results", f.options.Platform+".json"), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Packages) != 4 {
		t.Fatalf("want four exact package results, got %+v", result)
	}
	got := []string{}
	for _, item := range result.Packages {
		data, _ := json.Marshal(item.Slot)
		got = append(got, string(data))
	}
	want := []string{`{"name":"dep","version":"1","os":"any","arch":"any"}`, `{"name":"queue","version":"1","os":"any","arch":"any"}`, `{"name":"eigen","version":"3","os":"any","arch":"any"}`, `{"name":"eigen","version":"5","os":"any","arch":"any"}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exact package identities: %v", got)
	}
	if err := finalizeCI(f.plan, f.identity, []ciPlatformResult{result}); err != nil {
		t.Fatal(err)
	}
	before := transportSnapshot(t, filepath.Join(f.options.WorkRoot, "install"))
	members := f.transport(t, true)
	for _, name := range []string{"transport.json", "plan.json", "result.json", "install/eigen/package.toml", "install/queue/package.toml", "install/dep/package.toml", "install/eigen/3/autolib-import.cmake", "install/eigen/5/autolib-import.cmake", "install/" + ciImporterExecutable(f.options.Platform)} {
		if _, ok := members[name]; !ok {
			t.Errorf("missing transport member %s", name)
		}
	}
	if string(members["install/eigen/package.toml"]) != "latest='3'\n" || string(members["install/queue/package.toml"]) != "latest='1'\n" {
		t.Fatal("package-level candidates lost or rewritten")
	}
	if !reflect.DeepEqual(before, transportSnapshot(t, filepath.Join(f.options.WorkRoot, "install"))) {
		t.Fatal("transport modified its installation input")
	}
	var manifest struct {
		Packages []struct {
			Name, Version, OS, Arch string
			Dependencies            []string
			Publish                 bool
		}
		Importer       string
		ProducerPrefix string `json:"producer_prefix"`
	}
	if err := json.Unmarshal(members["transport.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Packages) != 4 || manifest.Importer != "install/"+ciImporterExecutable(f.options.Platform) || manifest.ProducerPrefix != filepath.Join(f.options.WorkRoot, "install") {
		t.Fatalf("wrong transport facts: %+v", manifest)
	}
	for _, item := range manifest.Packages {
		if !item.Publish {
			t.Errorf("single-platform package lost publisher eligibility: %+v", item)
		}
		if item.Name == "eigen" && !reflect.DeepEqual(item.Dependencies, []string{"dep@1"}) {
			t.Errorf("lost exact shared dependency: %+v", item)
		}
	}
	for name := range members {
		if strings.Contains(name, ".tar.") {
			t.Errorf("native transport contains a Release archive: %s", name)
		}
	}
}

type ciNoRemoteTransport struct{ request func() }

func (transport ciNoRemoteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.request()
	return nil, fmt.Errorf("native CI must not request %s", request.URL)
}

func TestCISelectedPlatformCompletion(t *testing.T) {
	plan, identity, results := finalizeFixture()
	plan.Platforms = plan.Platforms[:1]
	results = results[:1]
	if err := finalizeCI(plan, identity, results); err != nil {
		t.Fatalf("verified selected platform must not require Release upload: %v", err)
	}
	all, _, complete := finalizeFixture()
	for _, change := range []string{"missing", "duplicate", "attempt", "sha"} {
		t.Run(change, func(t *testing.T) {
			items := append([]ciPlatformResult{}, complete...)
			switch change {
			case "missing":
				items = items[:2]
			case "duplicate":
				items[2] = items[0]
			case "attempt":
				items[0].Run.Attempt = "2"
			case "sha":
				items[0].Run.SHA = strings.Repeat("b", 40)
			}
			if err := finalizeCI(all, identity, items); err == nil {
				t.Fatalf("accepted %s selected-platform completion", change)
			}
		})
	}
}

func TestCITransportSafety(t *testing.T) {
	for _, kind := range []string{"relative-link", "escape-link", "absolute-link", "fifo", "incomplete", "package-root-link"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "fifo" && runtime.GOOS != "linux" {
				t.Skip("FIFO fixture requires Linux mkfifo")
			}
			f := transportInputFixture(t)
			install := filepath.Join(f.options.WorkRoot, "install")
			path := filepath.Join(install, "queue", "1", "link")
			switch kind {
			case "package-root-link":
				original := filepath.Join(install, "queue")
				outside := filepath.Join(f.options.WorkRoot, "outside-queue")
				if err := os.Rename(original, outside); err != nil {
					t.Fatal(err)
				}
				createCITransportSymlink(t, outside, original)
			case "relative-link":
				createCITransportSymlink(t, "header.h", path)
			case "escape-link":
				createCITransportSymlink(t, "../../../outside", path)
			case "absolute-link":
				createCITransportSymlink(t, filepath.Join(f.options.WorkRoot, "outside"), path)
			case "fifo":
				if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
					t.Fatalf("mkfifo: %v %s", err, out)
				}
			case "incomplete":
				var result map[string]any
				data, _ := os.ReadFile(filepath.Join(f.options.WorkRoot, "results", f.options.Platform+".json"))
				json.Unmarshal(data, &result)
				result["complete"] = false
				writeCIJSON(filepath.Join(f.options.WorkRoot, "results", f.options.Platform+".json"), result)
			}
			before := transportSnapshot(t, install)
			f.transport(t, kind == "relative-link")
			if !reflect.DeepEqual(before, transportSnapshot(t, install)) {
				t.Fatal("transport changed input type, bytes, permissions, time or link")
			}
		})
	}
}

func createCITransportSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" && (os.IsPermission(err) || strings.Contains(strings.ToLower(err.Error()), "privilege")) {
			t.Skipf("host does not permit creating symlink fixtures: %v", err)
		}
		t.Fatal(err)
	}
}

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

func (f installedFixture) transport(t *testing.T, success bool) map[string][]byte {
	t.Helper()
	output := filepath.Join(f.options.WorkRoot, "transport.tar")
	var stdout, stderr bytes.Buffer
	code := run([]string{"transport", "--source-root", f.options.SourceRoot, "--plan", f.options.PlanFile, "--run-identity", f.options.RunIdentityFile, "--platform", f.options.Platform, "--work-root", f.options.WorkRoot, "--output", output}, &stdout, &stderr)
	if (code == 0) != success {
		t.Fatalf("transport success=%t code=%d: %s", success, code, stderr.String())
	}
	if !success {
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			t.Fatal("rejected input left transport output")
		}
		return nil
	}
	file, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	members := map[string][]byte{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := members[header.Name]; exists {
			t.Fatalf("duplicate member %s", header.Name)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		members[header.Name] = body
		if strings.HasPrefix(header.Name, "install/autolib-importer-") && header.Mode&0111 == 0 {
			t.Fatal("importer lost executable mode")
		}
		if strings.HasSuffix(header.Name, "/link") && (header.Typeflag != tar.TypeSymlink || header.Linkname != "header.h") {
			t.Fatalf("relative link changed: %+v", header)
		}
	}
	return members
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
	platform := "linux-" + map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	work := filepath.Join(root, "build", "ci", "123-1", platform)
	f := installedFixture{identity: identity, options: ciRunOptions{SourceRoot: root, Platform: platform, WorkRoot: work, PlanFile: filepath.Join(root, "plan.json"), RunIdentityFile: filepath.Join(root, "run.json")}}
	f.plan = ciPlan{Publisher: platform, Platforms: []ciPlatformPlan{{ID: platform, Roots: []string{"queue@1"}, Description: moduleDescription{Roots: []string{"queue@1"}, Modules: []describedModule{{Name: "queue", Version: "1", Target: "fixture-queue", Depends: []string{}, PlatformIndependent: true}}}, Layers: [][]string{{"queue@1"}}}}}
	result := map[string]any{"run": identity, "platform": platform, "packages": []any{map[string]any{"slot": map[string]string{"name": "queue", "version": "1", "os": "any", "arch": "any"}, "built": true, "verified": true}}, "roots": []any{map[string]any{"root": "queue@1", "verified": true}}, "importer": map[string]any{"file": ciImporterExecutable(platform), "verified": true}, "complete": true, "error": ""}
	for file, value := range map[string]any{f.options.PlanFile: f.plan, f.options.RunIdentityFile: identity, filepath.Join(work, "results", platform+".json"): result} {
		if err := writeCIJSON(file, value); err != nil {
			t.Fatal(err)
		}
	}
	writeCIFixture(t, work, "install/queue/package.toml", "latest='1'\n")
	writeCIFixture(t, work, "install/queue/1/header.h", "fixture header\n")
	writeCIFixture(t, work, "install/"+ciImporterExecutable(platform), "bare verified host tool\n")
	if err := os.Chmod(filepath.Join(work, "install", ciImporterExecutable(platform)), 0755); err != nil {
		t.Fatal(err)
	}
	return f
}

func installedCIFixture(t *testing.T) installedFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux native fixture; Windows production identity retained by owning tests")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(repository, "build", "ci-test")
	os.MkdirAll(base, 0755)
	root, err := os.MkdirTemp(base, "installed transport 中文 ")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	for _, file := range []string{"CMakeLists.txt", "go.mod", "go.sum", ".github/ci-tools.json", "src/autolib.cmake", "tests/autolib/run-installed.cmake", "tests/ci/jobserver/Makefile", "tests/ci/jobserver/child.mk"} {
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
		recipe := "name='" + name + "'\nversion='" + version + "'\ntype='copy'\nplatform_independent=true\n"
		if item.depends != "" {
			recipe += "depends=['" + item.depends + "']\n"
		}
		recipe += "[source]\nGIT_REPOSITORY='local-fixture'\nGIT_TAG='fixed'\n[[copy]]\nfiles=['header.h']\ndestination='include'\n"
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
set(AUTOLIB_LOCAL_ROOT "${RELEASE_ROOT}")
include("${AUTOLIB_ENTRY}")
add_executable(autolib_smoke smoke.cpp)
autolib_import(TARGET autolib_smoke PRIVATE ${ROOT_PACKAGES})
if(ROOT_PACKAGES STREQUAL "eigen@3")
  target_compile_definitions(autolib_smoke PRIVATE EXPECTED_EIGEN=3)
elseif(ROOT_PACKAGES STREQUAL "eigen@5")
  target_compile_definitions(autolib_smoke PRIVATE EXPECTED_EIGEN=5)
endif()
file(WRITE "${CMAKE_BINARY_DIR}/prepared-state.cmake" "")
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
	platform := "linux-" + map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
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
	f := installedFixture{plan: plan, identity: identity, options: ciRunOptions{SourceRoot: root, PlanFile: filepath.Join(root, "plan.json"), RunIdentityFile: filepath.Join(root, "run.json"), Platform: platform, WorkRoot: work, ImporterFile: filepath.Join(work, "install", ciImporterExecutable(platform)), CMakeCommand: cmakeCommand, MakeProgram: makeProgram, Budget: 2}}
	if err := writeCIJSON(f.options.PlanFile, plan); err != nil {
		t.Fatal(err)
	}
	if err := writeCIJSON(f.options.RunIdentityFile, identity); err != nil {
		t.Fatal(err)
	}
	return f
}
