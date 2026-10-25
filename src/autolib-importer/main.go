package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

type packageFlags []string

func (p *packageFlags) String() string         { return fmt.Sprint([]string(*p)) }
func (p *packageFlags) Set(value string) error { *p = append(*p, value); return nil }

func run(args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	if len(args) == 0 || args[0] != "prepare" {
		return fail(fmt.Errorf("usage: autolib-importer prepare [--local-root=<directory>] [--remote-root=<Release URL>] --work-root=<directory> --os=<target OS> --arch=<target arch> --result=<file> --package=<name> ..."))
	}
	o := prepareOptions{}
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	for _, field := range []struct {
		key   string
		value *string
	}{{"local-root", &o.LocalRoot}, {"remote-root", &o.RemoteRoot}, {"github-response", &o.GitHubResponse}, {"work-root", &o.WorkRoot}, {"os", &o.OS}, {"arch", &o.Arch}, {"result", &o.ResultPath}} {
		flags.StringVar(field.value, field.key, "", field.key)
	}
	var packages packageFlags
	flags.Var(&packages, "package", "requested package (repeatable)")
	if err := flags.Parse(args[1:]); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		return fail(fmt.Errorf("prepare: unexpected positional argument %q", flags.Arg(0)))
	}
	var emptyOption string
	flags.Visit(func(f *flag.Flag) {
		if (f.Name == "local-root" || f.Name == "remote-root" || f.Name == "github-response") && f.Value.String() == "" {
			emptyOption = f.Name
		}
	})
	if emptyOption != "" {
		return fail(fmt.Errorf("prepare: --%s must not be empty; omit the option instead", emptyOption))
	}
	o.Packages = packages
	if _, err := prepare(o); err != nil {
		return fail(err)
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
