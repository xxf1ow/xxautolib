package main

import (
	"archive/tar"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestCIPackageItem(t *testing.T) {
	for _, kind := range []string{"library", "importer", "unverified", "missing evidence", "wrong origin", "non publisher", "entry mismatch"} {
		t.Run(kind, func(t *testing.T) {
			f := transportInputFixture(t)
			if kind == "library" {
				var result ciPlatformResult
				file := filepath.Join(f.options.WorkRoot, "results", f.options.Platform+".json")
				if err := decodeStrictJSONFile(file, &result); err != nil {
					t.Fatal(err)
				}
				result.Complete = false
				result.Error = "another independent item failed"
				if err := writeCIJSON(file, result); err != nil {
					t.Fatal(err)
				}
			}
			writeCIFixture(t, f.options.SourceRoot, "src/dependencies/queue/package.toml", "latest='1'\n")
			writeCIFixture(t, f.options.SourceRoot, "src/dependencies/queue/1/module.toml", "# source version exists\n")
			r, _ := cacheRecordFixture()
			r.Key = ciCacheKey{Kind: "library", Name: "queue", Version: "1", OS: "any", Arch: "any"}
			r.Origin = f.identity
			r.Archive = ciCacheArchive{}
			r.Verification.Platform = f.options.Platform
			osName, arch := testPlatform()
			r.Verification.Context.OS = osName
			r.Verification.Context.Arch = arch
			if kind == "importer" || kind == "entry mismatch" {
				r.Kind = "importer"
				r.Key = ciCacheKey{Kind: "importer", Name: "autolib-importer", OS: osName, Arch: arch}
				r.LatestCandidate = ""
			}
			if kind == "unverified" {
				r.Verification.Passed = false
			}
			if kind == "missing evidence" {
				r.Verification.Evidence = nil
			}
			if kind == "wrong origin" {
				r.Origin.RunID = "999"
			}
			if kind == "entry mismatch" {
				writeCIFixture(t, f.options.WorkRoot, "install/autolib.cmake", "different")
			}
			if kind == "non publisher" {
				other := "linux-x86_64"
				if f.options.Platform == other {
					other = "windows-x86_64"
				}
				p := f.plan.Platforms[0]
				p.ID = other
				f.plan.Platforms = append(f.plan.Platforms, p)
				f.plan.Publishers = ciPublishers(f.plan.Platforms)
				if f.plan.Publishers["queue@1"] == f.options.Platform {
					t.Skip("native platform has publisher priority")
				}
				if err := writeCIJSON(f.options.PlanFile, f.plan); err != nil {
					t.Fatal(err)
				}
			}
			before := transportSnapshot(t, filepath.Join(f.options.WorkRoot, "install"))
			entry := mustReadText(t, filepath.Join(f.options.SourceRoot, "src/autolib.cmake"))
			origin := r.Origin
			asset, err := packageCIItem(context.Background(), f.options, r)
			if kind != "library" && kind != "importer" {
				if err == nil {
					t.Fatal("unqualified item packaged")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(asset.Path)
			if err != nil {
				t.Fatal(err)
			}
			if asset.Record.Origin != origin || !asset.Record.Verification.Passed || asset.Record.Archive.ID != 0 || asset.Record.Archive.SHA256 != testDigest(data) || asset.Record.Archive.Size != int64(len(data)) {
				t.Fatalf("final bytes or provenance changed: %+v", asset.Record)
			}
			if kind == "library" {
				if asset.Record.Archive.Name != "queue-1-any-any.tar.zst" || asset.Record.LatestCandidate != "1" {
					t.Fatal("wrong exact item")
				}
				z, err := zstd.NewReader(strings.NewReader(string(data)))
				if err != nil {
					t.Fatal(err)
				}
				defer z.Close()
				tr := tar.NewReader(z)
				found := false
				for {
					h, err := tr.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if h.Name == "queue/1/autolib-import.cmake" {
						body, _ := io.ReadAll(tr)
						found = strings.Contains(string(body), `set(AUTOLIB_PACKAGE_VERSION "1")`)
					}
				}
				if !found {
					t.Fatal("package lacks exact identity")
				}
			} else if asset.Record.Entry == nil || string(asset.Record.Entry.Bytes) != entry || asset.Record.LatestCandidate != "" {
				t.Fatal("tool lost raw entry")
			}
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(transportSnapshot(t, filepath.Join(f.options.WorkRoot, "install")))
			if string(a) != string(b) || mustReadText(t, filepath.Join(f.options.SourceRoot, "src/autolib.cmake")) != entry {
				t.Fatal("packaging modified inputs")
			}
			if err := validateCILocalCacheAsset(asset, f.options.Repository); err != nil {
				t.Fatal(err)
			}
		})
	}
}
