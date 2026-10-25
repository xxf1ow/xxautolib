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

	// The fake describe executable succeeds without reading module.toml, proving
	// CI planning consumes the builder boundary instead of parsing declarations.
	sourceRoot := t.TempDir()
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
	configureOutput, err := configure.CombinedOutput()
	if err != nil {
		t.Fatalf("root configure command %q failed: %v\n%s", append([]string{cmake}, args...), err, configureOutput)
	}
	t.Logf("command: %q\nexit code: 0\n%s", append([]string{cmake}, args...), configureOutput)
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
