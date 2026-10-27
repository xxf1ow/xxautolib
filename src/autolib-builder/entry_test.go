package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func managedGoRoot(f *fixture) string {
	return filepath.Join(f.root, ".tools", "go", "sdk", runtime.Version(), runtime.GOOS+"-"+runtime.GOARCH, "go")
}

func linkFixtureDirectory(f *fixture, destination, source string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		f.t.Fatal(err)
	}
	// Windows 的目录 junction 不要求开发者模式；只链接测试自行创建的位置。
	if runtime.GOOS == "windows" {
		f.must("cmd", "/c", "mklink", "/J", filepath.Clean(destination), filepath.Clean(source))
	} else if err := os.Symlink(source, destination); err != nil {
		f.t.Fatal(err)
	}
}

func seedManagedGo(f *fixture) {
	f.t.Helper()
	linkFixtureDirectory(f, managedGoRoot(f), runtime.GOROOT())
	for _, cache := range []struct{ variable, directory string }{{"GOMODCACHE", "gopath/pkg/mod"}, {"GOCACHE", "gocache"}} {
		source := strings.TrimSpace(f.must(goExecutable(), "env", cache.variable))
		linkFixtureDirectory(f, filepath.Join(f.root, ".tools", "go", filepath.FromSlash(cache.directory)), source)
	}
}

func TestEntryManagedGo(t *testing.T) {
	f := newEntryFixture(t)
	userConfig := filepath.Join(f.root, "user-go-env")
	putFile(t, userConfig, "GOPATH=unrelated-user-directory\nGOTOOLCHAIN=auto\n")
	t.Setenv("GOENV", userConfig)
	t.Setenv("GOROOT", filepath.Join(f.root, "unrelated-sdk"))
	t.Setenv("GOPATH", filepath.Join(f.root, "unrelated-gopath"))
	t.Setenv("GOMODCACHE", filepath.Join(f.root, "unrelated-modcache"))
	t.Setenv("GOCACHE", filepath.Join(f.root, "unrelated-gocache"))
	project := filepath.Join(f.root, "CMakeLists.txt")
	putFile(t, project, readFile(t, project)+`
xxautolib_prepare_go(managed_go managed_environment)
execute_process(COMMAND ${managed_environment} "${managed_go}" env -json GOROOT GOPATH GOMODCACHE GOCACHE GOENV GOTOOLCHAIN
    OUTPUT_FILE "${CMAKE_BINARY_DIR}/go-environment.json" COMMAND_ERROR_IS_FATAL ANY)
`)
	for _, name := range []string{"first build", "second build"} {
		build := filepath.Join(f.root, name)
		args := []string{f.context.CMakeCommand, "-S", f.root, "-B", build, "-G", f.context.Generator}
		if f.context.Platform != "" {
			args = append(args, "-A", f.context.Platform, "-T", f.context.Toolset)
		}
		f.must(args...)
		var environment map[string]string
		if err := json.Unmarshal([]byte(readFile(t, filepath.Join(build, "go-environment.json"))), &environment); err != nil {
			t.Fatal(err)
		}
		for variable, want := range map[string]string{
			"GOROOT": managedGoRoot(f), "GOPATH": filepath.Join(f.root, ".tools/go/gopath"),
			"GOMODCACHE": filepath.Join(f.root, ".tools/go/gopath/pkg/mod"), "GOCACHE": filepath.Join(f.root, ".tools/go/gocache"),
			"GOENV": "", "GOTOOLCHAIN": "local",
		} {
			if filepath.Clean(environment[variable]) != filepath.Clean(want) {
				t.Errorf("%s: want %q, got %q", variable, want, environment[variable])
			}
		}
	}
	requireFile(t, userConfig, "GOPATH=unrelated-user-directory\nGOTOOLCHAIN=auto\n")
}

func goExecutable() string {
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(runtime.GOROOT(), "bin", name)
}

func newEntryFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	for _, file := range []string{"CMakeLists.txt", "go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join("..", "..", file))
		if err != nil {
			t.Fatal(err)
		}
		putFile(t, filepath.Join(f.root, file), string(data))
	}
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := file.Name()
		if (strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")) || strings.HasSuffix(name, ".cmake") {
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			putFile(t, filepath.Join(f.root, "src/autolib-builder", name), string(data))
		}
	}
	importerSources, err := filepath.Glob("../autolib-importer/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range importerSources {
		if !strings.HasSuffix(path, "_test.go") {
			putFile(t, filepath.Join(f.root, "src/autolib-importer", filepath.Base(path)), readFile(t, path))
		}
	}
	seedManagedGo(f)
	return f
}

func installedImporter(root string) string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	name := "autolib-importer-" + runtime.GOOS + "-" + arch
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(root, name)
}

func TestEntryInstalledImporter(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "relative with spaces"}[custom], func(t *testing.T) {
			f := newEntryFixture(t)
			f.copyModule("demo")
			root := filepath.Join(f.root, "3rdparty")
			if custom {
				t.Setenv("AUTOLIB_INSTALL_ROOT", "relative install/../install with spaces")
				root = filepath.Join(f.root, "install with spaces")
			} else {
				t.Setenv("AUTOLIB_INSTALL_ROOT", "")
			}
			f.mustEntry("-DBUILD_LIST=demo")
			tool := installedImporter(root)
			if output, err := exec.Command(goExecutable(), "tool", "nm", tool).CombinedOutput(); err == nil || !strings.Contains(string(output), "no symbol") {
				t.Fatalf("installed importer retains symbols: nm exit=%v", err)
			}
			stat, err := os.Stat(tool)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("installed importer: %d bytes", stat.Size())
			if _, stderr, err := f.command(tool); err == nil || !strings.Contains(stderr, "usage: autolib-importer") {
				t.Fatalf("installed host tool cannot execute: %v %s", err, stderr)
			}
			command := exec.Command(f.context.CMakeCommand, "-S", f.root, "-B", f.context.BuildRoot)
			command.Dir = f.root
			if out, err := command.CombinedOutput(); err != nil {
				t.Fatalf("configure from another directory: %v\n%s", err, out)
			}
			other := filepath.Join(f.root, "must not install here")
			t.Setenv("AUTOLIB_INSTALL_ROOT", other)
			f.build()
			requireFile(t, filepath.Join(root, "demo/include/x.h"), "demo")
			if _, err := os.Stat(other); !os.IsNotExist(err) {
				t.Fatal("build re-read installation environment", err)
			}
		})
	}
}

func TestEntryImporterInputs(t *testing.T) {
	f := newEntryFixture(t)
	f.copyModule("demo")
	f.mustEntry("-DBUILD_LIST=demo")
	tool := installedImporter(filepath.Join(f.root, "3rdparty"))
	for _, path := range []string{"go.mod", "go.sum", "src/autolib-importer/main.go"} {
		if !strings.Contains(readFile(t, filepath.Join(f.context.BuildRoot, "generated/inputs.cmake")), filepath.ToSlash(filepath.Join(f.root, path))) {
			t.Fatalf("untracked importer input: %s", path)
		}
	}
	input := filepath.Join(f.root, "src/autolib-importer/fixture_input.go")
	for _, marker := range []string{"added importer", "changed importer", ""} {
		if marker == "" {
			if err := os.Remove(input); err != nil {
				t.Fatal(err)
			}
		} else {
			putFile(t, input, "package main\nimport (\"fmt\"; \"os\")\nfunc init() {fmt.Fprintln(os.Stderr, \""+marker+"\")}\n")
		}
		f.build()
		_, stderr, _ := f.command(tool)
		if marker != "" && !strings.Contains(stderr, marker) || marker == "" && strings.Contains(stderr, "changed importer") {
			t.Fatalf("importer source change ignored: %q %s", marker, stderr)
		}
	}
	putFile(t, input, "package main\ninvalid Go source\n")
	stdout, stderr, err := f.command(f.context.CMakeCommand, "--build", f.context.BuildRoot, "--config", "Release")
	if err == nil || !strings.Contains(stdout+stderr, "autolib-importer compilation failed") {
		t.Fatalf("old executable hid compilation failure: %v %s%s", err, stdout, stderr)
	}
}

func (f *fixture) entry(args ...string) (string, error) {
	f.t.Helper()
	command := []string{f.context.CMakeCommand, "-S", f.root, "-B", f.context.BuildRoot, "-G", f.context.Generator}
	if f.context.Platform != "" {
		command = append(command, "-A", f.context.Platform, "-T", f.context.Toolset)
	}
	out, stderr, err := f.command(append(command, args...)...)
	return out + stderr, err
}
func (f *fixture) mustEntry(args ...string) string {
	f.t.Helper()
	out, err := f.entry(args...)
	if err != nil {
		f.t.Fatalf("root configure: %v\n%s", err, out)
	}
	return out
}
func (f *fixture) copyModule(name string) *moduleDefinition {
	f.t.Helper()
	repo := f.repository(name, map[string]string{"x.h": name})
	return f.definition(name, "copy", "[source]\n"+repo+"[[copy]]\nfiles=['x.h']\ndestination='include'\n")
}

func TestEntrySDK(t *testing.T) {
	f := newEntryFixture(t)
	mod := readFile(t, filepath.Join(f.root, "go.mod"))
	sum := readFile(t, filepath.Join(f.root, "go.sum"))
	// 故意继承异目标环境，公共入口仍须生成可在当前宿主执行的工具。
	t.Setenv("GOOS", "plan9")
	t.Setenv("GOARCH", "arm")
	out := f.mustEntry()
	for _, heading := range []string{"Building Summary", "CMake Version", "Operation System", "CPU Architecture", "Generator Toolset", "Platform Toolset", "Install Directory", "BUILD_MODULES"} {
		if !strings.Contains(out, heading) {
			t.Fatal(heading, out)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(f.root, "3rdparty")); err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(installedImporter("")) {
		t.Fatal("empty selection did not install only the host tool", entries, err)
	}
	requireFile(t, filepath.Join(f.root, "go.mod"), mod)
	requireFile(t, filepath.Join(f.root, "go.sum"), sum)
	for _, tc := range []struct{ arg, want string }{{"-DCMAKE_DISABLE_FIND_PACKAGE_Git=ON", "Git"}} {
		out, err := f.entry(tc.arg)
		if err == nil || !strings.Contains(out, tc.want) {
			t.Fatalf("%v %s", err, out)
		}
	}
	f.context.BuildRoot = filepath.Join(f.root, "bad-version-build")
	putFile(t, filepath.Join(f.root, "go.mod"), strings.ReplaceAll(mod, "go 1.27.3", "go 1.0.0"))
	if out, err := f.entry(); err == nil || !strings.Contains(out, "metadata mismatch") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestEntryFailureStopsBeforeInclude(t *testing.T) {
	for _, mode := range []string{"toml", "sdk", "compile"} {
		t.Run(mode, func(t *testing.T) {
			f := newEntryFixture(t)
			m := f.copyModule("demo")
			f.mustEntry()
			putFile(t, filepath.Join(f.context.BuildRoot, "generated/demo.cmake"), "message(FATAL_ERROR \"STALE_OUTPUT_LOADED\")\n")
			args := []string{}
			switch mode {
			case "toml":
				putFile(t, m.Path, "name=[broken TOML")
			case "sdk":
				path := filepath.Join(f.root, "go.mod")
				putFile(t, path, strings.ReplaceAll(readFile(t, path), "go 1.27.3", "go 1.0.0"))
			case "compile":
				putFile(t, filepath.Join(f.root, "src/autolib-builder/broken.go"), "package main\nnot valid Go\n")
			}
			out, err := f.entry(args...)
			if err == nil || strings.Contains(out, "STALE_OUTPUT_LOADED") {
				t.Fatalf("%v %s", err, out)
			}
		})
	}
}

func TestEntryOffline(t *testing.T) {
	f := newEntryFixture(t)
	f.copyModule("demo")
	f.mustEntry()
	f.build()
	requireFile(t, f.installed("demo", "include/x.h"), "demo")
	// 仅移除 fixture 的缓存 junction，不触碰其指向的共享缓存。
	cache := filepath.Join(f.root, ".tools/go/gopath/pkg/mod")
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	out, err := f.entry()
	if err == nil || !strings.Contains(out, "module lookup disabled") || !strings.Contains(out, "builder compilation failed") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestEntryIncremental(t *testing.T) {
	f := newEntryFixture(t)
	repo := f.repository("dep", map[string]string{"CMakeLists.txt": `cmake_minimum_required(VERSION 3.20)
project(child NONE)
file(WRITE "${CMAKE_BINARY_DIR}/value" "${VALUE}")
file(WRITE "${CMAKE_BINARY_DIR}/policy" "${CMAKE_POLICY_VERSION_MINIMUM}")
install(FILES "${CMAKE_BINARY_DIR}/value" "${CMAKE_BINARY_DIR}/policy" DESTINATION .)
`})
	dep := f.definition("dep", "cmake", "[source]\n"+repo+"[cmake.cache]\nVALUE='one'\n")
	leaf := f.copyModule("leaf")
	putFile(t, leaf.Path, strings.ReplaceAll(readFile(t, leaf.Path), "type='copy'", "type='copy'\ndepends=['dep']"))
	unused := f.copyModule("unused")
	f.mustEntry("-DBUILD_LIST=leaf")
	f.build()
	requireFile(t, f.installed("dep", "value"), "one")
	time.Sleep(1100 * time.Millisecond)
	putFile(t, dep.Path, strings.ReplaceAll(readFile(t, dep.Path), "VALUE='one'", "VALUE='two'"))
	if out := f.build(); !strings.Contains(out, "Building Summary") {
		t.Fatal("dependency TOML did not reconfigure", out)
	}
	requireFile(t, f.installed("dep", "value"), "two")
	for _, file := range []string{leaf.Path, filepath.Join(f.root, "src/autolib-builder/apply_patch.cmake"), filepath.Join(f.root, "src/autolib-builder/main.go")} {
		generated := filepath.Join(f.context.BuildRoot, "generated/leaf.cmake")
		before, _ := os.Stat(generated)
		time.Sleep(1100 * time.Millisecond)
		comment := "\n# input change\n"
		if strings.HasSuffix(file, ".go") {
			comment = "\n// input change\n"
		}
		putFile(t, file, readFile(t, file)+comment)
		if out := f.build(); !strings.Contains(out, "Building Summary") {
			t.Fatal("input did not reconfigure", file, out)
		}
		after, _ := os.Stat(generated)
		if !before.ModTime().Equal(after.ModTime()) {
			t.Fatal("unchanged generated output rewritten")
		}
		if out := f.build(); strings.Contains(out, "Building Summary") {
			t.Fatal("reconfigure repeated", out)
		}
	}
	time.Sleep(1100 * time.Millisecond)
	putFile(t, unused.Path, readFile(t, unused.Path)+"\n# unused change\n")
	if out := f.build(); strings.Contains(out, "Building Summary") {
		t.Fatal("unselected TOML triggered configure", out)
	}
	for _, operation := range []string{"add", "remove"} {
		file := filepath.Join(f.root, "src/autolib-builder/new_input.go")
		time.Sleep(1100 * time.Millisecond)
		if operation == "add" {
			putFile(t, file, "package main\n")
		} else if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if out := f.build(); !strings.Contains(out, "Building Summary") {
			t.Fatal("source set did not reconfigure", operation, out)
		}
	}
	file := filepath.Join(f.root, "src/autolib-builder/cmake.go")
	time.Sleep(1100 * time.Millisecond)
	putFile(t, file, strings.ReplaceAll(readFile(t, file), `"CMAKE_POLICY_VERSION_MINIMUM": "3.5"`, `"CMAKE_POLICY_VERSION_MINIMUM": "3.6"`))
	f.build()
	requireFile(t, f.installed("dep", "policy"), "3.6")
}

func TestEntrySelection(t *testing.T) {
	f := newEntryFixture(t)
	f.copyModule("first")
	f.copyModule("second")
	f.mustEntry()
	f.mustEntry("-DBUILD_LIST= first,first , first")
	f.build()
	requireFile(t, f.installed("first", "include/x.h"), "first")
	if _, err := os.Stat(filepath.Join(f.root, "git/second/.git")); !os.IsNotExist(err) {
		t.Fatal("unselected source acquired")
	}
	if out, err := f.entry("-DBUILD_LIST=missing"); err == nil || !strings.Contains(out, "Module file NOT found") {
		t.Fatalf("%v %s", err, out)
	}
	t.Run("compiled dependency and include", func(t *testing.T) {
		f := newEntryFixture(t)
		repo := f.repository("dep", map[string]string{"x.h": "// dependency header\n"})
		f.definition("dep", "copy", "[source]\n"+repo+"[[copy]]\nfiles=['x.h']\ndestination='include'\n")
		caller := putFile(t, filepath.Join(f.root, "caller.cmake"), "set(CALLER_MARKER \"caller preserved\")\n")
		f.child("leaf", `if(NOT CALLER_MARKER)
message(FATAL_ERROR "caller include lost")
endif()
add_library(child STATIC child.cpp)
get_target_property(pic child POSITION_INDEPENDENT_CODE)
file(WRITE "${CMAKE_BINARY_DIR}/settings" "${CMAKE_POLICY_VERSION_MINIMUM}|${CMAKE_CXX_STANDARD}|${pic}|${CALLER_MARKER}")
install(TARGETS child ARCHIVE DESTINATION lib)
install(FILES "${CMAKE_BINARY_DIR}/settings" DESTINATION .)
`, "depends=['dep']\n", "", nil, map[string]string{"child.cpp": "#include <optional>\n#include <x.h>\nint value() { return std::optional<int>(17).value(); }\n"})
		f.mustEntry("-DBUILD_LIST=leaf", "-DCMAKE_PROJECT_INCLUDE="+filepath.ToSlash(caller))
		f.build()
		requireFile(t, f.installed("leaf", "settings"), "3.5|17|ON|caller preserved")
	})
}

func TestEntryArchitecture(t *testing.T) {
	f := newEntryFixture(t)
	x64 := f.repository("x64", map[string]string{"x.h": "x86_64"})
	arm := f.repository("arm", map[string]string{"x.h": "aarch64"})
	system := strings.ToLower(f.context.SystemName)
	definitionFile(t, f.root, "demo", "copy", "[[copy]]\nfiles=['x.h']\ndestination='include'\n"+
		"[platforms."+system+".architectures.x86_64.source]\n"+x64+
		"[platforms."+system+".architectures.aarch64.source]\n"+arm)
	args := []string{"-DBUILD_LIST=demo"}
	if runtime.GOOS == "windows" {
		args = append(args, "-DCMAKE_SYSTEM_PROCESSOR=ARM64")
	}
	f.mustEntry(args...)
	f.build()
	want := "x86_64"
	if runtime.GOOS != "windows" && runtime.GOARCH == "arm64" {
		want = "aarch64"
	}
	requireFile(t, f.installed("demo", "include/x.h"), want)
}

func TestEntryDefaults(t *testing.T) {
	t.Run("legacy and modern compilation", func(t *testing.T) {
		f := newEntryFixture(t)
		for _, tc := range []struct{ name, minimum, policy string }{{"legacy", "3.0", "CMP0054"}, {"modern", "3.20", "CMP0077"}} {
			repo := f.repository(tc.name, map[string]string{"CMakeLists.txt": "cmake_minimum_required(VERSION " + tc.minimum + ")\nproject(child CXX)\n" +
				"add_library(child STATIC child.cpp)\ncmake_policy(GET " + tc.policy + " behavior)\n" +
				"get_target_property(pic child POSITION_INDEPENDENT_CODE)\n" +
				"file(WRITE \"${CMAKE_BINARY_DIR}/settings\" \"${CMAKE_POLICY_VERSION_MINIMUM}|${CMAKE_CXX_STANDARD}|${pic}|${behavior}\")\n" +
				"install(TARGETS child ARCHIVE DESTINATION lib)\ninstall(FILES \"${CMAKE_BINARY_DIR}/settings\" DESTINATION .)\n",
				"child.cpp": "#include <optional>\nint value() { return std::optional<int>(17).value(); }\n"})
			f.definition(tc.name, "cmake", "[source]\n"+repo)
		}
		f.mustEntry()
		cache := readFile(t, filepath.Join(f.context.BuildRoot, "CMakeCache.txt"))
		for _, line := range strings.Split(cache, "\n") {
			for _, key := range []string{"CMAKE_POLICY_VERSION_MINIMUM", "CMAKE_CXX_STANDARD", "CMAKE_POSITION_INDEPENDENT_CODE"} {
				if strings.HasPrefix(line, key+":") {
					t.Fatal("default leaked into parent cache", line)
				}
			}
		}
		f.build()
		for _, name := range []string{"legacy", "modern"} {
			behavior := "NEW"
			if name == "legacy" && strings.Contains(f.must(f.context.CMakeCommand, "--version"), "version 3.") {
				behavior = ""
			}
			requireFile(t, f.installed(name, "settings"), "3.5|17|ON|"+behavior)
			files, err := os.ReadDir(f.installed(name, "lib"))
			if err != nil || len(files) == 0 {
				t.Fatal("compiled library missing", err)
			}
		}
	})
	t.Run("parent and module overrides", func(t *testing.T) {
		f := newEntryFixture(t)
		repo := f.repository("cache", map[string]string{"CMakeLists.txt": `cmake_minimum_required(VERSION 3.20)
project(child NONE)
file(WRITE "${CMAKE_BINARY_DIR}/settings" "${CMAKE_POLICY_VERSION_MINIMUM}|${CMAKE_CXX_STANDARD}|${CMAKE_POSITION_INDEPENDENT_CODE}")
install(FILES "${CMAKE_BINARY_DIR}/settings" DESTINATION .)
`})
		f.definition("common", "cmake", "[source]\n"+repo)
		f.definition("specific", "cmake", "[source]\n"+repo+"[cmake.cache]\nCMAKE_POLICY_VERSION_MINIMUM='3.10'\nCMAKE_CXX_STANDARD=17\nCMAKE_POSITION_INDEPENDENT_CODE=true\n")
		f.mustEntry("-DCMAKE_POLICY_VERSION_MINIMUM=3.6", "-DCMAKE_CXX_STANDARD=20", "-DCMAKE_POSITION_INDEPENDENT_CODE=OFF")
		f.build()
		requireFile(t, f.installed("common", "settings"), "3.6|20|OFF")
		requireFile(t, f.installed("specific", "settings"), "3.10|17|ON")
		time.Sleep(1100 * time.Millisecond)
		f.mustEntry("-DCMAKE_CXX_STANDARD=23")
		f.build()
		requireFile(t, f.installed("common", "settings"), "3.6|23|OFF")
		time.Sleep(1100 * time.Millisecond)
		f.mustEntry("-UCMAKE_POLICY_VERSION_MINIMUM", "-UCMAKE_CXX_STANDARD", "-UCMAKE_POSITION_INDEPENDENT_CODE")
		f.build()
		requireFile(t, f.installed("common", "settings"), "3.5|17|ON")
		requireFile(t, f.installed("specific", "settings"), "3.10|17|ON")
	})
}

func TestNoPythonTooling(t *testing.T) {
	f := newEntryFixture(t)
	f.copyModule("demo")
	blocked := filepath.Join(f.root, "blocked tools")
	marker := filepath.Join(f.root, "forbidden-call")
	for _, name := range []string{"uv", "python", "python2", "python3"} {
		file, body := filepath.Join(blocked, name), "#!/bin/sh\nprintf forbidden > '"+marker+"'\nexit 99\n"
		if runtime.GOOS == "windows" {
			file += ".cmd"
			body = "@echo forbidden>\"" + marker + "\"\r\n@exit /b 99\r\n"
		}
		putFile(t, file, body)
		if err := os.Chmod(file, 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", blocked+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VIRTUAL_ENV", "")
	for _, name := range []string{"pyproject.toml", "uv.lock", ".python-version", ".venv"} {
		if _, err := os.Stat(filepath.Join(f.root, name)); !os.IsNotExist(err) {
			t.Fatal("fixture contains Python input", name)
		}
	}
	f.mustEntry()
	f.build()
	requireFile(t, f.installed("demo", "include/x.h"), "demo")
	output := filepath.Join(f.context.BuildRoot, "generated/demo.cmake")
	before, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	f.mustEntry()
	f.build()
	after, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("unchanged generated output rewritten")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Python or uv was invoked")
	}
}
