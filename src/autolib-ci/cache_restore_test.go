package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func cacheRestoreLibrary(t *testing.T, directory string) ciLocalAsset {
	t.Helper()
	var buffer bytes.Buffer
	z, err := zstd.NewWriter(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	w := tar.NewWriter(z)
	err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Dir(filepath.Dir(directory)), path)
		if err != nil {
			return err
		}
		if err := w.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0644, Size: int64(len(data))}); err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	r, _ := cacheRecordFixture()
	r.Key = ciCacheKey{Kind: "library", Name: filepath.Base(filepath.Dir(directory)), Version: filepath.Base(directory), OS: "any", Arch: "any"}
	r.Archive.Name = r.Key.archiveName()
	r.Archive.SHA256, r.Archive.Size = testDigest(buffer.Bytes()), int64(buffer.Len())
	file := filepath.Join(t.TempDir(), r.Archive.Name)
	if err := os.WriteFile(file, buffer.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	return ciLocalAsset{Path: file, Record: r}
}

func TestCICacheRestoreNativeConsumer(t *testing.T) {
	f := cacheRunFixture(t)
	ctx := context.Background()
	for _, layer := range []string{"prepare", "layer0"} {
		f.options.Layer = layer
		if err := runCILayerWithCache(ctx, f.options, f.release.client); err != nil {
			t.Fatal(err)
		}
	}
	install := filepath.Join(f.options.WorkRoot, "install")
	producer := filepath.Join(f.options.SourceRoot, "static-fixture")
	writeCIFixture(t, producer, "CMakeLists.txt", "cmake_minimum_required(VERSION 3.20)\nproject(cache_fixture CXX)\nadd_library(cache_fixture STATIC fixture.cpp)\n")
	writeCIFixture(t, producer, "fixture.cpp", "int cached_fixture_value() { return 73; }\n")
	config, err := loadCIEnvironment(filepath.Join(f.options.SourceRoot, ".github/ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := ciProductionForPlatform(f.options.Platform, config)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-S", producer, "-B", filepath.Join(producer, "build"), "-DCMAKE_BUILD_TYPE=Release"}
	for _, key := range slices.Sorted(maps.Keys(c.CMakeDefaults)) {
		args = append(args, "-D"+key+"="+c.CMakeDefaults[key])
	}
	for _, args := range [][]string{args, {"--build", filepath.Join(producer, "build"), "--parallel", "2"}} {
		if out, err := exec.Command(f.options.CMakeCommand, args...).CombinedOutput(); err != nil {
			t.Fatalf("static producer: %v %s", err, out)
		}
	}
	data, err := os.ReadFile(filepath.Join(producer, "build/libcache_fixture.a"))
	if err != nil {
		t.Fatal(err)
	}
	writeCIFixture(t, install, "queue/1/lib/libcache_fixture.a", string(data))
	adapter := mustReadText(t, filepath.Join(install, "queue/1/autolib-import.cmake"))
	adapter = strings.Replace(adapter, "add_library(autolib::queue INTERFACE IMPORTED GLOBAL)", "add_library(autolib::queue STATIC IMPORTED GLOBAL)\nset_target_properties(autolib::queue PROPERTIES IMPORTED_LOCATION_RELEASE \"${CMAKE_CURRENT_LIST_DIR}/lib/libcache_fixture.a\")", 1)
	writeCIFixture(t, install, "queue/1/autolib-import.cmake", adapter)
	asset := cacheRestoreLibrary(t, filepath.Join(install, "queue/1"))
	// Move the validated host tool to the fresh installation; libraries only enter by restore.
	newWork := filepath.Join(f.options.WorkRoot, "restored-run")
	for _, file := range []string{"autolib.cmake", ciImporterExecutable(f.options.Platform)} {
		data, err := os.ReadFile(filepath.Join(install, file))
		if err != nil {
			t.Fatal(err)
		}
		writeCIFixture(t, newWork, "install/"+file, string(data))
		if err := os.Chmod(filepath.Join(newWork, "install", file), 0755); err != nil {
			t.Fatal(err)
		}
	}
	f.options.WorkRoot = newWork
	f.options.ImporterFile = filepath.Join(newWork, "install", ciImporterExecutable(f.options.Platform))
	if err := restoreCICacheItem(ctx, f.options, asset); err != nil {
		t.Fatal(err)
	}
	writeCIFixture(t, f.options.SourceRoot, "tests/autolib/smoke.cpp", "extern int cached_fixture_value();\nint main() { static_assert(FIXTURE_QUEUE==1); return cached_fixture_value()==73 ? 0 : 1; }\n")
	consumer := mustReadText(t, filepath.Join(f.options.SourceRoot, "tests/autolib/CMakeLists.txt"))
	consumer = strings.Replace(consumer, `file(WRITE "${CMAKE_BINARY_DIR}/link-inputs-Release.txt" "")`, `file(GENERATE OUTPUT "${CMAKE_BINARY_DIR}/link-inputs-$<CONFIG>.txt" CONTENT "$<TARGET_FILE:autolib::queue>\n")`, 1)
	writeCIFixture(t, f.options.SourceRoot, "tests/autolib/CMakeLists.txt", consumer)
	evidence, err := validateCIRelease(ctx, ciValidationOptions{SourceRoot: f.options.SourceRoot, InstallRoot: filepath.Join(newWork, "install"), BuildRoot: newWork, CMakeCommand: f.options.CMakeCommand, MakeProgram: f.options.MakeProgram, EvidenceDirectory: filepath.Join(newWork, "evidence"), Modules: []string{"queue@1"}, RootPackages: []string{"queue@1"}, Context: c})
	if err != nil {
		t.Fatal(err)
	}
	inputs := mustReadText(t, filepath.Join(evidence, "build/link-inputs-Release.txt"))
	if !strings.Contains(inputs, filepath.ToSlash(filepath.Join(newWork, "install/queue/1/lib/libcache_fixture.a"))) {
		t.Fatalf("consumer did not load restored exact package: %s", inputs)
	}
	if _, err := os.Stat(filepath.Join(newWork, "install/queue/package.toml")); !os.IsNotExist(err) {
		t.Fatal("restore wrote latest")
	}
	t.Logf("native %s: restored queue@1, linked %s; consumer returned 0", f.options.Platform, strings.TrimSpace(inputs))
}

func TestCICacheRestore(t *testing.T) {
	importer := buildTestImporter(t)
	for _, kind := range []string{"library", "importer", "digest", "size", "extra", "symlink", "entry", "existing", "platform"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			osName, arch := testPlatform()
			o := ciRunOptions{SourceRoot: root, WorkRoot: filepath.Join(root, "work"), Platform: osName + "-" + arch, ImporterFile: importer, CMakeCommand: "cmake"}
			writeCIFixture(t, root, "src/autolib.cmake", "# raw entry\r\n")
			writeCIFixture(t, o.WorkRoot, "install/unrelated/keep", "preserve")
			var asset ciLocalAsset
			if kind == "library" {
				writeCIFixture(t, root, "package/b/1/autolib-import.cmake", "set(AUTOLIB_PACKAGE_NAME \"b\")\nset(AUTOLIB_PACKAGE_VERSION \"1\")\nset(AUTOLIB_OS \"any\")\nset(AUTOLIB_ARCH \"any\")\nset(AUTOLIB_DEPENDENCIES \"\")\n")
				asset = cacheRestoreLibrary(t, filepath.Join(root, "package/b/1"))
			} else {
				name := ciImporterExecutable(o.Platform)
				writeCIFixture(t, root, "input/"+name, "cached host program")
				args := []string{"-E", "tar", "cfJ", filepath.Join(root, "tool.tar.xz"), "--", name}
				if kind == "extra" {
					writeCIFixture(t, root, "input/extra", "extra")
					args = append(args, "extra")
				}
				if kind == "symlink" {
					if err := os.Remove(filepath.Join(root, "input", name)); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink("../src/autolib.cmake", filepath.Join(root, "input", name)); err != nil {
						t.Skip(err)
					}
				}
				cmd := exec.Command("cmake", args...)
				cmd.Dir = filepath.Join(root, "input")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("archive: %v %s", err, out)
				}
				data, err := os.ReadFile(filepath.Join(root, "tool.tar.xz"))
				if err != nil {
					t.Fatal(err)
				}
				r, _ := cacheRecordFixture()
				r.Kind = "importer"
				r.Key = ciCacheKey{Kind: "importer", Name: "autolib-importer", OS: osName, Arch: arch}
				r.LatestCandidate = ""
				r.Archive = ciCacheArchive{ID: 42, Name: r.Key.archiveName(), SHA256: testDigest(data), Size: int64(len(data))}
				r.Entry = &ciCacheEntry{Bytes: []byte("# raw entry\r\n"), SHA256: testDigest([]byte("# raw entry\r\n"))}
				asset = ciLocalAsset{Path: filepath.Join(root, "tool.tar.xz"), Record: r}
				if kind == "digest" {
					asset.Record.Archive.SHA256 = strings.Repeat("0", 64)
				}
				if kind == "size" {
					asset.Record.Archive.Size++
				}
				if kind == "entry" {
					writeCIFixture(t, root, "src/autolib.cmake", "changed")
				}
				if kind == "existing" {
					writeCIFixture(t, o.WorkRoot, "install/"+name, "preserve tool")
				}
				if kind == "platform" {
					asset.Record.Key.OS = "windows"
					asset.Record.Key.Arch = "x86_64"
				}
			}
			before, _ := json.Marshal(asset.Record)
			original, _ := os.ReadFile(asset.Path)
			err := restoreCICacheItem(context.Background(), o, asset)
			if kind == "library" || kind == "importer" {
				if err != nil {
					t.Fatal(err)
				}
				if kind == "library" {
					cmd := exec.Command(importer, "prepare", "--cache-root", filepath.Join(o.WorkRoot, "install"), "--os", osName, "--arch", arch, "--package=b@1", "--result", filepath.Join(root, "consume.cmake"))
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("offline prepare: %v %s", err, out)
					}
				} else if mustReadText(t, filepath.Join(o.WorkRoot, "install", ciImporterExecutable(o.Platform))) != "cached host program" || mustReadText(t, filepath.Join(o.WorkRoot, "install/autolib.cmake")) != "# raw entry\r\n" {
					t.Fatal("restored bytes differ")
				}
			} else if err == nil {
				t.Fatal("invalid cache accepted")
			}
			after, _ := json.Marshal(asset.Record)
			current, _ := os.ReadFile(asset.Path)
			if !bytes.Equal(before, after) || !bytes.Equal(original, current) {
				t.Fatal("source cache changed")
			}
			if mustReadText(t, filepath.Join(o.WorkRoot, "install/unrelated/keep")) != "preserve" {
				t.Fatal("unrelated cache changed")
			}
			if kind == "existing" && mustReadText(t, filepath.Join(o.WorkRoot, "install", ciImporterExecutable(o.Platform))) != "preserve tool" {
				t.Fatal("existing tool replaced")
			}
		})
	}
}
