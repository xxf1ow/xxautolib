package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type nativeLoopFixture struct {
	producer, install, release, generator, arch, toolName, builderName string
}

func newNativeLoopFixture(t *testing.T, root string) nativeLoopFixture {
	t.Helper()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	producer := filepath.Join(canonicalRoot, "producer 中文")
	for _, file := range []string{"CMakeLists.txt", "go.mod", "go.sum"} {
		putTestFile(t, filepath.Join(producer, file), readTestFile(t, filepath.Join("../..", file)))
	}
	putTestFile(t, filepath.Join(producer, "src/autolib.cmake"), readTestFile(t, "../autolib.cmake"))
	for _, directory := range []string{"src/autolib-builder", "src/autolib-importer", "src/internal/packagemetadata"} {
		files, err := filepath.Glob(filepath.Join("../..", directory, "*"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") || (!strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, ".cmake")) {
				continue
			}
			putTestFile(t, filepath.Join(producer, directory, filepath.Base(file)), readTestFile(t, file))
		}
	}
	tools, err := filepath.Abs("../../.tools")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		loopCommand(t, "cmd", "/c", "mklink", "/J", filepath.Join(producer, ".tools"), tools)
	} else if err := os.Symlink(tools, filepath.Join(producer, ".tools")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	for _, version := range []string{"1", "2"} {
		upstream := filepath.Join(producer, "upstream/dep"+version)
		putTestFile(t, filepath.Join(upstream, "dep.h"), "#define DEP_VERSION "+version+"\nint dep_version();\n")
		putTestFile(t, filepath.Join(upstream, "dep.cpp"), "#include \"dep.h\"\nint dep_version() { return DEP_VERSION; }\n")
		putTestFile(t, filepath.Join(upstream, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(dep CXX)
add_library(dep STATIC dep.cpp)
target_include_directories(dep PUBLIC "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>" "$<INSTALL_INTERFACE:include>")
install(TARGETS dep EXPORT DepTargets ARCHIVE DESTINATION lib)
install(FILES dep.h DESTINATION include)
install(EXPORT DepTargets NAMESPACE fixture:: DESTINATION lib/cmake/Dep)
file(WRITE "${CMAKE_BINARY_DIR}/DepConfig.cmake" "include(\"\${CMAKE_CURRENT_LIST_DIR}/DepTargets.cmake\")\n")
install(FILES "${CMAKE_BINARY_DIR}/DepConfig.cmake" DESTINATION lib/cmake/Dep)
`)
		loopModule(t, producer, "dep", version, "", upstream, `include("${CMAKE_CURRENT_LIST_DIR}/lib/cmake/Dep/DepConfig.cmake")
add_library(autolib::dep INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::dep INTERFACE fixture::dep)
`)
	}
	upstream := filepath.Join(producer, "upstream/probe")
	putTestFile(t, filepath.Join(upstream, "probe.cpp"), "#include <dep.h>\nint probe_version() { return dep_version(); }\n")
	putTestFile(t, filepath.Join(upstream, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(probe CXX)
find_package(Dep REQUIRED CONFIG)
add_library(probe SHARED probe.cpp)
set_target_properties(probe PROPERTIES WINDOWS_EXPORT_ALL_SYMBOLS ON VERSION 1.0 SOVERSION 1)
target_link_libraries(probe PRIVATE fixture::dep)
install(TARGETS probe EXPORT ProbeTargets LIBRARY DESTINATION lib RUNTIME DESTINATION bin ARCHIVE DESTINATION lib)
install(EXPORT ProbeTargets NAMESPACE fixture:: DESTINATION lib/cmake/Probe)
file(WRITE "${CMAKE_BINARY_DIR}/ProbeConfig.cmake" "include(\"\${CMAKE_CURRENT_LIST_DIR}/ProbeTargets.cmake\")\n")
install(FILES "${CMAKE_BINARY_DIR}/ProbeConfig.cmake" DESTINATION lib/cmake/Probe)
`)
	loopModule(t, producer, "probe", "1", "dep@1", upstream, `include("${CMAKE_CURRENT_LIST_DIR}/lib/cmake/Probe/ProbeConfig.cmake")
add_library(autolib::probe INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::probe INTERFACE fixture::probe autolib::dep)
`)
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	install := filepath.Join(producer, "build", "ci", "42-1", runtime.GOOS+"-"+arch, "install")
	t.Setenv("AUTOLIB_INSTALL_ROOT", install)
	generator := "Unix Makefiles"
	if runtime.GOOS == "windows" {
		generator = "Visual Studio 17 2022"
	}
	loopCommand(t, "cmake", "-S", producer, "-B", filepath.Join(producer, "build"), "-G", generator, "-DBUILD_LIST=probe@1,dep@2")
	loopCommand(t, "cmake", "--build", filepath.Join(producer, "build"), "--config", "Release")
	toolName := "autolib-importer-" + runtime.GOOS + "-" + arch
	builderName := "autolib-builder"
	if runtime.GOOS == "windows" {
		toolName += ".exe"
		builderName += ".exe"
	}
	release := filepath.Join(producer, "build/release")
	loopCommand(t, filepath.Join(producer, "build/tools", builderName), "package", "--source-root="+producer, "--install-root="+install,
		"--output-root="+release, "--os="+runtime.GOOS, "--arch="+arch, "--host-os="+runtime.GOOS, "--host-arch="+arch,
		"--importer-file="+filepath.Join(install, toolName), "--cmake-command=cmake", "--module=probe@1", "--module=dep@1", "--module=dep@2")
	return nativeLoopFixture{producer, install, release, generator, arch, toolName, builderName}
}

// The installed header and linked implementation expose independent version markers.
// Selecting dep@2 for probe@1 must fail at runtime, even if configure succeeds.
func TestVersionedLocalLoop(t *testing.T) {
	root := t.TempDir()
	f := newNativeLoopFixture(t, root)
	if readTestFile(t, filepath.Join(f.install, "autolib.cmake")) != readTestFile(t, "../autolib.cmake") {
		t.Fatal("ordinary root did not install the source entry unchanged")
	}
	consumer := filepath.Join(root, "independent consumer")
	putTestFile(t, filepath.Join(consumer, "main.cpp"), loopConsumerSource)
	putTestFile(t, filepath.Join(consumer, "CMakeLists.txt"), loopConsumerCMake)
	before := sourceSnapshot(t, f.install)
	consume := func(label string) string {
		build := filepath.Join(root, label)
		loopCommand(t, "cmake", "-S", consumer, "-B", build, "-G", f.generator, "-DAUTOLIB_ENTRY="+filepath.Join(f.install, "autolib.cmake"))
		loopCommand(t, "cmake", "--build", build, "--config", "Release", "--verbose")
		out := loopCommand(t, "ctest", "--test-dir", build, "-C", "Release", "-V")
		if !strings.Contains(out, "header_dep=1 linked_dep=1") {
			t.Fatal("wrong native versions")
		}
		if runtime.GOOS != "windows" && !strings.Contains(out, "runtime_probe="+filepath.ToSlash(f.install)+"/probe/1/") {
			t.Fatal("loader missed installed probe")
		}
		for _, file := range []string{"imported.txt", "link-inputs-Release.txt"} {
			inputs := readTestFile(t, filepath.Join(build, file))
			if !strings.Contains(inputs, filepath.ToSlash(f.install)+"/dep/1/") || !strings.Contains(inputs, filepath.ToSlash(f.install)+"/probe/1/") {
				t.Fatal("wrong installed paths", inputs)
			}
		}
		return build
	}
	consume("first")
	consume("reuse")
	if !reflect.DeepEqual(before, sourceSnapshot(t, f.install)) {
		t.Fatal("direct consumption changed installation")
	}
	header := filepath.Join(f.install, "dep/1/include/dep.h")
	originalInfo, err := os.Stat(header)
	if err != nil {
		t.Fatal(err)
	}
	original := readTestFile(t, header)
	putTestFile(t, header, strings.Replace(original, "DEP_VERSION 1", "DEP_VERSION 2", 1))
	negative := filepath.Join(root, "negative")
	loopCommand(t, "cmake", "-S", consumer, "-B", negative, "-G", f.generator, "-DAUTOLIB_ENTRY="+filepath.Join(f.install, "autolib.cmake"))
	loopCommand(t, "cmake", "--build", negative, "--config", "Release")
	out, err := exec.Command("ctest", "--test-dir", negative, "-C", "Release", "-V").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "header_dep=2 linked_dep=1") {
		t.Fatalf("wrong-version oracle: %v\\n%s", err, out)
	}
	t.Logf("wrong-version negative rejected:\\n%s", out)
	putTestFile(t, header, original)
	if err := os.Chtimes(header, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	// Move only the produced archives and canonical entry into a new consumption root.
	originalInstall := f.install
	relocated := filepath.Join(filepath.Dir(f.producer), "relocated 中文")
	if err := os.MkdirAll(relocated, 0755); err != nil {
		t.Fatal(err)
	}
	for _, asset := range []string{f.toolName + ".tar.xz", "dep-1-" + runtime.GOOS + "-" + f.arch + ".tar.zst", "probe-1-" + runtime.GOOS + "-" + f.arch + ".tar.zst"} {
		cmd := exec.Command("cmake", "-E", "tar", "xf", filepath.Join(f.release, asset))
		cmd.Dir = relocated
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("extract: %v %s", err, out)
		}
	}
	putTestFile(t, filepath.Join(relocated, "autolib.cmake"), readTestFile(t, filepath.Join(originalInstall, "autolib.cmake")))
	for _, name := range []string{"probe", "dep"} {
		putTestFile(t, filepath.Join(relocated, name, "package.toml"), readTestFile(t, filepath.Join(f.release, name, "package.toml")))
	}
	f.install = relocated
	consume("relocated-build")
	if !reflect.DeepEqual(before, sourceSnapshot(t, originalInstall)) {
		t.Fatal("relocation changed production install")
	}
	driver, err := filepath.Abs("../../tests/autolib/run-installed.cmake")
	if err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(f.producer, "tests/autolib/CMakeLists.txt"), strings.Replace(loopConsumerCMake, "main.cpp", "smoke.cpp", 1))
	putTestFile(t, filepath.Join(f.producer, "tests/autolib/smoke.cpp"), loopConsumerSource)
	driverArgs := []string{"-DSOURCE_ROOT=" + f.producer, "-DINSTALL_ROOT=" + f.install, "-DVALIDATION_ROOT=" + filepath.Join(f.producer, "build/driver"), "-DGENERATOR=" + f.generator, "-DMODULES=probe@1;dep@1", "-DROOT_PACKAGES=probe@1"}
	driverArgs = append(driverArgs, nativeLoopCMakeArgs(t)...)
	loopCommand(t, "cmake", append(driverArgs, "-P", driver)...)
}

func loopCommand(t *testing.T, program string, args ...string) string {
	t.Helper()
	if filepath.Base(program) == "cmake" {
		for _, arg := range args {
			if arg == "-S" {
				args = append(args, nativeLoopCMakeArgs(t)...)
				break
			}
		}
	}
	out, err := exec.Command(program, args...).CombinedOutput()
	t.Logf("%s %q\n%s", program, args, out)
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}
	return string(out)
}

func loopModule(t *testing.T, producer, name, version, dependency, upstream, adapter string) {
	t.Helper()
	loopCommand(t, "git", "init", upstream)
	loopCommand(t, "git", "-C", upstream, "add", ".")
	loopCommand(t, "git", "-C", upstream, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	sha := strings.TrimSpace(loopCommand(t, "git", "-C", upstream, "rev-parse", "HEAD"))
	body := fmt.Sprintf("name=%q\nversion=%q\ntype='cmake'\nconfig_dir='lib/cmake/%s'\n", name, version, map[string]string{"dep": "Dep", "probe": "Probe"}[name])
	body += "supported_platforms=['windows-x86_64','linux-x86_64','linux-aarch64','darwin-aarch64']\n"
	if dependency != "" {
		body += fmt.Sprintf("depends=[%q]\n", dependency)
	}
	body += fmt.Sprintf("[source]\nGIT_REPOSITORY=%q\nGIT_TAG=%q\n", filepath.ToSlash(upstream), sha)
	putTestFile(t, filepath.Join(producer, "src/dependencies", name, version, "module.toml"), body)
	putTestFile(t, filepath.Join(producer, "src/dependencies", name, version, "autolib-import.cmake"), adapter)
	putTestFile(t, filepath.Join(producer, "src/dependencies", name, "package.toml"), fmt.Sprintf("latest=%q\n", version))
}

const loopConsumerCMake = `cmake_minimum_required(VERSION 3.20)
project(versioned_loop CXX)
if(APPLE)
 if(NOT CMAKE_OSX_ARCHITECTURES STREQUAL "arm64" OR NOT CMAKE_OSX_DEPLOYMENT_TARGET STREQUAL "15.0" OR NOT CMAKE_CXX_COMPILER_ID STREQUAL "AppleClang" OR NOT IS_ABSOLUTE "${CMAKE_OSX_SYSROOT}" OR NOT IS_ABSOLUTE "${CMAKE_C_COMPILER}" OR NOT IS_ABSOLUTE "${CMAKE_MAKE_PROGRAM}")
  message(FATAL_ERROR "consumer lost Apple native parameters")
 endif()
endif()
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_BUILD_TYPE Release)
if(NOT AUTOLIB_ENTRY)
 set(AUTOLIB_ENTRY "${CMAKE_CURRENT_SOURCE_DIR}/3rdparty/autolib.cmake")
endif()
include("${AUTOLIB_ENTRY}")
add_executable(probe main.cpp)
autolib_import(TARGET probe PRIVATE probe@1)
autolib_import(TARGET probe PRIVATE probe@1)
target_link_libraries(probe PRIVATE ${CMAKE_DL_LIBS})
if(NOT AUTOLIB_PACKAGE_VERSION_dep STREQUAL "1" OR NOT AUTOLIB_PREPARED_PACKAGES STREQUAL "dep;probe")
    message(FATAL_ERROR "wrong precise dependency closure")
endif()
get_target_property(includes fixture::dep INTERFACE_INCLUDE_DIRECTORIES)
get_target_property(library fixture::probe IMPORTED_LOCATION_RELEASE)
file(WRITE "${CMAKE_BINARY_DIR}/imported.txt" "${includes}\n${library}\n")
get_filename_component(cache_root "${AUTOLIB_ENTRY}" DIRECTORY)
file(WRITE "${CMAKE_BINARY_DIR}/prepared-state.cmake" "set(AUTOLIB_CACHE_ROOT [==[${cache_root}]==])\n")
file(WRITE "${CMAKE_BINARY_DIR}/imported-properties.txt" "${includes}\n${library}\n")
file(GENERATE OUTPUT "${CMAKE_BINARY_DIR}/link-inputs-$<CONFIG>.txt" CONTENT "$<TARGET_FILE:fixture::probe>\n$<TARGET_FILE:fixture::dep>\n")
enable_testing()
add_test(NAME versioned_native COMMAND probe)
if(WIN32)
    set_tests_properties(versioned_native PROPERTIES ENVIRONMENT "PATH=${AUTOLIB_PACKAGE_DIR_probe}/bin\;$ENV{PATH}")
endif()
`

const loopConsumerSource = `#include <dep.h>
#include <iostream>
#ifndef _WIN32
#include <dlfcn.h>
#include <filesystem>
#endif
int probe_version();
int main() {
    const int linked = probe_version();
    std::cout << "header_dep=" << DEP_VERSION << " linked_dep=" << linked << '\n';
#ifndef _WIN32
    Dl_info module = {};
    if (!dladdr(reinterpret_cast<void *>(&probe_version), &module) || !module.dli_fname) return 2;
    std::cout << "runtime_probe=" << std::filesystem::canonical(module.dli_fname).generic_string() << '\n';
#endif
    return DEP_VERSION == 1 && linked == 1 ? 0 : 1;
}
`
