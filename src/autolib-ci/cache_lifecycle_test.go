package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCICacheLifecycle(t *testing.T) {
	f := cacheRunFixture(t)
	producer := filepath.Join(f.options.SourceRoot, "build/queue-producer")
	writeCIFixture(t, producer, "CMakeLists.txt", "cmake_minimum_required(VERSION 3.20)\nproject(queue_payload CXX)\nadd_library(queue_payload STATIC value.cpp)\n")
	writeCIFixture(t, producer, "value.cpp", "int fixture_queue_value() {return 73;}\n")
	config, err := loadCIEnvironment(filepath.Join(f.options.SourceRoot, ".github/ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	production, err := ciProductionForPlatform(f.options.Platform, config)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-S", producer, "-B", filepath.Join(producer, "build"), "-DCMAKE_BUILD_TYPE=Release"}
	for _, key := range slices.Sorted(maps.Keys(production.CMakeDefaults)) {
		args = append(args, "-D"+key+"="+production.CMakeDefaults[key])
	}
	for i, command := range [][]string{args, {"--build", filepath.Join(producer, "build"), "--parallel", "2"}} {
		if err := ciCommand(context.Background(), f.options.CMakeCommand, command, f.options.SourceRoot, nil, filepath.Join(producer, fmt.Sprintf("%d.log", i))); err != nil {
			t.Fatal(err)
		}
	}
	library, err := os.ReadFile(filepath.Join(producer, "build/libqueue_payload.a"))
	if err != nil {
		t.Fatal(err)
	}
	writeCIFixture(t, f.options.SourceRoot, "git/queue/1/libqueue_payload.a", string(library))
	recipe := "src/dependencies/queue/1/module.toml"
	writeCIFixture(t, f.options.SourceRoot, recipe, strings.Replace(mustReadText(t, filepath.Join(f.options.SourceRoot, recipe)), "package_platforms=['any-any']", "package_platforms=['"+f.options.Platform+"']", 1)+"\n[[copy]]\nfiles=['libqueue_payload.a']\ndestination='lib'\n")
	adapter := "src/dependencies/queue/1/autolib-import.cmake"
	writeCIFixture(t, f.options.SourceRoot, adapter, strings.Replace(mustReadText(t, filepath.Join(f.options.SourceRoot, adapter)), "add_library(autolib::queue INTERFACE IMPORTED GLOBAL)", "add_library(autolib::queue STATIC IMPORTED GLOBAL)\nset_target_properties(autolib::queue PROPERTIES IMPORTED_LOCATION_RELEASE \"${CMAKE_CURRENT_LIST_DIR}/lib/libqueue_payload.a\")", 1))
	consumer := "tests/autolib/CMakeLists.txt"
	writeCIFixture(t, f.options.SourceRoot, consumer, mustReadText(t, filepath.Join(f.options.SourceRoot, consumer))+"\nif(ROOT_PACKAGES STREQUAL \"queue@1\")\nfile(GENERATE OUTPUT \"${CMAKE_BINARY_DIR}/link-inputs-$<CONFIG>.txt\" CONTENT \"$<TARGET_FILE:autolib::queue>\\n\")\nendif()\n")
	smoke := "tests/autolib/smoke.cpp"
	writeCIFixture(t, f.options.SourceRoot, smoke, "extern int fixture_queue_value();\n"+strings.Replace(mustReadText(t, filepath.Join(f.options.SourceRoot, smoke)), `std::puts("queue=1");`, `std::puts("queue=1"); if(fixture_queue_value()!=73) return 1;`, 1))
	platform, selection := f.options.Platform, "eigen@3,eigen@5,queue@1"
	f.plan, err = planCI(context.Background(), ciPlanOptions{SourceRoot: f.options.SourceRoot, BuilderFile: filepath.Join(f.options.SourceRoot, "tools/builder"), Platforms: &platform, Modules: &selection})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCIJSON(f.options.PlanFile, f.plan); err != nil {
		t.Fatal(err)
	}

	runAll := func() error {
		t.Helper()
		for _, layer := range []string{"prepare", "layer0", "layer1", "verify"} {
			if err := f.runLayer(layer); err != nil {
				return err
			}
		}
		r := f.result(t)
		return finalizeCI(f.plan, f.options.Repository, f.identity, []ciPlatformResult{r})
	}
	items := func() map[string]ciSlotResult {
		result := map[string]ciSlotResult{}
		for _, item := range f.result(t).Packages {
			result[item.Slot.Name+"@"+item.Slot.Version] = item
		}
		return result
	}
	next := func(number string) {
		t.Helper()
		old := f.options.WorkRoot
		if err := os.RemoveAll(old); err != nil {
			t.Fatal(err)
		}
		f.identity.RunID = number
		f.identity.Attempt = "2"
		f.options.WorkRoot = filepath.Join(f.options.SourceRoot, "build/ci", number+"-2", f.options.Platform)
		f.options.ImporterFile = filepath.Join(f.options.WorkRoot, "install", ciImporterExecutable(f.options.Platform))
		if err := writeCIJSON(f.options.RunIdentityFile, f.identity); err != nil {
			t.Fatal(err)
		}
		f.snapshot(t)
	}
	if err := os.Remove(filepath.Join(f.options.SourceRoot, "git/dep/1/header.h")); err != nil {
		t.Fatal(err)
	}
	f.snapshot(t)
	if err := runAll(); err == nil {
		t.Fatal("partial failure reported success")
	}
	first := items()
	if first["dep@1"].Status != "failed" || first["eigen@3"].Status != "dependency_failed" || first["eigen@5"].Status != "dependency_failed" || first["queue@1"].Status != "stored" {
		t.Fatalf("first result: %+v", first)
	}
	originalD := *first["queue@1"].Record
	writeCIFixture(t, f.options.SourceRoot, "git/dep/1/header.h", "#pragma once\n")
	if err := os.RemoveAll(filepath.Join(f.options.SourceRoot, "git/queue")); err != nil {
		t.Fatal(err)
	}
	command := ciExternalCommand(context.Background(), "git", "-C", f.options.SourceRoot, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "next source snapshot")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("new source: %v %s", err, out)
	}
	output, err := ciExternalCommand(context.Background(), "git", "-C", f.options.SourceRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	f.identity.SHA = strings.TrimSpace(string(output))
	f.identity.CheckoutSHA = f.identity.SHA
	next("124")
	if err := runAll(); err != nil {
		t.Fatal("independent retry did not complete:", err)
	}
	second := items()
	d := second["queue@1"]
	if d.Status != "reused" || d.Built || d.Record.Origin != originalD.Origin || d.Record.Archive.SHA256 != originalD.Archive.SHA256 {
		t.Fatal("cached producer or bytes rewritten", d)
	}
	for _, name := range []string{"packaged/" + originalD.Archive.Name, "logs/layer0/2/build.log"} {
		if _, err := os.Stat(filepath.Join(f.options.WorkRoot, name)); !os.IsNotExist(err) {
			t.Fatal("cache hit rebuilt or repackaged", name)
		}
	}
	inputs := mustReadText(t, filepath.Join(d.EvidenceDirectory, "build/link-inputs-Release.txt"))
	if !strings.Contains(inputs, filepath.ToSlash(filepath.Join(f.options.WorkRoot, "install/queue/1/lib/libqueue_payload.a"))) {
		t.Fatal("consumer did not link the restored machine code", inputs)
	}
	if log := mustReadText(t, filepath.Join(d.EvidenceDirectory, "test.log")); !strings.Contains(log, "100% tests passed") {
		t.Fatal("restored code did not run", log)
	}
	depAdapter := "src/dependencies/dep/1/autolib-import.cmake"
	writeCIFixture(t, f.options.SourceRoot, depAdapter, mustReadText(t, filepath.Join(f.options.SourceRoot, depAdapter))+"\n# changed same-version recipe input\n")
	next("125")
	if err := runAll(); err != nil {
		t.Fatal(err)
	}
	third := items()
	for _, ref := range []string{"dep@1", "eigen@3", "eigen@5"} {
		if third[ref].Status != "stored" || !third[ref].Built || third[ref].Record.Fingerprint == second[ref].Record.Fingerprint {
			t.Fatal("missing recursive invalidation", ref, third[ref])
		}
	}
	if third["queue@1"].Status != "reused" || third["queue@1"].Record.Archive != originalD.Archive {
		t.Fatal("unrelated cached product changed")
	}
	writeCIFixture(t, f.options.SourceRoot, "src/dependencies/eigen/package.toml", "latest='5'\n")
	next("126")
	if err := runAll(); err != nil {
		t.Fatal(err)
	}
	for _, item := range items() {
		if item.Status != "reused" {
			t.Fatal("latest-only rebuilt a library", item)
		}
	}
	oldTool := *f.result(t).Importer.Record
	entry := "src/autolib.cmake"
	writeCIFixture(t, f.options.SourceRoot, entry, mustReadText(t, filepath.Join(f.options.SourceRoot, entry))+"\n# entry-only change\n")
	next("127")
	if err := runAll(); err != nil {
		t.Fatal(err)
	}
	last := f.result(t)
	if last.Importer.Status != "stored" || last.Importer.Record.Fingerprint == oldTool.Fingerprint || last.Importer.Record.Entry.SHA256 == oldTool.Entry.SHA256 {
		t.Fatal("entry-only did not invalidate tool cache")
	}
	for _, item := range last.Packages {
		if item.Status != "reused" {
			t.Fatal("entry-only rebuilt a library", item)
		}
	}
}
