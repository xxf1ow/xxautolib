package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func run(args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	if len(args) < 1 || args[0] != "plan" {
		return fail(fmt.Errorf("usage: autolib-ci plan [options]"))
	}
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options ciPlanOptions
	var output string
	flags.StringVar(&options.SourceRoot, "source-root", "", "repository source root")
	flags.StringVar(&options.BuilderFile, "builder-file", "", "autolib-builder executable")
	flags.StringVar(&output, "output", "", "plan JSON output file")
	platforms := flags.String("platforms", "", "comma-separated target platforms")
	modules := flags.String("modules", "", "comma-separated module selection")
	flags.BoolVar(&options.Force, "force", false, "force rebuild of selected dependency closure")
	if err := flags.Parse(args[1:]); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		return fail(fmt.Errorf("plan: unexpected positional argument %q", flags.Arg(0)))
	}
	visited := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	for _, key := range []string{"source-root", "builder-file", "output"} {
		if !visited[key] || strings.TrimSpace(flags.Lookup(key).Value.String()) == "" {
			return fail(fmt.Errorf("plan: --%s is required and cannot be empty", key))
		}
	}
	if visited["platforms"] {
		options.Platforms = platforms
	}
	if visited["modules"] {
		options.Modules = modules
	}
	plan, err := planCI(context.Background(), options)
	if err != nil {
		return fail(err)
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(output, append(data, '\n'), 0644); err != nil {
		return fail(err)
	}
	fmt.Fprintln(stdout, output)
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
