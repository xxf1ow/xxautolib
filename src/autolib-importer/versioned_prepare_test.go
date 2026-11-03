package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func versionedFixture(t *testing.T, requests ...string) (prepareOptions, string) {
	t.Helper()
	o := localGraphFixture(t)
	o.Packages = requests
	cache := filepath.Join(filepath.Dir(o.WorkRoot), "cache")
	o.CacheRoot, o.CMakeCommand = cache, "cmake"
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

func TestVersionedLocalPriority(t *testing.T) {
	for _, kind := range []string{"cache", "installation", "archive", "bad cache", "bad installation", "wrong identity", "incomplete", "case version", "case archive"} {
		t.Run(kind, func(t *testing.T) {
			o, cache := versionedFixture(t, "demo@1.0")
			archive := versionedArchive(t, o.LocalRoot, "demo", "1.0", "")
			switch kind {
			case "cache", "bad cache":
				dir := versionedInstalled(t, cache, "demo", "1.0", "", "cache")
				versionedInstalled(t, o.LocalRoot, "demo", "1.0", "", "installation")
				if kind == "bad cache" {
					putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), "broken")
				}
			case "installation", "bad installation", "wrong identity", "incomplete":
				dir := versionedInstalled(t, o.LocalRoot, "demo", "1.0", "", "installation")
				if kind == "bad installation" {
					putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), "broken")
				}
				if kind == "wrong identity" {
					putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), identityHeader(packageIdentity{"demo", "1.0", "windows", "x86_64"}))
				}
				if kind == "incomplete" {
					if err := os.Remove(filepath.Join(dir, "autolib-import.cmake")); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "installation" {
					putTestFile(t, archive, "shadowed corrupt archive")
				}
			case "case version":
				versionedInstalled(t, cache, "demo", "A", "", "upper")
				versionedInstalled(t, cache, "demo", "a", "", "lower")
				entries, err := os.ReadDir(filepath.Join(cache, "demo"))
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 2 {
					t.Skip("host cannot represent case-colliding version directories")
				}
			case "case archive":
				if err := os.Rename(archive, strings.Replace(archive, "demo-", "Demo-", 1)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := prepare(o)
			if !slices.Contains([]string{"cache", "installation", "archive"}, kind) {
				if err == nil {
					t.Fatal("accepted damaged or aliased candidate")
				}
				requireNoResult(t, o)
				return
			}
			if err != nil || len(got) != 1 {
				t.Fatalf("%+v %v", got, err)
			}
			if got[0] != (preparedPackage{Name: "demo", Version: "1.0", Directory: filepath.Join(cache, "demo", "1.0")}) {
				t.Fatal(got)
			}
			if data := readTestFile(t, filepath.Join(got[0].Directory, "data")); data != kind {
				t.Fatalf("selected %q, want %q", data, kind)
			}
			entries, err := os.ReadDir(cache)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".") {
					t.Fatal("leaked staging", entry.Name())
				}
			}
		})
	}
}

func TestVersionedCacheLifecycle(t *testing.T) {
	t.Run("failed closure cannot revive stale latest", func(t *testing.T) {
		o, cache := versionedFixture(t, "demo")
		versionedLatest(t, cache, "demo", "1")
		versionedLatest(t, o.LocalRoot, "demo", "1")
		versionedArchive(t, o.LocalRoot, "demo", "1", "missing@7")
		if _, err := prepare(o); err == nil {
			t.Fatal("accepted incomplete closure")
		}
		if _, err := os.Lstat(filepath.Join(cache, "demo/1")); !os.IsNotExist(err) {
			t.Fatal("failed root revived stale latest", err)
		}
		requireNoResult(t, o)
	})
	t.Run("offline cache and exact requests", func(t *testing.T) {
		o, cache := versionedFixture(t, "demo")
		versionedInstalled(t, cache, "demo", "1", "leaf@2", "cache")
		versionedInstalled(t, cache, "leaf", "2", "", "leaf")
		versionedLatest(t, cache, "demo", "1")
		o.LocalRoot, o.RemoteRoot = "", ""
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
	t.Run("deleted target reselects without refreshing unrelated", func(t *testing.T) {
		o, cache := versionedFixture(t, "demo")
		versionedInstalled(t, cache, "demo", "1", "", "old")
		versionedLatest(t, cache, "demo", "1")
		versionedInstalled(t, cache, "unrelated", "1", "", "keep")
		versionedLatest(t, cache, "unrelated", "1")
		versionedLatest(t, o.LocalRoot, "demo", "2")
		versionedArchive(t, o.LocalRoot, "demo", "2", "")
		if _, err := prepare(o); err != nil {
			t.Fatal(err)
		}
		if version, err := readPackageLatest(cache, "demo"); err != nil || version != "1" {
			t.Fatal(version, err)
		}
		if err := os.Rename(filepath.Join(cache, "demo/1"), filepath.Join(cache, "demo/removed")); err != nil {
			t.Fatal(err)
		}
		if _, err := prepare(o); err != nil {
			t.Fatal(err)
		}
		if version, err := readPackageLatest(cache, "demo"); err != nil || version != "2" {
			t.Fatal(version, err)
		}
		if version, err := readPackageLatest(cache, "unrelated"); err != nil || version != "1" {
			t.Fatal(version, err)
		}
	})
	for _, kind := range []string{"missing latest", "latest missing artifact", "bad target", "failed dependency", "stale result"} {
		t.Run(kind, func(t *testing.T) {
			o, cache := versionedFixture(t, "demo")
			dir := versionedInstalled(t, cache, "demo", "1", "", "cached other version")
			versionedArchive(t, o.LocalRoot, "demo", "2", "missing@7")
			if kind != "missing latest" {
				versionedLatest(t, o.LocalRoot, "demo", "2")
			}
			if kind == "latest missing artifact" {
				versionedLatest(t, o.LocalRoot, "demo", "99")
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
			if kind != "bad target" {
				if _, err := os.Lstat(filepath.Join(cache, "demo/package.toml")); !os.IsNotExist(err) {
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
		versionedArchive(t, o.LocalRoot, "leaf", "2", "")
		versionedLatest(t, o.LocalRoot, "leaf", "3")
		versionedArchive(t, o.LocalRoot, "leaf", "3", "")
		versionedLatest(t, o.LocalRoot, "app", "9")
		got, err := prepare(o)
		if err != nil || len(got) != 2 || got[0].Name != "leaf" || got[0].Version != "2" || got[1].Name != "app" || got[1].Version != "1" {
			t.Fatalf("%+v %v", got, err)
		}
		if _, err := os.Lstat(filepath.Join(cache, "leaf/package.toml")); !os.IsNotExist(err) {
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
			versionedArchive(t, o.LocalRoot, p[0], p[1], p[2])
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
		versionedArchive(t, o.LocalRoot, "app", "1", "leaf@2")
		versionedArchive(t, o.LocalRoot, "leaf", "2", "app@1")
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

func TestVersionedSourceIsolation(t *testing.T) {
	t.Run("copy and relocate only cache", func(t *testing.T) {
		o, cache := versionedFixture(t, "demo@1")
		dir := versionedInstalled(t, o.LocalRoot, "demo", "1", "", "source")
		config := "set(PREFIX \"" + filepath.ToSlash(o.LocalRoot) + "/demo/1\")\n"
		putTestFile(t, filepath.Join(dir, "lib/cmake/demo/config.cmake"), config)
		if err := os.Chmod(filepath.Join(dir, "data"), 0755); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "linux" {
			if err := os.Symlink("data", filepath.Join(dir, "relative")); err != nil {
				t.Fatal(err)
			}
		}
		before := sourceSnapshot(t, o.LocalRoot)
		if _, err := prepare(o); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(before, sourceSnapshot(t, o.LocalRoot)) {
			t.Fatal("source installation changed")
		}
		if strings.Contains(readTestFile(t, filepath.Join(cache, "demo/1/lib/cmake/demo/config.cmake")), filepath.ToSlash(o.LocalRoot)) {
			t.Fatal("producer path remains")
		}
		if runtime.GOOS == "linux" {
			if link, err := os.Readlink(filepath.Join(cache, "demo/1/relative")); err != nil || link != "data" {
				t.Fatal(link, err)
			}
			if info, err := os.Stat(filepath.Join(cache, "demo/1/data")); err != nil || info.Mode().Perm() != 0755 {
				t.Fatal(info, err)
			}
		}
	})
	t.Run("nonexistent source not created", func(t *testing.T) {
		o, _ := versionedFixture(t, "demo@1")
		if _, err := prepare(o); err == nil {
			t.Fatal("accepted missing input")
		}
		if _, err := os.Lstat(o.LocalRoot); !os.IsNotExist(err) {
			t.Fatal("source created", err)
		}
	})
	if runtime.GOOS != "linux" {
		return
	}
	t.Run("physical root aliases rejected before writes", func(t *testing.T) {
		o, cache := versionedFixture(t, "demo@1")
		versionedInstalled(t, cache, "demo", "1", "", "keep")
		alias := filepath.Join(filepath.Dir(cache), "alias")
		if err := os.Symlink(cache, alias); err != nil {
			t.Fatal(err)
		}
		o.LocalRoot = alias
		before := sourceSnapshot(t, cache)
		if _, err := prepare(o); err == nil {
			t.Fatal("accepted physical alias")
		}
		if !slices.Equal(before, sourceSnapshot(t, cache)) {
			t.Fatal("alias rejection wrote cache")
		}
	})
}

func TestVersionedLocalDigest(t *testing.T) {
	for _, digest := range []string{"absent", "valid", "crlf", "padded", "mismatch", "uppercase", "malformed", "directory"} {
		t.Run(digest, func(t *testing.T) {
			o, _ := versionedFixture(t, "demo@1")
			archive := versionedArchive(t, o.LocalRoot, "demo", "1", "")
			sum := fmt.Sprintf("%x\n", sha256.Sum256([]byte(readTestFile(t, archive))))
			switch digest {
			case "valid":
				putTestFile(t, archive+".sha256", sum)
			case "crlf":
				putTestFile(t, archive+".sha256", strings.TrimSuffix(sum, "\n")+"\r\n")
			case "padded":
				putTestFile(t, archive+".sha256", " \t\r\n"+sum+"\r\n\t ")
			case "mismatch":
				putTestFile(t, archive+".sha256", strings.Repeat("0", 64)+"\n")
			case "uppercase":
				putTestFile(t, archive+".sha256", strings.ToUpper(sum))
			case "malformed":
				putTestFile(t, archive+".sha256", "not a hash\n")
			case "directory":
				if err := os.Mkdir(archive+".sha256", 0755); err != nil {
					t.Fatal(err)
				}
			}
			before := sourceSnapshot(t, o.LocalRoot)
			_, err := prepare(o)
			if (err == nil) != (digest == "absent" || digest == "valid" || digest == "crlf" || digest == "padded") {
				t.Fatal(err)
			}
			if !slices.Equal(before, sourceSnapshot(t, o.LocalRoot)) {
				t.Fatal("archive source changed")
			}
			if err != nil {
				requireNoResult(t, o)
			}
		})
	}
}

func TestVersionedReadonlyInstallation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires native Linux chmod permission semantics")
	}
	for _, kind := range []string{"copy and relocate", "missing dependency cleans staging", "relocation failure cleans staging"} {
		t.Run(kind, func(t *testing.T) {
			o, cache := versionedFixture(t, "demo@1")
			dependencies := ""
			if kind == "missing dependency cleans staging" {
				dependencies = "absent@2"
			}
			dir := versionedInstalled(t, o.LocalRoot, "demo", "1", dependencies, "keep")
			putTestFile(t, filepath.Join(dir, "lib/cmake/demo/config.cmake"), "set(PREFIX \""+filepath.ToSlash(dir)+"\")\n")
			if kind == "relocation failure cleans staging" {
				o.CMakeCommand = filepath.Join(t.TempDir(), "missing-cmake")
			}
			readonlyEntrySource(t, o.LocalRoot)
			entryDirectoryCleanup(t, cache)
			before := sourceSnapshot(t, o.LocalRoot)
			_, err := prepare(o)
			if (err == nil) != (kind == "copy and relocate") {
				t.Fatalf("readonly prepare: %v", err)
			}
			if !slices.Equal(before, sourceSnapshot(t, o.LocalRoot)) {
				t.Fatal("readonly installation changed")
			}
			entries, readErr := os.ReadDir(cache)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".") {
					t.Fatal("readonly failure retained owned staging", entry.Name())
				}
			}
			if err != nil {
				requireNoResult(t, o)
				if _, err := os.Lstat(filepath.Join(cache, "demo/1")); !os.IsNotExist(err) {
					t.Fatal("failed copy committed package", err)
				}
				return
			}
			for path, want := range map[string]fs.FileMode{"demo/1": 0555, "demo/1/lib/cmake/demo": 0555, "demo/1/lib/cmake/demo/config.cmake": 0444, "demo/1/data": 0444} {
				info, err := os.Stat(filepath.Join(cache, path))
				if err != nil || info.Mode().Perm() != want {
					t.Fatalf("copy permissions %s: %v %v, want %v", path, info, err, want)
				}
			}
			if strings.Contains(readTestFile(t, filepath.Join(cache, "demo/1/lib/cmake/demo/config.cmake")), filepath.ToSlash(o.LocalRoot)) {
				t.Fatal("readonly metadata was not relocated")
			}
		})
	}
}
