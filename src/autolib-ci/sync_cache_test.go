package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func syncCacheSource(r ciCacheRecord) ciSyncSource {
	module := describedModule{Name: r.Key.Name, Version: r.Key.Version, PackagePlatform: r.Key.OS + "-" + r.Key.Arch, Depends: []string{}}
	return ciSyncSource{Schema: 1, Repository: "owner/repo", Ref: "refs/heads/main", SHA: strings.Repeat("2", 40), Catalog: sourceCatalog{Packages: map[string]sourcePackage{r.Key.Name: {Latest: r.Key.Version, Versions: map[string]sourceVersion{r.Key.Version: {Platforms: []string{module.PackagePlatform}}}}}}, Plan: ciPlan{Platforms: []ciPlatformPlan{{ID: "linux-x86_64", Description: moduleDescription{Modules: []describedModule{module}}}}}, Inputs: ciFingerprintReport{Fingerprints: map[string]string{r.Archive.Name: r.Fingerprint}, Failures: map[string]ciFingerprintFailure{}}}
}

func TestCISyncCache(t *testing.T) {
	ctx := context.Background()
	for _, fault := range []string{"valid", "stale", "orphan", "orphan record", "origin", "ID", "missing release", "pagination"} {
		t.Run(fault, func(t *testing.T) {
			f := releaseCacheFixture(t)
			r := f.seed(t, localCacheFixture(t))
			source := syncCacheSource(r)
			switch fault {
			case "orphan record":
				delete(f.assets, r.Archive.ID)
			case "orphan":
				current, err := f.client.Records.Snapshot(ctx, false)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.client.Records.Update(ctx, current.Head, r.Archive.Name+".publication.json", nil); err != nil {
					t.Fatal(err)
				}
			case "stale":
				source.Inputs.Fingerprints[r.Archive.Name] = strings.Repeat("b", 64)
			case "origin", "ID":
				if fault == "origin" {
					r.Origin.Repository = "fake/repo"
				} else {
					r.Archive.ID++
				}
				data, _ := json.Marshal(r)
				f.setRecord(t, r.Archive.Name, data)
			case "missing release":
				f.exists = false
			case "pagination":
				for i := 0; i < 101; i++ {
					f.add(fmt.Sprint(i), []byte("unknown"), "uploaded")
				}
				f.pageFailure = true
			}
			snapshot, err := prepareCISyncCache(ctx, f.client, source)
			if fault == "pagination" {
				if err == nil {
					t.Fatal("partial list became usable")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.RecordHead == "" {
				t.Fatal("sync did not retain fixed Git record head")
			}
			if f.creates != 0 {
				t.Fatal("sync created a cache release")
			}
			if fault == "valid" {
				if snapshot.Records[r.Archive.Name].Origin.RunID != "10" {
					t.Fatal("original producer lost")
				}
			} else if fault == "missing release" {
				if snapshot.ReleaseID != 0 || len(snapshot.Assets) != 0 {
					t.Fatal("404 is not empty")
				}
			} else if fault == "orphan record" && snapshot.Records[r.Archive.Name].Key.Name != "b" {
				t.Fatal("orphan record lost cleanup identity")
			} else if snapshot.RecordErrors[r.Archive.Name] == "" {
				t.Fatal("invalid candidate accepted")
			}
		})
	}
}

func TestCISyncCacheDownload(t *testing.T) {
	f := releaseCacheFixture(t)
	r := f.seed(t, localCacheFixture(t))
	source := syncCacheSource(r)
	snapshot, err := prepareCISyncCache(context.Background(), f.client, source)
	if err != nil {
		t.Fatal(err)
	}
	original := f.client.HTTP.Transport
	archives := []string{}
	f.client.HTTP.Transport = cacheTestRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Accept") == "application/octet-stream" {
			archives = append(archives, req.URL.Path)
		}
		return original.RoundTrip(req)
	})
	asset, err := downloadCISyncCache(context.Background(), f.client, snapshot, r.Archive.Name, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(asset.Path)
	if err != nil || string(data) != "verified archive bytes" || len(archives) != 1 {
		t.Fatal("selected byte delivery failed")
	}
	snapshot.RecordErrors[r.Archive.Name] = "stale"
	if _, err := downloadCISyncCache(context.Background(), f.client, snapshot, r.Archive.Name, t.TempDir()); err == nil {
		t.Fatal("invalid candidate downloaded")
	}
	if len(archives) != 1 {
		t.Fatal("unselected archive downloaded")
	}
}

func TestCISyncCacheDependencyReferences(t *testing.T) {
	for _, consumerPlatform := range []string{"linux-x86_64", "any-any"} {
		for _, dependencyPlatform := range []string{"linux-x86_64", "any-any"} {
			for _, phase := range []string{"prepare", "download"} {
				for _, state := range []string{"current", "changed", "missing", "other platform"} {
					t.Run(strings.Join([]string{consumerPlatform, dependencyPlatform, phase, state}, "/"), func(t *testing.T) {
						ctx := context.Background()
						f := releaseCacheFixture(t)
						local := localCacheFixture(t)
						local.Record.Key.OS, local.Record.Key.Arch, _ = strings.Cut(consumerPlatform, "-")
						local.Record.Archive.Name = local.Record.Key.archiveName()
						local.Record.DependencyFingerprints = map[string]string{"d@1": strings.Repeat("d", 64)}
						r := f.seed(t, local)
						source := syncCacheSource(r)
						source.Plan.Platforms[0].Description.Modules[0].Depends = []string{"d@1"}
						source.Plan.Platforms[0].Description.Modules = append(source.Plan.Platforms[0].Description.Modules,
							describedModule{Name: "d", Version: "1", PackagePlatform: dependencyPlatform})
						depName := "d-1-" + dependencyPlatform + ".tar.zst"
						source.Inputs.Fingerprints[depName] = strings.Repeat("d", 64)
						switch state {
						case "changed":
							source.Inputs.Fingerprints[depName] = strings.Repeat("e", 64)
						case "missing", "other platform":
							delete(source.Inputs.Fingerprints, depName)
							if state == "other platform" {
								source.Inputs.Fingerprints["d-1-windows-x86_64.tar.zst"] = strings.Repeat("d", 64)
							}
						}
						if phase == "prepare" {
							snapshot, err := prepareCISyncCache(ctx, f.client, source)
							if err != nil {
								t.Fatal(err)
							}
							if got := snapshot.RecordErrors[r.Archive.Name]; (got == "") != (state == "current") {
								t.Fatalf("candidate validation for %s: %q", state, got)
							}
							return
						}
						snapshot, err := f.client.Snapshot(ctx, false)
						if err != nil {
							t.Fatal(err)
						}
						snapshot.Inputs = source.Inputs
						asset, err := downloadCISyncCache(ctx, f.client, snapshot, r.Archive.Name, t.TempDir())
						if state != "current" {
							if err == nil {
								t.Fatal("download accepted a non-current dependency")
							}
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						data, err := os.ReadFile(asset.Path)
						if err != nil || string(data) != "verified archive bytes" {
							t.Fatalf("downloaded archive: %q, %v", data, err)
						}
					})
				}
			}
		}
	}
}

func TestCISyncCacheDelete(t *testing.T) {
	for _, fault := range []string{"deleted", "present", "unknown platforms", "unknown versions", "replacement", "partial retry", "record replaced"} {
		t.Run(fault, func(t *testing.T) {
			f := releaseCacheFixture(t)
			r := f.seed(t, localCacheFixture(t))
			source := syncCacheSource(r)
			unknown := f.add("unknown.tar.zst", []byte("preserve"), "uploaded")
			snapshot, err := prepareCISyncCache(context.Background(), f.client, source)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "deleted", "replacement", "partial retry", "record replaced":
				delete(source.Catalog.Packages, r.Key.Name)
			case "unknown platforms":
				p := source.Catalog.Packages[r.Key.Name]
				p.Versions[r.Key.Version] = sourceVersion{Error: "bad recipe"}
				source.Catalog.Packages[r.Key.Name] = p
			case "unknown versions":
				source.Catalog.Packages[r.Key.Name] = sourcePackage{Error: "unreadable"}
			}
			if fault == "replacement" {
				delete(f.assets, r.Archive.ID)
				f.add(r.Archive.Name, []byte("new"), "uploaded")
			}
			if fault == "partial retry" {
				delete(f.assets, r.Archive.ID)
			}
			if fault == "record replaced" {
				changed := r
				changed.Origin.RunID = "11"
				body, _ := json.Marshal(changed)
				f.setRecord(t, r.Archive.Name, body)
			}
			err = deleteCISyncCache(context.Background(), f.client, source, snapshot, r.Key)
			if fault == "deleted" || fault == "partial retry" {
				if err != nil {
					t.Fatal(err)
				}
				if len(f.assets) != 1 {
					t.Fatal("managed assets remain")
				}
			} else if err == nil {
				t.Fatal("unsafe delete accepted")
			}
			remote, e := f.client.Records.Snapshot(context.Background(), false)
			if e != nil {
				t.Fatal(e)
			}
			if fault == "deleted" || fault == "partial retry" {
				if remote.Records[r.Archive.Name+".publication.json"] != nil {
					t.Fatal("cache record survived successful deletion")
				}
			}
			if fault == "record replaced" {
				if _, exists := f.assets[r.Archive.ID]; !exists {
					t.Fatal("new selected record lost its archive")
				}
			}
			if _, ok := f.assets[unknown.ID]; !ok {
				t.Fatal("unknown asset deleted")
			}
		})
	}
}

func TestCISyncCacheDeleteReadsOnlySelectedRecord(t *testing.T) {
	f := releaseCacheFixture(t)
	r := f.seed(t, localCacheFixture(t))
	other := localCacheFixture(t)
	other.Record.Key.Name = "d"
	other.Record.Archive.Name = other.Record.Key.archiveName()
	f.seed(t, other)
	source := syncCacheSource(r)
	snapshot, err := prepareCISyncCache(context.Background(), f.client, source)
	if err != nil {
		t.Fatal(err)
	}
	delete(source.Catalog.Packages, r.Key.Name)
	// An unrelated malformed record must not prevent deletion of the selected identity.
	f.setRecord(t, other.Record.Archive.Name, []byte("invalid"))
	original := f.client.HTTP.Transport
	reads := []string{}
	f.client.HTTP.Transport = cacheTestRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Accept") == "application/octet-stream" {
			reads = append(reads, req.URL.Path)
		}
		return original.RoundTrip(req)
	})
	if err := deleteCISyncCache(context.Background(), f.client, source, snapshot, r.Key); err != nil {
		t.Fatal(err)
	}
	remote, e := f.client.Records.Snapshot(context.Background(), false)
	if e != nil || remote.Records[r.Archive.Name+".publication.json"] != nil {
		t.Fatal("selected record was not removed", e)
	}
	if len(reads) != 0 {
		t.Fatalf("deletion downloaded GitHub sidecars: %v", reads)
	}
	if len(f.assets) != 1 {
		t.Fatalf("unrelated archive changed: %v", f.assets)
	}
	for _, asset := range f.assets {
		if asset.Name != other.Record.Archive.Name {
			t.Fatal("unexpected remaining archive", asset)
		}
	}
}

func TestCISyncCacheDeleteFailureOrder(t *testing.T) {
	for _, endpoint := range []string{"github", "git"} {
		t.Run(endpoint, func(t *testing.T) {
			f := releaseCacheFixture(t)
			r := f.seed(t, localCacheFixture(t))
			source := syncCacheSource(r)
			snapshot, err := prepareCISyncCache(context.Background(), f.client, source)
			if err != nil {
				t.Fatal(err)
			}
			delete(source.Catalog.Packages, r.Key.Name)
			if endpoint == "github" {
				f.fault = "delete:" + r.Archive.Name
				f.status = 400
			} else {
				cacheGitFault(t, "reject", f.client.Records.URL, nil)
			}
			if err = deleteCISyncCache(context.Background(), f.client, source, snapshot, r.Key); err == nil {
				t.Fatal("unconfirmed deletion accepted")
			}
			remote, err := f.client.Records.Snapshot(context.Background(), false)
			if err != nil || remote.Records[r.Archive.Name+".publication.json"] == nil {
				t.Fatal("deletion failure lost ownership", err)
			}
			_, archivePresent := f.assets[r.Archive.ID]
			if archivePresent != (endpoint == "github") {
				t.Fatal("incorrect archive/record deletion order")
			}
			f.fault = ""
			if endpoint == "git" {
				t.Setenv("AUTOLIB_CI_GIT_FAULT", "")
			}
			if err = deleteCISyncCache(context.Background(), f.client, source, snapshot, r.Key); err != nil {
				t.Fatal("retry did not finish deletion", err)
			}
		})
	}
}
