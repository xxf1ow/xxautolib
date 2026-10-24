package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("AUTOLIB_CI_JOBSERVER_FAKE_MAKE") == "1" {
		for _, arg := range os.Args[1:] {
			if strings.HasPrefix(arg, "LOG=") {
				file, err := os.OpenFile(strings.TrimPrefix(arg, "LOG="), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				_, err = file.WriteString("start 100\nend 200\n")
				if closeErr := file.Close(); err == nil {
					err = closeErr
				}
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				os.Exit(0)
			}
		}
		fmt.Fprintln(os.Stderr, "fake Make invocation did not include LOG")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

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

func TestFIFOJobserverClearsEventsWhenWorkRootIsReused(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	workRoot := t.TempDir()
	staleEvents := "start 1\nstart 2\nend 3\nend 4\n"
	if err := os.WriteFile(filepath.Join(workRoot, "events.log"), []byte(staleEvents), 0644); err != nil {
		t.Fatal(err)
	}
	makePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTOLIB_CI_JOBSERVER_FAKE_MAKE", "1")

	evidence, err := probeJobserver(t.Context(), ciJobserverOptions{SourceRoot: root, MakeProgram: makePath, WorkRoot: workRoot, Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Peak != 1 || evidence.Overlap {
		t.Fatalf("probe included events from a previous run: %+v", evidence)
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
