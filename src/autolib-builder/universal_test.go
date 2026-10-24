package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUniversalIdentity(t *testing.T) {
	path := filepath.Join("..", "dependencies", "queue", "module.toml")
	for _, platform := range [][2]string{{"Linux", "AMD64"}, {"Linux", "aarch64"}, {"Windows", "x64"}} {
		m, err := loadDefinition(path, platform[0], platform[1])
		if err != nil {
			t.Fatal(err)
		}
		if !m.PlatformIndependent {
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
	if err := os.Remove(filepath.Join(o.InstallRoot, "queue/include/input.h")); err != nil {
		t.Fatal(err)
	}
	module := "name='queue'\ntype='copy'\nplatform_independent=true\n" +
		"[resources.readerwriterqueue]\nGIT_REPOSITORY='fixture'\nGIT_TAG='fixed'\n" +
		"[resources.concurrentqueue]\nGIT_REPOSITORY='fixture'\nGIT_TAG='fixed'\n" +
		"[[copy]]\nresource='readerwriterqueue'\nfiles=['atomicops.h','readerwriterqueue.h','readerwritercircularbuffer.h']\ndestination='include/queue'\n" +
		"[[copy]]\nresource='concurrentqueue'\nfiles=['concurrentqueue.h','lightweightsemaphore.h','blockingconcurrentqueue.h']\ndestination='include/queue'\n"
	putFile(t, filepath.Join(o.SourceRoot, "src/dependencies/queue/module.toml"), module)
	putFile(t, filepath.Join(o.SourceRoot, "src/dependencies/queue/autolib-import.cmake"), "add_library(AutoLib::queue INTERFACE IMPORTED GLOBAL)\n")
	want := map[string]string{
		"atomicops.h": "readerwriterqueue", "readerwriterqueue.h": "readerwriterqueue", "readerwritercircularbuffer.h": "readerwriterqueue",
		"concurrentqueue.h": "concurrentqueue", "lightweightsemaphore.h": "concurrentqueue", "blockingconcurrentqueue.h": "concurrentqueue",
	}
	for name, resource := range want {
		body := "fixed source bytes: " + name
		putFile(t, filepath.Join(o.SourceRoot, "git/_resources/queue", resource, name), body)
		putFile(t, filepath.Join(o.InstallRoot, "queue/include/queue", name), body)
	}
	for _, target := range [][2]string{{"windows", "x86_64"}, {"linux", "x86_64"}, {"linux", "aarch64"}} {
		o.OS, o.Arch, o.Modules = target[0], target[1], []string{"queue"}
		if err := packageRelease(o); err != nil {
			t.Fatal(err)
		}
		asset := filepath.Join(o.OutputRoot, "queue-any-any.tar.zst")
		if _, err := os.Stat(asset); err != nil {
			t.Fatal(err)
		}
		staged, err := stagePackage(stageOptions{SourceRoot: o.SourceRoot, InstallRoot: o.InstallRoot, StageRoot: filepath.Join(o.SourceRoot, "build/universal-stage", target[0]+"-"+target[1]), Name: "queue", OS: target[0], Arch: target[1], CMakeCommand: o.CMakeCommand})
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
		requireTextFile(t, filepath.Join(staged, "autolib-import.cmake"), "set(AUTOLIB_PACKAGE_NAME \"queue\")\nset(AUTOLIB_OS \"any\")\nset(AUTOLIB_ARCH \"any\")\nset(AUTOLIB_DEPENDENCIES \"\")\nadd_library(AutoLib::queue INTERFACE IMPORTED GLOBAL)\n")
	}
	o.OS, o.Arch = "linux", "x86_64"
	putFile(t, filepath.Join(o.InstallRoot, "queue/generated/platform.cmake"), "platform generated")
	if err := packageRelease(o); err == nil || !strings.Contains(err.Error(), "six declared headers") {
		t.Fatalf("accepted extra universal installation file: %v", err)
	}
}
