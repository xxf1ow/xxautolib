package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRestoreLocalPackage(t *testing.T) {
	id := packageIdentity{Name: "b", Version: "1", OS: "linux", Arch: "x86_64"}
	for _, kind := range []string{"valid", "existing target", "wrong identity", "wrong dependencies", "wrong digest", "wrong size", "escape", "duplicate", "symlink escape", "truncated", "result overwrites archive", "symlink parent"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			cache := filepath.Join(root, "install")
			keep := putTestFile(t, filepath.Join(cache, "unrelated/keep"), "preserve")
			members := []archiveMember{{Name: "b/1/autolib-import.cmake", Body: identityHeader(id)}, {Name: "b/1/include/b.h", Body: "header"}}
			switch kind {
			case "wrong identity":
				members[0].Body = identityHeader(packageIdentity{Name: "other", Version: "1", OS: id.OS, Arch: id.Arch})
			case "wrong dependencies":
				members[0].Body = strings.Replace(identityHeader(id), `set(AUTOLIB_DEPENDENCIES "")`, `set(AUTOLIB_DEPENDENCIES "dep@1")`, 1)
			case "escape":
				members = append(members, archiveMember{Name: "b/1/../../unrelated/keep", Body: "bad"})
			case "duplicate":
				members = append(members, members[1])
			case "symlink escape":
				members = append(members, archiveMember{Name: "b/1/link", Kind: tar.TypeSymlink, Link: "../../unrelated/keep"})
			case "existing target":
				putTestFile(t, filepath.Join(cache, "b/1/keep"), "preserve target")
			case "symlink parent":
				if err := os.Symlink(t.TempDir(), filepath.Join(cache, "b")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			archive := testArchive(t, members)
			data, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "truncated" {
				data = data[:len(data)/2]
				if err := os.WriteFile(archive, data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			sum := sha256.Sum256(data)
			o := restoreOptions{ArchivePath: archive, CacheRoot: cache, Name: id.Name, Version: id.Version, OS: id.OS, Arch: id.Arch, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data)), ResultPath: filepath.Join(root, "result.cmake"), Dependencies: []string{}}
			if kind == "wrong digest" {
				o.SHA256 = strings.Repeat("0", 64)
			}
			if kind == "wrong size" {
				o.Size++
			}
			if kind == "result overwrites archive" {
				o.ResultPath = archive
			}
			got, err := restoreLocalPackage(o)
			if kind == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				if got.Name != "b" || got.Version != "1" || got.Directory != filepath.Join(cache, "b/1") {
					t.Fatalf("restored identity: %+v", got)
				}
				if _, err := prepare(prepareOptions{CacheRoot: cache, OS: id.OS, Arch: id.Arch, ResultPath: filepath.Join(root, "consume.cmake"), Packages: []string{"b@1"}}); err != nil {
					t.Fatalf("offline consumption: %v", err)
				}
				if _, err := os.Stat(filepath.Join(cache, "b/package.toml")); !os.IsNotExist(err) {
					t.Fatal("restore rewrote latest")
				}
			} else if err == nil {
				t.Fatal("unsafe or mismatched restore accepted")
			}
			if readTestFile(t, keep) != "preserve" {
				t.Fatal("unrelated cache changed")
			}
			if kind == "existing target" && readTestFile(t, filepath.Join(cache, "b/1/keep")) != "preserve target" {
				t.Fatal("target overwritten")
			}
			after, err := os.ReadFile(archive)
			if err != nil || !bytes.Equal(data, after) {
				t.Fatal("source archive changed")
			}
		})
	}
}

func TestRestoreLocalPackageCLI(t *testing.T) {
	id := packageIdentity{Name: "b", Version: "1", OS: "any", Arch: "any"}
	archive := testArchive(t, []archiveMember{{Name: "b/1/autolib-import.cmake", Body: identityHeader(id)}})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	root := t.TempDir()
	args := []string{"restore", "--archive", archive, "--cache-root", filepath.Join(root, "install"), "--name", "b", "--version", "1", "--os", "any", "--arch", "any", "--sha256", hex.EncodeToString(sum[:]), "--size", strconv.Itoa(len(data)), "--result", filepath.Join(root, "restored.cmake")}
	var stdout, stderr bytes.Buffer
	if code := run(append(append([]string{}, args...), "--remote-root=https://invalid.example"), &stdout, &stderr); code == 0 {
		t.Fatal("restore accepted a remote option")
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("restore CLI failed: %s", &stderr)
	}
	if !strings.Contains(readTestFile(t, filepath.Join(root, "restored.cmake")), "AUTOLIB_PACKAGE_VERSION_b [[1]]") {
		t.Fatal("missing exact result")
	}
}
