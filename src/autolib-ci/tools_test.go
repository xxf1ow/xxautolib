package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDescriptionPathsRejectNonCanonicalAliases(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
	}{
		{name: "dot segment", paths: []string{"x/./y"}},
		{name: "parent segment", paths: []string{"a/../b"}},
		{name: "alias duplicate", paths: []string{"a/../b", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateDescriptionPaths(tc.paths); err == nil {
				t.Fatalf("accepted non-canonical builder paths %q", tc.paths)
			}
		})
	}
}

func TestIndependentTargets(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "autolib-builder"+executableSuffix())
	build := exec.CommandContext(context.Background(), cmake, "-P", filepath.Join(root, "src", "autolib-builder", "go-sdk.cmake"), "--", "build", "-mod=readonly", "-trimpath", "-o", output, "./src/autolib-builder")
	build.Dir = root
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build independent builder target: %v\n%s", err, data)
	}
	command := exec.CommandContext(context.Background(), output, "ci", "plan")
	data, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(data), "usage: autolib-builder") || strings.Contains(string(data), "ci plan") {
		t.Fatalf("builder still accepts CI planning: err=%v output=%q", err, data)
	}
	platforms, modules := "linux-x86_64", "lz4"
	realPlan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: output, Platforms: &platforms, Modules: &modules})
	if err != nil {
		t.Fatalf("CI plan did not consume the real builder describe command: %v", err)
	}
	if len(realPlan.Platforms) != 1 || realPlan.Platforms[0].ID != platforms || !containsModule(realPlan.Platforms[0].Description.Modules, "lz4") {
		t.Fatalf("real builder describe result was not retained in CI plan: %+v", realPlan)
	}
	module := realPlan.Platforms[0].Description.Modules[0]
	if module.Version != "1.10.0" || module.Target != "_al_pkg_6c7a34_312e31302e30" || len(realPlan.Platforms[0].Roots) != 1 || realPlan.Platforms[0].Roots[0] != "lz4@1.10.0" {
		t.Fatalf("real builder identity and target: %+v", realPlan.Platforms[0])
	}

	// The fake describe executable succeeds without reading module.toml, proving
	// CI planning consumes the builder boundary instead of parsing declarations.
	sourceRoot := fakePlanRoot(t)
	t.Setenv("AUTOLIB_CI_HELPER_MODE", "valid")
	plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: sourceRoot, BuilderFile: testExecutable(t), Platforms: stringPointer("linux-x86_64"), Modules: stringPointer("lz4")})
	if err != nil || len(plan.Platforms) != 1 {
		t.Fatalf("CI did not consume an independent describe executable: %+v, %v", plan, err)
	}
}

func TestRootConfigureDoesNotBuildCIOrWatchCISource(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Skip("CMake is unavailable")
	}
	if _, err := exec.LookPath("ninja"); err != nil {
		t.Skip("Ninja is unavailable")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is unavailable")
	}
	clangxx, err := exec.LookPath("clang++")
	if err != nil {
		t.Skip("clang++ is unavailable")
	}
	buildRoot := filepath.Join(t.TempDir(), "root configure")
	args := []string{"-S", root, "-B", buildRoot, "-G", "Ninja", "-DCMAKE_C_COMPILER=" + clang, "-DCMAKE_CXX_COMPILER=" + clangxx, "-DBUILD_LIST=queue"}
	configure := exec.Command(cmake, args...)
	installRoot := filepath.Join(t.TempDir(), "isolated install")
	configure.Env = append(withoutEnvironment(os.Environ(), "AUTOLIB_INSTALL_ROOT"), "AUTOLIB_INSTALL_ROOT="+installRoot)
	configureOutput, err := configure.CombinedOutput()
	if err != nil {
		t.Fatalf("root configure command %q failed: %v\n%s", append([]string{cmake}, args...), err, configureOutput)
	}
	t.Logf("command: %q\nexit code: 0\n%s", append([]string{cmake}, args...), configureOutput)
	if installed := mustReadText(t, filepath.Join(installRoot, "autolib.cmake")); installed != mustReadText(t, filepath.Join(root, "src", "autolib.cmake")) {
		t.Fatal("ordinary root failed to install the unchanged source entry")
	}
	toolsRoot := filepath.Join(buildRoot, "tools")
	if _, err := os.Stat(filepath.Join(toolsRoot, "autolib-builder"+executableSuffix())); err != nil {
		t.Fatalf("root configure did not build builder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(toolsRoot, "autolib-ci"+executableSuffix())); !os.IsNotExist(err) {
		t.Fatalf("root configure unexpectedly built autolib-ci: stat err=%v", err)
	}

	ciSource := filepath.Join(root, "src", "autolib-ci", "run.go")
	info, err := os.Stat(ciSource)
	if err != nil {
		t.Fatal(err)
	}
	originalModTime := info.ModTime()
	t.Cleanup(func() {
		if err := os.Chtimes(ciSource, originalModTime, originalModTime); err != nil {
			t.Errorf("restore CI source timestamp: %v", err)
		}
	})
	changedModTime := time.Now().Add(2 * time.Second)
	if changedModTime.Before(originalModTime) {
		changedModTime = originalModTime.Add(2 * time.Second)
	}
	if err := os.Chtimes(ciSource, changedModTime, changedModTime); err != nil {
		t.Fatal(err)
	}
	buildArgs := []string{"--build", buildRoot, "--target", "help"}
	build := exec.Command(cmake, buildArgs...)
	buildOutput, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("root help command %q failed: %v\n%s", append([]string{cmake}, buildArgs...), err, buildOutput)
	}
	if strings.Contains(string(buildOutput), "Configuring done") || strings.Contains(string(buildOutput), "Re-running CMake") {
		t.Fatalf("CI source timestamp triggered root reconfigure:\n%s", buildOutput)
	}
	t.Logf("command: %q\nexit code: 0\nCI source timestamp advanced; no CMake reconfigure output\n%s", append([]string{cmake}, buildArgs...), buildOutput)
}

func containsModule(modules []describedModule, name string) bool {
	for _, module := range modules {
		if module.Name == name {
			return true
		}
	}
	return false
}

func testExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func TestCIDescriptionPackagePlatform(t *testing.T) {
	for _, platform := range []string{"any-any", "linux-x86_64", "", "windows-x86_64"} {
		d := moduleDescription{SystemName: "Linux", Architecture: "x86_64", Roots: []string{"demo@1"}, RequestedRoots: []string{"demo@1"}, FilteredRoots: []filteredRoot{}, FailedRoots: []failedRoot{}, GoModuleFiles: []string{}, BuilderFiles: []string{}, Modules: []describedModule{{Name: "demo", Version: "1", Target: "demo", Depends: []string{}, PackagePlatform: platform, SystemDependencies: map[string][]string{}, InputFiles: []string{}}}}
		err := validateModuleDescription(d, "Linux", "x86_64", nil)
		if (err == nil) != (platform == "any-any" || platform == "linux-x86_64") {
			t.Fatalf("package_platform=%q: %v", platform, err)
		}
	}
}

func TestCIPreparedImporter(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	osName, arch := testPlatform()
	name := ciImporterExecutable(osName + "-" + arch)
	for _, mode := range []string{"script", "root"} {
		for _, kind := range []string{"prepared", "in place", "default", "wrong name", "missing", "directory", "symlink"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				source := t.TempDir()
				install := filepath.Join(source, "install")
				builder := discoveryBuilder(t)
				for _, file := range []string{"CMakeLists.txt", "go.mod", "go.sum", "src/autolib.cmake", "src/autolib-importer/main.go", "src/autolib-importer/fixup-metadata.cmake", "src/internal/packagemetadata/references.go", "src/autolib-builder/index.cmake", "src/autolib-builder/prepare-tools.cmake"} {
					writeCIFixture(t, source, file, mustReadText(t, filepath.Join(root, file)))
				}
				writeCIFixture(t, source, "src/dependencies/queue/package.toml", "latest='1'\n")
				writeCIFixture(t, source, "src/dependencies/queue/1/module.toml", "type='copy'\npackage_platforms=['any-any']\n[source]\nGIT_REPOSITORY='fixture'\nGIT_TAG='0123456789012345678901234567890123456789'\n[[copy]]\nfiles=['h.h']\ndestination='include'\n")
				writeCIFixture(t, source, "src/dependencies/queue/1/autolib-import.cmake", "add_library(autolib::queue INTERFACE IMPORTED GLOBAL)\n")
				writeCIFixture(t, source, "src/autolib-builder/go-sdk.cmake", `function(xxautolib_prepare_go executable_output environment_output)
set(${executable_output} "${CMAKE_COMMAND}" PARENT_SCOPE)
set(${environment_output} "${CMAKE_COMMAND};-DREAL_BUILDER=${REAL_BUILDER};-P;${CMAKE_CURRENT_FUNCTION_LIST_DIR}/compile.cmake;--" PARENT_SCOPE)
endfunction()
`)
				writeCIFixture(t, source, "src/autolib-builder/compile.cmake", `math(EXPR last "${CMAKE_ARGC}-1")
foreach(i RANGE 0 ${last})
 if(CMAKE_ARGV${i} STREQUAL "-o")
  math(EXPR next "${i}+1")
  set(output "${CMAKE_ARGV${next}}")
 endif()
endforeach()
if(CMAKE_ARGV${last} STREQUAL "./src/autolib-importer")
 message(FATAL_ERROR "importer compilation reached")
endif()
configure_file("${REAL_BUILDER}" "${output}" COPYONLY)
`)
				prepared := filepath.Join(source, "prepared", name)
				if kind == "in place" {
					prepared = filepath.Join(install, name)
				}
				if kind == "wrong name" {
					prepared = filepath.Join(source, "prepared", "wrong")
				}
				if kind != "missing" {
					writeCIFixture(t, filepath.Dir(prepared), filepath.Base(prepared), "verified prepared tool")
				}
				if kind == "directory" {
					os.Remove(prepared)
					if err := os.Mkdir(prepared, 0755); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "symlink" {
					os.Remove(prepared)
					if err := os.Symlink(builder, prepared); err != nil {
						t.Skip(err)
					}
				}
				args := []string{"-DREAL_BUILDER=" + builder}
				if kind != "default" {
					args = append(args, "-DAUTOLIB_PREPARED_IMPORTER="+prepared)
				}
				if mode == "script" {
					args = append(args, "-DAUTOLIB_SOURCE_ROOT="+source, "-DAUTOLIB_INSTALL_ROOT="+install, "-P", filepath.Join(source, "src/autolib-builder/prepare-tools.cmake"))
				} else {
					args = append(args, "-S", source, "-B", filepath.Join(source, "build"), "-DBUILD_LIST=queue")
					config, err := loadCIEnvironment(filepath.Join(root, ".github/ci-tools.json"))
					if err != nil {
						t.Fatal(err)
					}
					production, err := ciProductionForPlatform(osName+"-"+arch, config)
					if err != nil {
						t.Fatal(err)
					}
					for _, key := range sortedKeysStringMap(production.CMakeDefaults) {
						args = append(args, "-D"+key+"="+production.CMakeDefaults[key])
					}
				}
				cmd := exec.Command("cmake", args...)
				cmd.Env = append(withoutEnvironment(os.Environ(), "AUTOLIB_INSTALL_ROOT"), "AUTOLIB_INSTALL_ROOT="+install)
				out, err := cmd.CombinedOutput()
				if kind == "prepared" || kind == "in place" {
					if err != nil {
						t.Fatalf("prepared importer: %v\n%s", err, out)
					}
					if mustReadText(t, filepath.Join(install, name)) != "verified prepared tool" {
						t.Fatal("prepared program was not installed")
					}
				} else {
					if err == nil {
						t.Fatal("invalid input or unexpected compilation accepted")
					}
					compiled := strings.Contains(string(out), "importer compilation reached")
					if compiled != (kind == "default") {
						t.Fatalf("unexpected importer compilation=%t: %s", compiled, out)
					}
				}
			})
		}
	}
}
