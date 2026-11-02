package main

import (
	"context"
	"strings"
	"testing"
)

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
	f.options.BuilderFile = "unused"
	f.options.ImporterFile = "unused"
	f.options.CMakeCommand = "unused"
	if err := runCILayer(context.Background(), f.options); err == nil {
		t.Fatal("unselected platform launched work")
	}
}
