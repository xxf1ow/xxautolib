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

type ciJobserverOptions struct {
	SourceRoot, MakeProgram, WorkRoot string
	Budget                            int
}

type ciJobserverEvidence struct {
	Budget, Peak int
	Overlap      bool
	Warnings     []string
}

func probeJobserver(ctx context.Context, o ciJobserverOptions) (ciJobserverEvidence, error) {
	if o.Budget < 1 {
		return ciJobserverEvidence{}, fmt.Errorf("jobserver budget must be at least 1")
	}
	if strings.TrimSpace(o.SourceRoot) == "" {
		return ciJobserverEvidence{}, fmt.Errorf("source root is required for jobserver fixtures")
	}
	if strings.TrimSpace(o.MakeProgram) == "" {
		return ciJobserverEvidence{}, fmt.Errorf("Make program is required for jobserver probe")
	}
	if strings.TrimSpace(o.WorkRoot) == "" {
		return ciJobserverEvidence{}, fmt.Errorf("work root is required for jobserver probe")
	}
	makeProgram, err := filepath.Abs(o.MakeProgram)
	if err != nil {
		return ciJobserverEvidence{}, err
	}
	if err := os.MkdirAll(o.WorkRoot, 0755); err != nil {
		return ciJobserverEvidence{}, err
	}
	fixture := filepath.Join(o.SourceRoot, "tests", "ci", "jobserver")
	for _, file := range []string{"Makefile", "child.mk"} {
		data, err := os.ReadFile(filepath.Join(fixture, file))
		if err != nil {
			return ciJobserverEvidence{}, err
		}
		if err := os.WriteFile(filepath.Join(o.WorkRoot, file), data, 0644); err != nil {
			return ciJobserverEvidence{}, err
		}
	}
	events := filepath.Join(o.WorkRoot, "events.log")
	if err := os.WriteFile(events, nil, 0644); err != nil {
		return ciJobserverEvidence{}, fmt.Errorf("clear jobserver events: %w", err)
	}
	cmd := exec.CommandContext(ctx, makeProgram, fmt.Sprintf("-j%d", o.Budget), "--jobserver-style=fifo", "--no-print-directory", "-f", "Makefile", "LOG="+events)
	cmd.Dir = o.WorkRoot
	cmd.Env = prependPath(withoutEnvironment(os.Environ(), "MAKEFLAGS", "CMAKE_BUILD_PARALLEL_LEVEL"), filepath.Dir(makeProgram))
	output, commandErr := cmd.CombinedOutput()
	warnings, warningErr := validateJobserverWarnings(string(output), o.Budget)
	evidence := ciJobserverEvidence{Budget: o.Budget, Warnings: warnings}
	data, readErr := os.ReadFile(events)
	if readErr != nil {
		return evidence, fmt.Errorf("read jobserver events: %w; make: %v; output: %s", readErr, commandErr, output)
	}
	evidence.Peak, evidence.Overlap, readErr = jobserverPeak(string(data))
	if readErr != nil {
		return evidence, readErr
	}
	if commandErr != nil {
		return evidence, fmt.Errorf("GNU Make jobserver probe failed: %w: %s", commandErr, output)
	}
	versionOutput, err := exec.CommandContext(ctx, makeProgram, "--version").Output()
	if err != nil {
		return evidence, fmt.Errorf("read GNU Make version: %w", err)
	}
	if version := strings.Split(strings.TrimSpace(string(versionOutput)), "\n")[0]; version != "GNU Make 4.4.1" {
		return evidence, fmt.Errorf("jobserver probe requires GNU Make 4.4.1, got %q", version)
	}
	if evidence.Peak > o.Budget {
		evidence.Warnings = append(evidence.Warnings, fmt.Sprintf("observed worker peak %d exceeds budget %d", evidence.Peak, o.Budget))
	}
	if o.Budget > 1 && !evidence.Overlap {
		evidence.Warnings = append(evidence.Warnings, "FIFO jobserver did not produce overlapping workers")
	}
	if len(evidence.Warnings) != 0 {
		if warningErr != nil {
			return evidence, warningErr
		}
		return evidence, fmt.Errorf("GNU Make jobserver probe warnings: %s", strings.Join(evidence.Warnings, "; "))
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
			continue
		}
		if strings.Contains(line, "MAKEFLAGS=") && strings.Contains(line, "--jobserver-auth=") && !strings.Contains(line, "--jobserver-auth=fifo:") {
			warnings = append(warnings, "recursive Make is using a non-FIFO jobserver: "+strings.TrimSpace(line))
		}
	}
	return warnings
}

func validateJobserverWarnings(output string, budget int) ([]string, error) {
	warnings := detectedJobserverWarnings(output)
	if budget > 1 && !hasFIFOJobserver(output) {
		warnings = append(warnings, "recursive Make did not expose a FIFO jobserver in MAKEFLAGS")
	}
	if len(warnings) != 0 {
		return warnings, fmt.Errorf("GNU Make jobserver probe warnings: %s", strings.Join(warnings, "; "))
	}
	return warnings, nil
}

func hasFIFOJobserver(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "MAKEFLAGS=") && strings.Contains(line, "--jobserver-auth=fifo:") {
			return true
		}
	}
	return false
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
