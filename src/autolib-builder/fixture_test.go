package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fixture struct {
	t       *testing.T
	root    string
	context buildContext
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	base, err := filepath.Abs(filepath.Join("..", "..", "build", "tests"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(base, "go pipeline ")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	// MSBuild 对系统临时目录禁用部分增量行为；与原 fixture 一样放在 build/tests。
	root := filepath.Join(directory, "中文 fixture")
	f := &fixture{t: t, root: root, context: buildContext{SourceRoot: root, BuildRoot: filepath.Join(root, "build"), SystemName: "Linux", Generator: "Unix Makefiles", CMakeCommand: cmake, GitExecutable: git}}
	if runtime.GOOS == "windows" {
		f.context.SystemName = "Windows"
		f.context.Generator = "Visual Studio 17 2022"
		f.context.Platform = "x64"
		f.context.Toolset = "v143,host=x64"
		f.context.MultiConfig = true
	}
	for _, file := range []string{"apply_patch.cmake", "configure_dependencies.cmake"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		putFile(t, filepath.Join(root, "src", "autolib-builder", file), string(data))
	}
	return f
}

func (f *fixture) command(args ...string) (string, string, error) {
	f.t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func (f *fixture) must(args ...string) string {
	f.t.Helper()
	out, errout, err := f.command(args...)
	if err != nil {
		f.t.Fatalf("%q: %v\n%s\n%s", args, err, out, errout)
	}
	return out + errout
}

func (f *fixture) repository(name string, files map[string]string) string {
	f.t.Helper()
	path := filepath.Join(f.root, "upstream", name)
	for filename, body := range files {
		putFile(f.t, filepath.Join(path, filename), body)
	}
	f.must(f.context.GitExecutable, "init", path)
	f.must(f.context.GitExecutable, "-C", path, "config", "core.autocrlf", "false")
	f.must(f.context.GitExecutable, "-C", path, "add", ".")
	f.must(f.context.GitExecutable, "-C", path, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	sha := strings.TrimSpace(f.must(f.context.GitExecutable, "-C", path, "rev-parse", "HEAD"))
	return fmt.Sprintf("GIT_REPOSITORY='%s'\nGIT_TAG='%s'\n", filepath.ToSlash(path), sha)
}

func (f *fixture) definition(name, kind, body string) *moduleDefinition {
	f.t.Helper()
	path := definitionFile(f.t, f.root, name, kind, body)
	m, err := loadDefinition(path, f.context.SystemName, f.context.Architecture)
	if err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *fixture) project(modules []*moduleDefinition, suffix string) {
	f.t.Helper()
	byName := map[string]*moduleDefinition{}
	for _, m := range modules {
		byName[m.Name] = m
	}
	var generated strings.Builder
	for _, m := range modules {
		deps := []*moduleDefinition{}
		for _, dep := range m.Depends {
			deps = append(deps, byName[dep])
		}
		text, err := renderModule(m, f.context, deps)
		if err != nil {
			f.t.Fatal(err)
		}
		generated.WriteString(text + "\n")
	}
	putFile(f.t, filepath.Join(f.root, "modules.cmake"), generated.String())
	putFile(f.t, filepath.Join(f.root, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(fixture NONE)\ninclude(ExternalProject)\ninclude(modules.cmake)\n"+suffix)
	f.configure()
}

func (f *fixture) configure() string {
	f.t.Helper()
	args := []string{f.context.CMakeCommand, "-S", f.root, "-B", f.context.BuildRoot, "-G", f.context.Generator}
	if f.context.Platform != "" {
		args = append(args, "-A", f.context.Platform, "-T", f.context.Toolset)
	}
	return f.must(args...)
}
func (f *fixture) build() string {
	f.t.Helper()
	return f.must(f.context.CMakeCommand, "--build", f.context.BuildRoot, "--config", "Release")
}
func (f *fixture) installed(name, path string) string {
	root := f.context.InstallRoot
	if root == "" {
		root = filepath.Join(f.root, "3rdparty")
	}
	return filepath.Join(root, name, path)
}
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func requireFile(t *testing.T, path, want string) {
	t.Helper()
	if got := readFile(t, path); got != want {
		t.Fatalf("%s: want %q, got %q", path, want, got)
	}
}

func requireTextFile(t *testing.T, path, want string) {
	t.Helper()
	// CMake 文本输出使用宿主换行；参数保真和二进制检查仍逐字节比较。
	if got := strings.ReplaceAll(readFile(t, path), "\r\n", "\n"); got != want {
		t.Fatalf("%s: want %q, got %q", path, want, got)
	}
}
