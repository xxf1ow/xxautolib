package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func finalizeFixture() (ciPlan, ciRunIdentity, []ciPlatformResult) {
	identity := ciRunIdentity{Repository: ciGitHubRepository, Ref: "refs/heads/master", SHA: strings.Repeat("a", 40), CheckoutSHA: strings.Repeat("a", 40), RunID: "123", Attempt: "1"}
	plan := ciPlan{Publisher: "linux-x86_64", RequestedModules: []string{"queue@1"}}
	results := []ciPlatformResult{}
	for _, platform := range []string{"linux-x86_64", "windows-x86_64", "linux-aarch64"} {
		plan.Platforms = append(plan.Platforms, ciPlatformPlan{ID: platform, Roots: []string{"queue@1"}, Description: moduleDescription{Modules: []describedModule{{Name: "queue", Version: "1", Target: "fixture-queue", PackagePlatform: "any-any"}}}})
		results = append(results, ciPlatformResult{Run: identity, Platform: platform, Complete: true, Importer: ciImporterResult{File: ciImporterExecutable(platform), Verified: true}, Entry: ciEntryResult{File: "autolib.cmake", SHA256: strings.Repeat("c", 64), Verified: true}, Packages: []ciSlotResult{{Slot: ciSlot{Name: "queue", Version: "1", OS: "any", Arch: "any"}, Built: true, Verified: true}}, Roots: []ciRootResult{{Root: "queue@1", Verified: true}}})
	}
	return plan, identity, results
}

func TestCIFinalize(t *testing.T) {
	plan, run, results := finalizeFixture()
	if err := finalizeCI(plan, ciGitHubRepository, run, results); err != nil {
		t.Fatal(err)
	}
	if err := finalizeCI(plan, ciGitHubRepository, run, nil); err == nil {
		t.Fatal("missing results accepted")
	}
	if err := finalizeCI(plan, ciGitHubRepository, run, append(results, results[0])); err == nil {
		t.Fatal("duplicate results accepted")
	}
	results[0].Packages[0].Built = false
	if err := finalizeCI(plan, ciGitHubRepository, run, results); err == nil {
		t.Fatal("package without current build accepted")
	}
}

func TestCIResultBinding(t *testing.T) {
	for _, change := range []string{"run", "attempt", "sha", "platform", "unverified", "incomplete", "error", "duplicate-package", "missing-package", "wrong-slot", "wrong-version", "archive-importer", "importer", "duplicate-root", "missing-root", "wrong-root", "entry-unverified", "entry-file", "entry-digest", "entry-platform-digest", "repository"} {
		t.Run(change, func(t *testing.T) {
			plan, run, results := finalizeFixture()
			switch change {
			case "run":
				results[0].Run.RunID = "old"
			case "attempt":
				results[0].Run.Attempt = "0"
			case "sha":
				results[0].Run.SHA = strings.Repeat("d", 40)
			case "platform":
				results[0].Platform = "linux-aarch64"
			case "unverified":
				results[0].Packages[0].Verified = false
			case "incomplete":
				results[0].Complete = false
			case "error":
				results[0].Error = "failed later"
			case "duplicate-package":
				results[0].Packages = append(results[0].Packages, results[0].Packages[0])
			case "missing-package":
				results[0].Packages = nil
			case "wrong-slot":
				results[0].Packages[0].Slot.OS = "windows"
			case "wrong-version":
				results[0].Packages[0].Slot.Version = "2"
			case "importer":
				results[0].Importer.Verified = false
			case "archive-importer":
				results[0].Importer.File = "autolib-importer-linux-x86_64.tar.xz"
			case "duplicate-root":
				results[0].Roots = append(results[0].Roots, results[0].Roots[0])
			case "missing-root":
				results[0].Roots = nil
			case "wrong-root":
				results[0].Roots[0].Root = "queue@2"
			case "entry-unverified":
				results[0].Entry.Verified = false
			case "entry-file":
				results[0].Entry.File = "other.cmake"
			case "entry-digest":
				results[0].Entry.SHA256 = "broken"
			case "entry-platform-digest":
				results[0].Entry.SHA256 = strings.Repeat("d", 64)
			case "repository":
				run.Repository = "self-reported/repository"
			}
			if err := finalizeCI(plan, ciGitHubRepository, run, results); err == nil {
				t.Fatalf("accepted %s", change)
			}
		})
	}
}

func TestCIRunCLI(t *testing.T) {
	for _, command := range []string{"run", "transport", "finalize"} {
		var out, diagnostic bytes.Buffer
		if code := run([]string{command}, &out, &diagnostic); code == 0 || !strings.Contains(diagnostic.String(), "--plan") {
			t.Fatalf("missing %s inputs accepted: %d %s", command, code, diagnostic.String())
		}
	}
	plan, identity, results := finalizeFixture()
	plan.Platforms[0].RequestedRoots = []string{"queue@1", "local@1"}
	plan.Platforms[0].FilteredRoots = []string{"local@1"}
	plan.Platforms[0].Description.FilteredRoots = []filteredRoot{{Root: "local@1", Code: "missing_adapter", Chain: []string{"local@1"}}}
	root := t.TempDir()
	for name, value := range map[string]any{"plan.json": plan, "run.json": identity, "results/linux.json": results[0], "results/windows.json": results[1], "results/arm.json": results[2]} {
		file := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(file), 0755)
		data, _ := json.Marshal(value)
		if err := os.WriteFile(file, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	var out, diagnostic bytes.Buffer
	if code := run([]string{"finalize", "--repository", ciGitHubRepository, "--plan", filepath.Join(root, "plan.json"), "--run-identity", filepath.Join(root, "run.json"), "--results", filepath.Join(root, "results")}, &out, &diagnostic); code != 0 {
		t.Fatalf("finalize CLI: %s", diagnostic.String())
	}
	if !strings.Contains(out.String(), "requested_roots=[queue@1 local@1] roots=[queue@1] filtered_roots=[local@1]") {
		t.Fatalf("selection summary lost filtering facts: %s", &out)
	}
	if strings.Count(out.String(), "linux-x86_64: filtered local@1: missing_adapter (local@1)\n") != 1 {
		t.Fatalf("selection summary lost or repeated the filter reason: %s", &out)
	}
}
