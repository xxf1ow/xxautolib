package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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

	// The fake describe executable succeeds without reading module.toml, proving
	// CI planning consumes the builder boundary instead of parsing declarations.
	sourceRoot := t.TempDir()
	t.Setenv("AUTOLIB_CI_HELPER_MODE", "valid")
	plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: sourceRoot, BuilderFile: testExecutable(t), Platforms: stringPointer("linux-x86_64"), Modules: stringPointer("lz4")})
	if err != nil || len(plan.Platforms) != 1 {
		t.Fatalf("CI did not consume an independent describe executable: %+v, %v", plan, err)
	}
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
