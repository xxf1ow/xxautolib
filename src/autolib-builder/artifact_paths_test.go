package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionedArtifactPathIdentity(t *testing.T) {
	paths := []struct{ name, path string }{
		{"install", "3rdparty/demo/v1/keep"},
		{"main source", "git/demo/v1/.git/keep"},
		{"resource source", "git/_resources/demo/v1/extra/.git/keep"},
		{"intermediate", "build/ep/demo/v1/keep"},
		{"generated module", "build/generated/demo/v1.cmake"},
		{"dependency setup", "build/generated/_dependency-setup/demo/v1/dependency-setup.cmake"},
	}
	for _, tc := range paths {
		t.Run(tc.name, func(t *testing.T) {
			f := newEntryFixture(t)
			f.definitionVersion("dep", "1", "cmake", testSource)
			f.definitionVersion("demo", "V1", "cmake", "depends=['dep@1']\n"+testSource+"[resources.extra]\nGIT_REPOSITORY='unused-resource'\nGIT_TAG='fixed'\n")
			protected := putFile(t, filepath.Join(f.root, tc.path), "preserve existing version\n")
			err := generate(f.context, []string{"demo@V1"})
			requireFile(t, protected, "preserve existing version\n")
			if err == nil || !strings.Contains(err.Error(), "case") || !strings.Contains(err.Error(), "v1") {
				t.Fatalf("expected case alias rejection for %s, got %v", tc.path, err)
			}
			if _, err := os.Stat(filepath.Join(f.context.BuildRoot, "generated/inputs.cmake")); !os.IsNotExist(err) {
				t.Fatalf("rejection wrote the generated manifest: %v", err)
			}
		})
	}
	t.Run("unrelated colliding versions", func(t *testing.T) {
		f := newEntryFixture(t)
		f.definitionVersion("demo", "V1", "cmake", testSource)
		first := putFile(t, filepath.Join(f.root, "3rdparty/demo/Other/keep"), "upper")
		second := putFile(t, filepath.Join(f.root, "3rdparty/demo/other/keep"), "lower")
		entries, err := os.ReadDir(filepath.Join(f.root, "3rdparty/demo"))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Skip("filesystem cannot represent case-colliding directories")
		}
		if err := generate(f.context, []string{"demo@V1"}); err == nil {
			t.Error("accepted a package with colliding artifact versions")
		}
		requireFile(t, first, "upper")
		requireFile(t, second, "lower")
	})
	t.Run("new and exact paths with other versions", func(t *testing.T) {
		f := newEntryFixture(t)
		f.definitionVersion("dep", "1", "cmake", testSource)
		f.definitionVersion("demo", "V1", "cmake", "depends=['dep@1']\n"+testSource+"[resources.extra]\nGIT_REPOSITORY='unused-resource'\nGIT_TAG='fixed'\n")
		for _, tc := range paths {
			putFile(t, filepath.Join(f.root, strings.ReplaceAll(tc.path, "v1", "V2")), "another version")
		}
		if err := generate(f.context, []string{"demo@V1"}); err != nil {
			t.Fatal(err)
		}
		for _, tc := range paths {
			if tc.name != "generated module" {
				putFile(t, filepath.Join(f.root, strings.ReplaceAll(tc.path, "v1", "V1")), "exact version")
			}
		}
		if err := generate(f.context, []string{"demo@V1"}); err != nil {
			t.Fatal(err)
		}
		for _, tc := range paths {
			requireFile(t, filepath.Join(f.root, strings.ReplaceAll(tc.path, "v1", "V2")), "another version")
		}
	})
	t.Run("direct installation preparation", func(t *testing.T) {
		f := newFixture(t)
		f.definitionVersion("demo", "V1", "cmake", testSource)
		putFile(t, filepath.Join(f.root, "src/dependencies/demo/V1/autolib-import.cmake"), "# adapter\n")
		protected := putFile(t, filepath.Join(f.root, "3rdparty/demo/v1/keep"), "preserve installation")
		for _, phase := range []string{"begin", "installed", "reuse"} {
			err := prepareConsumption(consumptionOptions{SourceRoot: f.root, InstallRoot: filepath.Join(f.root, "3rdparty"), Name: "demo", Version: "V1", OS: "linux", Arch: "x86_64", Phase: phase})
			if err == nil || !strings.Contains(err.Error(), "case") {
				t.Errorf("%s: expected case alias rejection, got %v", phase, err)
			}
			requireFile(t, protected, "preserve installation")
		}
	})
}

func TestVersionedArtifactPathIdentityEntry(t *testing.T) {
	for _, version := range []string{"v1", "V1"} {
		t.Run("source-only cached "+version, func(t *testing.T) {
			f := newEntryFixture(t)
			f.definitionVersion("demo", "V1", "copy", "sentinel='include/input.h'\n"+testSource+"[[copy]]\nfiles=['input.h']\ndestination='include'\n")
			protected := putFile(t, filepath.Join(f.root, "3rdparty/demo", version, "include/input.h"), "cached payload")
			out, err := f.entry("-DBUILD_LIST=demo@V1")
			if version == "v1" {
				if err == nil || !strings.Contains(out, "case") {
					t.Fatalf("root entry accepted case alias: %v\n%s", err, out)
				}
			} else {
				if err != nil || !strings.Contains(out, "[demo@V1] cached") {
					t.Fatalf("exact cached path was not reused: %v\n%s", err, out)
				}
				f.build()
				requireFile(t, filepath.Join(f.root, "3rdparty/demo/package.toml"), "latest='V1'\n")
				if _, err := os.Stat(filepath.Join(f.root, "3rdparty/demo/V1/autolib-import.cmake")); !os.IsNotExist(err) {
					t.Fatalf("source-only reuse required an adapter: %v", err)
				}
			}
			requireFile(t, protected, "cached payload")
		})
	}
	for _, resource := range []bool{false, true} {
		t.Run(map[bool]string{false: "main Git", true: "resource Git"}[resource], func(t *testing.T) {
			f := newEntryFixture(t)
			source := f.repository("demo", map[string]string{"input.h": "persistent payload"})
			body := "[source]\n" + source + "[[copy]]\nfiles=['input.h']\ndestination='include'\n"
			base := "git/demo"
			if resource {
				body = "[resources.extra]\n" + source + "[[copy]]\nresource='extra'\nfiles=['input.h']\ndestination='include'\n"
				base = "git/_resources/demo"
			}
			f.definitionVersion("demo", "V1", "copy", body)
			alias := filepath.Join(f.root, base, "v1")
			clone := alias
			if resource {
				clone = filepath.Join(alias, "extra")
			}
			f.must(f.context.GitExecutable, "clone", filepath.Join(f.root, "upstream/demo"), clone)
			marker := putFile(t, filepath.Join(clone, "input.h"), "local modification")
			out, err := f.entry("-DBUILD_LIST=demo@V1")
			requireFile(t, marker, "local modification")
			if err == nil || !strings.Contains(out, "case") {
				t.Fatalf("root entry accepted Git version alias: %v\n%s", err, out)
			}
			if err := os.Rename(alias, filepath.Join(f.root, base, "V1")); err != nil {
				t.Fatal(err)
			}
			f.mustEntry("-DBUILD_LIST=demo@V1")
			f.build()
			requireFile(t, filepath.Join(f.root, "3rdparty/demo/V1/include/input.h"), "local modification")
		})
	}
}
