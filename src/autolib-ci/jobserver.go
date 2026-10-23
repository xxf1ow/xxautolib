package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type parallelEvidence struct {
	Budget, Peak           int
	Overlap                bool
	MakeVersion, Jobserver string
	Warnings               []string
}

func findMakeProgram() (string, error) {
	if path, err := exec.LookPath("make"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("GNU Make executable was not found")
}

func probeJobserver(ctx context.Context, makePath, workRoot, sourceRoot string, budget int) (parallelEvidence, error) {
	if budget < 1 {
		return parallelEvidence{}, fmt.Errorf("jobserver budget must be at least 1")
	}
	if strings.TrimSpace(sourceRoot) == "" {
		return parallelEvidence{}, fmt.Errorf("source root is required for jobserver fixtures")
	}
	if err := os.MkdirAll(workRoot, 0755); err != nil {
		return parallelEvidence{}, err
	}
	fixture := filepath.Join(sourceRoot, "tests", "ci", "jobserver")
	for _, file := range []string{"Makefile", "child.mk"} {
		data, err := os.ReadFile(filepath.Join(fixture, file))
		if err != nil {
			return parallelEvidence{}, err
		}
		if err := os.WriteFile(filepath.Join(workRoot, file), data, 0644); err != nil {
			return parallelEvidence{}, err
		}
	}
	events := filepath.Join(workRoot, "events.log")
	cmd := exec.CommandContext(ctx, makePath, fmt.Sprintf("-j%d", budget), "--jobserver-style=fifo", "--no-print-directory", "-f", "Makefile", "LOG="+events)
	cmd.Dir = workRoot
	cmd.Env = prependPath(withoutEnvironment(os.Environ(), "MAKEFLAGS", "CMAKE_BUILD_PARALLEL_LEVEL"), filepath.Dir(makePath))
	output, err := cmd.CombinedOutput()
	versionOutput, versionErr := exec.CommandContext(ctx, makePath, "--version").Output()
	if versionErr != nil {
		return parallelEvidence{}, versionErr
	}
	evidence := parallelEvidence{Budget: budget, MakeVersion: strings.Split(strings.TrimSpace(string(versionOutput)), "\n")[0]}
	if evidence.MakeVersion != "GNU Make 4.4.1" {
		return evidence, fmt.Errorf("jobserver probe requires GNU Make 4.4.1, got %q", evidence.MakeVersion)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, "jobserver-auth=") {
			evidence.Jobserver = strings.TrimSpace(line)
		}
	}
	evidence.Warnings = detectedJobserverWarnings(string(output))
	if budget > 1 && !strings.Contains(evidence.Jobserver, "fifo:") {
		evidence.Warnings = append(evidence.Warnings, "recursive Make did not expose a FIFO jobserver in MAKEFLAGS")
	}
	data, readErr := os.ReadFile(events)
	if readErr != nil {
		return evidence, fmt.Errorf("read jobserver events: %w; make: %v; output: %s", readErr, err, output)
	}
	evidence.Peak, evidence.Overlap, readErr = jobserverPeak(string(data))
	if readErr != nil {
		return evidence, readErr
	}
	if err != nil {
		return evidence, fmt.Errorf("GNU Make jobserver probe failed: %w: %s", err, output)
	}
	if evidence.Peak > budget {
		evidence.Warnings = append(evidence.Warnings, fmt.Sprintf("observed worker peak %d exceeds budget %d", evidence.Peak, budget))
	}
	return evidence, nil
}

func withoutEnvironment(values []string, names ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		if !slices.Contains(names, key) {
			result = append(result, value)
		}
	}
	return result
}

func prependPath(values []string, directory string) []string {
	result := make([]string, 0, len(values)+1)
	separator := string(os.PathListSeparator)
	for _, value := range values {
		key, current, found := strings.Cut(value, "=")
		if key == "PATH" && found {
			result = append(result, "PATH="+directory+separator+current)
		} else {
			result = append(result, value)
		}
	}
	return result
}

func detectedJobserverWarnings(output string) []string {
	warnings := []string{}
	for _, line := range strings.Split(output, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "jobserver unavailable") || strings.Contains(lower, "resetting jobserver") {
			warnings = append(warnings, strings.TrimSpace(line))
		}
	}
	return warnings
}

func jobserverPeak(events string) (int, bool, error) {
	type event struct {
		at    int64
		delta int
	}
	eventsList := []event{}
	for _, line := range strings.Split(strings.TrimSpace(events), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || (fields[0] != "start" && fields[0] != "end") {
			return 0, false, fmt.Errorf("invalid jobserver event %q", line)
		}
		at, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, false, err
		}
		delta := 1
		if fields[0] == "end" {
			delta = -1
		}
		eventsList = append(eventsList, event{at: at, delta: delta})
	}
	slices.SortFunc(eventsList, func(a, b event) int {
		if a.at < b.at {
			return -1
		}
		if a.at > b.at {
			return 1
		}
		return a.delta - b.delta
	})
	active, peak := 0, 0
	for _, item := range eventsList {
		active += item.delta
		if active < 0 {
			return 0, false, fmt.Errorf("jobserver event ended before it started")
		}
		if active > peak {
			peak = active
		}
	}
	if active != 0 {
		return 0, false, fmt.Errorf("jobserver probe left %d active tasks", active)
	}
	return peak, peak > 1, nil
}
