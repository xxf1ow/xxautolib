package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFIFOJobserver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GNU Make FIFO jobserver execution requires Linux")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tests", "ci", "jobserver", "Makefile")); err != nil {
		t.Fatal(err)
	}
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{1, 2} {
		evidence, err := probeJobserver(t.Context(), ciJobserverOptions{SourceRoot: root, MakeProgram: makePath, WorkRoot: t.TempDir(), Budget: budget})
		if err != nil {
			t.Fatal(err)
		}
		if evidence.Budget != budget || evidence.Peak > budget {
			t.Fatalf("budget exceeded: %+v", evidence)
		}
		if len(evidence.Warnings) != 0 {
			t.Fatalf("jobserver was not diagnosed as healthy: %+v", evidence)
		}
		if budget == 2 && !evidence.Overlap {
			t.Fatalf("two workers did not overlap: %+v", evidence)
		}
	}
}

func TestFIFOJobserverWarnings(t *testing.T) {
	for _, tc := range []struct{ name, output string }{
		{name: "child independent jobs", output: "make[2]: warning: -j2 forced in submake: resetting jobserver mode."},
		{name: "single-thread fallback", output: "make[1]: warning: jobserver unavailable: using -j1. Add '+' to parent make rule."},
		{name: "lost FIFO", output: "MAKEFLAGS= -j2 --jobserver-auth=pipe:3,4 --jobserver-style=fifo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warnings, err := validateJobserverWarnings(tc.output, 2)
			if err == nil || len(warnings) == 0 {
				t.Fatalf("jobserver warning did not fail the probe: warnings=%v err=%v", warnings, err)
			}
		})
	}
	if warnings, err := validateJobserverWarnings("MAKEFLAGS= -j2 --jobserver-auth=fifo:3,4 --jobserver-style=fifo", 2); err != nil || len(warnings) != 0 {
		t.Fatalf("valid FIFO did not pass: warnings=%v err=%v", warnings, err)
	}
}
