package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type cacheRunTestFixture struct {
	installedFixture
	release *releaseCacheTestFixture
}

func cacheRunFixture(t *testing.T) cacheRunTestFixture {
	t.Helper()
	f := cacheRunTestFixture{installedFixture: installedCIFixture(t), release: releaseCacheFixture(t)}
	f.release.client.Repository = f.options.Repository
	cmake := mustReadText(t, filepath.Join(f.options.SourceRoot, "tests/autolib/CMakeLists.txt"))
	cmake = strings.Replace(cmake, `if(ROOT_PACKAGES STREQUAL "eigen@3")`, `if(ROOT_PACKAGES STREQUAL "dep@1")
 target_compile_definitions(autolib_smoke PRIVATE EXPECTED_DEP=1)
elseif(ROOT_PACKAGES STREQUAL "eigen@3")`, 1)
	writeCIFixture(t, f.options.SourceRoot, "tests/autolib/CMakeLists.txt", cmake)
	smoke := mustReadText(t, filepath.Join(f.options.SourceRoot, "tests/autolib/smoke.cpp"))
	smoke = strings.Replace(smoke, "#ifdef EXPECTED_EIGEN", "#ifdef EXPECTED_DEP\n static_assert(FIXTURE_DEP==1);\n#elif defined(EXPECTED_EIGEN)", 1)
	writeCIFixture(t, f.options.SourceRoot, "tests/autolib/smoke.cpp", smoke)
	f.snapshot(t)
	return f
}

func (f *cacheRunTestFixture) snapshot(t *testing.T) {
	t.Helper()
	f.options.CacheFile = filepath.Join(f.options.SourceRoot, "cache.json")
	if err := prepareCICacheWithClient(context.Background(), ciCachePrepareOptions{Repository: f.options.Repository, SourceRoot: f.options.SourceRoot, PlanFile: f.options.PlanFile, RunIdentityFile: f.options.RunIdentityFile, Output: f.options.CacheFile}, f.release.client); err != nil {
		t.Fatal(err)
	}
}

func (f *cacheRunTestFixture) runLayer(layer string) error {
	o := f.options
	o.Layer = layer
	return runCILayerWithCache(context.Background(), o, f.release.client)
}

func (f *cacheRunTestFixture) result(t *testing.T) ciPlatformResult {
	t.Helper()
	var r ciPlatformResult
	if err := decodeStrictJSONFile(filepath.Join(f.options.WorkRoot, "results", f.options.Platform+".json"), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCICacheRunIsolation(t *testing.T) {
	for _, failure := range []string{"build", "verify", "package", "store", "dependent"} {
		t.Run(failure, func(t *testing.T) {
			f := cacheRunFixture(t)
			switch failure {
			case "build":
				if err := os.Remove(filepath.Join(f.options.SourceRoot, "git/dep/1/header.h")); err != nil {
					t.Fatal(err)
				}
			case "verify":
				file := filepath.Join(f.options.SourceRoot, "src/dependencies/dep/1/autolib-import.cmake")
				writeCIFixture(t, filepath.Dir(file), filepath.Base(file), strings.Replace(mustReadText(t, file), "FIXTURE_DEP=1", "FIXTURE_DEP=999", 1))
			case "package":
				file := filepath.Join(f.options.SourceRoot, "src/autolib-importer/fixup-metadata.cmake")
				writeCIFixture(t, filepath.Dir(file), filepath.Base(file), mustReadText(t, file)+"\nif(PACKAGE_DIR MATCHES \"/dep/1$\")\nmessage(FATAL_ERROR \"controlled dep packaging failure\")\nendif()\n")
			case "store":
				f.release.fault = "upload:dep-1-any-any.tar.zst.publication.json"
				f.release.status = 400
			case "dependent":
				file := filepath.Join(f.options.SourceRoot, "src/dependencies/eigen/3/autolib-import.cmake")
				writeCIFixture(t, filepath.Dir(file), filepath.Base(file), strings.Replace(mustReadText(t, file), "FIXTURE_EIGEN=3", "FIXTURE_EIGEN=999", 1))
			}
			f.snapshot(t)
			for _, layer := range []string{"prepare", "layer0", "layer1"} {
				if err := f.runLayer(layer); err != nil {
					t.Fatalf("%s stopped independent work: %v", layer, err)
				}
			}
			if err := f.runLayer("verify"); err == nil {
				t.Fatal("partial failure reported success")
			}
			r := f.result(t)
			if !r.Processed || r.Complete {
				t.Fatalf("dishonest platform terminal result: %+v", r)
			}
			statuses := map[string]string{}
			for _, item := range r.Packages {
				statuses[item.Slot.Name+"@"+item.Slot.Version] = item.Status
				if item.Status == "dependency_failed" && len(item.DependencyChain) < 2 {
					t.Fatal("missing dependency chain")
				}
			}
			if statuses["queue@1"] != "stored" {
				t.Fatalf("independent package lost: %v", statuses)
			}
			if failure == "dependent" {
				if statuses["eigen@3"] != "failed" || statuses["eigen@5"] != "stored" || statuses["dep@1"] != "stored" {
					t.Fatalf("failure spread across independent versions: %v", statuses)
				}
			} else if statuses["dep@1"] != "failed" || statuses["eigen@3"] != "dependency_failed" || statuses["eigen@5"] != "dependency_failed" {
				t.Fatalf("wrong dependency failure scope: %v", statuses)
			}
			s, err := f.release.client.Snapshot(context.Background(), false)
			if err != nil || s.Records["queue-1-any-any.tar.zst"].Archive.ID == 0 {
				t.Fatalf("healthy product not retained: %v", err)
			}
			if err := finalizeCI(f.plan, f.options.Repository, f.identity, []ciPlatformResult{r}); err == nil {
				t.Fatal("finalize accepted partial failure")
			}
		})
	}
}

func TestCICacheRunReuse(t *testing.T) {
	f := cacheRunFixture(t)
	for _, layer := range []string{"prepare", "layer0", "layer1", "verify"} {
		if err := f.runLayer(layer); err != nil {
			t.Fatalf("first %s: %v", layer, err)
		}
	}
	first := f.result(t)
	if err := finalizeCI(f.plan, f.options.Repository, f.identity, []ciPlatformResult{first}); err != nil {
		t.Fatal(err)
	}
	f.identity.RunID = "124"
	f.identity.Attempt = "2"
	f.options.WorkRoot = filepath.Join(f.options.SourceRoot, "build/ci/124-2", f.options.Platform)
	f.options.ImporterFile = filepath.Join(f.options.WorkRoot, "install", ciImporterExecutable(f.options.Platform))
	if err := writeCIJSON(f.options.RunIdentityFile, f.identity); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.options.SourceRoot, "git"), filepath.Join(f.options.SourceRoot, "disabled-upstreams")); err != nil {
		t.Fatal(err)
	}
	f.snapshot(t)
	writes := len(f.release.ops)
	for _, layer := range []string{"prepare", "layer0", "layer1", "verify"} {
		if err := f.runLayer(layer); err != nil {
			t.Fatalf("reused %s: %v", layer, err)
		}
	}
	second := f.result(t)
	if !second.Complete || !second.Processed || second.Importer.Status != "reused" {
		t.Fatalf("cache hit failed: %+v", second)
	}
	for _, item := range second.Packages {
		if item.Status != "reused" || item.Record == nil || item.Record.Origin != first.Run {
			t.Fatalf("reuser rewrote producer: %+v", item)
		}
	}
	if len(f.release.ops) != writes {
		t.Fatal("cache hit rewrote release")
	}
	for _, name := range []string{"packaged", "logs/layer0/1/build.log", "logs/layer1/1/build.log"} {
		if _, err := os.Stat(filepath.Join(f.options.WorkRoot, name)); !os.IsNotExist(err) {
			t.Fatalf("cache hit rebuilt/repackaged: %s", name)
		}
	}
}

func TestCICacheRunPlanningErrors(t *testing.T) {
	f := cacheRunFixture(t)
	writeCIFixture(t, f.options.SourceRoot, "src/dependencies/eigen/package.toml", "latest='deleted'\n")
	platform, selection := f.options.Platform, "eigen@3,eigen@5,queue@1"
	var err error
	f.plan, err = planCI(context.Background(), ciPlanOptions{SourceRoot: f.options.SourceRoot, BuilderFile: filepath.Join(f.options.SourceRoot, "tools/builder"), Platforms: &platform, Modules: &selection})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.plan.Platforms[0].FailedRoots) != 2 {
		t.Fatalf("dangling latest not reported: %+v", f.plan)
	}
	if err := writeCIJSON(f.options.PlanFile, f.plan); err != nil {
		t.Fatal(err)
	}
	f.snapshot(t)
	for _, layer := range []string{"prepare", "layer0"} {
		if err := f.runLayer(layer); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.runLayer("verify"); err == nil || !strings.Contains(err.Error(), "eigen@3") {
		t.Fatal("missing named planning failure:", err)
	}
	r := f.result(t)
	if !r.Processed || r.Complete || len(r.Packages) != 1 || r.Packages[0].Status != "stored" {
		t.Fatalf("healthy work lost: %+v", r)
	}
	if err := finalizeCI(f.plan, f.options.Repository, f.identity, []ciPlatformResult{r}); err == nil || !strings.Contains(err.Error(), "eigen@3") {
		t.Fatal("finalize lost planning error:", err)
	}
}

func TestCICacheRunCredentials(t *testing.T) {
	f := cacheRunFixture(t)
	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITEE_TOKEN"} {
		t.Setenv(key, "controlled-secret")
	}
	check := `foreach(key GITHUB_TOKEN GH_TOKEN GITEE_TOKEN)
 if(DEFINED ENV{${key}})
  message(FATAL_ERROR "credential reached child process")
 endif()
endforeach()
`
	for _, name := range []string{"src/autolib-builder/prepare-tools.cmake", "tests/autolib/CMakeLists.txt", "src/dependencies/dep/1/autolib-import.cmake", "src/autolib-importer/fixup-metadata.cmake"} {
		file := filepath.Join(f.options.SourceRoot, name)
		writeCIFixture(t, f.options.SourceRoot, name, check+mustReadText(t, file))
	}
	f.snapshot(t)
	for _, layer := range []string{"prepare", "layer0", "layer1", "verify"} {
		if err := f.runLayer(layer); err != nil {
			t.Fatalf("%s: %v", layer, err)
		}
	}
	if !f.result(t).Complete {
		t.Fatal("credential isolation blocked cache delivery")
	}
}

func TestCICacheRunNonpublisher(t *testing.T) {
	f := cacheRunFixture(t)
	other := "linux-x86_64"
	if f.options.Platform == other {
		t.Skip("fixture requires a platform below Linux x86 publisher")
	}
	p := f.plan.Platforms[0]
	p.ID = other
	f.plan.Platforms = append(f.plan.Platforms, p)
	for key := range f.plan.Publishers {
		f.plan.Publishers[key] = other
	}
	if err := writeCIJSON(f.options.PlanFile, f.plan); err != nil {
		t.Fatal(err)
	}
	f.snapshot(t)
	for _, layer := range []string{"prepare", "layer0", "layer1", "verify"} {
		if err := f.runLayer(layer); err != nil {
			t.Fatal(err)
		}
	}
	r := f.result(t)
	for _, item := range r.Packages {
		if item.Status != "local_ready" || item.Record != nil || !item.Verified {
			t.Fatalf("nonpublisher delivery: %+v", item)
		}
	}
	s, err := f.release.client.Snapshot(context.Background(), false)
	if err != nil || len(s.Records) != 1 {
		t.Fatalf("nonpublisher uploaded libraries: %+v %v", s.Records, err)
	}
	if err := finalizeCI(f.plan, f.options.Repository, f.identity, []ciPlatformResult{r}); err == nil {
		t.Fatal("missing publisher accepted")
	}
}

func TestCICacheRunCancellation(t *testing.T) {
	f := cacheRunFixture(t)
	if err := f.runLayer("prepare"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := f.options
	o.Layer = "layer0"
	if err := runCILayerWithCache(ctx, o, f.release.client); err == nil {
		t.Fatal("cancelled work succeeded")
	}
	r := f.result(t)
	if r.Processed || r.Complete || len(r.Packages) != 0 {
		t.Fatal("cancel fabricated package results", r)
	}
}

func nextCacheRun(t *testing.T, f *cacheRunTestFixture) {
	t.Helper()
	if err := os.RemoveAll(f.options.WorkRoot); err != nil {
		t.Fatal(err)
	}
	f.identity.RunID = "124"
	f.identity.Attempt = "2"
	f.options.WorkRoot = filepath.Join(f.options.SourceRoot, "build/ci/124-2", f.options.Platform)
	f.options.ImporterFile = filepath.Join(f.options.WorkRoot, "install", ciImporterExecutable(f.options.Platform))
	if err := writeCIJSON(f.options.RunIdentityFile, f.identity); err != nil {
		t.Fatal(err)
	}
	f.snapshot(t)
}

func runCacheFixture(t *testing.T, f *cacheRunTestFixture) {
	t.Helper()
	for _, layer := range []string{"prepare", "layer0", "layer1", "verify"} {
		if err := f.runLayer(layer); err != nil {
			t.Fatalf("%s: %v", layer, err)
		}
	}
	if err := finalizeCI(f.plan, f.options.Repository, f.identity, []ciPlatformResult{f.result(t)}); err != nil {
		t.Fatal(err)
	}
}

func TestCICacheRunPackagingInputChange(t *testing.T) {
	f := cacheRunFixture(t)
	runCacheFixture(t, &f)
	first := f.result(t)
	file := "src/autolib-importer/fixup-metadata.cmake"
	writeCIFixture(t, f.options.SourceRoot, file, mustReadText(t, filepath.Join(f.options.SourceRoot, file))+"\n# changed packaging input\n")
	nextCacheRun(t, &f)
	runCacheFixture(t, &f)
	for i, item := range f.result(t).Packages {
		if item.Status != "stored" || !item.Built || item.Record.Fingerprint == first.Packages[i].Record.Fingerprint {
			t.Fatal("packaging input reused stale library", item)
		}
	}
}

func TestCICacheRunRestoredCopyDependency(t *testing.T) {
	for _, sentinel := range []bool{true, false} {
		t.Run(map[bool]string{true: "sentinel", false: "without sentinel"}[sentinel], func(t *testing.T) {
			f := cacheRunFixture(t)
			if sentinel {
				file := "src/dependencies/dep/1/module.toml"
				writeCIFixture(t, f.options.SourceRoot, file, "sentinel='include/header.h'\n"+mustReadText(t, filepath.Join(f.options.SourceRoot, file)))
				f.snapshot(t)
			}
			runCacheFixture(t, &f)
			original := f.result(t).Packages[0]
			file := "src/dependencies/eigen/3/autolib-import.cmake"
			writeCIFixture(t, f.options.SourceRoot, file, mustReadText(t, filepath.Join(f.options.SourceRoot, file))+"\n# changed dependent only\n")
			if err := os.RemoveAll(filepath.Join(f.options.SourceRoot, "git/dep")); err != nil {
				t.Fatal(err)
			}
			nextCacheRun(t, &f)
			runCacheFixture(t, &f)
			for _, item := range f.result(t).Packages {
				if item.Slot.Name == "eigen" && item.Slot.Version == "3" {
					if item.Status != "stored" || !item.Built {
						t.Fatal("dependent did not rebuild", item)
					}
				} else if item.Status != "reused" || item.Built {
					t.Fatal("unchanged dependency rebuilt", item)
				}
				if item.Slot.Name == "dep" && (item.Record.Origin != original.Record.Origin || item.Record.Archive != original.Record.Archive) {
					t.Fatal("restored dependency producer changed", item)
				}
			}
			if _, err := os.Stat(filepath.Join(f.options.SourceRoot, "git/dep")); !os.IsNotExist(err) {
				t.Fatal("restored dependency tried to prepare upstream", err)
			}
		})
	}
}
