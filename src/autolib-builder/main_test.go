package main

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func cliArguments(context buildContext, names ...string) []string {
	args := []string{"generate"}
	for _, name := range names {
		args = append(args, "--module="+name)
	}
	for _, pair := range [][2]string{{"source-root", context.SourceRoot}, {"build-root", context.BuildRoot}, {"install-root", context.InstallRoot}, {"make-program", context.MakeProgram}, {"system-name", context.SystemName}, {"architecture", context.Architecture}, {"generator", context.Generator}, {"platform", context.Platform}, {"toolset", context.Toolset}, {"instance", context.Instance}, {"cmake-command", context.CMakeCommand}, {"git-executable", context.GitExecutable}} {
		args = append(args, "--"+pair[0]+"="+pair[1])
	}
	config := "OFF"
	if context.MultiConfig {
		config = "ON"
	}
	return append(args, "--multi-config="+config)
}

func TestCLI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 || stdout.String() != "autolib-builder 1\n" || stderr.Len() != 0 {
		t.Fatalf("%d %q %q", code, stdout.String(), stderr.String())
	}
	f := newEntryFixture(t)
	f.definition("demo", "cmake", testSource)
	f.context.InstallRoot = filepath.Join(f.root, "explicit install")
	if runtime.GOOS != "windows" {
		f.context.MakeProgram = "/explicit/toolchain/make"
	}
	args := cliArguments(f.context, "demo", "demo")
	stdout.Reset()
	stderr.Reset()
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("%d %s", code, stderr.String())
	}
	inputs := readFile(t, filepath.Join(f.context.BuildRoot, "generated/inputs.cmake"))
	if strings.Count(inputs, "generated/demo.cmake") != 1 {
		t.Fatal(inputs)
	}
	generated := readFile(t, filepath.Join(f.context.BuildRoot, "generated/demo.cmake"))
	if !strings.Contains(generated, filepath.ToSlash(filepath.Join(f.context.InstallRoot, "demo"))) {
		t.Fatalf("--install-root was not applied:\n%s", generated)
	}
	if f.context.Generator == "Unix Makefiles" && !strings.Contains(generated, "CMAKE_MAKE_PROGRAM:STRING=/explicit/toolchain/make") {
		t.Fatalf("--make-program was not applied:\n%s", generated)
	}
	for _, bad := range [][]string{nil, {"unknown"}, {"version", "extra"}, {"generate"}, {"generate", "--unknown=value"}, {"ci", "plan"}, append(append([]string{}, args...), "--multi-config=bad"), append(append([]string{}, args...), "--cmake-default=not-a-pair")} {
		stdout.Reset()
		stderr.Reset()
		if code := run(bad, &stdout, &stderr); code == 0 || stderr.Len() == 0 {
			t.Fatalf("accepted %q: %d %q", bad, code, stderr.String())
		}
	}
	stderr.Reset()
	if code := run(cliArguments(f.context, "missing"), &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "module.toml") || !strings.Contains(stderr.String(), "missing") {
		t.Fatal(stderr.String())
	}
}
