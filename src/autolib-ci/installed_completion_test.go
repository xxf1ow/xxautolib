package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCICacheNativePackageCompletion(t *testing.T) {
	f := cacheRunFixture(t)
	requests := 0
	original := http.DefaultTransport
	http.DefaultTransport = ciNoRemoteTransport{request: func() { requests++ }}
	t.Cleanup(func() { http.DefaultTransport = original })
	t.Cleanup(func() {
		if requests != 0 {
			t.Errorf("native build issued %d remote API requests", requests)
		}
	})
	for _, layer := range []string{"prepare", "layer0", "layer1", "verify"} {
		f.options.Layer = layer
		if err := runCILayerWithCache(context.Background(), f.options, f.release.client); err != nil {
			t.Fatalf("%s: %v", layer, err)
		}
	}
	for layer, keys := range f.plan.Platforms[0].Layers {
		for position, key := range keys {
			logs := filepath.Join(f.options.WorkRoot, "logs", fmt.Sprintf("layer%d", layer), fmt.Sprint(position+1))
			if content := mustReadText(t, filepath.Join(logs, "configure.log")); !strings.Contains(content, "-DBUILD_LIST="+key) {
				t.Fatalf("configure log for %s was overwritten: %s", key, content)
			}
			if content := mustReadText(t, filepath.Join(logs, "build.log")); !strings.Contains(content, "--build") {
				t.Fatalf("build log for %s is missing its command", key)
			}
		}
	}
	var result ciPlatformResult
	if err := decodeStrictJSONFile(filepath.Join(f.options.WorkRoot, "results", f.options.Platform+".json"), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Packages) != 4 {
		t.Fatalf("want four exact package results, got %+v", result)
	}
	if installed := mustReadText(t, filepath.Join(f.options.WorkRoot, "install", "autolib.cmake")); installed != mustReadText(t, filepath.Join(f.options.SourceRoot, "src", "autolib.cmake")) {
		t.Fatal("ordinary root configure did not install the source entry unchanged")
	}
	got := []string{}
	for _, item := range result.Packages {
		data, _ := json.Marshal(item.Slot)
		got = append(got, string(data))
	}
	want := []string{`{"name":"dep","version":"1","os":"any","arch":"any"}`, `{"name":"queue","version":"1","os":"any","arch":"any"}`, `{"name":"eigen","version":"3","os":"any","arch":"any"}`, `{"name":"eigen","version":"5","os":"any","arch":"any"}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exact package identities: %v", got)
	}
	if err := finalizeCI(f.plan, ciGitHubRepository, f.identity, []ciPlatformResult{result}); err != nil {
		t.Fatal(err)
	}
}

type ciNoRemoteTransport struct{ request func() }

func (transport ciNoRemoteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.request()
	return nil, fmt.Errorf("native CI must not request %s", request.URL)
}

func TestCISelectedPlatformCompletion(t *testing.T) {
	plan, identity, results := finalizeFixture()
	plan.Platforms = plan.Platforms[:1]
	results = results[:1]
	if err := finalizeCI(plan, ciGitHubRepository, identity, results); err != nil {
		t.Fatalf("verified selected platform must not require Release upload: %v", err)
	}
	all, _, complete := finalizeFixture()
	darwin := all.Platforms[0]
	darwin.ID = "darwin-aarch64"
	all.Platforms = append(all.Platforms, darwin)
	darwinResult := complete[0]
	darwinResult.Platform = "darwin-aarch64"
	darwinResult.Importer.File = "autolib-importer-darwin-aarch64"
	complete = append(complete, darwinResult)
	if err := finalizeCI(all, ciGitHubRepository, identity, complete); err != nil {
		t.Fatalf("four selected platforms must complete: %v", err)
	}
	if err := finalizeCI(all, ciGitHubRepository, identity, complete[:3]); err == nil {
		t.Fatal("missing Darwin result accepted")
	}
	for _, change := range []string{"missing", "duplicate", "attempt", "sha"} {
		t.Run(change, func(t *testing.T) {
			items := append([]ciPlatformResult{}, complete...)
			switch change {
			case "missing":
				items = items[:2]
			case "duplicate":
				items[2] = items[0]
			case "attempt":
				items[0].Run.Attempt = "2"
			case "sha":
				items[0].Run.SHA = strings.Repeat("b", 40)
			}
			if err := finalizeCI(all, ciGitHubRepository, identity, items); err == nil {
				t.Fatalf("accepted %s selected-platform completion", change)
			}
		})
	}
}
