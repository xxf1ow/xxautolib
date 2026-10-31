package main

import (
	"archive/tar"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	if fake {
		dir := cmakeEntryPackage(t, entryCache(f), "demo", "1.0", "", "")
		t.Setenv("AUTOLIB_TEST_PACKAGE_DIR", filepath.ToSlash(dir))
		t.Setenv("AUTOLIB_TEST_EXEC_LOG", filepath.Join(f.root, "executed-tool"))
	}
	versionedEntryConsumer(f, "set(AUTOLIB_LOCAL_ROOT \"${FIXTURE_RELEASE}\")\n", "demo@1.0", "")
	return f
}

func versionedEntryConsumer(f *entryFixture, setup, packages, tail string) {
	f.t.Helper()
	putTestFile(f.t, filepath.Join(f.source, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(versioned_entry NONE)
`+setup+`set(AUTOLIB_REMOTE_ROOT "")
include([==[`+filepath.ToSlash(filepath.Join(entryCache(f), "autolib.cmake"))+`]==])
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

func TestVersionedCMakeLocalBootstrap(t *testing.T) {
	for _, kind := range []string{"cache priority", "bare priority", "archive", "valid digest", "missing", "corrupt", "truncated", "multiple", "absolute", "traversal", "symlink", "missing program", "bad digest", "malformed digest"} {
		t.Run(kind, func(t *testing.T) {
			f := versionedEntryFixture(t, true)
			versionedEntryConsumer(f, "set(AUTOLIB_LOCAL_ROOT \"${FIXTURE_RELEASE}\")\n", "demo", "")
			bare := filepath.Join(f.release, f.toolAsset)
			body := readTestFile(t, f.importerArchive(f.toolAsset, readTestFile(t, bare)))
			success := slices.Contains([]string{"cache priority", "bare priority", "archive", "valid digest"}, kind)
			switch kind {
			case "cache priority":
				copyEntryTool(t, f, entryCache(f))
				putTestFile(t, bare, "invalid lower priority bare tool")
				body = "invalid lower priority archive"
			case "bare priority":
				body = "invalid lower priority archive"
			case "corrupt":
				body = "not an archive"
			case "truncated":
				body = body[:len(body)/2]
			case "multiple", "absolute", "traversal", "symlink", "missing program":
				members := []archiveMember{{Name: f.toolAsset, Body: readTestFile(t, bare)}}
				switch kind {
				case "multiple":
					members = append(members, archiveMember{Name: "other-program", Body: "bad"})
				case "absolute":
					members[0].Name = "/" + f.toolAsset
				case "traversal":
					members[0].Name = "../" + f.toolAsset
				case "symlink":
					members[0] = archiveMember{Name: f.toolAsset, Link: "../outside", Kind: tar.TypeSymlink}
				case "missing program":
					members[0].Name = "other-program"
				}
				body = readTestFile(t, testArchive(t, members))
			}
			archive := filepath.Join(f.release, f.toolAsset+".tar.xz")
			if kind != "missing" {
				putTestFile(t, archive, body)
			}
			if kind == "valid digest" || kind == "bad digest" || kind == "malformed digest" {
				digest := fmt.Sprintf("%x\n", sha256.Sum256([]byte(body)))
				if kind == "bad digest" {
					digest = strings.Repeat("0", 64) + "\n"
				}
				if kind == "malformed digest" {
					digest = "not sha256\n"
				}
				putTestFile(t, archive+".sha256", digest)
			}
			if kind != "cache priority" && kind != "bare priority" {
				if err := os.Remove(bare); err != nil {
					t.Fatal(err)
				}
			}
			readonlyEntrySource(t, f.release)
			before := sourceSnapshot(t, f.release)
			t.Setenv("PATH", t.TempDir())
			out, err := f.configure()
			if (err == nil) != success {
				t.Fatalf("bootstrap %s: %v\n%s", kind, err, out)
			}
			if !slices.Equal(before, sourceSnapshot(t, f.release)) {
				t.Fatal("bootstrap wrote read-only source")
			}
			cachedTool := filepath.Join(entryCache(f), f.toolAsset)
			if success {
				if got := readTestFile(t, filepath.Join(f.root, "executed-tool")); got != filepath.ToSlash(cachedTool) {
					t.Fatalf("executed %q, want cache program %q", got, cachedTool)
				}
				if runtime.GOOS == "linux" {
					info, err := os.Stat(cachedTool)
					if err != nil || info.Mode()&0111 == 0 {
						t.Fatal("cached tool is not executable", err)
					}
				}
			} else {
				if !strings.Contains(out, "importer") {
					t.Fatal("missing bootstrap diagnostic", out)
				}
				if _, err := os.Lstat(cachedTool); !os.IsNotExist(err) {
					t.Fatal("failed bootstrap committed tool", err)
				}
				if _, err := os.Stat(filepath.Join(f.root, "executed-tool")); !os.IsNotExist(err) {
					t.Fatal("invalid archive executed tool", err)
				}
			}
		})
	}
}

func TestVersionedCMakeCacheOnly(t *testing.T) {
	t.Run("first preparation and source-free reuse", func(t *testing.T) {
		f := versionedEntryFixture(t, false)
		cmakeEntryPackage(t, f.release, "demo", "1.0", "leaf@2", "")
		cmakeEntryPackage(t, f.release, "leaf", "2", "", "")
		readonlyEntrySource(t, f.release)
		entryDirectoryCleanup(t, entryCache(f))
		before := sourceSnapshot(t, f.release)
		checks := `if(NOT AUTOLIB_PREPARED_PACKAGES STREQUAL "leaf;demo" OR NOT AUTOLIB_PACKAGE_VERSION_demo STREQUAL "1.0" OR NOT AUTOLIB_PACKAGE_VERSION_leaf STREQUAL "2")
message(FATAL_ERROR "missing precise closure variables")
endif()
get_property(cache GLOBAL PROPERTY _AUTOLIB_CACHE_ROOT)
if(NOT AUTOLIB_PACKAGE_DIR_demo STREQUAL "${cache}/demo/1.0")
message(FATAL_ERROR "package did not use entry cache")
endif()
autolib_import(TARGET consumer INTERFACE demo@1.0)
`
		versionedEntryConsumer(f, "set(AUTOLIB_LOCAL_ROOT \"${FIXTURE_RELEASE}\")\n", "demo@1.0", checks)
		t.Setenv("PATH", t.TempDir())
		f.mustConfigure()
		if !slices.Equal(before, sourceSnapshot(t, f.release)) {
			t.Fatal("first preparation changed source")
		}
		versionedLatest(t, entryCache(f), "demo", "1.0")
		versionedEntryConsumer(f, "set(AUTOLIB_LOCAL_ROOT \"\")\n", "demo", checks)
		f.mustConfigure()
	})
	t.Run("local source defaults disabled", func(t *testing.T) {
		f := versionedEntryFixture(t, false)
		versionedEntryConsumer(f, "", "demo@1.0", "")
		if out, err := f.configure(); err == nil || !strings.Contains(out, "importer") {
			t.Fatalf("default source used producer: %v\n%s", err, out)
		}
	})
	for _, kind := range []string{"same physical root", "relative physical root", "symlink physical root"} {
		t.Run(kind, func(t *testing.T) {
			f := versionedEntryFixture(t, true)
			root := entryCache(f)
			if kind == "relative physical root" {
				var err error
				root, err = filepath.Rel(f.source, root)
				if err != nil {
					t.Fatal(err)
				}
			}
			if kind == "symlink physical root" {
				root = filepath.Join(f.root, "cache alias")
				if err := os.Symlink(entryCache(f), root); err != nil {
					t.Skipf("native symlinks unavailable: %v", err)
				}
			}
			before := sourceSnapshot(t, entryCache(f))
			versionedEntryConsumer(f, "set(AUTOLIB_LOCAL_ROOT [==["+filepath.ToSlash(root)+"]==])\n", "demo@1.0", "")
			out, err := f.configure()
			if err == nil || !strings.Contains(out, "same") {
				t.Fatalf("cache alias accepted: %v\n%s", err, out)
			}
			if !slices.Equal(before, sourceSnapshot(t, entryCache(f))) {
				t.Fatal("alias check wrote cache")
			}
		})
	}
}

func TestVersionedCMakeConflicts(t *testing.T) {
	for _, kind := range []string{"same version reused", "different version", "transitive different version"} {
		t.Run(kind, func(t *testing.T) {
			f := versionedEntryFixture(t, false)
			marker := filepath.ToSlash(filepath.Join(f.root, "second-adapter"))
			cmakeEntryPackage(t, f.release, "demo", "1.0", "", "")
			cmakeEntryPackage(t, f.release, "demo", "2", "", "file(WRITE [==["+marker+"]==] loaded)\n")
			cmakeEntryPackage(t, f.release, "app", "1", "demo@2", "file(WRITE [==["+marker+"]==] loaded)\n")
			second := "demo@1.0"
			if kind == "different version" {
				second = "demo@2"
			}
			if kind == "transitive different version" {
				second = "app@1"
			}
			versionedEntryConsumer(f, "set(AUTOLIB_LOCAL_ROOT \"${FIXTURE_RELEASE}\")\n", "demo@1.0", "autolib_import(TARGET consumer INTERFACE "+second+")\n")
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
		versionedEntryConsumer(f, "set(AUTOLIB_LOCAL_ROOT \"${FIXTURE_RELEASE}\")\n", "demo@1.0", "autolib_import(TARGET consumer INTERFACE demo demo@1.0)\n")
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
				versionedEntryConsumer(f, "set(AUTOLIB_LOCAL_ROOT \"${FIXTURE_RELEASE}\")\n", "demo", "")
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
