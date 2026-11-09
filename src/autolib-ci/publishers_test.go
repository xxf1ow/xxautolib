package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCIPublishersByPackage(t *testing.T) {
	builder := discoveryBuilder(t)
	root := discoveryRoot(t)
	discoveryModule(t, root, "x86", true, "package_platforms=['linux-x86_64']\n")
	discoveryModule(t, root, "arm", true, "package_platforms=['linux-aarch64']\n")
	discoveryModule(t, root, "desktop", true, "package_platforms=['windows-x86_64','darwin-aarch64']\n")
	discoveryVersion(t, root, "a", "1", true, "package_platforms=['any-any']\ndepends=['x86@1.0']\n")
	discoveryVersion(t, root, "a", "2", true, "package_platforms=['any-any']\ndepends=['arm@1.0']\n")
	discoveryVersion(t, root, "b", "1", true, "package_platforms=['any-any']\ndepends=['desktop@1.0']\n")
	for _, platforms := range []string{"windows-x86_64,linux-x86_64,linux-aarch64,darwin-aarch64", "darwin-aarch64,linux-aarch64,linux-x86_64,windows-x86_64"} {
		plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Platforms: &platforms, Modules: stringPointer("a@1,a@2,b@1")})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(plan)
		var wire struct {
			Publishers map[string]string `json:"publishers"`
		}
		json.Unmarshal(data, &wire)
		want := map[string]string{"a@1": "linux-x86_64", "a@2": "linux-aarch64", "b@1": "windows-x86_64"}
		if !reflect.DeepEqual(wire.Publishers, want) {
			t.Fatalf("publishers: got %v want %v", wire.Publishers, want)
		}
	}
}

func TestCIPublisherTransportBinding(t *testing.T) {
	for _, publishers := range []map[string]string{nil, {}, {"queue@1": "windows-x86_64"}, {"queue@1": "darwin-aarch64"}, {"queue": "linux-x86_64"}, {"queue@1": "linux-x86_64", "extra@1": "linux-x86_64"}} {
		t.Run("invalid-plan", func(t *testing.T) {
			plan, run, results := finalizeFixture()
			plan.Publishers = publishers
			if err := finalizeCI(plan, ciGitHubRepository, run, results); err == nil {
				t.Fatalf("accepted publishers %v", publishers)
			}
		})
	}
	t.Run("nonpublisher-transport", func(t *testing.T) {
		f := transportInputFixture(t)
		other := "linux-x86_64"
		if f.options.Platform == other {
			t.Skip("highest priority platform cannot be a nonpublisher")
		}
		candidate := f.plan.Platforms[0]
		candidate.ID = other
		f.plan.Platforms = append(f.plan.Platforms, candidate)
		f.plan.Publishers = map[string]string{"queue@1": other}
		if err := writeCIJSON(f.options.PlanFile, f.plan); err != nil {
			t.Fatal(err)
		}
		if err := writeCITransport(context.Background(), f.options, filepath.Join(f.options.WorkRoot, "nonpublisher.tar")); err != nil {
			t.Fatal(err)
		}
		// The absent publisher result still prevents global completion.
		var result ciPlatformResult
		if err := decodeStrictJSONFile(filepath.Join(f.options.WorkRoot, "results", f.options.Platform+".json"), &result); err != nil {
			t.Fatal(err)
		}
		if err := finalizeCI(f.plan, ciGitHubRepository, f.identity, []ciPlatformResult{result}); err == nil {
			t.Fatal("global finalize accepted absent publisher")
		}
	})
}

func TestCIPublisherRetrySameSelection(t *testing.T) {
	plan, run, results := finalizeFixture()
	run.Attempt = "2"
	for i := range results {
		results[i].Run = run
	}
	if err := finalizeCI(plan, ciGitHubRepository, run, results); err != nil {
		t.Fatal(err)
	}
	results[0].Complete = false
	if err := finalizeCI(plan, ciGitHubRepository, run, results); err == nil {
		t.Fatal("other platforms replaced failed publisher")
	}
}
