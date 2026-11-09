package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestUniversalIdentity(t *testing.T) {
	ref, err := resolveModuleRef(filepath.Join("..", ".."), moduleRef{Name: "queue"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "dependencies", "queue", ref.Version, "module.toml")
	for _, platform := range [][2]string{{"Linux", "AMD64"}, {"Linux", "aarch64"}, {"Windows", "x64"}} {
		m, err := loadDefinition(path, platform[0], platform[1])
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(m.PackagePlatforms, "any-any") {
			t.Fatal("queue is not explicitly platform independent")
		}
		os, arch := packagePlatform(m, "linux", "x86_64")
		if os != "any" || arch != "any" {
			t.Fatalf("package platform for %s: %s/%s", platform[0]+"/"+platform[1], os, arch)
		}
	}
}

func TestUniversalPackageAcrossTargets(t *testing.T) {
	o := releaseFixture(t)
	if err := os.Remove(filepath.Join(o.InstallRoot, "queue/1.0/include/input.h")); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(o.InstallRoot, "queue/1.0/autolib-import.cmake"), "set(AUTOLIB_PACKAGE_NAME \"queue\")\nset(AUTOLIB_PACKAGE_VERSION \"1.0\")\nset(AUTOLIB_OS \"any\")\nset(AUTOLIB_ARCH \"any\")\nset(AUTOLIB_DEPENDENCIES \"\")\nadd_library(autolib::queue INTERFACE IMPORTED GLOBAL)\n")
	want := map[string]string{
		"atomicops.h": "readerwriterqueue", "readerwriterqueue.h": "readerwriterqueue", "readerwritercircularbuffer.h": "readerwriterqueue",
		"concurrentqueue.h": "concurrentqueue", "lightweightsemaphore.h": "concurrentqueue", "blockingconcurrentqueue.h": "concurrentqueue",
	}
	for name, resource := range want {
		body := "fixed source bytes: " + name
		putFile(t, filepath.Join(o.SourceRoot, "git/_resources/queue", resource, name), body)
		putFile(t, filepath.Join(o.InstallRoot, "queue/1.0/include/queue", name), body)
	}
	for _, target := range [][2]string{{"windows", "x86_64"}, {"linux", "x86_64"}, {"linux", "aarch64"}, {"darwin", "aarch64"}} {
		o.OS, o.Arch, o.Modules = target[0], target[1], []string{"queue"}
		if err := packageRelease(o); err != nil {
			t.Fatal(err)
		}
		asset := filepath.Join(o.OutputRoot, "queue-1.0-any-any.tar.zst")
		if _, err := os.Stat(asset); err != nil {
			t.Fatal(err)
		}
		staged, err := stagePackage(stageOptions{SourceRoot: o.SourceRoot, InstallRoot: o.InstallRoot, StageRoot: filepath.Join(o.SourceRoot, "build/universal-stage", target[0]+"-"+target[1]), Name: "queue", Version: "1.0", OS: target[0], Arch: target[1], CMakeCommand: o.CMakeCommand})
		if err != nil {
			t.Fatal(err)
		}
		for name, resource := range want {
			got, err := os.ReadFile(filepath.Join(staged, "include/queue", name))
			if err != nil {
				t.Fatal(err)
			}
			fixed, err := os.ReadFile(filepath.Join(o.SourceRoot, "git/_resources/queue", resource, name))
			if err != nil || string(got) != string(fixed) {
				t.Fatalf("header bytes differ for %s: %v", name, err)
			}
		}
		requireTextFile(t, filepath.Join(staged, "autolib-import.cmake"), "set(AUTOLIB_PACKAGE_NAME \"queue\")\nset(AUTOLIB_PACKAGE_VERSION \"1.0\")\nset(AUTOLIB_OS \"any\")\nset(AUTOLIB_ARCH \"any\")\nset(AUTOLIB_DEPENDENCIES \"\")\nadd_library(autolib::queue INTERFACE IMPORTED GLOBAL)\n")
	}
}
