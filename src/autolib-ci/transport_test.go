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

	"github.com/klauspost/compress/zstd"
)

func TestCIPackagedTransport(t *testing.T) {
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
	if installed := mustReadText(t, filepath.Join(f.options.WorkRoot, "install", "autolib.cmake")); installed != mustReadText(t, filepath.Join(f.options.SourceRoot, "src", "autolib.cmake")) {
		t.Fatal("ordinary root configure did not install the source entry unchanged")
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
	if err := finalizeCI(f.plan, ciGitHubRepository, f.identity, []ciPlatformResult{result}); err != nil {
		t.Fatal(err)
	}
	before := transportSnapshot(t, filepath.Join(f.options.WorkRoot, "install"))
	members := f.transport(t, true)
	for _, name := range []string{"transport.json", "plan.json", "result.json", "autolib.cmake", "candidates/eigen/package.toml", "candidates/queue/package.toml", "candidates/dep/package.toml", "assets/eigen-3-any-any.tar.zst", "assets/eigen-5-any-any.tar.zst", "assets/queue-1-any-any.tar.zst", "assets/dep-1-any-any.tar.zst", "assets/" + ciImporterExecutable(f.options.Platform) + ".tar.xz"} {
		if _, ok := members[name]; !ok {
			t.Errorf("missing transport member %s", name)
		}
	}
	if string(members["candidates/eigen/package.toml"]) != "latest='3'\n" || string(members["candidates/queue/package.toml"]) != "latest='1'\n" {
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
		Entry  string
		Assets []struct {
			Name, Path, SHA256 string
			Size               int64
		}
	}
	if err := json.Unmarshal(members["transport.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Packages) != 4 || manifest.Entry != "autolib.cmake" || len(manifest.Assets) != 5 {
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
	for _, asset := range manifest.Assets {
		body, exists := members[asset.Path]
		if !exists || testDigest(body) != asset.SHA256 || int64(len(body)) != asset.Size {
			t.Errorf("transport changed final asset bytes: %+v", asset)
		}
	}
	for name := range members {
		if strings.HasPrefix(name, "install/") || strings.HasSuffix(name, ".sha256") {
			t.Errorf("unexpected unpackaged member: %s", name)
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
	if err := finalizeCI(plan, ciGitHubRepository, identity, results); err != nil {
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
			if err := finalizeCI(all, ciGitHubRepository, identity, items); err == nil {
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
			members := f.transport(t, kind == "relative-link")
			if kind == "relative-link" {
				decoder, err := zstd.NewReader(bytes.NewReader(members["assets/queue-1-any-any.tar.zst"]))
				if err != nil {
					t.Fatal(err)
				}
				defer decoder.Close()
				reader := tar.NewReader(decoder)
				found := false
				for {
					header, err := reader.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if header.Name == "queue/1/link" {
						found = header.Typeflag == tar.TypeSymlink && header.Linkname == "header.h"
					}
				}
				if !found {
					t.Fatal("library archive lost relative link")
				}
			}
			if !reflect.DeepEqual(before, transportSnapshot(t, install)) {
				t.Fatal("transport changed input type, bytes, permissions, time or link")
			}
		})
	}
}

func TestCIPackageSelection(t *testing.T) {
	for _, selection := range []string{"ordinary", "non-publisher", "tools"} {
		t.Run(selection, func(t *testing.T) {
			f := transportInputFixture(t)
			if selection == "non-publisher" {
				f.plan.Publisher = "linux-aarch64"
			}
			if selection == "ordinary" {
				f.plan.Platforms[0].Description.Modules[0].PlatformIndependent = false
				osName, arch, _ := strings.Cut(f.options.Platform, "-")
				writeCIFixture(t, f.options.WorkRoot, "install/queue/1/autolib-import.cmake", fmt.Sprintf("set(AUTOLIB_PACKAGE_NAME \"queue\")\nset(AUTOLIB_PACKAGE_VERSION \"1\")\nset(AUTOLIB_OS %q)\nset(AUTOLIB_ARCH %q)\nset(AUTOLIB_DEPENDENCIES \"\")\n", osName, arch))
			}
			resultFile := filepath.Join(f.options.WorkRoot, "results", f.options.Platform+".json")
			var result ciPlatformResult
			if err := decodeStrictJSONFile(resultFile, &result); err != nil {
				t.Fatal(err)
			}
			if selection == "tools" {
				f.plan.Platforms[0].Roots = []string{}
				f.plan.Platforms[0].Description.Roots = []string{}
				f.plan.Platforms[0].Description.Modules = []describedModule{}
				f.plan.Platforms[0].Layers = [][]string{}
				result.Packages, result.Roots = []ciSlotResult{}, []ciRootResult{}
			} else {
				result.Packages[0].Slot = ciModuleSlot(f.plan.Platforms[0].Description.Modules[0], f.options.Platform)
			}
			if err := writeCIJSON(f.options.PlanFile, f.plan); err != nil {
				t.Fatal(err)
			}
			if err := writeCIJSON(resultFile, result); err != nil {
				t.Fatal(err)
			}
			members := f.transport(t, true)
			var manifest struct {
				Assets   []struct{ Kind, Platform string }
				Packages []struct{ Publish bool }
			}
			if err := json.Unmarshal(members["transport.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			libraries, tools := 0, 0
			for _, asset := range manifest.Assets {
				switch asset.Kind {
				case "library":
					libraries++
				case "importer":
					tools++
					if asset.Platform != f.options.Platform {
						t.Fatal(asset)
					}
				default:
					t.Fatal(asset)
				}
			}
			want := 0
			if selection == "ordinary" {
				want = 1
			}
			if libraries != want || tools != 1 || len(members["autolib.cmake"]) == 0 {
				t.Fatalf("incorrect delivery: %s", members["transport.json"])
			}
			_, candidate := members["candidates/queue/package.toml"]
			if candidate != (selection != "tools") {
				t.Fatal("candidate coverage differs from planned libraries")
			}
		})
	}
}

func TestCIPackageFailure(t *testing.T) {
	for _, failure := range []string{"builder", "cmake"} {
		t.Run(failure, func(t *testing.T) {
			f := transportInputFixture(t)
			if failure == "builder" {
				if err := os.Remove(filepath.Join(f.options.WorkRoot, "build/tools/autolib-builder"+executableSuffix())); err != nil {
					t.Fatal(err)
				}
			} else {
				f.options.CMakeCommand = filepath.Join(t.TempDir(), "missing-cmake")
			}
			f.transport(t, false)
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
	code := run([]string{"transport", "--repository", f.options.Repository, "--source-root", f.options.SourceRoot, "--plan", f.options.PlanFile, "--run-identity", f.options.RunIdentityFile, "--platform", f.options.Platform, "--work-root", f.options.WorkRoot, "--cmake-command", f.options.CMakeCommand, "--output", output}, &stdout, &stderr)
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
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			t.Fatalf("outer transport member is not a file or directory: %+v", header)
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
	f := installedFixture{identity: identity, options: ciRunOptions{Repository: ciGitHubRepository, SourceRoot: root, Platform: platform, WorkRoot: work, PlanFile: filepath.Join(root, "plan.json"), RunIdentityFile: filepath.Join(root, "run.json")}}
	f.plan = ciPlan{Publisher: platform, Platforms: []ciPlatformPlan{{ID: platform, Roots: []string{"queue@1"}, Description: moduleDescription{Roots: []string{"queue@1"}, Modules: []describedModule{{Name: "queue", Version: "1", Target: "fixture-queue", Depends: []string{}, PlatformIndependent: true}}}, Layers: [][]string{{"queue@1"}}}}}
	result := map[string]any{"run": identity, "platform": platform, "packages": []any{map[string]any{"slot": map[string]string{"name": "queue", "version": "1", "os": "any", "arch": "any"}, "built": true, "verified": true}}, "roots": []any{map[string]any{"root": "queue@1", "verified": true}}, "importer": map[string]any{"file": ciImporterExecutable(platform), "verified": true}, "complete": true, "error": ""}
	entry := "set(_autolib_release_root \"\")\n# transport fixture\n"
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
	f := installedFixture{plan: plan, identity: identity, options: ciRunOptions{Repository: ciGitHubRepository, SourceRoot: root, PlanFile: filepath.Join(root, "plan.json"), RunIdentityFile: filepath.Join(root, "run.json"), Platform: platform, WorkRoot: work, ImporterFile: filepath.Join(work, "install", ciImporterExecutable(platform)), CMakeCommand: cmakeCommand, MakeProgram: makeProgram, Budget: 2}}
	if err := writeCIJSON(f.options.PlanFile, plan); err != nil {
		t.Fatal(err)
	}
	if err := writeCIJSON(f.options.RunIdentityFile, identity); err != nil {
		t.Fatal(err)
	}
	return f
}
