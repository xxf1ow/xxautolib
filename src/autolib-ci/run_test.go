package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIRunDoesNotRequireUnusedBuilder(t *testing.T) {
	f := transportInputFixture(t)
	work := filepath.Join(f.options.SourceRoot, "build", "ci", "123-1", "windows-x86_64")
	var stdout, stderr bytes.Buffer
	code := runCIRun([]string{"--source-root", f.options.SourceRoot, "--plan", f.options.PlanFile,
		"--run-identity", f.options.RunIdentityFile, "--platform", "windows-x86_64",
		"--layer", "prepare", "--work-root", work,
		"--importer-file", "unused", "--cmake-command", "unused"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "selected") || strings.Contains(stderr.String(), "builder") {
		t.Fatalf("run did not reach the platform selection check without an extra builder: code=%d stderr=%s", code, &stderr)
	}
}

func TestCIRunIdentity(t *testing.T) {
	root := t.TempDir()
	valid := transportIdentity(t, root)
	for _, change := range []string{"valid", "topic", "repository", "tag", "pull", "empty-branch", "invalid-branch", "sha", "checkout", "run", "attempt"} {
		t.Run(change, func(t *testing.T) {
			identity := valid
			switch change {
			case "topic":
				identity.Ref = "refs/heads/feat/delivery"
			case "repository":
				identity.Repository = "fork/project"
			case "tag":
				identity.Ref = "refs/tags/v1"
			case "pull":
				identity.Ref = "refs/pull/1/head"
			case "empty-branch":
				identity.Ref = "refs/heads/"
			case "invalid-branch":
				identity.Ref = "refs/heads/bad..branch"
			case "sha":
				identity.SHA = strings.Repeat("a", 40)
				identity.CheckoutSHA = identity.SHA
			case "checkout":
				identity.CheckoutSHA = strings.Repeat("b", 40)
			case "run":
				identity.RunID = "0"
			case "attempt":
				identity.Attempt = "01"
			}
			err := validateCIRunIdentity(context.Background(), root, identity)
			if (err == nil) != (change == "valid" || change == "topic") {
				t.Fatalf("%s error=%v", change, err)
			}
		})
	}
}

func TestCIRunRejectsUnselectedPlatform(t *testing.T) {
	f := transportInputFixture(t)
	f.options.Platform = "windows-x86_64"
	f.options.Layer = "prepare"
	f.options.ImporterFile = "unused"
	f.options.CMakeCommand = "unused"
	if err := runCILayer(context.Background(), f.options); err == nil {
		t.Fatal("unselected platform launched work")
	}
}
