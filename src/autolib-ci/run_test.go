package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCIConfigureKeepsSelectionOutOfLogNames(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native Unix Makefiles configure")
	}
	root := t.TempDir()
	writeCIFixture(t, root, "CMakeLists.txt", "cmake_minimum_required(VERSION 3.20)\nproject(log_names NONE)\nmessage(STATUS \"Selection: ${BUILD_LIST}\")\n")
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	makeProgram, err := exec.LookPath("make")
	if err != nil {
		t.Fatal(err)
	}
	o := ciRunOptions{SourceRoot: root, WorkRoot: filepath.Join(root, "work"), CMakeCommand: cmake, MakeProgram: makeProgram, Budget: 1, Layer: "prepare"}
	c := ciProductionContext{OS: "linux", Generator: "Unix Makefiles", Configuration: "Release"}
	modules := []string{}
	for i := 0; i < 40; i++ {
		modules = append(modules, fmt.Sprintf("selected-module-%02d@1.0.0", i))
	}
	if err := configureCIProduction(context.Background(), o, c, modules, filepath.Join(root, "install"), filepath.Join(o.WorkRoot, "logs", "prepare")); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(o.WorkRoot, "logs", "prepare", "configure.log")
	content := mustReadText(t, log)
	if !strings.Contains(content, "-DBUILD_LIST="+strings.Join(modules, ",")) {
		t.Fatal("configure log lost the exact module selection")
	}
}

func TestCIRunDoesNotRequireUnusedBuilder(t *testing.T) {
	f := transportInputFixture(t)
	work := filepath.Join(f.options.SourceRoot, "build", "ci", "123-1", "windows-x86_64")
	var stdout, stderr bytes.Buffer
	code := runCIRun([]string{"--repository", ciGitHubRepository, "--source-root", f.options.SourceRoot, "--plan", f.options.PlanFile,
		"--cache", "unused-cache.json", "--run-identity", f.options.RunIdentityFile, "--platform", "windows-x86_64",
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
			err := validateCIRunIdentity(context.Background(), root, ciGitHubRepository, identity)
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
