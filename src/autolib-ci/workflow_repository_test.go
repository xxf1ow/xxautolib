package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIWorkflowTrustedRepository(t *testing.T) {
	document := strings.ReplaceAll(mustReadText(t, "../../.github/workflows/build-platform.yml"), "\r\n", "\n")
	for _, job := range []string{"plan", "linux", "windows", "darwin", "finalize"} {
		block := workflowSection(t, document, "  "+job+":")
		calls := 0
		for _, line := range strings.Split(block, "\n") {
			if !strings.Contains(line, "--run-identity") {
				continue
			}
			calls++
			script := strings.TrimSpace(line)
			script = strings.TrimPrefix(script, "run: ")
			commandName := "bash"
			var args []string
			if job == "windows" {
				pwsh, err := exec.LookPath("pwsh")
				if err != nil {
					t.Log("PowerShell unavailable; Windows invocation requires native runner")
					continue
				}
				commandName = pwsh
				script = strings.ReplaceAll(script, `& "$env:WORK_ROOT/autolib-ci.exe"`, "Invoke-TrustedCI")
				stub := `function Invoke-TrustedCI { $position=[Array]::IndexOf($args,'--repository'); if ($position -lt 0 -or $args[$position+1] -ne 'trusted-fork/source') { throw 'trusted repository was not passed' } }; `
				args = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", stub + script}
			} else {
				script = strings.ReplaceAll(script, `"$WORK_ROOT/autolib-ci"`, "invoke_trusted_ci")
				script = strings.ReplaceAll(script, "build/tools/autolib-ci", "invoke_trusted_ci")
				stub := "invoke_trusted_ci() { local found=0; while (($#)); do if [[ $1 == --repository ]]; then [[ ${2-} == trusted-fork/source ]] || return 17; found=1; fi; shift; done; ((found == 1)); }; "
				args = []string{"-e", "-c", stub + script}
			}
			command := exec.Command(commandName, args...)
			command.Dir = filepath.FromSlash(t.TempDir())
			command.Env = append(withoutEnvironment(os.Environ(), "GITHUB_REPOSITORY", "WORK_ROOT", "PLATFORM"), "GITHUB_REPOSITORY=trusted-fork/source", "WORK_ROOT=unused", "PLATFORM=linux-x86_64")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s invocation omitted trusted repository: %v\n%s", job, err, output)
			}
		}
		want := 3
		if job == "finalize" || job == "plan" {
			want = 1
		}
		if calls != want {
			t.Fatalf("%s checked %d CI calls, want %d", job, calls, want)
		}
	}
}

func TestCISyncWorkflowBranch(t *testing.T) {
	document := mustReadText(t, "../../.github/workflows/publish-artifacts.yml")
	for _, text := range []string{"sync-source", "--repository \"$GITHUB_REPOSITORY\"", "--ref \"$SYNC_REF\"", "ref: ${{ steps.snapshot.outputs.sha }}", "persist-credentials: false"} {
		if !strings.Contains(document, text) {
			t.Fatal("missing fixed source binding", text)
		}
	}
	for _, text := range []string{"source_run_id", "source_attempt", "actions/download-artifact", " transport "} {
		if strings.Contains(document, text) {
			t.Fatal("retired sync input", text)
		}
	}
}
