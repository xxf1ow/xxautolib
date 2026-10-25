package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These helpers select indentation-delimited sections, not a general YAML parser.
func workflowSection(t *testing.T, document, heading string) string {
	t.Helper()
	lines := strings.Split(document, "\n")
	for i, line := range lines {
		if line != heading {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		end := i + 1
		for ; end < len(lines); end++ {
			next := lines[end]
			if strings.TrimSpace(next) != "" && len(next)-len(strings.TrimLeft(next, " ")) <= indent {
				break
			}
		}
		return strings.Join(lines[i+1:end], "\n")
	}
	t.Fatalf("missing workflow section %q", heading)
	return ""
}

func workflowField(t *testing.T, block, key string) string {
	t.Helper()
	for _, line := range strings.Split(block, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), key+": "); ok {
			return value
		}
	}
	t.Fatalf("missing workflow field %s", key)
	return ""
}

func TestCIWorkflowContract(t *testing.T) {
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/build-platform.yml"))
	if err != nil {
		t.Fatal(err)
	}
	document := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, text := range []string{"  workflow_dispatch:", "  contents: read", "  group: ${{ github.repository }}-autolib-release", "  cancel-in-progress: false"} {
		if !strings.Contains(document, text) {
			t.Fatalf("missing workflow contract %q", text)
		}
	}
	if strings.Contains(document, "permissions: ${{") || strings.Contains(document, "${{ inputs.") && strings.Contains(document, "--modules ${{") {
		t.Fatal("dynamic permissions or shell-interpolated input")
	}
	trigger := workflowSection(t, document, "on:")
	for _, line := range strings.Split(trigger, "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.TrimSpace(line) != "workflow_dispatch:" && strings.TrimSpace(line) != "" {
			t.Fatalf("unexpected trigger %s", line)
		}
	}
	jobs := workflowSection(t, document, "jobs:")
	if strings.Count(jobs, "\n  ")-strings.Count(jobs, "\n   ") != 4 {
		t.Fatal("expected only plan, initialize, linux, windows, finalize jobs")
	}
	for _, id := range []string{"plan", "initialize", "linux", "windows", "finalize"} {
		job := workflowSection(t, document, "  "+id+":")
		minutes := "15"
		if id == "linux" || id == "windows" {
			minutes = "120"
		}
		if workflowField(t, job, "timeout-minutes") != minutes {
			t.Fatalf("wrong timeout for %s", id)
		}
		if !strings.Contains(job, "ref: ${{ github.sha }}") || !strings.Contains(job, "persist-credentials: false") {
			t.Fatalf("%s checkout is not event-bound", id)
		}
		write := id == "initialize" || id == "linux" || id == "windows"
		if strings.Contains(job, "contents: write") != write {
			t.Fatalf("unexpected write permissions on %s", id)
		}
	}
	plan := workflowSection(t, document, "  plan:")
	guard := workflowSection(t, plan, "      - name: Require default branch")
	script := workflowSection(t, guard, "        run: |")
	var commands []string
	for _, line := range strings.Split(script, "\n") {
		commands = append(commands, strings.TrimPrefix(line, "          "))
	}
	bash := "bash"
	if runtime.GOOS == "windows" {
		bash = filepath.Join(os.Getenv("ProgramFiles"), "Git/bin/bash.exe")
	}
	for _, tc := range []struct {
		ref string
		ok  bool
	}{{"refs/heads/master", true}, {"refs/heads/topic", false}, {"refs/tags/master", false}} {
		command := exec.Command(bash, "-c", strings.Join(commands, "\n"))
		command.Env = append(withoutEnvironment(os.Environ(), "GITHUB_REF", "DEFAULT_BRANCH"), "GITHUB_REF="+tc.ref, "DEFAULT_BRANCH=master")
		output, err := command.CombinedOutput()
		if (err == nil) != tc.ok {
			t.Fatalf("branch %s: %v %s", tc.ref, err, output)
		}
	}
	if strings.Index(plan, "name: Require default branch") > strings.Index(plan, "uses: actions/checkout@") {
		t.Fatal("branch check runs after checkout/tools")
	}
	linux := workflowSection(t, document, "  linux:")
	preparation := workflowSection(t, linux, "      - name: Prepare native environment and tools")
	if !strings.Contains(preparation, "bash .github/scripts/prepare-linux.sh --config") {
		t.Fatal("Linux preparation must invoke the non-executable script with bash")
	}
	if !strings.Contains(linux, "fail-fast: false") || !strings.Contains(linux, "image: ${{ needs.plan.outputs.linux_image }}") {
		t.Fatal("Linux matrix/container contract missing")
	}
	t.Run("Linux diagnostics paths find every evidence file from the checkout", func(t *testing.T) {
		checkout := t.TempDir()
		hostWorkRoot := filepath.ToSlash(filepath.Join(t.TempDir(), "unmounted-host-work"))
		prefix := "build/ci/42-1/linux-x86_64/"
		expected := []string{"logs/build-lz4.log", "evidence/lz4/consumer.json", "evidence/lz4/ctest.log", "evidence/lz4/abi.txt", "jobserver.json"}
		for _, name := range expected {
			file := filepath.Join(checkout, filepath.FromSlash(prefix+name))
			if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("evidence"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		step := workflowSection(t, linux, "      - name: Retain diagnostics")
		paths := workflowSection(t, step, "          path: |")
		paths = strings.NewReplacer("${{ env.WORK_ROOT }}", hostWorkRoot, "${{ github.run_id }}", "42", "${{ github.run_attempt }}", "1", "${{ matrix.platform }}", "linux-x86_64").Replace(paths)
		found := map[string]bool{}
		for _, line := range strings.Split(paths, "\n") {
			pattern := filepath.FromSlash(strings.TrimSpace(line))
			if pattern == "" {
				continue
			}
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(checkout, pattern)
			}
			matches, err := filepath.Glob(pattern)
			if err != nil {
				t.Fatal(err)
			}
			for _, match := range matches {
				err := filepath.WalkDir(match, func(file string, entry os.DirEntry, err error) error {
					if err == nil && !entry.IsDir() {
						found[file] = true
					}
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, name := range expected {
			if !found[filepath.Join(checkout, filepath.FromSlash(prefix+name))] {
				t.Errorf("Linux diagnostics omit %s from the container checkout", name)
			}
		}
	})
	for _, pair := range []string{"linux-x86_64", "linux-aarch64", "ubuntu-24.04", "ubuntu-24.04-arm", ".ubuntu.image"} {
		if !strings.Contains(plan, pair) {
			t.Fatalf("missing planned native runner/image %s", pair)
		}
	}
	for _, id := range []string{"linux", "windows"} {
		job := workflowSection(t, document, "  "+id+":")
		for _, layer := range []string{"prepare", "layer0", "layer1", "verify"} {
			if !strings.Contains(job, "--layer "+layer) {
				t.Fatalf("%s missing %s", id, layer)
			}
		}
		if !strings.Contains(job, "if: ${{ always() && !cancelled() }}") || !strings.Contains(job, "if-no-files-found: warn") {
			t.Fatal("partial results are not retained")
		}
		if strings.Contains(job, "path: build/ci/**") {
			t.Fatal("whole build or package tree uploaded as Actions artifact")
		}
	}
	finalize := workflowSection(t, document, "  finalize:")
	condition := workflowField(t, finalize, "if")
	if workflowField(t, finalize, "needs") != "[plan, initialize, linux, windows]" {
		t.Fatal("finalize does not wait for all jobs")
	}
	// The shipped condition uses only conjunction and the plan result comparison.
	// Exercise failure cases without implementing GitHub's workflow engine.
	for _, tc := range []struct {
		plan, initialize, platform string
		canceled, want             bool
	}{
		{"success", "success", "success", false, true},
		{"failure", "skipped", "skipped", false, false},
		{"success", "failure", "skipped", false, true},
		{"success", "success", "failure", false, true},
		{"success", "success", "success", true, false},
	} {
		value := strings.TrimSuffix(strings.TrimPrefix(condition, "${{ "), " }}")
		value = strings.ReplaceAll(value, "!cancelled()", map[bool]string{true: "false", false: "true"}[tc.canceled])
		value = strings.ReplaceAll(value, "needs.plan.result == 'success'", map[bool]string{true: "true", false: "false"}[tc.plan == "success"])
		got := true
		for _, term := range strings.Split(value, " && ") {
			if term != "true" && term != "false" {
				t.Fatalf("unsupported summary condition %q", condition)
			}
			got = got && term == "true"
		}
		if got != tc.want {
			t.Fatalf("summary skipped/misreported after plan=%s init=%s platform=%s cancel=%t", tc.plan, tc.initialize, tc.platform, tc.canceled)
		}
	}
	if strings.Contains(finalize, "GITHUB_TOKEN:") {
		t.Fatal("read-only summary received a publication token")
	}
	for _, platform := range []string{"linux", "windows"} {
		file := filepath.Join(root, ".github/scripts/prepare-"+platform+map[string]string{"linux": ".sh", "windows": ".ps1"}[platform])
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"autolib-builder", "autolib-importer", "autolib-ci"} {
			if strings.Count(string(data), "./src/"+target) != 1 {
				t.Fatalf("%s must build %s exactly once", file, target)
			}
		}
	}
	t.Run("Windows skips an absent dependency layer", func(t *testing.T) {
		pwsh, err := exec.LookPath("pwsh")
		if err != nil {
			t.Skip("PowerShell is unavailable")
		}
		job := workflowSection(t, document, "  windows:")
		step := workflowSection(t, job, "      - name: Prepare native environment and tools")
		script := workflowSection(t, step, "        run: |")
		_, script, ok := strings.Cut(script, "$plan = ")
		if !ok {
			t.Fatal("missing Windows layer selection")
		}
		script = "$plan = " + script
		for _, tc := range []struct{ layers, want string }{
			{`[["lz4"]]`, "layer0=true\nlayer1=false"},
			{`[["lz4","queue"],["flann"]]`, "layer0=true\nlayer1=true"},
		} {
			work := t.TempDir()
			input := filepath.Join(work, "build/ci-input")
			if err := os.MkdirAll(input, 0755); err != nil {
				t.Fatal(err)
			}
			plan := `{"platforms":[{"id":"windows-x86_64","layers":` + tc.layers + `}]}`
			if err := os.WriteFile(filepath.Join(input, "plan.json"), []byte(plan), 0644); err != nil {
				t.Fatal(err)
			}
			outputFile := filepath.Join(work, "output.txt")
			command := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
			command.Dir = work
			command.Env = append(withoutEnvironment(os.Environ(), "GITHUB_OUTPUT"), "GITHUB_OUTPUT="+outputFile)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("Windows layer selection: %v %s", err, output)
			}
			data, err := os.ReadFile(outputFile)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n")); got != tc.want {
				t.Fatalf("layers %s: got %q want %q", tc.layers, got, tc.want)
			}
		}
	})
	t.Run("Make configures in its build directory", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(root, ".github/scripts/prepare-linux.sh"))
		if err != nil {
			t.Fatal(err)
		}
		_, setup, ok := strings.Cut(string(data), "make_source=\"$tools/src/make-$make_version\"")
		if !ok {
			t.Fatal("missing Make preparation")
		}
		setup, _, ok = strings.Cut(setup, "actual_gcc_version=")
		if !ok {
			t.Fatal("missing Make preparation end")
		}
		tools := t.TempDir()
		source := filepath.Join(tools, "src/make-fixture")
		if err := os.MkdirAll(source, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "configure"), []byte("#!/usr/bin/env bash\nprintf configured > Makefile\n"), 0755); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(bash, "-e", "-c", "make() { test -f \"$2/Makefile\"; }\n"+setup)
		command.Dir = t.TempDir()
		command.Env = append(os.Environ(), "make_source="+filepath.ToSlash(source), "tools="+filepath.ToSlash(tools))
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("Make used an unconfigured source directory: %v %s", err, output)
		}
	})
	t.Run("Linux job supplies UTF-8 to child processes", func(t *testing.T) {
		command := exec.Command(bash, "-e", "-c", "locale charmap")
		command.Env = append(withoutEnvironment(os.Environ(), "LC_ALL", "LANG", "LC_CTYPE"), "LC_ALL=C", "LANG=C", "LC_CTYPE=C")
		for _, line := range strings.Split(workflowSection(t, linux, "    env:"), "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), ": ")
			if ok && (key == "LANG" || strings.HasPrefix(key, "LC_")) {
				command.Env = append(command.Env, key+"="+value)
			}
		}
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("Linux job locale: %v %s", err, output)
		}
		if got := strings.TrimSpace(string(output)); got != "UTF-8" {
			t.Fatalf("child process character map = %q, want UTF-8", got)
		}
	})
	t.Run("Linux preparation trusts only its checkout for Go VCS", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(root, ".github/scripts/prepare-linux.sh"))
		if err != nil {
			t.Fatal(err)
		}
		_, setup, ok := strings.Cut(string(data), "export PATH=")
		if !ok {
			t.Fatal("missing Linux tool environment")
		}
		setup, _, ok = strings.Cut("export PATH="+setup, `"$cmake_command" -P`)
		if !ok {
			t.Fatal("missing first Go build")
		}
		checkout := filepath.ToSlash(t.TempDir())
		config := filepath.ToSlash(filepath.Join(t.TempDir(), "gitconfig"))
		environment := append(withoutEnvironment(os.Environ(), "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "GIT_TEST_ASSUME_DIFFERENT_OWNER"), "GIT_CONFIG_GLOBAL="+config, "GIT_CONFIG_NOSYSTEM=1")
		command := exec.Command("git", "init", "--quiet", checkout)
		command.Env = environment
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("initialize checkout: %v %s", err, output)
		}
		command = exec.Command(bash, "-e", "-c", setup+"\ngit -C \"$repo_root\" status --porcelain\ngit config --global --get-all safe.directory")
		command.Env = append(environment, "GIT_TEST_ASSUME_DIFFERENT_OWNER=1", "repo_root="+checkout, "tools="+checkout, "gcc_major=9")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("Git VCS query after Linux preparation: %v %s", err, output)
		}
		if got := strings.TrimSpace(string(output)); got != checkout {
			t.Fatalf("trusted directories = %q, want only %q", got, checkout)
		}
	})
}
