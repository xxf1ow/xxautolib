package main

import (
	"bytes"
	"testing"
)

func prepareArguments(o prepareOptions) []string {
	args := []string{"prepare", "--work-root=" + o.WorkRoot, "--os=" + o.OS, "--arch=" + o.Arch, "--result=" + o.ResultPath}
	if o.LocalRoot != "" {
		args = append(args, "--local-root="+o.LocalRoot)
	}
	if o.SourceResponse != "" {
		args = append(args, "--source-response="+o.SourceResponse)
	}
	if o.RemoteRoot != "" {
		args = append(args, "--remote-root="+o.RemoteRoot)
	}
	for _, name := range o.Packages {
		args = append(args, "--package="+name)
	}
	return args
}

func TestImporterCLI(t *testing.T) {
	o := localReleaseFixture(t, "demo")
	args := prepareArguments(o)
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 0 || stdout.Len() != 0 {
		t.Fatalf("%d %s %s", code, stdout.String(), stderr.String())
	}
	for _, bad := range [][]string{nil, {"version"}, {"prepare"}, append(append([]string{}, args...), "extra"), append(append([]string{}, args...), "--unknown=x"), append(append([]string{}, args...), "--os=any"), append(append([]string{}, args...), "--package="), append(append([]string{}, args...), "--local-root="), append(append([]string{}, args...), "--remote-root="), append(append([]string{}, args...), "--source-response="), append(append([]string{}, args...), "--uri=old"), append(append([]string{}, args...), "--manifest=")} {
		stderr.Reset()
		if code := run(bad, &stdout, &stderr); code == 0 || stderr.Len() == 0 {
			t.Fatalf("accepted %q: %d %s", bad, code, stderr.String())
		}
	}
	for _, remoteOnly := range []bool{false, true} {
		f := newRemotePrepareFixture(t)
		o := f.options
		o.Packages = []string{"flann"}
		if remoteOnly {
			o.LocalRoot = ""
		}
		stderr.Reset()
		if code := run(prepareArguments(o), &stdout, &stderr); code != 0 {
			t.Fatalf("remote CLI %d: %s", code, stderr.String())
		}
	}
}
