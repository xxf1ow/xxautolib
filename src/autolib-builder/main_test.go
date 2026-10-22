package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func cliArguments(context buildContext, names ...string) []string {
	args := []string{"generate"}
	for _, name := range names {
		args = append(args, "--module="+name)
	}
	for _, pair := range [][2]string{{"source-root", context.SourceRoot}, {"build-root", context.BuildRoot}, {"system-name", context.SystemName}, {"architecture", context.Architecture}, {"generator", context.Generator}, {"platform", context.Platform}, {"toolset", context.Toolset}, {"instance", context.Instance}, {"cmake-command", context.CMakeCommand}, {"git-executable", context.GitExecutable}} {
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
	for _, bad := range [][]string{nil, {"unknown"}, {"version", "extra"}, {"generate"}, {"generate", "--unknown=value"}, append(append([]string{}, args...), "--multi-config=bad"), append(append([]string{}, args...), "--cmake-default=not-a-pair")} {
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
