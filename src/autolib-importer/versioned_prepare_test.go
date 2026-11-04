package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func versionedFixture(t *testing.T, requests ...string) (prepareOptions, string) {
	t.Helper()
	o := localGraphFixture(t)
	o.Packages = requests
	cache := o.CacheRoot
	return o, cache
}

func versionedInstalled(t *testing.T, root, name, version, dependencies, data string) string {
	t.Helper()
	directory := filepath.Join(root, name, version)
	header := strings.Replace(identityHeader(packageIdentity{name, version, "linux", "aarch64"}), `set(AUTOLIB_DEPENDENCIES "")`, `set(AUTOLIB_DEPENDENCIES "`+dependencies+`")`, 1)
	putTestFile(t, filepath.Join(directory, "autolib-import.cmake"), header)
	putTestFile(t, filepath.Join(directory, "data"), data)
	return directory
}

func versionedArchive(t *testing.T, root, name, version, dependencies string) string {
	t.Helper()
	header := strings.Replace(identityHeader(packageIdentity{name, version, "linux", "aarch64"}), `set(AUTOLIB_DEPENDENCIES "")`, `set(AUTOLIB_DEPENDENCIES "`+dependencies+`")`, 1)
	prefix := name + "/" + version + "/"
	archive := testArchive(t, []archiveMember{{Name: prefix + "autolib-import.cmake", Body: header}, {Name: prefix + "data", Body: "archive"}})
	return putTestFile(t, filepath.Join(root, name+"-"+version+"-linux-aarch64.tar.zst"), readTestFile(t, archive))
}

func versionedLatest(t *testing.T, root, name, version string) {
	t.Helper()
	putTestFile(t, filepath.Join(root, name, "package.toml"), "latest = \""+version+"\"\n")
}

func requireNoResult(t *testing.T, o prepareOptions) {
	t.Helper()
	if _, err := os.Lstat(o.ResultPath); !os.IsNotExist(err) {
		t.Fatalf("failed prepare exposed result: %v", err)
	}
}

func TestVersionedCacheLifecycle(t *testing.T) {
	t.Run("failed closure preserves existing root", func(t *testing.T) {
		o, cache := versionedFixture(t, "demo")
		versionedLatest(t, cache, "demo", "1")
		versionedLatest(t, o.CacheRoot, "demo", "1")
		versionedInstalled(t, o.CacheRoot, "demo", "1", "missing@7", "cache")
		if _, err := prepare(o); err == nil {
			t.Fatal("accepted incomplete closure")
		}
		if got := readTestFile(t, filepath.Join(cache, "demo/1/data")); got != "cache" {
			t.Fatal("failed closure changed existing root")
		}
		requireNoResult(t, o)
	})
	t.Run("offline cache and exact requests", func(t *testing.T) {
		o, cache := versionedFixture(t, "demo")
		versionedInstalled(t, cache, "demo", "1", "leaf@2", "cache")
		versionedInstalled(t, cache, "leaf", "2", "", "leaf")
		versionedLatest(t, cache, "demo", "1")
		o.RemoteRoot = ""
		got, err := prepare(o)
		if err != nil || len(got) != 2 {
			t.Fatalf("offline cache: %+v %v", got, err)
		}
		o.Packages = []string{"leaf@2"}
		if _, err := prepare(o); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(cache, "leaf/package.toml")); !os.IsNotExist(err) {
			t.Fatal("exact request registered latest", err)
		}
	})
	for _, kind := range []string{"missing latest", "latest missing artifact", "bad target", "failed dependency", "stale result"} {
		t.Run(kind, func(t *testing.T) {
			o, cache := versionedFixture(t, "demo")
			dir := versionedInstalled(t, cache, "demo", "1", "", "cached other version")
			versionedInstalled(t, o.CacheRoot, "demo", "2", "missing@7", "cache")
			if kind != "missing latest" {
				versionedLatest(t, o.CacheRoot, "demo", "2")
			}
			if kind == "latest missing artifact" {
				versionedLatest(t, o.CacheRoot, "demo", "99")
			}
			if kind == "bad target" {
				versionedLatest(t, cache, "demo", "1")
				putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), "broken")
			}
			if kind == "stale result" {
				putTestFile(t, o.ResultPath, "previous successful result")
			}
			if _, err := prepare(o); err == nil {
				t.Fatal("accepted unavailable root or closure")
			}
			requireNoResult(t, o)
			if kind != "bad target" && kind != "missing latest" {
				if latest, err := readPackageLatest(cache, "demo"); err != nil || latest != map[string]string{"missing latest": "", "latest missing artifact": "99", "failed dependency": "2", "stale result": "2"}[kind] {
					t.Fatal("failed root registered latest", err)
				}
			}
		})
	}
}

func TestVersionedExactClosure(t *testing.T) {
	t.Run("only precise missing dependency", func(t *testing.T) {
		o, cache := versionedFixture(t, "app")
		versionedInstalled(t, cache, "app", "1", "leaf@2", "root remains")
		versionedLatest(t, cache, "app", "1")
		versionedInstalled(t, o.CacheRoot, "leaf", "2", "", "cache")
		versionedLatest(t, o.CacheRoot, "leaf", "3")
		versionedInstalled(t, o.CacheRoot, "leaf", "3", "", "cache")
		got, err := prepare(o)
		if err != nil || len(got) != 2 || got[0].Name != "leaf" || got[0].Version != "2" || got[1].Name != "app" || got[1].Version != "1" {
			t.Fatalf("%+v %v", got, err)
		}
		if latest, err := readPackageLatest(cache, "leaf"); err != nil || latest != "3" {
			t.Fatal("dependency latest registered", err)
		}
		if readTestFile(t, filepath.Join(cache, "app/1/data")) != "root remains" {
			t.Fatal("root refreshed")
		}
		result := readTestFile(t, o.ResultPath)
		if !strings.Contains(result, "AUTOLIB_PACKAGE_VERSION_leaf [[2]]") || !strings.Contains(result, "AUTOLIB_PREPARED_PACKAGES [[leaf;app]]") || strings.Contains(result, "VERSION_leaf@") {
			t.Fatal(result)
		}
	})
	t.Run("diamond conflict reports both chains", func(t *testing.T) {
		o, _ := versionedFixture(t, "app@1")
		for _, p := range [][3]string{{"app", "1", "left@1;right@1"}, {"left", "1", "leaf@2"}, {"right", "1", "leaf@3"}, {"leaf", "2", ""}, {"leaf", "3", ""}} {
			versionedInstalled(t, o.CacheRoot, p[0], p[1], p[2], "cache")
		}
		_, err := prepare(o)
		if err == nil {
			t.Fatal("accepted conflict")
		}
		for _, fragment := range []string{"app@1 -> left@1 -> leaf@2", "app@1 -> right@1 -> leaf@3"} {
			if !strings.Contains(err.Error(), fragment) {
				t.Fatalf("missing %q: %v", fragment, err)
			}
		}
		requireNoResult(t, o)
	})
	t.Run("cycle", func(t *testing.T) {
		o, _ := versionedFixture(t, "app@1")
		versionedInstalled(t, o.CacheRoot, "app", "1", "leaf@2", "cache")
		versionedInstalled(t, o.CacheRoot, "leaf", "2", "app@1", "cache")
		if _, err := prepare(o); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatal(err)
		}
	})
}

func sourceSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var records []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content := ""
		if info.Mode().IsRegular() {
			content = readTestFile(t, path)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			content, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		records = append(records, fmt.Sprintf("%s %s %d %x", rel, info.Mode(), info.ModTime().UnixNano(), sha256.Sum256([]byte(content))))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func TestVersionedCacheCandidates(t *testing.T) {
	for _, kind := range []string{"complete", "bad metadata", "wrong identity", "missing metadata", "case version"} {
		t.Run(kind, func(t *testing.T) {
			o, cache := versionedFixture(t, "demo@1.0")
			dir := versionedInstalled(t, cache, "demo", "1.0", "", "cache")
			switch kind {
			case "bad metadata":
				putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), "broken")
			case "wrong identity":
				putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), identityHeader(packageIdentity{"demo", "1.0", "windows", "x86_64"}))
			case "missing metadata":
				if err := os.Remove(filepath.Join(dir, "autolib-import.cmake")); err != nil {
					t.Fatal(err)
				}
			case "case version":
				versionedInstalled(t, cache, "demo", "A", "", "upper")
				versionedInstalled(t, cache, "demo", "a", "", "lower")
				entries, err := os.ReadDir(filepath.Join(cache, "demo"))
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 3 {
					t.Skip("host cannot represent case-colliding versions")
				}
			}
			before := sourceSnapshot(t, cache)
			got, err := prepare(o)
			if (err == nil) != (kind == "complete") {
				t.Fatalf("%+v %v", got, err)
			}
			if kind == "complete" && (len(got) != 1 || got[0] != (preparedPackage{Name: "demo", Version: "1.0", Directory: filepath.Join(cache, "demo", "1.0")})) {
				t.Fatal(got)
			}
			if err != nil {
				requireNoResult(t, o)
			}
			if !slices.Equal(before, sourceSnapshot(t, cache)) {
				t.Fatal("prepare changed authoritative cache")
			}
		})
	}
}
func TestVersionedReadonlyInstallation(t *testing.T) {
	for _, kind := range []string{"complete", "missing dependency"} {
		t.Run(kind, func(t *testing.T) {
			o, cache := versionedFixture(t, "demo")
			versionedInstalled(t, cache, "demo", "1", "leaf@2", "keep")
			versionedLatest(t, cache, "demo", "1")
			if kind == "complete" {
				versionedInstalled(t, cache, "leaf", "2", "", "leaf")
			}
			readonlyEntrySource(t, cache)
			before := sourceSnapshot(t, cache)
			_, err := prepare(o)
			if (err == nil) != (kind == "complete") {
				t.Fatal(err)
			}
			if !slices.Equal(before, sourceSnapshot(t, cache)) {
				t.Fatal("consumption changed installed inputs")
			}
			if err != nil {
				requireNoResult(t, o)
			}
		})
	}
}
