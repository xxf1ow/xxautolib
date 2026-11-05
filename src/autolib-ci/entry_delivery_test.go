package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCIToolOnlySelection(t *testing.T) {
	for _, selection := range []*string{nil, stringPointer("")} {
		plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: t.TempDir(), BuilderFile: "must-not-run-describe", Modules: selection})
		if err != nil {
			t.Fatalf("tool-only plan invoked discovery: %v", err)
		}
		if len(plan.Platforms) != 3 || len(plan.RequestedModules) != 0 || plan.Publisher != "" {
			t.Fatalf("tool-only platforms lost: %+v", plan)
		}
		for _, platform := range plan.Platforms {
			if len(platform.Roots) != 0 || len(platform.Description.Modules) != 0 || len(platform.Layers) != 0 {
				t.Fatalf("tool-only platform contains library work: %+v", platform)
			}
		}
	}
	builder := discoveryBuilder(t)
	root := discoveryRoot(t)
	discoveryModule(t, root, "fresh", true, "")
	discoveryModule(t, root, "local", false, "")
	for _, selection := range []string{"all", "fresh,local", "local"} {
		plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Modules: &selection})
		if err != nil || len(plan.Platforms) != 3 {
			t.Fatalf("selection %q lost platform: %+v %v", selection, plan, err)
		}
		for _, platform := range plan.Platforms {
			data, _ := json.Marshal(platform)
			var fields map[string]any
			json.Unmarshal(data, &fields)
			wantRequested := []any{"fresh@1.0", "local@1.0"}
			if selection == "local" {
				wantRequested = []any{"local@1.0"}
			}
			if !reflect.DeepEqual(fields["requested_roots"], wantRequested) || !reflect.DeepEqual(fields["filtered_roots"], []any{"local@1.0"}) {
				t.Fatalf("request/filter facts missing for %q: %s", selection, data)
			}
			if selection == "local" && (len(platform.Roots) != 0 || len(platform.Layers) != 0) {
				t.Fatalf("filtered platform contains library work: %+v", platform)
			}
		}
	}
	for _, selection := range []string{" all", "all ", "ALL", "all,fresh", "fresh,all", "all,", " \t ", "autolib-importer"} {
		if _, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Modules: &selection}); err == nil {
			t.Errorf("invalid selection %q accepted", selection)
		}
	}
}

func TestCIForkContext(t *testing.T) {
	root := t.TempDir()
	identity := transportIdentity(t, root)
	identity.Repository = "another-owner/another-repository"
	if err := validateCIRunIdentity(context.Background(), root, "another-owner/another-repository", identity); err != nil {
		t.Fatalf("valid trusted fork rejected: %v", err)
	}
	for _, repository := range []string{ciGitHubRepository, "", "invalid", "owner/../repository"} {
		if err := validateCIRunIdentity(context.Background(), root, repository, identity); err == nil {
			t.Fatalf("untrusted repository %q accepted", repository)
		}
	}
	identity.CheckoutSHA = strings.Repeat("b", 40)
	if err := validateCIRunIdentity(context.Background(), root, "another-owner/another-repository", identity); err == nil {
		t.Fatal("fork with mismatched checkout accepted")
	}
	f := transportInputFixture(t)
	f.options.Repository = "another-owner/another-repository"
	f.identity.Repository = f.options.Repository
	if err := writeCIJSON(f.options.RunIdentityFile, f.identity); err != nil {
		t.Fatal(err)
	}
	resultFile := filepath.Join(f.options.WorkRoot, "results", f.options.Platform+".json")
	var result ciPlatformResult
	if err := decodeStrictJSONFile(resultFile, &result); err != nil {
		t.Fatal(err)
	}
	result.Run = f.identity
	if err := writeCIJSON(resultFile, result); err != nil {
		t.Fatal(err)
	}
	members := f.transport(t, true)
	if err := json.Unmarshal(members["result.json"], &result); err != nil {
		t.Fatal(err)
	}
	if err := finalizeCI(f.plan, "another-owner/another-repository", f.identity, []ciPlatformResult{result}); err != nil {
		t.Fatalf("fork finalize: %v", err)
	}
	if err := finalizeCI(f.plan, ciGitHubRepository, f.identity, []ciPlatformResult{result}); err == nil {
		t.Fatal("self-reported fork replaced trusted finalize repository")
	}
	f.options.Repository = ciGitHubRepository
	if err := writeCITransport(context.Background(), f.options, filepath.Join(f.options.WorkRoot, "untrusted.tar")); err == nil {
		t.Fatal("self-reported fork replaced trusted transport repository")
	}
}

func TestCIEntryTransport(t *testing.T) {
	for _, change := range []string{"valid", "missing", "corrupt", "directory", "symlink", "unverified", "wrong-file", "wrong-digest", "missing-tool", "empty-libraries"} {
		t.Run(change, func(t *testing.T) {
			f := transportInputFixture(t)
			entry := "set(_autolib_release_root \"\")\n# source entry\n"
			writeCIFixture(t, f.options.SourceRoot, "src/autolib.cmake", entry)
			writeCIFixture(t, f.options.WorkRoot, "install/autolib.cmake", entry)
			resultFile := filepath.Join(f.options.WorkRoot, "results", f.options.Platform+".json")
			var result map[string]any
			json.Unmarshal([]byte(mustReadText(t, resultFile)), &result)
			entryResult := map[string]any{"file": "autolib.cmake", "sha256": testDigest([]byte(entry)), "verified": true}
			result["entry"] = entryResult
			installedEntry := filepath.Join(f.options.WorkRoot, "install", "autolib.cmake")
			switch change {
			case "missing":
				os.Remove(installedEntry)
			case "corrupt":
				os.WriteFile(installedEntry, []byte("corrupt\n"), 0644)
			case "directory":
				os.Remove(installedEntry)
				os.Mkdir(installedEntry, 0755)
			case "symlink":
				os.Remove(installedEntry)
				createCITransportSymlink(t, filepath.Join(f.options.SourceRoot, "src/autolib.cmake"), installedEntry)
			case "unverified":
				entryResult["verified"] = false
			case "wrong-file":
				entryResult["file"] = "other.cmake"
			case "wrong-digest":
				entryResult["sha256"] = strings.Repeat("a", 64)
			case "missing-tool":
				os.Remove(filepath.Join(f.options.WorkRoot, "install", ciImporterExecutable(f.options.Platform)))
			case "empty-libraries":
				f.plan.Platforms[0].Roots = []string{}
				f.plan.Platforms[0].Description.Roots = []string{}
				f.plan.Platforms[0].Description.Modules = []describedModule{}
				f.plan.Platforms[0].Layers = [][]string{}
				result["packages"], result["roots"] = []any{}, []any{}
				if err := writeCIJSON(f.options.PlanFile, f.plan); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeCIJSON(resultFile, result); err != nil {
				t.Fatal(err)
			}
			before := transportSnapshot(t, filepath.Join(f.options.WorkRoot, "install"))
			members := f.transport(t, change == "valid" || change == "empty-libraries")
			if members != nil {
				var manifest map[string]any
				json.Unmarshal(members["transport.json"], &manifest)
				if manifest["entry"] != "autolib.cmake" || !bytes.Equal(members["autolib.cmake"], []byte(entry)) {
					t.Fatalf("entry bytes/path missing: %s", members["transport.json"])
				}
			}
			if !reflect.DeepEqual(before, transportSnapshot(t, filepath.Join(f.options.WorkRoot, "install"))) {
				t.Fatal("transport modified input")
			}
		})
	}
}

func TestCIToolOnlyRun(t *testing.T) {
	f := installedCIFixture(t)
	f.options.MakeProgram = ""
	f.options.Budget = 0
	f.plan.RequestedModules = []string{}
	f.plan.Publisher = ""
	f.plan.Platforms[0].Roots = []string{}
	f.plan.Platforms[0].Description.Roots = []string{}
	f.plan.Platforms[0].Description.Modules = []describedModule{}
	f.plan.Platforms[0].Layers = [][]string{}
	if err := writeCIJSON(f.options.PlanFile, f.plan); err != nil {
		t.Fatal(err)
	}
	// An empty library selection must not enter the library consumer driver.
	writeCIFixture(t, f.options.SourceRoot, "tests/autolib/run-installed.cmake", "message(FATAL_ERROR \"tool-only invoked library verification\")\n")
	for _, layer := range []string{"prepare", "verify"} {
		f.options.Layer = layer
		if err := runCILayer(context.Background(), f.options); err != nil {
			t.Fatalf("%s: %v", layer, err)
		}
	}
	install := filepath.Join(f.options.WorkRoot, "install")
	entries, err := os.ReadDir(install)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("tool-only install contains library work: %v", entries)
	}
	if got := mustReadText(t, filepath.Join(install, "autolib.cmake")); got != mustReadText(t, filepath.Join(f.options.SourceRoot, "src/autolib.cmake")) {
		t.Fatal("installed entry differs from source")
	}
	if _, err := os.Stat(filepath.Join(f.options.WorkRoot, "build", "generated")); !os.IsNotExist(err) {
		t.Fatalf("tool-only generated dependencies: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.options.WorkRoot, "jobserver.json")); !os.IsNotExist(err) {
		t.Fatalf("tool-only ran the library jobserver probe: %v", err)
	}
	f.transport(t, true)
}
