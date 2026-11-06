package main

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCIDarwinNative(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Darwin ARM64")
	}
	for _, selection := range []string{"libraries", "tools", "filtered"} {
		t.Run(selection, func(t *testing.T) {
			f := installedCIFixture(t)
			config, err := loadCIEnvironment(filepath.Join(f.options.SourceRoot, ".github/ci-tools.json"))
			if err != nil {
				t.Fatal(err)
			}
			c, err := ciProductionForPlatform("darwin-aarch64", config)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateCIProductionIdentity(c, config); err != nil {
				t.Fatal(err)
			}
			if len(config.Production.Common.CMakeDefaults) != 2 {
				t.Fatal("mutated common defaults")
			}
			for _, key := range []string{"CMAKE_OSX_ARCHITECTURES", "CMAKE_OSX_DEPLOYMENT_TARGET", "CMAKE_OSX_SYSROOT", "CMAKE_C_COMPILER", "CMAKE_CXX_COMPILER"} {
				bad := c
				bad.CMakeDefaults = map[string]string{}
				for k, v := range c.CMakeDefaults {
					bad.CMakeDefaults[k] = v
				}
				bad.CMakeDefaults[key] = "wrong"
				if err := validateCIProductionIdentity(bad, config); err == nil {
					t.Fatalf("invalid %s accepted", key)
				}
			}
			wrong, err := ciProductionForPlatform("linux-aarch64", config)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateCIProductionIdentity(wrong, config); err == nil {
				t.Fatal("foreign host accepted")
			}
			platform := "darwin-aarch64"
			modules := ""
			if selection == "filtered" {
				recipe := filepath.Join(f.options.SourceRoot, "src/dependencies/queue/1/module.toml")
				writeCIFixture(t, f.options.SourceRoot, "src/dependencies/queue/1/module.toml", strings.Replace(mustReadText(t, recipe), "'darwin-aarch64'", "'linux-x86_64'", 1))
				// Avoid a duplicate whitelist item in the deliberately unsupported root.
				writeCIFixture(t, f.options.SourceRoot, "src/dependencies/queue/1/module.toml", strings.Replace(mustReadText(t, recipe), "'linux-x86_64','linux-aarch64','linux-x86_64'", "'linux-x86_64','linux-aarch64'", 1))
				modules = "queue@1"
			}
			if selection != "libraries" {
				plan, err := planCI(t.Context(), ciPlanOptions{SourceRoot: f.options.SourceRoot, BuilderFile: filepath.Join(f.options.SourceRoot, "tools/builder"), Platforms: &platform, Modules: &modules})
				if err != nil {
					t.Fatal(err)
				}
				f.plan = plan
				if len(plan.Platforms) != 1 || len(plan.Platforms[0].Roots) != 0 {
					t.Fatal(plan)
				}
				if err := writeCIJSON(f.options.PlanFile, plan); err != nil {
					t.Fatal(err)
				}
			} else if f.plan.Publisher != "darwin-aarch64" {
				t.Fatal("Darwin universal publisher missing", f.plan.Publisher)
			}
			if selection == "tools" {
				f.options.Budget = 0
			}
			phases := []string{"prepare"}
			for i := range f.plan.Platforms[0].Layers {
				phases = append(phases, "layer"+string(rune('0'+i)))
			}
			phases = append(phases, "verify")
			for _, phase := range phases {
				f.options.Layer = phase
				if err := runCILayer(t.Context(), f.options); err != nil {
					t.Fatalf("%s: %v", phase, err)
				}
			}
			var result ciPlatformResult
			if err := decodeStrictJSONFile(filepath.Join(f.options.WorkRoot, "results/darwin-aarch64.json"), &result); err != nil {
				t.Fatal(err)
			}
			if !result.Complete || !result.Importer.Verified || !result.Entry.Verified {
				t.Fatal(result)
			}
			if selection != "libraries" {
				if len(result.Packages) != 0 || len(result.Roots) != 0 {
					t.Fatal(result)
				}
				if _, err := os.Stat(filepath.Join(f.options.WorkRoot, "build/generated")); !os.IsNotExist(err) {
					t.Fatal("tool-only generated library targets")
				}
			} else {
				if len(result.Packages) != 4 {
					t.Fatal(result)
				}
				for _, root := range append([]ciRootResult{{EvidenceDirectory: f.options.WorkRoot}}, result.Roots...) {
					cache := mustReadText(t, filepath.Join(root.EvidenceDirectory, "build/CMakeCache.txt"))
					for key, value := range c.CMakeDefaults {
						if !strings.Contains(cache, "="+value+"\n") {
							t.Errorf("cache lost %s=%s", key, value)
						}
					}
					if !strings.Contains(cache, "="+f.options.MakeProgram+"\n") {
						t.Fatal("consumer lost Make")
					}
				}
			}
			all := "all"
			p, err := planCI(t.Context(), ciPlanOptions{SourceRoot: f.options.SourceRoot, BuilderFile: "unused", Platforms: &all})
			if err != nil || len(p.Platforms) != 4 {
				t.Fatal("all did not retain four platforms", p, err)
			}
		})
	}
}

func TestCIDarwinMachO(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Darwin ARM64")
	}
	tool := buildTestImporter(t)
	if err := verifyImporterNoCRuntime(tool); err != nil {
		t.Fatal(err)
	}
	m, err := macho.Open(tool)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	libs, err := m.ImportedLibraries()
	if err != nil || !slices.Contains(libs, "/usr/lib/libSystem.B.dylib") {
		t.Fatal(libs, err)
	}
	original, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"Intel", "fat", "deployment", "third-party", "C++ runtime"} {
		t.Run(kind, func(t *testing.T) {
			data := bytes.Clone(original)
			switch kind {
			case "Intel":
				binary.LittleEndian.PutUint32(data[4:8], uint32(macho.CpuAmd64))
			case "fat":
				data = []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 0}
			case "deployment":
				offset := 32
				changed := false
				for _, load := range m.Loads {
					raw := load.Raw()
					cmd := m.ByteOrder.Uint32(raw[:4])
					if cmd == 0x32 {
						m.ByteOrder.PutUint32(data[offset+12:offset+16], 16<<16)
						changed = true
					}
					if cmd == 0x24 {
						m.ByteOrder.PutUint32(data[offset+8:offset+12], 16<<16)
						changed = true
					}
					offset += len(raw)
				}
				if !changed {
					t.Fatal("missing version command")
				}
			case "third-party", "C++ runtime":
				old := []byte("/usr/lib/libSystem.B.dylib")
				replacement := make([]byte, len(old))
				copy(replacement, "/opt/homebrew/lib/x.dylib")
				if kind == "C++ runtime" {
					clear(replacement)
					copy(replacement, "/usr/lib/libc++.1.dylib")
				}
				if !bytes.Contains(data, old) {
					t.Fatal("missing system dependency")
				}
				data = bytes.Replace(data, old, replacement, 1)
			}
			path := filepath.Join(t.TempDir(), "bad")
			if err := os.WriteFile(path, data, 0755); err != nil {
				t.Fatal(err)
			}
			if err := validateDarwinBinary(path, "15.0", true); err == nil {
				t.Fatal("invalid Mach-O accepted")
			}
		})
	}
	t.Run("static members", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "probe.c")
		object := filepath.Join(root, "probe.o")
		writeCIFixture(t, root, "probe.c", "int probe(void) { return 1; }\n")
		if out, err := exec.Command("xcrun", "clang", "-arch", "arm64", "-mmacosx-version-min=15.0", "-c", source, "-o", object).CombinedOutput(); err != nil {
			t.Fatalf("native object: %v %s", err, out)
		}
		data, err := os.ReadFile(object)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []bool{false, true} {
			member := bytes.Clone(data)
			if bad {
				binary.LittleEndian.PutUint32(member[4:8], uint32(macho.CpuAmd64))
			}
			header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d`\n", "probe.o/", 0, 0, 0, 0644, len(member))
			archive := append([]byte("!<arch>\n"+header), member...)
			if len(archive)%2 != 0 {
				archive = append(archive, '\n')
			}
			path := filepath.Join(root, "libprobe.a")
			if err := os.WriteFile(path, archive, 0644); err != nil {
				t.Fatal(err)
			}
			if err := validateDarwinArchive(path, "15.0"); (err != nil) != bad {
				t.Fatalf("bad=%t: %v", bad, err)
			}
		}
	})
	run := t.TempDir()
	pkg := filepath.Join(run, "install/probe/1")
	if err := os.MkdirAll(pkg, 0755); err != nil {
		t.Fatal(err)
	}
	writeCIFixture(t, run, "build/autolib_smoke", string(original))
	writeCIFixture(t, run, "build/link-inputs-Release.txt", "/opt/homebrew/lib/libaccidental.a\n")
	if err := verifyDarwinPackageInputs(run, []string{pkg}, "15.0"); err == nil || !strings.Contains(err.Error(), "Darwin consumer link input") {
		t.Fatal("Homebrew input did not reach path validation", err)
	}
	writeCIFixture(t, run, "build/link-inputs-Release.txt", filepath.Join(run, "production/libbad.a")+"\n")
	if err := verifyDarwinPackageInputs(run, []string{pkg}, "15.0"); err == nil || !strings.Contains(err.Error(), "Darwin consumer link input") {
		t.Fatal("producer input did not reach path validation", err)
	}
}

func TestCIDarwinDriverParameters(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Darwin ARM64")
	}
	repository, _ := filepath.Abs("../..")
	root, err := os.MkdirTemp(filepath.Join(repository, "build"), "driver Apple 中文 ")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	config, err := loadCIEnvironment(filepath.Join(repository, ".github/ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := ciProductionForPlatform("darwin-aarch64", config)
	if err != nil {
		t.Fatal(err)
	}
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	makeProgram, err := exec.LookPath("make")
	if err != nil {
		t.Fatal(err)
	}
	body := `cmake_minimum_required(VERSION 3.20)
project(driver_parameters C CXX)
`
	for key, value := range c.CMakeDefaults {
		body += fmt.Sprintf("if(NOT %s STREQUAL [==[%s]==])\nmessage(FATAL_ERROR \"lost %s\")\nendif()\n", key, value, key)
	}
	body += fmt.Sprintf("if(NOT CMAKE_MAKE_PROGRAM STREQUAL [==[%s]==] OR NOT CMAKE_C_COMPILER_ID STREQUAL AppleClang OR NOT CMAKE_CXX_COMPILER_ID STREQUAL AppleClang)\nmessage(FATAL_ERROR \"lost native compiler or Make\")\nendif()\n", makeProgram)
	body += `enable_testing()
file(WRITE "${CMAKE_BINARY_DIR}/prepared-state.cmake" "")
add_test(NAME forwarded COMMAND "${CMAKE_COMMAND}" -E true)
`
	writeCIFixture(t, root, "tests/autolib/CMakeLists.txt", body)
	writeCIFixture(t, root, "tests/autolib/smoke.cpp", "")
	writeCIFixture(t, root, "entry/autolib.cmake", "# fixture entry\n")
	for _, driver := range []string{"run-installed.cmake", "run-github.cmake"} {
		t.Run(driver, func(t *testing.T) {
			args := []string{"-DSOURCE_ROOT=" + root, "-DVALIDATION_ROOT=" + filepath.Join(root, "build", driver), "-DGENERATOR=Unix Makefiles", "-DINSTALL_ROOT=" + filepath.Join(root, "entry"), "-DENTRY_URL=file://" + filepath.Join(root, "entry/autolib.cmake"), "-DCMAKE_MAKE_PROGRAM=" + makeProgram}
			for key, value := range c.CMakeDefaults {
				args = append(args, "-D"+key+"="+value)
			}
			args = append(args, "-P", filepath.Join(repository, "tests/autolib", driver))
			if out, err := exec.Command(cmake, args...).CombinedOutput(); err != nil {
				t.Fatalf("driver parameters: %v\n%s", err, out)
			}
		})
	}
}
