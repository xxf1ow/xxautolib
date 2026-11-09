package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fingerprintFixture(t *testing.T) (string, ciPlan) {
	t.Helper()
	root := discoveryRoot(t)
	for name, content := range map[string]string{
		"src/autolib-builder/fixture.go":             "package main\n",
		"src/autolib-importer/main.go":               "package main\n",
		"src/autolib.cmake":                          "# raw entry\r\n",
		"src/autolib-builder/prepare-tools.cmake":    "# prepare tools\n",
		"src/internal/packagemetadata/references.go": "package packagemetadata\n",
	} {
		writeCIFixture(t, root, name, content)
	}
	config, err := os.ReadFile("../../.github/ci-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	writeCIFixture(t, root, ".github/ci-tools.json", string(config))
	modules := []describedModule{}
	for _, name := range []string{"b", "a", "c", "d"} {
		deps := []string{}
		if name == "a" || name == "c" {
			deps = []string{"b@1"}
		}
		discoveryVersion(t, root, name, "1", true, "")
		base := "src/dependencies/" + name + "/1/"
		writeCIFixture(t, root, base+"patch.diff", "patch")
		writeCIFixture(t, root, base+"local/input.cmake", "step")
		modules = append(modules, describedModule{Name: name, Version: "1", Target: "fixture-" + name, Depends: deps, PackagePlatform: "linux-x86_64", InputFiles: []string{base + "module.toml", base + "autolib-import.cmake", base + "patch.diff", base + "local", "src/dependencies/" + name + "/package.toml"}})
	}
	desc := moduleDescription{Modules: modules, BuilderFiles: []string{"src/autolib-builder/fixture.go", "src/autolib-builder/prepare-tools.cmake", "CMakeLists.txt"}, GoModuleFiles: []string{"go.mod", "go.sum"}}
	return root, ciPlan{Platforms: []ciPlatformPlan{{ID: "linux-x86_64", Description: desc}}, Publishers: map[string]string{}}
}

func TestCIFingerprintClosure(t *testing.T) {
	for _, path := range []string{"module.toml", "patch.diff", "autolib-import.cmake", "local/input.cmake", "local/added.cmake"} {
		t.Run(path, func(t *testing.T) {
			root, plan := fingerprintFixture(t)
			before, err := computeCIFingerprints(root, plan)
			if err != nil {
				t.Fatal(err)
			}
			writeCIFixture(t, root, "src/dependencies/b/1/"+path, "changed")
			after, err := computeCIFingerprints(root, plan)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"b", "a", "c"} {
				key := name + "-1-linux-x86_64.tar.zst"
				if before.Fingerprints[key] == "" || before.Fingerprints[key] == after.Fingerprints[key] {
					t.Fatalf("stale closure %s", key)
				}
			}
			if before.Fingerprints["d-1-linux-x86_64.tar.zst"] != after.Fingerprints["d-1-linux-x86_64.tar.zst"] {
				t.Fatal("independent item invalidated")
			}
			if path == "local/added.cmake" {
				if err := os.Remove(filepath.Join(root, "src/dependencies/b/1/", path)); err != nil {
					t.Fatal(err)
				}
				reverted, err := computeCIFingerprints(root, plan)
				if err != nil || reverted.Fingerprints["b-1-linux-x86_64.tar.zst"] != before.Fingerprints["b-1-linux-x86_64.tar.zst"] {
					t.Fatal("directory deletion not reflected")
				}
			}
		})
	}
	t.Run("unrelated and latest only", func(t *testing.T) {
		root, plan := fingerprintFixture(t)
		before, err := computeCIFingerprints(root, plan)
		if err != nil {
			t.Fatal(err)
		}
		writeCIFixture(t, root, "docs/unrelated.md", "unrelated commit")
		writeCIFixture(t, root, "src/dependencies/b/package.toml", "latest='another'\n")
		after, err := computeCIFingerprints(root, plan)
		if err != nil {
			t.Fatal(err)
		}
		for key, value := range before.Fingerprints {
			if value != after.Fingerprints[key] {
				t.Fatalf("unrelated invalidation %s", key)
			}
		}
	})
	t.Run("missing item input stays local", func(t *testing.T) {
		root, plan := fingerprintFixture(t)
		if err := os.Remove(filepath.Join(root, "src/dependencies/b/1/patch.diff")); err != nil {
			t.Fatal(err)
		}
		report, err := computeCIFingerprints(root, plan)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"b", "a", "c"} {
			key := name + "-1-linux-x86_64.tar.zst"
			failure := report.Failures[key]
			if failure.Error == "" || len(failure.DependencyChain) == 0 || report.Fingerprints[key] != "" {
				t.Fatalf("lost failure %s: %+v", key, report)
			}
		}
		if report.Fingerprints["d-1-linux-x86_64.tar.zst"] == "" {
			t.Fatal("healthy fingerprint lost")
		}
	})
	for _, kind := range []string{"shared input", "escape"} {
		t.Run(kind, func(t *testing.T) {
			root, plan := fingerprintFixture(t)
			if kind == "shared input" {
				if err := os.Remove(filepath.Join(root, "go.mod")); err != nil {
					t.Fatal(err)
				}
			} else {
				external := filepath.Join(t.TempDir(), "file")
				if err := os.WriteFile(external, []byte("external"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filepath.Join(root, "src/dependencies/b/1/local/escape")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := computeCIFingerprints(root, plan); err == nil {
				t.Fatal("global input error accepted")
			} else if kind == "escape" && !strings.Contains(err.Error(), "escapes source root") {
				t.Fatalf("wrong error for escaped input: %v", err)
			} else if kind == "shared input" && !strings.Contains(err.Error(), "go.mod") {
				t.Fatalf("wrong error for missing shared input: %v", err)
			}
		})
	}
}

func TestCIFingerprintUniversalAndTools(t *testing.T) {
	root, plan := fingerprintFixture(t)
	plan.Platforms[0].Description.Modules[3].PackagePlatform = "any-any"
	second := plan.Platforms[0]
	second.ID = "windows-x86_64"
	second.Description.Modules = append([]describedModule{}, second.Description.Modules...)
	for i := range second.Description.Modules {
		if second.Description.Modules[i].PackagePlatform != "any-any" {
			second.Description.Modules[i].PackagePlatform = second.ID
		}
	}
	plan.Platforms = append(plan.Platforms, second)
	plan.Publishers["d@1"] = "linux-x86_64"
	first, err := computeCIFingerprints(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	other, err := computeCIFingerprints(root, ciPlan{Platforms: []ciPlatformPlan{second}, Publishers: map[string]string{"d@1": "windows-x86_64"}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprints["d-1-any-any.tar.zst"] == "" || first.Fingerprints["d-1-any-any.tar.zst"] != other.Fingerprints["d-1-any-any.tar.zst"] {
		t.Fatal("universal fingerprint depends on publisher")
	}
	writeCIFixture(t, root, "src/autolib.cmake", "# entry changed\n")
	entry, err := computeCIFingerprints(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range first.Fingerprints {
		if strings.HasPrefix(key, "autolib-importer-") {
			if value == entry.Fingerprints[key] {
				t.Fatal("entry did not invalidate importer")
			}
		} else if value != entry.Fingerprints[key] {
			t.Fatalf("entry invalidated library %s", key)
		}
	}
	var config map[string]any
	data, err := os.ReadFile(filepath.Join(root, ".github/ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, asset := range config["cmake"].(map[string]any)["assets"].([]any) {
		a := asset.(map[string]any)
		if a["platform"] == "windows-x86_64" {
			a["sha256"] = strings.Repeat("f", 64)
		}
	}
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	writeCIFixture(t, root, ".github/ci-tools.json", string(data))
	changed, err := computeCIFingerprints(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"b-1-linux-x86_64.tar.zst", "d-1-any-any.tar.zst"} {
		if changed.Fingerprints[key] != entry.Fingerprints[key] {
			t.Fatalf("unrelated platform invalidated %s", key)
		}
	}
	if changed.Fingerprints["b-1-windows-x86_64.tar.zst"] == entry.Fingerprints["b-1-windows-x86_64.tar.zst"] {
		t.Fatal("platform configuration not fingerprinted")
	}
	eigen := plan.Platforms[0].Description.Modules[3]
	eigen.Name = "eigen"
	eigen.Version = "3.4.0"
	otherEigen := eigen
	otherEigen.Version = "5.0.1"
	plan.Platforms[0].Description.Modules = append(plan.Platforms[0].Description.Modules, eigen, otherEigen)
	versions, err := computeCIFingerprints(root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if versions.Fingerprints["eigen-3.4.0-any-any.tar.zst"] == "" || versions.Fingerprints["eigen-3.4.0-any-any.tar.zst"] == versions.Fingerprints["eigen-5.0.1-any-any.tar.zst"] {
		t.Fatal("exact versions lost identity")
	}
}

func TestCIFingerprintLocalGoInputs(t *testing.T) {
	for _, owner := range []string{"autolib-builder", "autolib-importer"} {
		t.Run(owner, func(t *testing.T) {
			root, plan := fingerprintFixture(t)
			file := "src/" + owner + "/imports.go"
			writeCIFixture(t, root, file, "package main\nimport _ \"xxautolib/src/internal/fixture\"\n")
			writeCIFixture(t, root, "src/internal/fixture/fixture.go", "package fixture\nconst Value=1\n")
			before, err := computeCIFingerprints(root, plan)
			if err != nil {
				t.Fatal(err)
			}
			writeCIFixture(t, root, "src/internal/fixture/fixture.go", "package fixture\nconst Value=2\n")
			after, err := computeCIFingerprints(root, plan)
			if err != nil {
				t.Fatal(err)
			}
			if before.Fingerprints["autolib-importer-linux-x86_64.tar.xz"] == after.Fingerprints["autolib-importer-linux-x86_64.tar.xz"] {
				t.Fatal("tool dependency input missed")
			}
			changed := before.Fingerprints["b-1-linux-x86_64.tar.zst"] != after.Fingerprints["b-1-linux-x86_64.tar.zst"]
			if changed != (owner == "autolib-builder") {
				t.Fatal("wrong library production inputs")
			}
			writeCIFixture(t, root, "src/internal/fixture/unrelated_test.go", "not a production input")
			last, err := computeCIFingerprints(root, plan)
			if err != nil {
				t.Fatal(err)
			}
			if last.Fingerprints["autolib-importer-linux-x86_64.tar.xz"] != after.Fingerprints["autolib-importer-linux-x86_64.tar.xz"] {
				t.Fatal("test source invalidated production")
			}
		})
	}
}
