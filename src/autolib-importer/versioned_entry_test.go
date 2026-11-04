package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func entryCache(f *entryFixture) string {
	return filepath.Join(f.source, "缓存 中文", "3rdparty")
}

func versionedEntryFixture(t *testing.T, fake bool) *entryFixture {
	t.Helper()
	f := newEntryFixture(t, fake, "producer 中文 空格${literal}")
	putTestFile(t, filepath.Join(entryCache(f), "autolib.cmake"), readTestFile(t, "../autolib.cmake"))
	copyEntryTool(t, f, entryCache(f))
	if fake {
		dir := cmakeEntryPackage(t, entryCache(f), "demo", "1.0", "", "")
		t.Setenv("AUTOLIB_TEST_PACKAGE_DIR", filepath.ToSlash(dir))
		t.Setenv("AUTOLIB_TEST_EXEC_LOG", filepath.Join(f.root, "executed-tool"))
	}
	versionedEntryConsumer(f, "", "demo@1.0", "")
	return f
}

func versionedEntryConsumer(f *entryFixture, setup, packages, tail string) {
	f.t.Helper()
	putTestFile(f.t, filepath.Join(f.source, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(versioned_entry NONE)
`+setup+`include([==[`+filepath.ToSlash(filepath.Join(entryCache(f), "autolib.cmake"))+`]==])
add_library(consumer INTERFACE)
autolib_import(TARGET consumer INTERFACE `+packages+`)
`+tail)
}

func cmakeEntryPackage(t *testing.T, root, name, version, dependencies, body string) string {
	t.Helper()
	dir := filepath.Join(root, name, version)
	header := strings.Replace(identityHeader(packageIdentity{name, version, "linux", "aarch64"}), `AUTOLIB_DEPENDENCIES ""`, `AUTOLIB_DEPENDENCIES "`+dependencies+`"`, 1)
	putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), header+body+"if(NOT TARGET autolib::"+name+")\nadd_library(autolib::"+name+" INTERFACE IMPORTED GLOBAL)\nendif()\n")
	return dir
}

func copyEntryTool(t *testing.T, f *entryFixture, root string) string {
	t.Helper()
	path := putTestFile(t, filepath.Join(root, f.toolAsset), readTestFile(t, f.tool))
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readonlyEntrySource(t *testing.T, root string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	entryDirectoryCleanup(t, root)
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0444)
		if info.IsDir() || info.Mode()&0111 != 0 {
			mode = 0555
		}
		return os.Chmod(path, mode)
	}); err != nil {
		t.Fatal(err)
	}
}

func entryDirectoryCleanup(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				_ = os.Chmod(path, 0755)
			}
			return err
		})
	})
}

func TestVersionedCMakeConflicts(t *testing.T) {
	for _, kind := range []string{"same version reused", "different version", "transitive different version"} {
		t.Run(kind, func(t *testing.T) {
			f := versionedEntryFixture(t, false)
			marker := filepath.ToSlash(filepath.Join(f.root, "second-adapter"))
			cmakeEntryPackage(t, entryCache(f), "demo", "1.0", "", "")
			cmakeEntryPackage(t, entryCache(f), "demo", "2", "", "file(WRITE [==["+marker+"]==] loaded)\n")
			cmakeEntryPackage(t, entryCache(f), "app", "1", "demo@2", "file(WRITE [==["+marker+"]==] loaded)\n")
			second := "demo@1.0"
			if kind == "different version" {
				second = "demo@2"
			}
			if kind == "transitive different version" {
				second = "app@1"
			}
			versionedEntryConsumer(f, "", "demo@1.0", "autolib_import(TARGET consumer INTERFACE "+second+")\n")
			out, err := f.configure()
			if (err == nil) != (kind == "same version reused") {
				t.Fatalf("conflict %s: %v\n%s", kind, err, out)
			}
			if err != nil && !strings.Contains(out, "version conflict") {
				t.Fatal("missing version conflict diagnostic", out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("second adapter executed before conflict", err)
			}
		})
	}
	t.Run("prepared result reuse", func(t *testing.T) {
		f := versionedEntryFixture(t, true)
		versionedEntryConsumer(f, "", "demo@1.0", "autolib_import(TARGET consumer INTERFACE demo demo@1.0)\n")
		f.mustConfigure()
		lines := strings.Split(strings.TrimSpace(readTestFile(t, filepath.Join(f.root, "calls.jsonl"))), "\n")
		if len(lines) != 1 {
			t.Fatalf("prepared result was not reused: %s", strings.Join(lines, "\n"))
		}
	})
}

func TestVersionedCMakeResultValidation(t *testing.T) {
	for _, kind := range []string{"valid", "wrong requested version", "outside cache", "relative directory", "wrong name header", "wrong version header", "wrong platform header", "missing header", "missing dependency", "wrong dependency version", "nonprecise dependency", "wrong order", "duplicate name", "invalid name", "invalid version", "reserved version", "symlink escape"} {
		t.Run(kind, func(t *testing.T) {
			f := versionedEntryFixture(t, true)
			cache := entryCache(f)
			if kind != "wrong requested version" {
				versionedEntryConsumer(f, "", "demo", "")
			}
			marker := filepath.Join(f.root, "adapter-executed")
			body := "file(APPEND [==[" + filepath.ToSlash(marker) + "]==] loaded)\n"
			leaf := cmakeEntryPackage(t, cache, "leaf", "2", "", body)
			demo := cmakeEntryPackage(t, cache, "demo", "1.0", "leaf@2", body)
			names, version, leafVersion := "leaf;demo", "1.0", "2"
			switch kind {
			case "wrong requested version":
				version = "2"
				demo = cmakeEntryPackage(t, cache, "demo", "2", "leaf@2", body)
			case "outside cache":
				demo = cmakeEntryPackage(t, filepath.Join(f.root, "external"), "demo", "1.0", "leaf@2", body)
			case "relative directory":
				demo = "demo/1.0"
			case "wrong name header", "wrong version header", "wrong platform header", "missing header":
				path := filepath.Join(demo, "autolib-import.cmake")
				header := readTestFile(t, path)
				switch kind {
				case "wrong name header":
					header = strings.Replace(header, `AUTOLIB_PACKAGE_NAME "demo"`, `AUTOLIB_PACKAGE_NAME "other"`, 1)
				case "wrong version header":
					header = strings.Replace(header, `AUTOLIB_PACKAGE_VERSION "1.0"`, `AUTOLIB_PACKAGE_VERSION "2"`, 1)
				case "wrong platform header":
					header = strings.Replace(header, `AUTOLIB_OS "linux"`, `AUTOLIB_OS "windows"`, 1)
				case "missing header":
					header = body
				}
				putTestFile(t, path, header)
			case "missing dependency":
				names = "demo"
			case "wrong dependency version":
				leafVersion = "3"
				leaf = cmakeEntryPackage(t, cache, "leaf", "3", "", body)
			case "nonprecise dependency":
				demo = cmakeEntryPackage(t, cache, "demo", "1.0", "leaf", body)
			case "wrong order":
				names = "demo;leaf"
			case "duplicate name":
				names = "leaf;demo;demo"
			case "invalid name":
				names = "leaf;demo;../other"
			case "invalid version":
				version = "../outside"
			case "reserved version":
				version = "NUL"
			case "symlink escape":
				outside := cmakeEntryPackage(t, filepath.Join(f.root, "external"), "demo", "1.0", "leaf@2", body)
				if err := os.Rename(demo, demo+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, demo); err != nil {
					t.Skipf("native symlinks unavailable: %v", err)
				}
			}
			result := fmt.Sprintf("set(AUTOLIB_PREPARED_PACKAGES [==[%s]==])\nset(AUTOLIB_PACKAGE_DIR_demo [==[%s]==])\nset(AUTOLIB_PACKAGE_VERSION_demo [==[%s]==])\nset(AUTOLIB_PACKAGE_DIR_leaf [==[%s]==])\nset(AUTOLIB_PACKAGE_VERSION_leaf [==[%s]==])\n", names, filepath.ToSlash(demo), version, filepath.ToSlash(leaf), leafVersion)
			t.Setenv("AUTOLIB_TEST_RESULT", result)
			out, err := f.configure()
			if (err == nil) != (kind == "valid") {
				t.Fatalf("validation %s: %v\n%s", kind, err, out)
			}
			_, markerErr := os.Stat(marker)
			if kind != "valid" && !os.IsNotExist(markerErr) {
				t.Fatal("invalid result executed an adapter", markerErr)
			}
			if kind == "valid" && markerErr != nil {
				t.Fatal("valid result never loaded adapters", markerErr)
			}
		})
	}
}
