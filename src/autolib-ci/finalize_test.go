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
	run := ciRunIdentity{Repository: ciGitHubRepository, Ref: "refs/heads/master", SHA: strings.Repeat("a", 40), CheckoutSHA: strings.Repeat("a", 40), RunID: "123", Attempt: "1"}
	plan := ciPlan{Publisher: "linux-x86_64", RequestedModules: []string{"queue"}, Platforms: []ciPlatformPlan{{ID: "linux-x86_64", Description: moduleDescription{Modules: []describedModule{{Name: "queue", PlatformIndependent: true}}}}, {ID: "windows-x86_64", Description: moduleDescription{Modules: []describedModule{{Name: "queue", PlatformIndependent: true}}}}}}
	results := []ciPlatformResult{}
	for _, platform := range plan.Platforms {
		tool := "autolib-importer-" + platform.ID
		if strings.HasPrefix(platform.ID, "windows-") {
			tool += ".exe"
		}
		tool += ".tar.xz"
		results = append(results, ciPlatformResult{Run: run, Platform: platform.ID, Complete: true, Importer: ciImporterResult{Asset: tool, SHA256: strings.Repeat("b", 64), Verified: true, Uploaded: true}, Packages: []ciSlotResult{{Slot: ciSlot{Name: "queue", OS: "any", Arch: "any"}, Asset: "queue-any-any.tar.zst", SHA256: strings.Repeat("c", 64), Built: true, Verified: true, Uploaded: platform.ID == plan.Publisher}}})
	}
	return plan, run, results
}

func TestCIImporterAssetNames(t *testing.T) {
	for platform, want := range map[string]string{
		"windows-x86_64": "autolib-importer-windows-x86_64.exe.tar.xz",
		"linux-x86_64":   "autolib-importer-linux-x86_64.tar.xz",
		"linux-aarch64":  "autolib-importer-linux-aarch64.tar.xz",
	} {
		if got := ciImporterAsset(platform); got != want {
			t.Errorf("%s: asset = %q, want %q", platform, got, want)
		}
	}
}

func TestCIFinalize(t *testing.T) {
	plan, run, results := finalizeFixture()
	if err := finalizeCI(plan, run, results); err != nil {
		t.Fatal(err)
	}
	if err := finalizeCI(plan, run, nil); err == nil {
		t.Fatal("missing results accepted")
	}
	if err := finalizeCI(plan, run, append(results, results[0])); err == nil {
		t.Fatal("duplicate results accepted")
	}
	results[0].Packages[0].Uploaded = false
	if err := finalizeCI(plan, run, results); err == nil {
		t.Fatal("publisher build without upload accepted")
	}
	results[0].Packages[0].Built = false
	if err := finalizeCI(plan, run, results); err == nil {
		t.Fatal("package without current build accepted")
	}
}

func TestCIResultBinding(t *testing.T) {
	for _, change := range []string{"run", "attempt", "sha", "platform", "unverified", "incomplete", "error", "duplicate-package", "missing-package", "wrong-slot", "wrong-asset", "bare-importer", "importer", "unexpected-upload"} {
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
			case "wrong-asset":
				results[0].Packages[0].Asset = "other.tar.zst"
			case "importer":
				results[0].Importer.Uploaded = false
			case "bare-importer":
				results[0].Importer.Asset = "autolib-importer-linux-x86_64"
			case "unexpected-upload":
				results[1].Packages[0].Uploaded = true
			}
			if err := finalizeCI(plan, run, results); err == nil {
				t.Fatalf("accepted %s", change)
			}
		})
	}
}

func TestCIRunCLI(t *testing.T) {
	for _, command := range []string{"run", "finalize"} {
		var out, diagnostic bytes.Buffer
		if code := run([]string{command}, &out, &diagnostic); code == 0 || !strings.Contains(diagnostic.String(), "--plan") {
			t.Fatalf("missing %s inputs accepted: %d %s", command, code, diagnostic.String())
		}
	}
	plan, identity, results := finalizeFixture()
	root := t.TempDir()
	for name, value := range map[string]any{"plan.json": plan, "run.json": identity, "results/linux.json": results[0], "results/windows.json": results[1]} {
		file := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(file), 0755)
		data, _ := json.Marshal(value)
		if err := os.WriteFile(file, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	var out, diagnostic bytes.Buffer
	if code := run([]string{"finalize", "--plan", filepath.Join(root, "plan.json"), "--run-identity", filepath.Join(root, "run.json"), "--results", filepath.Join(root, "results")}, &out, &diagnostic); code != 0 {
		t.Fatalf("finalize CLI: %s", diagnostic.String())
	}
}
