package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func nativeLoopCMakeArgs(t *testing.T) []string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return nil
	}
	return []string{"-DCMAKE_OSX_ARCHITECTURES=arm64", "-DCMAKE_OSX_DEPLOYMENT_TARGET=15.0",
		"-DCMAKE_C_COMPILER=" + strings.TrimSpace(loopCommand(t, "xcrun", "--find", "clang")),
		"-DCMAKE_CXX_COMPILER=" + strings.TrimSpace(loopCommand(t, "xcrun", "--find", "clang++")),
		"-DCMAKE_OSX_SYSROOT=" + strings.TrimSpace(loopCommand(t, "xcrun", "--sdk", "macosx", "--show-sdk-path")),
		"-DCMAKE_MAKE_PROGRAM=" + strings.TrimSpace(loopCommand(t, "sh", "-c", "command -v make"))}
}

func TestDarwinEntryIdentity(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Darwin ARM64")
	}
	f := newEntryFixture(t, false, "")
	path := filepath.Join(f.source, "demo/1.0/autolib-import.cmake")
	marker := filepath.Join(f.root, "adapter-ran")
	body := identityHeader(packageIdentity{"demo", "1.0", "darwin", "aarch64"}) + "file(WRITE [==[" + filepath.ToSlash(marker) + "]==] ran)\nadd_library(autolib::demo INTERFACE IMPORTED GLOBAL)\n"
	putTestFile(t, path, body)
	if out, err := f.configure("-DCMAKE_SYSTEM_NAME=Darwin", "-DCMAKE_SYSTEM_PROCESSOR=arm64", "-DCMAKE_OSX_ARCHITECTURES=arm64"); err != nil {
		t.Fatalf("Darwin entry: %v\n%s", err, out)
	}
	if readTestFile(t, marker) != "ran" {
		t.Fatal("adapter not loaded")
	}
	for _, header := range []string{identityHeader(packageIdentity{"demo", "1.0", "linux", "aarch64"}), "broken metadata\n"} {
		if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		putTestFile(t, path, header+"file(WRITE [==["+filepath.ToSlash(marker)+"]==] ran)\n")
		if out, err := f.configure("-DCMAKE_SYSTEM_NAME=Darwin", "-DCMAKE_SYSTEM_PROCESSOR=arm64", "-DCMAKE_OSX_ARCHITECTURES=arm64"); err == nil {
			t.Fatal("invalid cached identity accepted", out)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("adapter ran before metadata validation")
		}
	}
	for _, arch := range []string{"x86_64", "arm64;x86_64"} {
		putTestFile(t, path, body)
		if out, err := f.configure("-DCMAKE_SYSTEM_NAME=Darwin", "-DCMAKE_SYSTEM_PROCESSOR=arm64", "-DCMAKE_OSX_ARCHITECTURES="+arch); err == nil {
			t.Fatal("unsupported target accepted", out)
		}
	}
}
