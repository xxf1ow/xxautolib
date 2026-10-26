package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func consumptionCommand(f *fixture, module, phase string) error {
	var diagnostic bytes.Buffer
	code := run([]string{"prepare-consumption", "--source-root=" + f.root, "--install-root=" + filepath.Join(f.root, "3rdparty"), "--name=" + module, "--os=windows", "--arch=x86_64", "--phase=" + phase}, io.Discard, &diagnostic)
	if code != 0 {
		return fmt.Errorf("prepare-consumption (%d): %s", code, &diagnostic)
	}
	return nil
}

func consumptionModule(f *fixture, name, depends string) string {
	f.t.Helper()
	f.definition(name, "cmake", depends+testSource)
	putFile(f.t, f.installed(name, "include/input.h"), name)
	return putFile(f.t, filepath.Join(f.root, "src/dependencies", name, "autolib-import.cmake"), "# adapter "+name+"\n")
}

func TestConsumptionPreparation(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"zlib", "lz4"} {
		consumptionModule(f, name, "")
		if err := consumptionCommand(f, name, "reuse"); err != nil {
			t.Fatal(err)
		}
	}
	consumptionModule(f, "demo", "depends=['zlib','lz4','zlib']\n")
	if err := consumptionCommand(f, "demo", "reuse"); err != nil {
		t.Fatal(err)
	}
	output := f.installed("demo", "autolib-import.cmake")
	want := "set(AUTOLIB_PACKAGE_NAME \"demo\")\nset(AUTOLIB_OS \"windows\")\nset(AUTOLIB_ARCH \"x86_64\")\nset(AUTOLIB_DEPENDENCIES \"lz4;zlib\")\n# adapter demo\n"
	requireFile(t, output, want)
	before, _ := os.Stat(output)
	if err := consumptionCommand(f, "demo", "reuse"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(output)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("unchanged preparation rewrote metadata")
	}
	definitionFile(t, f.root, "demo", "cmake", "depends=['lz4','zlib']\n"+testSource)
	if err := consumptionCommand(f, "demo", "reuse"); err != nil {
		t.Fatal(err)
	}
	after, _ = os.Stat(output)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("dependency declaration order changed metadata")
	}
	for _, bad := range []string{strings.Replace(want, `"windows"`, `"linux"`, 1), strings.Replace(want, `"demo"`, `"other"`, 1), "invalid header\n", "# AutoLib installation incomplete\n"} {
		putFile(t, output, bad)
		if err := consumptionCommand(f, "demo", "reuse"); err == nil {
			t.Fatalf("accepted invalid existing metadata: %q", bad)
		}
		requireFile(t, output, bad)
	}
	putFile(t, output, want)
	if err := consumptionCommand(f, "demo", "begin"); err != nil {
		t.Fatal(err)
	}
	requireFile(t, output, "# AutoLib installation incomplete\n")
	if err := consumptionCommand(f, "demo", "reuse"); err == nil {
		t.Fatal("failed reinstall reused its old valid descriptor")
	}
	if err := consumptionCommand(f, "demo", "installed"); err != nil {
		t.Fatal(err)
	}
	requireFile(t, output, want)

	f.definition("sourceonly", "cmake", testSource)
	if err := consumptionCommand(f, "sourceonly", "reuse"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.installed("sourceonly", "autolib-import.cmake")); !os.IsNotExist(err) {
		t.Fatalf("source-only module got metadata: %v", err)
	}
	consumptionModule(f, "needs-sourceonly", "depends=['sourceonly']\n")
	if err := consumptionCommand(f, "needs-sourceonly", "reuse"); err == nil || !strings.Contains(err.Error(), "sourceonly") {
		t.Fatalf("did not reject unconsumable dependency: %v", err)
	}
}

func consumptionEntryFixture(t *testing.T, sentinel bool) *fixture {
	f := newEntryFixture(t)
	f.must(f.context.GitExecutable, "init", f.root)
	f.must(f.context.GitExecutable, "-C", f.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture")
	for _, name := range []string{"dep", "app"} {
		checks := ""
		depends := ""
		if name == "app" {
			depends = "depends=['dep']\n"
			checks = `file(READ "${CMAKE_INSTALL_PREFIX}/../dep/autolib-import.cmake" dependency)
if(NOT dependency MATCHES "AUTOLIB_PACKAGE_NAME")
    message(FATAL_ERROR "dependency consumption is not ready")
endif()
`
		}
		source := `cmake_minimum_required(VERSION 3.20)
project(install_fixture NONE)
` + checks + `install(CODE [[
file(READ "${CMAKE_INSTALL_PREFIX}/autolib-import.cmake" state)
if(NOT state STREQUAL "# AutoLib installation incomplete\n")
    message(FATAL_ERROR "installation began with consumable metadata")
endif()
file(MAKE_DIRECTORY "${CMAKE_INSTALL_PREFIX}/include")
file(WRITE "${CMAKE_INSTALL_PREFIX}/include/input.h" "installed")
if(EXISTS "${CMAKE_INSTALL_PREFIX}/fail-install")
    message(FATAL_ERROR "controlled partial install failure")
endif()
file(APPEND "${CMAKE_INSTALL_PREFIX}/install-count" "installed\n")
]])
`
		repository := f.repository(name, map[string]string{"CMakeLists.txt": source})
		f.must(f.context.GitExecutable, "clone", filepath.Join(f.root, "upstream", name), filepath.Join(f.root, "git", name))
		if sentinel {
			depends += "sentinel='include/input.h'\n"
		}
		f.definition(name, "cmake", depends+"[source]\n"+repository)
		putFile(t, filepath.Join(f.root, "src/dependencies", name, "autolib-import.cmake"), "# "+name+" adapter\n")
	}
	return f
}

func TestConsumptionPreparationUniversal(t *testing.T) {
	f := newFixture(t)
	putFile(t, filepath.Join(f.root, "src/dependencies/queue/module.toml"), readFile(t, "../dependencies/queue/module.toml"))
	putFile(t, filepath.Join(f.root, "src/dependencies/queue/autolib-import.cmake"), "# queue adapter\n")
	for _, entry := range [][2]string{{"atomicops.h", "readerwriterqueue"}, {"readerwriterqueue.h", "readerwriterqueue"}, {"readerwritercircularbuffer.h", "readerwriterqueue"}, {"concurrentqueue.h", "concurrentqueue"}, {"lightweightsemaphore.h", "concurrentqueue"}, {"blockingconcurrentqueue.h", "concurrentqueue"}} {
		putFile(t, filepath.Join(f.root, "git/_resources/queue", entry[1], entry[0]), entry[0])
		putFile(t, f.installed("queue", "include/queue/"+entry[0]), entry[0])
	}
	for range 2 {
		if err := consumptionCommand(f, "queue", "reuse"); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(readFile(t, f.installed("queue", "autolib-import.cmake")), "set(AUTOLIB_OS \"any\")\nset(AUTOLIB_ARCH \"any\")") {
		t.Fatal("universal identity not emitted")
	}
	putFile(t, f.installed("queue", "extra.cmake"), "extra")
	if err := consumptionCommand(f, "queue", "reuse"); err == nil {
		t.Fatal("generated file exemption accepted another cmake file")
	}
}

func TestConsumptionInstallOrder(t *testing.T) {
	f := consumptionEntryFixture(t, false)
	f.mustEntry("-DBUILD_LIST=app")
	f.must(f.context.CMakeCommand, "--build", f.context.BuildRoot, "--config", "Release", "--target", "app", "--parallel", "2")
	for _, name := range []string{"dep", "app"} {
		data := readFile(t, f.installed(name, "autolib-import.cmake"))
		if !strings.Contains(data, `set(AUTOLIB_PACKAGE_NAME "`+name+`")`) {
			t.Fatalf("module target finished without metadata: %s", data)
		}
		requireSingleInstallation(t, f, name)
	}
}

func TestConsumptionReuse(t *testing.T) {
	for _, sentinel := range []bool{false, true} {
		t.Run(fmt.Sprint(sentinel), func(t *testing.T) {
			f := consumptionEntryFixture(t, sentinel)
			f.mustEntry("-DBUILD_LIST=app")
			f.build()
			output := f.installed("app", "autolib-import.cmake")
			if err := os.Remove(output); err != nil {
				t.Fatal(err)
			}
			f.mustEntry("-DBUILD_LIST=app")
			reused := f.build()
			for _, name := range []string{"dep", "app"} {
				count := strings.Count(readFile(t, f.installed(name, "install-count")), "installed")
				if count != 1 {
					t.Fatalf("%s reinstalled during reconfigure/reuse:\n%s", name, reused)
				}
			}
			if !strings.Contains(readFile(t, output), `AUTOLIB_DEPENDENCIES "dep"`) {
				t.Fatal("reuse did not restore dependency header")
			}
			before, _ := os.Stat(output)
			rebuild := f.build()
			after, _ := os.Stat(output)
			if !before.ModTime().Equal(after.ModTime()) {
				t.Fatalf("ordinary rebuild changed metadata:\n%s", rebuild)
			}
			putFile(t, filepath.Join(f.root, "src/dependencies/app/autolib-import.cmake"), "# changed adapter\n")
			f.build()
			if !strings.HasSuffix(readFile(t, output), "# changed adapter\n") {
				t.Fatal("adapter changes ignored")
			}
			for _, name := range []string{"dep", "app"} {
				requireSingleInstallation(t, f, name)
			}
		})
	}
}

func requireSingleInstallation(t *testing.T, f *fixture, name string) {
	t.Helper()
	count := strings.ReplaceAll(readFile(t, f.installed(name, "install-count")), "\r\n", "\n")
	if count != "installed\n" {
		t.Fatalf("%s installed more than once: %q", name, count)
	}
}

func TestConsumptionInstallFailure(t *testing.T) {
	f := consumptionEntryFixture(t, true)
	// The source is unchanged; a payload-side switch fails after writing the sentinel.
	fail := putFile(t, f.installed("app", "fail-install"), "fail")
	f.mustEntry("-DBUILD_LIST=app")
	_, stderr, err := f.command(f.context.CMakeCommand, "--build", f.context.BuildRoot, "--config", "Release")
	if err == nil {
		t.Fatal("partial installation unexpectedly passed", stderr)
	}
	requireFile(t, f.installed("app", "autolib-import.cmake"), "# AutoLib installation incomplete\n")
	f.mustEntry("-DBUILD_LIST=app")
	if _, _, err := f.command(f.context.CMakeCommand, "--build", f.context.BuildRoot, "--config", "Release"); err == nil {
		t.Fatal("sentinel restored a failed installation")
	}
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.installed("app", "include/input.h")); err != nil {
		t.Fatal(err)
	}
	f.mustEntry("-DBUILD_LIST=app")
	f.build()
	if !strings.Contains(readFile(t, f.installed("app", "autolib-import.cmake")), "AUTOLIB_PACKAGE_NAME") {
		t.Fatal("successful real installation did not restore metadata")
	}
}
