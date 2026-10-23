package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	makePath, err := findMakeProgram()
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{1, 2} {
		evidence, err := probeJobserver(t.Context(), makePath, t.TempDir(), root, budget)
		if err != nil {
			t.Fatal(err)
		}
		if evidence.Budget != budget || evidence.Peak > budget {
			t.Fatalf("budget exceeded: %+v", evidence)
		}
		if evidence.MakeVersion != "GNU Make 4.4.1" || (budget > 1 && !strings.Contains(evidence.Jobserver, "fifo:")) || len(evidence.Warnings) != 0 {
			t.Fatalf("jobserver was not diagnosed as healthy: %+v", evidence)
		}
		if budget == 2 && !evidence.Overlap {
			t.Fatalf("two workers did not overlap: %+v", evidence)
		}
	}
}

func TestFIFOJobserverWarnings(t *testing.T) {
	for _, output := range []string{
		"make[2]: warning: -j2 forced in submake: resetting jobserver mode.",
		"make[1]: warning: jobserver unavailable: using -j1. Add '+' to parent make rule.",
	} {
		if len(detectedJobserverWarnings(output)) == 0 {
			t.Fatalf("jobserver warning was not diagnosed: %q", output)
		}
	}
}
