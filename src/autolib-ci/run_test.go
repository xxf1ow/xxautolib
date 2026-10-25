package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type layerFixture struct {
	options ciRunOptions
	client  *githubClient
	plan    ciPlan
	run     ciRunIdentity
	writes  []string
	reads   int
}

func newLayerFixture(t *testing.T, modules []string) *layerFixture {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("command fixture emits native Windows consumer evidence")
	}
	root, identity := publishTestIdentity(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(root, "tools", "fixture.exe")
	if err := os.MkdirAll(filepath.Dir(tool), 0755); err != nil {
		t.Fatal(err)
	}
	if err := copyCIFile(executable, tool); err != nil {
		t.Fatal(err)
	}
	executable = tool
	f := &layerFixture{run: identity}
	f.plan = ciPlan{RequestedModules: modules, Publisher: "windows-x86_64"}
	description := moduleDescription{SystemName: "Windows", Architecture: "x86_64", GoModuleFiles: []string{}, BuilderFiles: []string{}}
	for _, name := range modules {
		m := describedModule{Name: name, Depends: []string{}, PlatformIndependent: name == "queue", Configuration: json.RawMessage(`{}`), InputFiles: []string{}, AdapterFile: "src/dependencies/" + name + "/autolib-import.cmake"}
		if name == "flann" {
			m.Depends = []string{"lz4"}
		}
		description.Modules = append(description.Modules, m)
		writeCIFixture(t, root, m.AdapterFile, "# fixture adapter\n")
	}
	layers, err := ciLayers(description.Modules)
	if err != nil {
		t.Fatal(err)
	}
	f.plan.Platforms = []ciPlatformPlan{{ID: "windows-x86_64", Description: description, Layers: layers}}
	config, err := os.ReadFile(filepath.Join("..", "..", ".github", "ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeCIFixture(t, root, ".github/ci-tools.json", string(config))
	for file, value := range map[string]any{"plan.json": f.plan, "run.json": identity} {
		data, _ := json.Marshal(value)
		writeCIFixture(t, root, file, string(data))
	}
	work := filepath.Join(root, "build", "ci", identity.RunID+"-"+identity.Attempt, "windows-x86_64")
	f.options = ciRunOptions{SourceRoot: root, PlanFile: filepath.Join(root, "plan.json"), RunIdentityFile: filepath.Join(root, "run.json"), Platform: "windows-x86_64", WorkRoot: work, BuilderFile: executable, ImporterFile: executable, CMakeCommand: executable}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			f.writes = append(f.writes, r.Method+" "+r.URL.Path)
		}
		switch {
		case r.URL.Path == "/repos/xxf1ow/xxautolib":
			fmt.Fprint(w, `{"full_name":"xxf1ow/xxautolib","default_branch":"master"}`)
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			f.reads++
			fmt.Fprint(w, `{"id":42,"tag_name":"autolib-release","assets_url":"`+serverURL(r)+`/repos/xxf1ow/xxautolib/releases/42/assets"}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/assets"):
			fmt.Fprint(w, `[]`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/assets"):
			name := r.URL.Query().Get("name")
			label := r.URL.Query().Get("label")
			digest, err := hashReader(r.Body)
			if err != nil {
				t.Error(err)
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(githubAsset{ID: 9, Name: name, Label: label, State: "uploaded", Digest: "sha256:" + digest})
		default:
			http.Error(w, "unexpected request", 500)
		}
	}))
	t.Cleanup(server.Close)
	f.client = &githubClient{BaseURL: server.URL, UploadURL: server.URL, Token: "secret", HTTP: server.Client()}
	t.Setenv("AUTOLIB_CI_HELPER_MODE", "run-fixture")
	t.Setenv("AUTOLIB_CI_COMMAND_LOG", filepath.Join(root, "commands.jsonl"))
	return f
}

func (f *layerFixture) layer(t *testing.T, name string) error {
	t.Helper()
	f.options.Layer = name
	return runCILayerWithClient(context.Background(), f.options, f.client)
}
func (f *layerFixture) result(t *testing.T) ciPlatformResult {
	t.Helper()
	var result ciPlatformResult
	if err := decodeStrictJSONFile(filepath.Join(f.options.WorkRoot, "results", f.options.Platform+".json"), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCIRunLayers(t *testing.T) {
	t.Run("real local dependency", testRealCILocalDependency)
	f := newLayerFixture(t, []string{"lz4", "flann"})
	for _, layer := range []string{"prepare", "layer0", "layer1", "verify"} {
		if err := f.layer(t, layer); err != nil {
			t.Fatalf("%s: %v", layer, err)
		}
	}
	result := f.result(t)
	if err := finalizeCI(f.plan, f.run, []ciPlatformResult{result}); err != nil {
		t.Fatal(err)
	}
	commands := mustReadText(t, os.Getenv("AUTOLIB_CI_COMMAND_LOG"))
	if strings.Count(commands, `"--package=ci-smoke"`) != 1 || strings.Count(commands, `"-DVALIDATION_MODE=single"`) != 2 || strings.Contains(commands, `"-DVALIDATION_MODE=relocate"`) {
		t.Fatalf("unexpected prepare/validation count: %s", commands)
	}
	if strings.Index(commands, `"-DBUILD_LIST=lz4"`) >= strings.Index(commands, `"-DBUILD_LIST=flann"`) {
		t.Fatalf("dependency not built first: %s", commands)
	}
	for _, name := range []string{"lz4", "flann"} {
		if strings.Count(commands, `"--target","`+name+`"`) != 1 {
			t.Fatalf("module %s was not built exactly once: %s", name, commands)
		}
	}
	if len(result.Packages) != 2 || !result.Packages[0].Uploaded || !result.Packages[1].Uploaded {
		t.Fatalf("missing uploaded results: %+v", result)
	}
}

func testRealCILocalDependency(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("local integration fixture uses Windows import libraries")
	}
	f := newLayerFixture(t, []string{"lz4"})
	t.Setenv("AUTOLIB_CI_HELPER_MODE", "")
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// MSBuild needs a workspace-owned fixture rather than the system temporary directory.
	base := filepath.Join(repository, "build", "ci-test")
	os.MkdirAll(base, 0755)
	root, err := os.MkdirTemp(base, "real-layer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	for _, args := range [][]string{{"init", "-b", "master"}, {"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v %s", err, output)
		}
	}
	head := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	sha, err := head.Output()
	if err != nil {
		t.Fatal(err)
	}
	f.run.SHA = strings.TrimSpace(string(sha))
	f.run.CheckoutSHA = f.run.SHA
	for _, name := range []string{"go.mod", "go.sum", ".github/ci-tools.json", "src/autolib.cmake", "tests/autolib/CMakeLists.txt", "tests/autolib/run-local.cmake"} {
		data, err := os.ReadFile(filepath.Join(repository, name))
		if err != nil {
			t.Fatal(err)
		}
		writeCIFixture(t, root, name, string(data))
	}
	files, err := filepath.Glob(filepath.Join(repository, "src", "autolib-builder", "*.cmake"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		writeCIFixture(t, root, "src/autolib-builder/"+filepath.Base(file), string(data))
	}
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"autolib-builder", "autolib-importer", "autolib-ci"} {
		output := filepath.Join(root, "tools", tool+".exe")
		cmd := exec.Command(cmake, "-P", filepath.Join(repository, "src/autolib-builder/go-sdk.cmake"), "--", "build", "-mod=readonly", "-trimpath", "-o", output, "./src/"+tool)
		cmd.Dir = repository
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v %s", tool, err, data)
		}
	}
	f.options.SourceRoot = root
	f.options.BuilderFile = filepath.Join(root, "tools/autolib-builder.exe")
	f.options.ImporterFile = filepath.Join(root, "tools/autolib-importer.exe")
	f.options.CMakeCommand = cmake
	f.options.PlanFile = filepath.Join(root, "plan.json")
	f.options.RunIdentityFile = filepath.Join(root, "run.json")
	writeCIFixture(t, root, "CMakeLists.txt", `cmake_minimum_required(VERSION 3.20)
project(local_ci LANGUAGES C CXX)
include(ExternalProject)
foreach(name IN ITEMS lz4 flann)
 if(name STREQUAL BUILD_LIST)
  ExternalProject_Add(${name} SOURCE_DIR "${CMAKE_SOURCE_DIR}/upstream/${name}" DOWNLOAD_COMMAND ""
   CMAKE_ARGS "-DCMAKE_INSTALL_PREFIX=$ENV{AUTOLIB_INSTALL_ROOT}/${name}" "-DSUPPORT=$ENV{AUTOLIB_INSTALL_ROOT}/lz4/lib/lz4.lib" "-DCMAKE_MSVC_RUNTIME_LIBRARY=${CMAKE_MSVC_RUNTIME_LIBRARY}")
 endif()
endforeach()
`)
	for _, name := range []string{"lz4", "flann"} {
		depends := ""
		if name == "flann" {
			depends = "depends=['lz4']\n"
		}
		writeCIFixture(t, root, "src/dependencies/"+name+"/module.toml", "name='"+name+"'\ntype='cmake'\nsentinel='include/"+name+".h'\n"+depends+"[source]\nGIT_REPOSITORY='local-fixture'\nGIT_TAG='fixed'\n")
		kind := "STATIC"
		code := "int lz4_value(void) { return 7; }\n"
		link := ""
		if name == "flann" {
			kind = "SHARED"
			code = "int lz4_value(void); int flann_value(void) { return lz4_value()+35; }\n"
			link = "target_link_libraries(flann PRIVATE \"${SUPPORT}\")\n"
		}
		writeCIFixture(t, root, "upstream/"+name+"/CMakeLists.txt", "cmake_minimum_required(VERSION 3.20)\nproject(local_"+name+" LANGUAGES C)\nadd_library("+name+" "+kind+" fixture.c)\nset_target_properties("+name+" PROPERTIES WINDOWS_EXPORT_ALL_SYMBOLS ON)\n"+link+"install(TARGETS "+name+" RUNTIME DESTINATION bin LIBRARY DESTINATION lib ARCHIVE DESTINATION lib)\ninstall(FILES "+name+".h DESTINATION include)\n")
		writeCIFixture(t, root, "upstream/"+name+"/fixture.c", code)
		writeCIFixture(t, root, "upstream/"+name+"/"+name+".h", "int "+name+"_value(void);\n")
	}
	writeCIFixture(t, root, "src/dependencies/lz4/autolib-import.cmake", `add_library(LZ4::lz4_static STATIC IMPORTED GLOBAL)
set_target_properties(LZ4::lz4_static PROPERTIES IMPORTED_CONFIGURATIONS RELEASE IMPORTED_LOCATION_RELEASE "${CMAKE_CURRENT_LIST_DIR}/lib/lz4.lib" INTERFACE_INCLUDE_DIRECTORIES "${CMAKE_CURRENT_LIST_DIR}/include")
add_library(autolib::lz4 ALIAS LZ4::lz4_static)
`)
	writeCIFixture(t, root, "src/dependencies/flann/autolib-import.cmake", `add_library(flann::flann SHARED IMPORTED GLOBAL)
set_target_properties(flann::flann PROPERTIES IMPORTED_CONFIGURATIONS RELEASE IMPORTED_LOCATION_RELEASE "${CMAKE_CURRENT_LIST_DIR}/bin/flann.dll" IMPORTED_IMPLIB_RELEASE "${CMAKE_CURRENT_LIST_DIR}/lib/flann.lib" INTERFACE_LINK_LIBRARIES "${CMAKE_CURRENT_LIST_DIR}/../lz4/lib/lz4.lib" INTERFACE_INCLUDE_DIRECTORIES "${CMAKE_CURRENT_LIST_DIR}/include")
add_library(flann::flann_cpp STATIC IMPORTED GLOBAL)
set_target_properties(flann::flann_cpp PROPERTIES IMPORTED_CONFIGURATIONS RELEASE IMPORTED_LOCATION_RELEASE "${CMAKE_CURRENT_LIST_DIR}/../lz4/lib/lz4.lib")
add_library(autolib::flann ALIAS flann::flann)
`)
	writeCIFixture(t, root, "tests/autolib/smoke.cpp", `#include <windows.h>
#include <iostream>
#include <filesystem>
extern "C" int lz4_value(void);
#ifdef AUTOLIB_TEST_FLANN
extern "C" __declspec(dllimport) int flann_value(void);
#endif
int main() {
 if(lz4_value()!=7) return 1;
#ifdef AUTOLIB_TEST_FLANN
 if(flann_value()!=42) return 2;
 HMODULE module=GetModuleHandleW(L"flann.dll"); wchar_t path[32768]={};
 if(!module || !GetModuleFileNameW(module,path,32768)) return 3;
 std::cout << "runtime_flann=" << std::filesystem::canonical(path).generic_u8string() << '\n';
#endif
 return 0;
}
`)
	assets := []githubAsset{}
	var nextAssetID int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/xxf1ow/xxautolib":
			fmt.Fprint(w, `{"full_name":"xxf1ow/xxautolib","default_branch":"master"}`)
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			fmt.Fprint(w, `{"id":42,"tag_name":"autolib-release","assets_url":"`+serverURL(r)+`/repos/xxf1ow/xxautolib/releases/42/assets"}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/42/assets"):
			json.NewEncoder(w).Encode(assets)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/42/assets"):
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			nextAssetID++
			id := nextAssetID
			asset := githubAsset{ID: id, Name: r.URL.Query().Get("name"), Label: r.URL.Query().Get("label"), State: "uploaded", Digest: "sha256:" + testDigest(data)}
			assets = append(assets, asset)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(asset)
		case r.Method == "DELETE":
			var id int64
			fmt.Sscanf(filepath.Base(r.URL.Path), "%d", &id)
			for i, a := range assets {
				if a.ID == id {
					assets = append(assets[:i], assets[i+1:]...)
					break
				}
			}
			w.WriteHeader(204)
		default:
			http.Error(w, "unexpected request", 500)
		}
	}))
	defer server.Close()
	f.client = &githubClient{BaseURL: server.URL, UploadURL: server.URL, Token: "fixture", HTTP: server.Client()}
	for _, attempt := range []string{"1", "2"} {
		modules := "lz4"
		if attempt == "2" {
			modules = "flann"
		}
		platform := "windows-x86_64"
		plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: f.options.BuilderFile, Modules: &modules, Platforms: &platform})
		if err != nil {
			t.Fatal(err)
		}
		f.plan = plan
		f.run.Attempt = attempt
		f.options.WorkRoot = filepath.Join(root, "build/ci", f.run.RunID+"-"+attempt, platform)
		if err := writeCIJSON(f.options.PlanFile, plan); err != nil {
			t.Fatal(err)
		}
		if err := writeCIJSON(f.options.RunIdentityFile, f.run); err != nil {
			t.Fatal(err)
		}
		layers := []string{"prepare", "layer0"}
		if attempt == "2" {
			layers = append(layers, "layer1")
		}
		layers = append(layers, "verify")
		for _, layer := range layers {
			if err := f.layer(t, layer); err != nil {
				t.Fatalf("real attempt %s %s: %v", attempt, layer, err)
			}
		}
		result := f.result(t)
		if err := finalizeCI(plan, f.run, []ciPlatformResult{result}); err != nil {
			t.Fatal(err)
		}
		if attempt == "2" && (!result.Packages[0].Built || !result.Packages[1].Built) {
			t.Fatalf("selected dependency closure was not rebuilt: %+v", result)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.options.Layer = "layer0"
	if err := runCILayerWithClient(ctx, f.options, f.client); err == nil {
		t.Fatal("canceled run reused old success")
	}
}

func TestCIRebuildClosure(t *testing.T) {
	f := newLayerFixture(t, []string{"lz4", "flann"})
	writeCIFixture(t, f.options.SourceRoot, "3rdparty/lz4/sentinel", "old installation")
	for _, layer := range []string{"prepare", "layer0", "layer1", "verify"} {
		if err := f.layer(t, layer); err != nil {
			t.Fatal(err)
		}
	}
	if f.reads != 3 {
		t.Fatalf("build consulted package cache: release reads=%d want 3 uploads", f.reads)
	}
	commands := mustReadText(t, os.Getenv("AUTOLIB_CI_COMMAND_LOG"))
	if !strings.Contains(commands, filepath.ToSlash(filepath.Join(f.options.WorkRoot, "install"))) && !strings.Contains(commands, strings.ReplaceAll(filepath.Join(f.options.WorkRoot, "install"), `\`, `\\`)) {
		t.Fatalf("isolated install root not passed: %s", commands)
	}
	if got := mustReadText(t, filepath.Join(f.options.SourceRoot, "3rdparty/lz4/sentinel")); got != "old installation" {
		t.Fatal("build modified existing installation")
	}
}

func TestCIPartialFailure(t *testing.T) {
	f := newLayerFixture(t, []string{"lz4", "flann"})
	if err := f.layer(t, "prepare"); err != nil {
		t.Fatal(err)
	}
	if err := f.layer(t, "layer0"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTOLIB_CI_FAIL_MODULE", "flann")
	if err := f.layer(t, "layer1"); err == nil {
		t.Fatal("failed build accepted")
	}
	result := f.result(t)
	if len(result.Packages) != 1 || !result.Packages[0].Uploaded || result.Error == "" || result.Complete {
		t.Fatalf("lost successful dependency or failure: %+v", result)
	}
	if err := finalizeCI(f.plan, f.run, []ciPlatformResult{result}); err == nil {
		t.Fatal("partial success accepted")
	}
}

func TestCIPublisherFailure(t *testing.T) {
	f := newLayerFixture(t, []string{"queue"})
	f.plan.Publisher = "linux-x86_64"
	data, _ := json.Marshal(f.plan)
	if err := os.WriteFile(f.options.PlanFile, data, 0644); err != nil {
		t.Fatal(err)
	}
	for _, layer := range []string{"prepare", "layer0", "verify"} {
		if err := f.layer(t, layer); err != nil {
			t.Fatal(err)
		}
	}
	result := f.result(t)
	if !result.Complete || !result.Packages[0].Verified || result.Packages[0].Uploaded || len(f.writes) != 1 {
		t.Fatalf("nonpublisher did not validate independently: %+v writes=%v", result, f.writes)
	}
	identity := f.run
	identity.Ref = "refs/heads/topic"
	data, _ = json.Marshal(identity)
	os.WriteFile(f.options.RunIdentityFile, data, 0644)
	before := len(f.writes)
	commandsBefore := mustReadText(t, os.Getenv("AUTOLIB_CI_COMMAND_LOG"))
	if err := f.layer(t, "layer0"); err == nil || len(f.writes) != before {
		t.Fatal("nondefault run was not rejected before writes")
	}
	if after := mustReadText(t, os.Getenv("AUTOLIB_CI_COMMAND_LOG")); after != commandsBefore {
		t.Fatal("nondefault run launched a build or validation command")
	}
}

func runLayerCommandFixture() {
	args := os.Args[1:]
	file, err := os.OpenFile(os.Getenv("AUTOLIB_CI_COMMAND_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		os.Exit(80)
	}
	json.NewEncoder(file).Encode(args)
	file.Close()
	value := func(key string) string {
		for i, a := range args {
			if strings.HasPrefix(a, key+"=") {
				return strings.TrimPrefix(a, key+"=")
			}
			if a == key && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	if len(args) > 0 && args[0] == "prepare" {
		fmt.Fprintln(os.Stderr, "package ci-smoke not found")
		os.Exit(1)
	}
	if value("-DBUILD_LIST") == os.Getenv("AUTOLIB_CI_FAIL_MODULE") && value("-DBUILD_LIST") != "" {
		os.Exit(3)
	}
	if len(args) > 0 && args[0] == "package" {
		output := value("--output-root")
		module := value("--module")
		os.MkdirAll(output, 0755)
		osName, arch := value("--os"), value("--arch")
		if module == "queue" {
			osName, arch = "any", "any"
		}
		os.WriteFile(filepath.Join(output, module+"-"+osName+"-"+arch+".tar.zst"), []byte("fixture archive "+module), 0644)
		os.Exit(0)
	}
	if value("-DVALIDATION_MODE") != "" {
		root := filepath.Join(value("-DEVIDENCE_DIRECTORY"), "fixture-run")
		os.MkdirAll(filepath.Join(root, "first"), 0755)
		os.WriteFile(filepath.Join(root, "first", "link-inputs-Release.txt"), nil, 0644)
		os.WriteFile(filepath.Join(root, "first", "prepared-root.txt"), []byte(filepath.Join(root, "packages")), 0644)
		fmt.Println("-- Local binary validation: " + root)
	}
	os.Exit(0)
}
