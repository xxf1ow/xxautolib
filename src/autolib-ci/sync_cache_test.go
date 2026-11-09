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
				for id, a := range f.assets {
					if strings.HasSuffix(a.Name, ".publication.json") {
						delete(f.assets, id)
					}
				}
			case "stale":
				source.Inputs.Fingerprints[r.Archive.Name] = strings.Repeat("b", 64)
			case "origin", "ID":
				if fault == "origin" {
					r.Origin.Repository = "fake/repo"
				} else {
					r.Archive.ID++
				}
				for id, a := range f.assets {
					if strings.HasSuffix(a.Name, ".publication.json") {
						data, _ := json.Marshal(r)
						f.data[id] = data
						a.Size = int64(len(data))
						a.Digest = "sha256:" + testDigest(data)
						f.assets[id] = a
					}
				}
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

func TestCISyncCacheDelete(t *testing.T) {
	for _, fault := range []string{"deleted", "present", "unknown platforms", "unknown versions", "replacement", "partial retry"} {
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
			case "deleted", "replacement", "partial retry":
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
				for id, a := range f.assets {
					if strings.HasSuffix(a.Name, ".publication.json") {
						delete(f.assets, id)
					}
				}
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
			if _, ok := f.assets[unknown.ID]; !ok {
				t.Fatal("unknown asset deleted")
			}
		})
	}
}
