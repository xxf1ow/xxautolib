package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCICachePrepareCapacity(t *testing.T) {
	assets := []ciCacheAsset{}
	for i := 0; i < 999; i++ {
		assets = append(assets, ciCacheAsset{ID: int64(i + 1), Name: fmt.Sprintf("unknown-%d", i)})
	}
	names := []string{"new.tar.zst"}
	if err := preflightCICacheCapacity(assets, names); err != nil {
		t.Fatal(err)
	}
	assets = append(assets, ciCacheAsset{ID: 1000, Name: "unknown-999"})
	if err := preflightCICacheCapacity(assets, names); err == nil {
		t.Fatal("overflow accepted")
	}
	if err := preflightCICacheCapacity(assets, []string{"unknown-0"}); err != nil {
		t.Fatal(err)
	}
	_, plan := fingerprintFixture(t)
	plan.Publishers = ciPublishers(plan.Platforms)
	planned, err := plannedCICacheNames(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range planned {
		if strings.HasSuffix(name, ".publication.json") {
			t.Fatal("capacity reserves publication attachments")
		}
	}
	if err := preflightCICacheCapacity(assets, []string{"same", "same"}); err == nil {
		t.Fatal("duplicate planned writer accepted")
	}
}

func TestCICachePrepare(t *testing.T) {
	f := releaseCacheFixture(t)
	root, plan := fingerprintFixture(t)
	run := transportIdentity(t, root)
	run.Repository = "owner/repo"
	plan.Publishers = ciPublishers(plan.Platforms)
	o := ciCachePrepareOptions{Repository: run.Repository, SourceRoot: root, PlanFile: filepath.Join(root, "plan.json"), RunIdentityFile: filepath.Join(root, "run.json"), Output: filepath.Join(root, "snapshot.json")}
	if err := writeCIJSON(o.PlanFile, plan); err != nil {
		t.Fatal(err)
	}
	if err := writeCIJSON(o.RunIdentityFile, run); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "src/dependencies/b/1/patch.diff")); err != nil {
		t.Fatal(err)
	}
	f.exists = false
	if err := prepareCICacheWithClient(context.Background(), o, f.client); err != nil {
		t.Fatal(err)
	}
	var snapshot ciCacheSnapshot
	if err := decodeStrictJSONFile(o.Output, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Inputs.Failures) != 3 || snapshot.Inputs.Fingerprints["d-1-linux-x86_64.tar.zst"] == "" {
		t.Fatal("local input failure lost healthy work")
	}
	if strings.Contains(mustReadText(t, o.Output), "test-secret") {
		t.Fatal("snapshot leaked token")
	}
	if f.creates != 1 || f.client.TargetSHA != run.SHA {
		t.Fatal("cache not initialized for verified source")
	}
	if err := prepareCICacheWithClient(context.Background(), o, f.client); err != nil || f.creates != 1 {
		t.Fatal("initialization not idempotent")
	}
	if err := os.Remove(o.Output); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 999; i++ {
		f.add(fmt.Sprint(i), []byte("x"), "uploaded")
	}
	if err := prepareCICacheWithClient(context.Background(), o, f.client); err == nil {
		t.Fatal("capacity overflow accepted")
	}
	if _, err := os.Stat(o.Output); !os.IsNotExist(err) {
		t.Fatal("failed preparation left a usable snapshot")
	}
	if len(f.ops) != 1 {
		t.Fatal("preflight modified existing cache")
	}
	plan.Platforms = append(plan.Platforms, plan.Platforms[0])
	if _, err := plannedCICacheNames(plan); err == nil {
		t.Fatal("duplicate platform writers accepted")
	}
}

func TestCICachePrepareRequiresCachedDependencies(t *testing.T) {
	for _, tt := range []struct {
		name, want string
	}{
		{"only dependencies cached", ""},
		{"missing record", "b@1"},
		{"missing archive", "b@1"},
		{"stale fingerprint", "b@1"},
		{"stale dependency fingerprint", "a@1"},
		{"no root", "exact root"},
		{"multiple roots", "exact root"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const root = "opencv@4.14.0"
			b, assets := cacheRecordFixture()
			a := b
			a.Key.Name, a.Archive.ID = "a", 43
			a.Archive.Name = a.Key.archiveName()
			a.DependencyFingerprints = map[string]string{"b@1": b.Fingerprint}
			assets = append(assets, ciCacheAsset{ID: a.Archive.ID, Name: a.Archive.Name, State: "uploaded", Size: a.Archive.Size})
			plan := ciPlan{RequestedModules: []string{root}, Platforms: []ciPlatformPlan{{
				ID: "linux-x86_64", Roots: []string{root}, Description: moduleDescription{Modules: []describedModule{
					{Name: "opencv", Version: "4.14.0", PackagePlatform: "linux-x86_64", Depends: []string{"a@1"}},
					{Name: "a", Version: "1", PackagePlatform: "linux-x86_64", Depends: []string{"b@1"}},
					{Name: "b", Version: "1", PackagePlatform: "linux-x86_64"},
				}},
			}}}
			snapshot := ciCacheSnapshot{Repository: b.Origin.Repository, Assets: assets,
				Records: map[string]ciCacheRecord{a.Archive.Name: a, b.Archive.Name: b},
				Inputs:  ciFingerprintReport{Fingerprints: map[string]string{a.Archive.Name: a.Fingerprint, b.Archive.Name: b.Fingerprint}},
			}
			switch tt.name {
			case "missing record":
				delete(snapshot.Records, b.Archive.Name)
			case "missing archive":
				snapshot.Assets = assets[1:]
			case "stale fingerprint":
				snapshot.Inputs.Fingerprints[b.Archive.Name] = strings.Repeat("b", 64)
			case "stale dependency fingerprint":
				a.DependencyFingerprints["b@1"] = strings.Repeat("b", 64)
			case "no root":
				plan.Platforms[0].Roots = nil
			case "multiple roots":
				plan.Platforms[0].Roots = []string{root, "a@1"}
			}
			err := requireCICachedDependencies(plan, snapshot)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "linux-x86_64") {
				t.Fatalf("expected platform and %q in error, got %v", tt.want, err)
			}
		})
	}
}
