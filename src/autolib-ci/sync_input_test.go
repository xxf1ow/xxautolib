package main

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCISyncSource(t *testing.T) {
	builder := discoveryBuilder(t)
	root, _ := fingerprintFixture(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		dep := "package_platforms=['any-any']\n"
		if name == "a" || name == "c" {
			dep += "depends=['b@1']\n"
		}
		discoveryVersion(t, root, name, "1", true, dep)
	}
	discoveryVersion(t, root, "local", "1", false, "")
	discoveryVersion(t, root, "broken", "1", true, "")
	writeCIFixture(t, root, "src/dependencies/broken/1/module.toml", "broken=[")
	discoveryVersion(t, root, "dangling", "1", true, "")
	writeCIFixture(t, root, "src/dependencies/dangling/package.toml", "latest='deleted'\n")
	for _, args := range [][]string{{"init", "-b", "feature/cache"}, {"add", "."}, {"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "source"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	report, err := prepareCISyncSource(context.Background(), root, builder, "owner/repo", "refs/heads/feature/cache")
	if err != nil {
		t.Fatal(err)
	}
	if report.Schema != 1 || report.Ref != "refs/heads/feature/cache" || len(report.SHA) != 40 || len(report.Plan.Platforms) != 4 {
		t.Fatalf("source identity: %+v", report)
	}
	if len(report.Entry.Bytes) == 0 || report.Entry.SHA256 != ciSHA256(report.Entry.Bytes) {
		t.Fatal("fixed source entry missing")
	}
	if _, ok := report.Inputs.Fingerprints["d-1-any-any.tar.zst"]; !ok {
		t.Fatal("healthy cache identity lost")
	}
	if _, ok := report.Catalog.Packages["local"].Versions["1"]; !ok {
		t.Fatal("filtered identity lost")
	}
	if _, ok := report.Catalog.Packages["broken"].Versions["1"]; !ok {
		t.Fatal("broken identity lost")
	}
	var out, diag bytes.Buffer
	output := filepath.Join(root, "build/source.json")
	if code := run([]string{"sync-source", "--source-root", root, "--builder-file", builder, "--repository", "owner/repo", "--ref", "refs/heads/feature/cache", "--output", output}, &out, &diag); code != 0 {
		t.Fatalf("sync-source CLI: %s", &diag)
	}
	var decoded ciSyncSource
	if err := decodeStrictJSONFile(output, &decoded); err != nil || decoded.SHA != report.SHA {
		t.Fatalf("CLI source identity: %+v %v", decoded, err)
	}

	for _, ref := range []string{"refs/tags/v1", "refs/pull/1/head", "refs/heads/a..b", ""} {
		if _, err := prepareCISyncSource(context.Background(), root, builder, "owner/repo", ref); err == nil {
			t.Fatalf("accepted ref %q", ref)
		}
	}
	if _, err := prepareCISyncSource(context.Background(), root, builder, "../repo", "refs/heads/main"); err == nil {
		t.Fatal("invalid repository accepted")
	}
	if _, err := prepareCISyncSource(context.Background(), filepath.Join(root, "missing"), builder, "owner/repo", "refs/heads/main"); err == nil {
		t.Fatal("missing snapshot accepted")
	}
}
