package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func testDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func prepareArguments(o prepareOptions) []string {
	args := []string{"prepare", "--uri=" + o.URI, "--work-root=" + o.WorkRoot, "--os=" + o.OS, "--arch=" + o.Arch, "--result=" + o.ResultPath}
	if o.ManifestPath != "" {
		args = append(args, "--manifest="+o.ManifestPath)
	}
	for _, name := range o.Packages {
		args = append(args, "--package="+name)
	}
	return args
}

func TestImporterCLI(t *testing.T) {
	o, _ := localReleaseFixture(t, "demo")
	args := prepareArguments(o)
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 0 || stdout.Len() != 0 {
		t.Fatalf("%d %s %s", code, stdout.String(), stderr.String())
	}
	for _, bad := range [][]string{nil, {"version"}, {"prepare"}, append(append([]string{}, args...), "extra"), append(append([]string{}, args...), "--unknown=x"), append(append([]string{}, args...), "--os=any"), append(append([]string{}, args...), "--package="), append(append([]string{}, args...), "--uri="), append(append([]string{}, args...), "--manifest=")} {
		stderr.Reset()
		if code := run(bad, &stdout, &stderr); code == 0 || stderr.Len() == 0 {
			t.Fatalf("accepted %q: %d %s", bad, code, stderr.String())
		}
	}
}
