package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type entryFixture struct {
	t                                                              *testing.T
	root, source, release, work, cmake, generator, buildTool, tool string
	options                                                        prepareOptions
	toolAsset                                                      string
}

func buildEntryTool(t *testing.T, fake bool) string {
	t.Helper()
	name := "autolib-importer"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output := filepath.Join(t.TempDir(), name)
	goCommand := filepath.Join(os.Getenv("GOROOT"), "bin", "go")
	if runtime.GOOS == "windows" {
		goCommand += ".exe"
	}
	input := "."
	if fake {
		input = putTestFile(t, filepath.Join(t.TempDir(), "main.go"), fakeImporterSource)
	}
	if out, err := exec.Command(goCommand, "build", "-mod=readonly", "-buildvcs=false", "-trimpath", "-o", output, input).CombinedOutput(); err != nil {
		t.Fatalf("build fixture tool: %v\n%s", err, out)
	}
	return output
}

func newEntryFixture(t *testing.T, fake bool, suffix string) *entryFixture {
	t.Helper()
	o := localGraphFixture(t)
	root := filepath.Dir(o.CacheRoot)
	release := filepath.Join(root, "release "+suffix)
	tool := buildEntryTool(t, fake)
	data, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	toolAsset := "autolib-importer-" + runtime.GOOS + "-" + map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if runtime.GOOS == "windows" {
		toolAsset += ".exe"
	}
	putTestFile(t, filepath.Join(release, toolAsset), string(data))
	if err := os.Chmod(filepath.Join(release, toolAsset), 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "consumer")
	entry, err := os.ReadFile("../autolib.cmake")
	if err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(source, "autolib.cmake"), string(entry))
	consumer, err := os.ReadFile("testdata/consumer/CMakeLists.txt")
	if err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(source, "CMakeLists.txt"), string(consumer))
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	generator, toolName := "Ninja", "ninja"
	if runtime.GOOS == "linux" {
		generator, toolName = "Unix Makefiles", "make"
	}
	buildTool, err := exec.LookPath(toolName)
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "consumer-build", "autolib")
	f := &entryFixture{t: t, root: root, source: source, release: release, work: work, cmake: cmake, generator: generator, buildTool: buildTool, tool: tool, options: o, toolAsset: toolAsset}
	if fake {
		dir := filepath.Join(source, "demo", "1.0")
		putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), identityHeader(packageIdentity{"demo", "1.0", "any", "any"})+"if(NOT TARGET autolib::demo)\nadd_library(autolib::demo INTERFACE IMPORTED GLOBAL)\nendif()\n")
		t.Setenv("AUTOLIB_TEST_PACKAGE_DIR", filepath.ToSlash(dir))
		t.Setenv("AUTOLIB_TEST_TOOL_LOG", filepath.Join(root, "calls.jsonl"))
	}
	copyEntryTool(t, f, source)
	if !fake {
		cmakeEntryPackage(t, source, "demo", "1.0", "", "")
	}
	versionedLatest(t, source, "demo", "1.0")
	return f
}

func (f *entryFixture) configure(extra ...string) (string, error) {
	f.t.Helper()
	args := []string{"-S", f.source, "-B", filepath.Join(f.root, "consumer-build"), "-G", f.generator, "-DFIXTURE_RELEASE=" + f.release, "-DCMAKE_SYSTEM_NAME=Linux", "-DCMAKE_SYSTEM_PROCESSOR=" + f.options.Arch}
	if !strings.HasPrefix(f.generator, "Visual Studio") {
		args = append(args, "-DCMAKE_MAKE_PROGRAM="+f.buildTool)
	}
	args = append(args, extra...)
	out, err := exec.Command(f.cmake, args...).CombinedOutput()
	return string(out), err
}

func (f *entryFixture) mustConfigure() {
	f.t.Helper()
	if out, err := f.configure(); err != nil {
		f.t.Fatalf("configure: %v\n%s", err, out)
	}
}

func (f *entryFixture) importerArchive(member, body string) string {
	f.t.Helper()
	root := f.t.TempDir()
	path := putTestFile(f.t, filepath.Join(root, member), body)
	if err := os.Chmod(path, 0755); err != nil {
		f.t.Fatal(err)
	}
	archive := filepath.Join(root, f.toolAsset+".tar.xz")
	command := exec.Command(f.cmake, "-E", "tar", "cJf", archive, "--format=gnutar", member)
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		f.t.Fatalf("create importer archive: %v\n%s", err, out)
	}
	return archive
}

func TestCMakeBootstrap(t *testing.T) {
	f := newEntryFixture(t, false, "")
	f.mustConfigure()
	want := filepath.ToSlash(packageDirectory(f.source, packageIdentity{"demo", "1.0", f.options.OS, f.options.Arch}))
	if got := readTestFile(t, filepath.Join(f.root, "consumer-build/observed.txt")); got != want {
		t.Fatalf("%q != %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(f.release, "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("unexpected manifest", err)
	}
	f.mustConfigure()
}

func TestUniversalPackageThroughCMakeEntry(t *testing.T) {
	f := newEntryFixture(t, false, "")
	id := packageIdentity{"demo", "1.0", "any", "any"}
	putTestFile(t, filepath.Join(f.source, "demo/1.0/autolib-import.cmake"), identityHeader(id)+"add_library(autolib::demo INTERFACE IMPORTED GLOBAL)\n")
	f.mustConfigure()
	m, err := readPackageMetadata(packageDirectory(f.source, packageIdentity{"demo", "1.0", f.options.OS, f.options.Arch}))
	if err != nil || m.Identity != id {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestCMakeBootstrapFailure(t *testing.T) {
	for _, kind := range []string{"missing tool", "cannot start", "prepare fails", "no result", "incomplete result"} {
		t.Run(kind, func(t *testing.T) {
			f := newEntryFixture(t, true, "")
			putTestFile(t, filepath.Join(f.work, "previous-result.cmake"), "message(FATAL_ERROR STALE_RESULT_LOADED)\n")
			marker := filepath.Join(f.root, "adapter-loaded")
			putTestFile(t, filepath.Join(os.Getenv("AUTOLIB_TEST_PACKAGE_DIR"), "autolib-import.cmake"), "file(WRITE [==["+filepath.ToSlash(marker)+"]==] loaded)\n")
			switch kind {
			case "missing tool":
				if err := os.Rename(filepath.Join(f.source, f.toolAsset), filepath.Join(f.source, "tool-away")); err != nil {
					t.Fatal(err)
				}
			case "cannot start":
				putTestFile(t, filepath.Join(f.source, f.toolAsset), "not executable")
			case "prepare fails":
				t.Setenv("AUTOLIB_TEST_MODE", "fail")
			case "no result":
				t.Setenv("AUTOLIB_TEST_MODE", "no-result")
			case "incomplete result":
				t.Setenv("AUTOLIB_TEST_MODE", "incomplete")
			}
			out, err := f.configure()
			if err == nil || strings.Contains(out, "STALE_RESULT_LOADED") {
				t.Fatalf("%v\n%s", err, out)
			}
			if kind == "incomplete result" && !strings.Contains(out, "invalid package version") {
				t.Fatalf("incomplete result did not fail directory validation:\n%s", out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("adapter loaded on failure", err)
			}
		})
	}
}

func TestCMakeEntryPaths(t *testing.T) {
	t.Run("real importer special paths", func(t *testing.T) {
		f := newEntryFixture(t, false, "中文 空格${literal}")
		f.mustConfigure()
		want := filepath.ToSlash(packageDirectory(f.source, packageIdentity{Name: "demo", Version: "1.0", OS: f.options.OS, Arch: f.options.Arch}))
		if got := readTestFile(t, filepath.Join(f.root, "consumer-build/observed.txt")); got != want {
			t.Fatalf("%q != %q", got, want)
		}
	})
}

func TestCMakeWorkRootSemicolon(t *testing.T) {
	f := newEntryFixture(t, true, "")
	build := filepath.Join(f.root, "build;literal")
	work := filepath.Join(build, "autolib")
	putTestFile(t, filepath.Join(work, "sentinel"), "preserve")
	out, err := f.configure("-B", build)
	if err == nil || !strings.Contains(out, "WORK_ROOT must not contain semicolons") {
		t.Fatalf("unsafe work root: %v\\n%s", err, out)
	}
	entries, err := os.ReadDir(work)
	if err != nil || len(entries) != 1 || readTestFile(t, filepath.Join(work, "sentinel")) != "preserve" {
		t.Fatal("rejected work root changed", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "calls.jsonl")); !os.IsNotExist(err) {
		t.Fatal("importer executed for rejected work root")
	}
}

func TestCMakeConfigureIdentity(t *testing.T) {
	for _, kind := range []string{"same", "target", "old URI", "stale directory"} {
		t.Run(kind, func(t *testing.T) {
			f := newEntryFixture(t, true, "")
			consumer := filepath.Join(f.source, "CMakeLists.txt")
			body := readTestFile(t, consumer)
			body = strings.Replace(body, "include(\"${CMAKE_CURRENT_LIST_DIR}/autolib.cmake\")", "include(\"${CMAKE_CURRENT_LIST_DIR}/autolib.cmake\")\ninclude(\"${CMAKE_CURRENT_LIST_DIR}/autolib.cmake\")", 1)
			index := strings.LastIndex(body, "autolib_import(")
			second := body[index:]
			switch kind {
			case "target":
				second = "set(CMAKE_SYSTEM_NAME Windows)\n" + second
			case "old URI":
				second = "autolib_import(URI /old PACKAGES demo)\n"
			case "stale directory":
				second = "set(ENV{AUTOLIB_TEST_MODE} incomplete)\nautolib_import(TARGET consumer INTERFACE other)\n"
			}
			putTestFile(t, consumer, body[:index]+second)
			out, err := f.configure()
			if (err == nil) != (kind == "same") {
				t.Fatalf("%v\n%s", err, out)
			}
			if kind == "stale directory" && !strings.Contains(out, "invalid package version") {
				t.Fatalf("incomplete result inherited an earlier directory:\n%s", out)
			}
			lines := strings.Split(strings.TrimSpace(readTestFile(t, filepath.Join(f.root, "calls.jsonl"))), "\n")
			want := 1
			if kind == "stale directory" {
				want = 2
			}
			if len(lines) != want {
				t.Fatalf("expected %d prepares, got %d: %s", want, len(lines), out)
			}
		})
	}
}

func representativeFixture(t *testing.T, packages string) *entryFixture {
	t.Helper()
	f := newEntryFixture(t, false, "")
	for _, name := range []string{"queue", "lz4", "flann"} {
		version := map[string]string{"queue": "45db9a8b443122184c1f6b9db2c57453a9da1c6bcc18a5d7cfa4da29832fafed", "lz4": "1.10.0", "flann": "f9caaf609d8b8cb2b7104a85cf59eb92c275a25d"}[name]
		adapter, err := os.ReadFile(filepath.Join("../dependencies", name, version, "autolib-import.cmake"))
		if err != nil {
			t.Fatal(err)
		}
		id := packageIdentity{Name: name, Version: "1.0", OS: f.options.OS, Arch: f.options.Arch}
		members := []archiveMember{{Name: name + "/1.0/autolib-import.cmake", Body: strings.Replace(identityHeader(id), `set(AUTOLIB_DEPENDENCIES "")`, `set(AUTOLIB_DEPENDENCIES "`+map[string]string{"flann": "lz4@1.0"}[name]+`")`, 1) + string(adapter)}, {Name: name + "/1.0/include/", Kind: '5'}}
		if name == "queue" {
			members = append(members, archiveMember{Name: "queue/1.0/include/queue/readerwriterqueue.h", Body: "// fixture header\n"})
		}
		if name == "lz4" {
			// 上游 Targets 在完整目标集已存在时直接返回，不能靠重新定义报错来发现来源冲突。
			members = append(members, archiveMember{Name: "lz4/1.0/lib/cmake/lz4/lz4Config.cmake", Body: "if(TARGET LZ4::lz4 AND TARGET LZ4::lz4_static)\nreturn()\nendif()\nadd_library(LZ4::lz4 INTERFACE IMPORTED)\nadd_library(LZ4::lz4_static INTERFACE IMPORTED)\n"})
		}
		if name == "flann" {
			members = append(members, archiveMember{Name: "flann/1.0/lib/cmake/flann/flann-config.cmake", Body: "if(NOT TARGET autolib::lz4)\nmessage(FATAL_ERROR \"lz4 not loaded first\")\nendif()\nif(TARGET flann::flann_cpp AND TARGET flann::flann)\nreturn()\nendif()\nadd_library(flann::flann_cpp INTERFACE IMPORTED)\nadd_library(flann::flann INTERFACE IMPORTED)\n"})
		}
		installTestMembers(t, f.source, members)
		versionedLatest(t, f.source, name, "1.0")
	}
	putTestFile(t, filepath.Join(f.source, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(representatives NONE)\ninclude(autolib.cmake)\nadd_library(root_consumer INTERFACE)\nautolib_import(TARGET root_consumer INTERFACE "+packages+")\nadd_subdirectory(child)\n")
	putTestFile(t, filepath.Join(f.source, "child/CMakeLists.txt"), "foreach(name IN ITEMS queue lz4 flann)\nif(NOT TARGET autolib::${name})\nmessage(FATAL_ERROR \"target not visible: ${name}\")\nendif()\nendforeach()\nadd_library(consumer INTERFACE)\nautolib_import(TARGET consumer INTERFACE flann queue lz4)\n")
	return f
}

func TestRepresentativeAdapters(t *testing.T) {
	f := representativeFixture(t, "flann queue")
	consumer := filepath.Join(f.source, "CMakeLists.txt")
	putTestFile(t, consumer, strings.Replace(readTestFile(t, consumer), "add_subdirectory(child)", "if(NOT AUTOLIB_PREPARED_PACKAGES STREQUAL \"lz4;flann;queue\")\nmessage(FATAL_ERROR \"wrong dependency order: ${AUTOLIB_PREPARED_PACKAGES}\")\nendif()\nadd_subdirectory(child)", 1))
	putTestFile(t, consumer, readTestFile(t, consumer)+`
foreach(name IN ITEMS queue lz4 flann)
    if(NOT TARGET autolib::${name} OR TARGET AutoLib::${name})
        message(FATAL_ERROR "missing unique lowercase public target: ${name}")
    endif()
endforeach()
if(NOT AUTOLIB_PACKAGE_VERSION_lz4 STREQUAL "1.0" OR NOT AUTOLIB_PACKAGE_DIR_lz4 STREQUAL "${CMAKE_CURRENT_SOURCE_DIR}/lz4/1.0")
    message(FATAL_ERROR "FLANN did not use the prepared exact lz4 result")
endif()
get_target_property(links autolib::flann INTERFACE_LINK_LIBRARIES)
foreach(required IN ITEMS flann::flann_cpp flann::flann autolib::lz4)
    if(NOT required IN_LIST links)
        message(FATAL_ERROR "missing FLANN dependency: ${required}")
    endif()
endforeach()
`)
	destination := f.release + "中文 空格"
	if err := os.Rename(f.release, destination); err != nil {
		t.Fatal(err)
	}
	f.release = destination
	f.mustConfigure()
}

func TestAdapterExistingTargets(t *testing.T) {
	for _, name := range []string{"lz4", "flann"} {
		t.Run(name, func(t *testing.T) {
			f := representativeFixture(t, "flann queue lz4")
			targets := []string{"LZ4::lz4", "LZ4::lz4_static"}
			if name == "flann" {
				targets = []string{"flann::flann_cpp", "flann::flann"}
			}
			definitions := ""
			for _, target := range targets {
				definitions += "add_library(" + target + " INTERFACE IMPORTED)\n"
			}
			consumer := filepath.Join(f.source, "CMakeLists.txt")
			putTestFile(t, consumer, strings.Replace(readTestFile(t, consumer), "autolib_import(", definitions+"autolib_import(", 1))
			out, err := f.configure()
			if err == nil || !strings.Contains(out, "AutoLib "+name+" cannot import existing target") {
				t.Fatalf("existing package-external targets were not rejected clearly: %v\n%s", err, out)
			}
		})
	}
}

func TestAdapterDependencyOrder(t *testing.T) {
	t.Run("root only", func(t *testing.T) {
		f := representativeFixture(t, "flann")
		putTestFile(t, filepath.Join(f.source, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(root_only NONE)\ninclude(autolib.cmake)\nadd_library(consumer INTERFACE)\nautolib_import(TARGET consumer INTERFACE flann)\nif(NOT TARGET autolib::lz4 OR NOT TARGET autolib::flann OR NOT AUTOLIB_PREPARED_PACKAGES STREQUAL \"lz4;flann\")\nmessage(FATAL_ERROR \"missing dependency closure\")\nendif()\n")
		f.mustConfigure()
	})
	f := representativeFixture(t, "flann")
	system := filepath.Join(f.root, "system")
	marker := filepath.Join(f.root, "system-was-used")
	putTestFile(t, filepath.Join(system, "lz4Config.cmake"), "file(WRITE \""+filepath.ToSlash(marker)+"\" \"unexpected system fallback\")\nadd_library(LZ4::lz4 INTERFACE IMPORTED)\n")
	if err := os.Rename(filepath.Join(f.source, "lz4/1.0"), filepath.Join(f.source, "lz4/hidden")); err != nil {
		t.Fatal(err)
	}
	if out, err := f.configure("-DCMAKE_PREFIX_PATH=" + system); err == nil {
		t.Fatalf("unlisted sibling was imported: %s", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("adapter fell back to system lz4")
	}
}

func TestCMakeImportTargetContract(t *testing.T) {
	for _, call := range []string{
		"TARGET consumer INTERFACE demo demo", "TARGET absent INTERFACE demo",
		"TARGET consumer demo", "TARGET consumer INTERFACE", "TARGET consumer INTERFACE demo PRIVATE other",
		"TARGET consumer PRIVATE demo", "TARGET consumer INTERFACE demo LOCAL_ROOT /old",
		"LOCAL_ROOT /old PACKAGES demo",
	} {
		t.Run(call, func(t *testing.T) {
			f := newEntryFixture(t, true, "")
			putTestFile(t, filepath.Join(f.source, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(contract NONE)
include(autolib.cmake)
add_library(consumer INTERFACE)
autolib_import(`+call+`)
get_target_property(links consumer INTERFACE_LINK_LIBRARIES)
if(NOT links STREQUAL "autolib::demo")
    message(FATAL_ERROR "incorrect root links: ${links}")
endif()
`)
			out, err := f.configure()
			if (err == nil) != (call == "TARGET consumer INTERFACE demo demo") {
				t.Fatalf("target contract: %v\n%s", err, out)
			}
		})
	}
}

func TestCMakeConsumerVisibility(t *testing.T) {
	for _, visibility := range []string{"PRIVATE", "PUBLIC"} {
		t.Run(visibility, func(t *testing.T) {
			f := newEntryFixture(t, true, "")
			var extra []string
			if runtime.GOOS == "windows" {
				f.generator = "Visual Studio 17 2022"
				extra = []string{"-DCMAKE_SYSTEM_NAME=Windows", "-DCMAKE_SYSTEM_PROCESSOR=AMD64"}
			}
			putTestFile(t, filepath.Join(f.source, "empty.cpp"), "int value() { return 1; }\n")
			putTestFile(t, filepath.Join(f.source, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(visibility LANGUAGES CXX)
include(autolib.cmake)
add_library(consumer STATIC empty.cpp)
autolib_import(TARGET consumer `+visibility+` demo)
get_target_property(own_links consumer LINK_LIBRARIES)
get_target_property(propagated consumer INTERFACE_LINK_LIBRARIES)
if(NOT own_links STREQUAL "autolib::demo")
    message(FATAL_ERROR "missing own usage: ${own_links}")
endif()
if("`+visibility+`" STREQUAL "PUBLIC" AND NOT propagated STREQUAL "autolib::demo")
    message(FATAL_ERROR "missing public usage: ${propagated}")
endif()
if("`+visibility+`" STREQUAL "PRIVATE" AND propagated STREQUAL "autolib::demo")
    message(FATAL_ERROR "private compile usage leaked")
endif()
`)
			if out, err := f.configure(extra...); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if out, err := exec.Command(f.cmake, "--build", filepath.Join(f.root, "consumer-build"), "--config", "Release").CombinedOutput(); err != nil {
				t.Fatalf("build through public entry: %v\n%s", err, out)
			}
		})
	}
}

func TestCMakeMultipleConsumers(t *testing.T) {
	for _, kind := range []string{"success", "missing", "adapter failure"} {
		t.Run(kind, func(t *testing.T) {
			f := representativeFixture(t, "lz4")
			if kind == "adapter failure" {
				cmakeEntryPackage(t, f.source, "flann", "1.0", "lz4@1.0", "add_library(autolib::partial INTERFACE IMPORTED GLOBAL)\nmessage(FATAL_ERROR EXPECTED_ADAPTER_FAILURE)\n")
			}
			second := "flann queue"
			if kind == "missing" {
				second = "absent"
			}
			putTestFile(t, filepath.Join(f.source, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(multiple NONE)
include(autolib.cmake)
add_library(first INTERFACE)
autolib_import(TARGET first INTERFACE lz4)
add_subdirectory(second)
`)
			putTestFile(t, filepath.Join(f.source, "second/CMakeLists.txt"), `add_library(second INTERFACE)
autolib_import(TARGET second INTERFACE `+second+`)
get_target_property(first_links first INTERFACE_LINK_LIBRARIES)
get_target_property(second_links second INTERFACE_LINK_LIBRARIES)
if(NOT first_links STREQUAL "autolib::lz4" OR NOT second_links STREQUAL "autolib::flann;autolib::queue" OR NOT AUTOLIB_PREPARED_PACKAGES STREQUAL "lz4;flann;queue")
    message(FATAL_ERROR "incorrect independent consumers")
endif()
file(WRITE "${CMAKE_BINARY_DIR}/after-second" reached)
`)
			out, err := f.configure()
			if (err == nil) != (kind == "success") {
				t.Fatalf("%v\n%s", err, out)
			}
			_, markerErr := os.Stat(filepath.Join(f.root, "consumer-build/after-second"))
			if kind != "success" && !os.IsNotExist(markerErr) {
				t.Fatal("continued after failed import")
			}
			if kind == "adapter failure" && !strings.Contains(out, "EXPECTED_ADAPTER_FAILURE") {
				t.Fatal(out)
			}
		})
	}
}

func TestCMakeTargetPlatform(t *testing.T) {
	for _, tc := range []struct{ processor, generatorPlatform, want string }{
		{"AMD64", "", "x86_64"}, {"arm64", "", "aarch64"}, {"aarch64", "x64,version=10.0", "x86_64"}, {"unknown", "", ""},
	} {
		t.Run(tc.processor+tc.generatorPlatform, func(t *testing.T) {
			f := newEntryFixture(t, true, "")
			body := readTestFile(t, filepath.Join(f.source, "CMakeLists.txt"))
			body = strings.Replace(body, "autolib_import(", "set(CMAKE_GENERATOR_PLATFORM \""+tc.generatorPlatform+"\")\nautolib_import(", 1)
			putTestFile(t, filepath.Join(f.source, "CMakeLists.txt"), body)
			out, err := f.configure("-DCMAKE_SYSTEM_PROCESSOR=" + tc.processor)
			if (err == nil) != (tc.want != "") {
				t.Fatalf("%v\n%s", err, out)
			}
			if tc.want != "" && !strings.Contains(readTestFile(t, filepath.Join(f.root, "calls.jsonl")), "--arch="+tc.want) {
				t.Fatal("wrong target architecture")
			}
		})
	}
}

const fakeImporterSource = `package main
import("encoding/json";"flag";"fmt";"os";"strings")
func main(){
 if log:=os.Getenv("AUTOLIB_TEST_EXEC_LOG");log!="" {os.WriteFile(log,[]byte(os.Args[0]),0644)}
 log:=os.Getenv("AUTOLIB_TEST_TOOL_LOG");if log!="" {f,e:=os.OpenFile(log,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0644);if e!=nil {panic(e)};json.NewEncoder(f).Encode(os.Args[1:]);f.Close()}
 if len(os.Args)<2 || os.Args[1]!="prepare" {fmt.Fprintln(os.Stderr,"only prepare is supported");os.Exit(2)}
 result:="";for _,arg:=range os.Args[2:] {if strings.HasPrefix(arg,"--result="){result=strings.TrimPrefix(arg,"--result=")}}
fs:=flag.NewFlagSet("prepare",flag.ExitOnError);for _,n:=range []string{"remote-root","metadata-root","cache-root","os","arch","result","package"}{fs.String(n,"",n)};fs.Parse(os.Args[2:])
 mode:=os.Getenv("AUTOLIB_TEST_MODE");if mode=="no-result" {return}
 if mode=="fail" {os.WriteFile(result,[]byte("message(FATAL_ERROR STALE_RESULT_LOADED)\n"),0644);os.Exit(7)}
 body:="set(AUTOLIB_PREPARED_PACKAGES demo)\nset(AUTOLIB_PACKAGE_DIR_demo [==["+os.Getenv("AUTOLIB_TEST_PACKAGE_DIR")+"]==])\nset(AUTOLIB_PACKAGE_VERSION_demo 1.0)\n"
 if custom:=os.Getenv("AUTOLIB_TEST_RESULT");custom!="" {body=custom}
 if mode=="incomplete" {body="set(AUTOLIB_PREPARED_PACKAGES demo)\n"}
 if e:=os.WriteFile(result,[]byte(body),0644);e!=nil {panic(e)}
}`
