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
