package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type repeatedFlag []string

func (f *repeatedFlag) String() string         { return strings.Join(*f, ",") }
func (f *repeatedFlag) Set(value string) error { *f = append(*f, value); return nil }

func run(args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintln(stdout, "autolib-builder 1")
		return 0
	}
	if len(args) > 0 && args[0] == "package" {
		return runPackage(args[1:], stderr)
	}
	if len(args) == 0 || args[0] != "generate" {
		return fail(fmt.Errorf("usage: autolib-builder version | generate [options] | package [options]"))
	}
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	context := buildContext{CMakeDefaults: map[string]string{}}
	for _, field := range []struct {
		key   string
		value *string
	}{{"source-root", &context.SourceRoot}, {"build-root", &context.BuildRoot}, {"system-name", &context.SystemName}, {"architecture", &context.Architecture}, {"generator", &context.Generator}, {"platform", &context.Platform}, {"toolset", &context.Toolset}, {"instance", &context.Instance}, {"cmake-command", &context.CMakeCommand}, {"git-executable", &context.GitExecutable}} {
		flags.StringVar(field.value, field.key, "", field.key)
	}
	multiConfig := flags.String("multi-config", "", "ON or OFF")
	runtime := flags.String("msvc-runtime-library", "", "explicit CMake runtime selection")
	var modules, defaults repeatedFlag
	flags.Var(&modules, "module", "selected module (repeatable)")
	flags.Var(&defaults, "cmake-default", "KEY=VALUE (repeatable)")
	if err := flags.Parse(args[1:]); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		return fail(fmt.Errorf("generate: unexpected positional argument %q", flags.Arg(0)))
	}
	visited := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	for _, key := range []string{"source-root", "build-root", "system-name", "generator", "platform", "toolset", "instance", "multi-config", "cmake-command", "git-executable"} {
		if !visited[key] {
			return fail(fmt.Errorf("generate: missing required --%s", key))
		}
	}
	for _, key := range []string{"source-root", "build-root", "system-name", "generator", "cmake-command", "git-executable"} {
		if flags.Lookup(key).Value.String() == "" {
			return fail(fmt.Errorf("generate: --%s cannot be empty", key))
		}
	}
	if *multiConfig != "ON" && *multiConfig != "OFF" {
		return fail(fmt.Errorf("generate: --multi-config must be ON or OFF"))
	}
	context.MultiConfig = *multiConfig == "ON"
	if visited["msvc-runtime-library"] {
		context.MSVCRuntimeLibrary = runtime
	}
	for _, pair := range defaults {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || !cacheKey.MatchString(key) {
			return fail(fmt.Errorf("generate: invalid --cmake-default %q; expected KEY=VALUE", pair))
		}
		context.CMakeDefaults[key] = value
	}
	var err error
	if context.SourceRoot, err = filepath.Abs(context.SourceRoot); err != nil {
		return fail(err)
	}
	if context.BuildRoot, err = filepath.Abs(context.BuildRoot); err != nil {
		return fail(err)
	}
	if err := generate(context, modules); err != nil {
		return fail(fmt.Errorf("Module generation failed: %w", err))
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
