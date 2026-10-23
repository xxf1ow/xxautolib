package main

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestCICacheIntegrity(t *testing.T) {
	archive := []byte("package archive bytes")
	archiveDigest := cacheTestDigest(archive)
	validManifest := ciManifest{Importers: []ciImporterRecord{}, Packages: []ciPackageRecord{{Name: "queue", OS: "any", Arch: "any", Asset: "queue-any-any.tar.zst", SHA256: archiveDigest}}}
	validIndex := ciCacheIndex{Packages: []ciCacheRecord{{Slot: ciSlot{Name: "queue", OS: "any", Arch: "any"}, BuildID: "build-1", SHA256: archiveDigest, Commit: strings.Repeat("a", 40)}}}
	cases := []struct {
		name       string
		manifest   ciManifest
		index      ciCacheIndex
		buildID    string
		wantReason string
		badJSON    bool
	}{
		{name: "build ID mismatch is a miss", manifest: validManifest, index: validIndex, buildID: "different-build", wantReason: "build ID"},
		{name: "private digest disagreement is a miss", manifest: validManifest, index: ciCacheIndex{Packages: []ciCacheRecord{{Slot: ciSlot{Name: "queue", OS: "any", Arch: "any"}, BuildID: "build-1", SHA256: strings.Repeat("b", 64), Commit: strings.Repeat("a", 40)}}}, buildID: "build-1", wantReason: "digests differ"},
		{name: "missing public package is a miss", manifest: ciManifest{Importers: []ciImporterRecord{}, Packages: []ciPackageRecord{}}, index: validIndex, buildID: "build-1", wantReason: "no matching package"},
		{name: "missing private record is a miss", manifest: validManifest, index: ciCacheIndex{Packages: []ciCacheRecord{}}, buildID: "build-1", wantReason: "no matching package slot"},
		{name: "malformed production manifest is an error", index: validIndex, buildID: "build-1", badJSON: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			manifestJSON, _ := json.Marshal(test.manifest)
			if test.badJSON {
				manifestJSON = []byte(`{"importers":[],"packages":[{"name":"queue","os":"any","arch":"any","asset":"../escape","sha256":"bad"}]}`)
			}
			indexJSON, _ := json.Marshal(test.index)
			client := newCacheTestGitHub(t, manifestJSON, indexJSON, archive, archiveDigest)
			options := cacheTestOptions(t, test.buildID)
			got, err := lookupCICache(context.Background(), client, options)
			if test.badJSON {
				if err == nil {
					t.Fatal("malformed production manifest must fail, not become an empty cache")
				}
				return
			}
			if err != nil || got.Hit || !strings.Contains(got.Reason, test.wantReason) {
				t.Fatalf("want explained miss containing %q, got %+v, %v", test.wantReason, got, err)
			}
		})
	}

	t.Run("GitHub public digest disagreement is a miss", func(t *testing.T) {
		manifestJSON, _ := json.Marshal(validManifest)
		indexJSON, _ := json.Marshal(validIndex)
		client := newCacheTestGitHub(t, manifestJSON, indexJSON, archive, strings.Repeat("b", 64))
		got, err := lookupCICache(context.Background(), client, cacheTestOptions(t, "build-1"))
		if err != nil || got.Hit || !strings.Contains(got.Reason, "GitHub asset digest") {
			t.Fatalf("expected public metadata digest miss, got %+v, %v", got, err)
		}
	})

	t.Run("actual package bytes are bound to the API digest", func(t *testing.T) {
		expected := []byte("expected package bytes")
		corrupt := []byte("different package bytes")
		digest := cacheTestDigest(expected)
		manifest, _ := json.Marshal(ciManifest{Importers: []ciImporterRecord{}, Packages: []ciPackageRecord{{Name: "queue", OS: "any", Arch: "any", Asset: "queue-any-any.tar.zst", SHA256: digest}}})
		index, _ := json.Marshal(ciCacheIndex{Packages: []ciCacheRecord{{Slot: ciSlot{Name: "queue", OS: "any", Arch: "any"}, BuildID: "build-1", SHA256: digest, Commit: strings.Repeat("a", 40)}}})
		client := newCacheTestGitHub(t, manifest, index, corrupt, digest)
		got, err := lookupCICache(context.Background(), client, cacheTestOptions(t, "build-1"))
		if err != nil || got.Hit || !strings.Contains(got.Reason, "downloaded archive bytes") {
			t.Fatalf("expected corrupted package byte miss, got %+v, %v", got, err)
		}
	})
}

func newCacheTestGitHub(t *testing.T, manifestJSON, indexJSON, archive []byte, packageAssetDigest string) *githubClient {
	t.Helper()
	metadata := map[string][]byte{"manifest.json": manifestJSON, "build-cache.json": indexJSON, "queue-any-any.tar.zst": archive}
	assetNames := []string{"manifest.json", "build-cache.json", "queue-any-any.tar.zst"}
	assetDigests := map[string]string{"manifest.json": cacheTestDigest(manifestJSON), "build-cache.json": cacheTestDigest(indexJSON), "queue-any-any.tar.zst": packageAssetDigest}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/xxf1ow/xxautolib/releases/tags/autolib-release":
			fmt.Fprintf(w, `{"id":9,"tag_name":"autolib-release","assets_url":"%s/repos/xxf1ow/xxautolib/releases/9/assets"}`, "http://"+r.Host)
		case "/repos/xxf1ow/xxautolib/releases/9/assets":
			rows := []githubAsset{}
			for i, name := range assetNames {
				rows = append(rows, githubAsset{ID: int64(i + 1), Name: name, Digest: "sha256:" + assetDigests[name]})
			}
			_ = json.NewEncoder(w).Encode(rows)
		case "/repos/xxf1ow/xxautolib/releases/assets/1", "/repos/xxf1ow/xxautolib/releases/assets/2", "/repos/xxf1ow/xxautolib/releases/assets/3":
			var index int
			fmt.Sscanf(filepath.Base(r.URL.Path), "%d", &index)
			w.Write(metadata[assetNames[index-1]])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &githubClient{BaseURL: server.URL, HTTP: server.Client()}
}

func cacheTestOptions(t *testing.T, buildID string) ciCacheOptions {
	t.Helper()
	root := t.TempDir()
	return ciCacheOptions{SourceRoot: root, WorkRoot: filepath.Join(root, "work"), InstallRoot: filepath.Join(root, "install"), ReleaseRoot: filepath.Join(root, "release"), ImporterFile: "unused", CMakeCommand: "unused", BuildID: buildID, Slot: ciSlot{Name: "queue", OS: "windows", Arch: "x86_64"}}
}

func cacheTestDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func TestCICachePrepareBoundary(t *testing.T) {
	archive := cacheTestPackageArchive(t, "queue", "any", "any", []cacheArchiveMember{{Name: "queue/payload", Body: "ok"}})
	targetOS, targetArch := cacheTestPlatform()
	importer := buildCacheTestImporter(t)
	unsafeArchive := cacheTestPackageArchive(t, "queue", "any", "any", []cacheArchiveMember{{Name: "queue/../outside", Body: "must not restore"}})
	wrongIdentityArchive := cacheTestPackageArchive(t, "other", "any", "any", []cacheArchiveMember{{Name: "other/payload", Body: "wrong identity"}})
	for _, item := range []struct {
		name         string
		importerFile string
		cmakeCommand string
		archive      []byte
	}{
		{name: "missing importer", importerFile: filepath.Join(t.TempDir(), "missing-importer.exe"), cmakeCommand: "cmake", archive: archive},
		{name: "missing CMake", importerFile: importer, cmakeCommand: "missing-cmake-command", archive: archive},
		{name: "unsafe archive path", importerFile: importer, cmakeCommand: "cmake", archive: unsafeArchive},
		{name: "wrong archive identity", importerFile: importer, cmakeCommand: "cmake", archive: wrongIdentityArchive},
	} {
		t.Run(item.name, func(t *testing.T) {
			options := cacheTestOptions(t, "build-1")
			options.ImporterFile, options.CMakeCommand = item.importerFile, item.cmakeCommand
			options.SourceRoot = cacheTestSourceRoot(t)
			options.Slot.OS, options.Slot.Arch = targetOS, targetArch
			client := cacheTestClient(t, item.archive, "queue", "any", "any", "build-1", cacheTestDigest(item.archive))
			got, err := lookupCICache(context.Background(), client, options)
			if err == nil || got.Hit {
				t.Fatalf("missing execution tool must fail without reporting a hit: %+v %v", got, err)
			}
			if _, statErr := os.Stat(filepath.Join(options.InstallRoot, "queue")); !os.IsNotExist(statErr) {
				t.Fatalf("failed preparation restored a package: %v", statErr)
			}
		})
	}
}

func TestCICacheRestoredDependency(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	options := cacheTestOptions(t, "build-1")
	options.SourceRoot = source
	options.ImporterFile = buildCacheTestImporter(t)
	options.CMakeCommand = "cmake"
	if err := os.MkdirAll(filepath.Join(source, "src", "autolib-ci"), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("restore-cache.cmake")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "src", "autolib-ci", "restore-cache.cmake"), data, 0644); err != nil {
		t.Fatal(err)
	}
	targetOS, targetArch := cacheTestPlatform()
	libraryName := "libfixture.a"
	if runtime.GOOS == "windows" {
		libraryName = "libfixture.lib"
	}
	adapter := "add_library(AutoLib::queue STATIC IMPORTED GLOBAL)\nset_target_properties(AutoLib::queue PROPERTIES IMPORTED_LOCATION \"${CMAKE_CURRENT_LIST_DIR}/lib/" + libraryName + "\" INTERFACE_INCLUDE_DIRECTORIES \"${CMAKE_CURRENT_LIST_DIR}/include\")\n"
	if err := os.MkdirAll(filepath.Join(source, "src", "dependencies", "queue"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "src", "dependencies", "queue", "autolib-import.cmake"), []byte(adapter), 0644); err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(root, "package", "queue")
	if err := os.MkdirAll(filepath.Join(packageRoot, "include"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "include", "fixture.h"), []byte("int fixture_value(void);\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packageRoot, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	buildRoot := filepath.Join(root, "producer")
	if err := os.MkdirAll(buildRoot, 0755); err != nil {
		t.Fatal(err)
	}
	cmakeSource := filepath.Join(root, "cmake-source")
	if err := os.MkdirAll(cmakeSource, 0755); err != nil {
		t.Fatal(err)
	}
	cmakeLists := "cmake_minimum_required(VERSION 3.20)\nproject(fixture LANGUAGES C)\nadd_library(fixture STATIC fixture.c)\ntarget_include_directories(fixture PUBLIC \"${CMAKE_CURRENT_SOURCE_DIR}\")\nset_target_properties(fixture PROPERTIES OUTPUT_NAME fixture ARCHIVE_OUTPUT_DIRECTORY \"${PACKAGE_LIB}\")\n"
	if err := os.WriteFile(filepath.Join(cmakeSource, "CMakeLists.txt"), []byte(cmakeLists), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmakeSource, "fixture.c"), []byte("int fixture_value(void) { return 42; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	configure := exec.Command("cmake", "-S", cmakeSource, "-B", buildRoot, "-DPACKAGE_LIB="+filepath.Join(packageRoot, "lib"))
	if output, err := configure.CombinedOutput(); err != nil {
		t.Fatalf("configure dependency fixture: %v\n%s", err, output)
	}
	build := exec.Command("cmake", "--build", buildRoot)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dependency fixture: %v\n%s", err, output)
	}
	library := ""
	if err := filepath.WalkDir(filepath.Join(packageRoot, "lib"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".lib") {
			library = path
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if library == "" {
		t.Fatalf("dependency build produced no library below %s", filepath.Join(packageRoot, "lib"))
	}
	contents := []cacheArchiveMember{
		{Name: "queue/include/fixture.h", Body: "int fixture_value(void);\n"},
		{Name: "queue/lib/libfixture.lib", Body: mustReadText(t, library)},
	}
	linkProbeRoot := t.TempDir()
	linkProbeTarget := filepath.Join(linkProbeRoot, "target")
	linkProbe := filepath.Join(linkProbeRoot, "link")
	hasInternalLink := false
	if err := os.WriteFile(linkProbeTarget, []byte("probe"), 0644); err == nil {
		if err := os.Symlink("target", linkProbe); err == nil {
			contents = append(contents, cacheArchiveMember{Name: "queue/include/fixture-alias.h", Link: "fixture.h", Kind: tar.TypeSymlink})
			hasInternalLink = true
		} else {
			t.Logf("host cannot create the internal-link fixture: %v", err)
		}
	}
	archive := cacheTestPackageArchiveWithAdapterExtra(t, "queue", "any", "any", contents, "# archived adapter must not be copied\n")
	client := cacheTestClient(t, archive, "queue", "any", "any", "build-1", cacheTestDigest(archive))
	options.Slot = ciSlot{Name: "queue", OS: targetOS, Arch: targetArch}
	got, err := lookupCICache(context.Background(), client, options)
	if err != nil || !got.Hit {
		t.Fatalf("restore verified fixture: %+v %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(options.InstallRoot, "queue", "autolib-import.cmake")); err != nil || !strings.Contains(string(got), "IMPORTED_LOCATION") || strings.Contains(string(got), "archived adapter") {
		t.Fatalf("restore did not inject the source adapter: %s %v", got, err)
	}
	if hasInternalLink {
		if _, err := os.Lstat(filepath.Join(options.InstallRoot, "queue", "include", "fixture-alias.h")); err != nil {
			t.Fatalf("internal link was not restored: %v", err)
		}
		if link, err := os.Readlink(filepath.Join(options.InstallRoot, "queue", "include", "fixture-alias.h")); err != nil || link != "fixture.h" {
			t.Fatalf("restored internal link changed: %q %v", link, err)
		}
	}
	consumer := filepath.Join(root, "consumer")
	if err := os.MkdirAll(consumer, 0755); err != nil {
		t.Fatal(err)
	}
	consumerSource := "cmake_minimum_required(VERSION 3.20)\nproject(consumer LANGUAGES C)\ninclude(\"${PACKAGE_ROOT}/autolib-import.cmake\")\nadd_executable(consumer main.c)\ntarget_link_libraries(consumer PRIVATE AutoLib::queue)\n"
	if err := os.WriteFile(filepath.Join(consumer, "CMakeLists.txt"), []byte(consumerSource), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "main.c"), []byte("#include <fixture.h>\nint main(void) { return fixture_value() == 42 ? 0 : 1; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	consumerBuild := filepath.Join(root, "consumer-build")
	configureConsumer := exec.Command("cmake", "-S", consumer, "-B", consumerBuild, "-DPACKAGE_ROOT="+filepath.Join(options.InstallRoot, "queue"))
	if output, err := configureConsumer.CombinedOutput(); err != nil {
		t.Fatalf("configure restored dependency consumer: %v\n%s", err, output)
	}
	linkConsumer := exec.Command("cmake", "--build", consumerBuild)
	if output, err := linkConsumer.CombinedOutput(); err != nil {
		t.Fatalf("link restored dependency consumer: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(options.ReleaseRoot, "queue-any-any.tar.zst")); err != nil {
		t.Fatalf("original archive was not retained: %v", err)
	}
}

func cacheTestSourceRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range []string{"src/autolib-ci", "src/dependencies/queue"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile("restore-cache.cmake")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src/autolib-ci/restore-cache.cmake"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src/dependencies/queue/autolib-import.cmake"), []byte("# adapter\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func buildCacheTestImporter(t *testing.T) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	outputName := "autolib-importer"
	if runtime.GOOS == "windows" {
		outputName += ".exe"
	}
	output := filepath.Join(t.TempDir(), outputName)
	command := exec.Command("cmake", "-P", filepath.Join(repositoryRoot, "src", "autolib-builder", "go-sdk.cmake"), "--", "build", "-mod=readonly", "-trimpath", "-o", output, "./src/autolib-importer")
	command.Dir = repositoryRoot
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build importer fixture: %v\n%s", err, data)
	}
	return output
}

func cacheTestClient(t *testing.T, archive []byte, name, osName, arch, buildID, digest string) *githubClient {
	t.Helper()
	manifest, _ := json.Marshal(ciManifest{Importers: []ciImporterRecord{}, Packages: []ciPackageRecord{{Name: name, OS: osName, Arch: arch, Asset: name + "-" + osName + "-" + arch + ".tar.zst", SHA256: cacheTestDigest(archive)}}})
	index, _ := json.Marshal(ciCacheIndex{Packages: []ciCacheRecord{{Slot: ciSlot{Name: name, OS: osName, Arch: arch}, BuildID: buildID, SHA256: cacheTestDigest(archive), Commit: strings.Repeat("a", 40)}}})
	return newCacheTestGitHub(t, manifest, index, archive, digest)
}

type cacheArchiveMember struct {
	Name, Body, Link string
	Kind             byte
}

func cacheTestPackageArchive(t *testing.T, name, osName, arch string, contents []cacheArchiveMember) []byte {
	return cacheTestPackageArchiveWithAdapterExtra(t, name, osName, arch, contents, "")
}

func cacheTestPackageArchiveWithAdapterExtra(t *testing.T, name, osName, arch string, contents []cacheArchiveMember, adapterExtra string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.tar.zst")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressor, err := zstd.NewWriter(file)
	if err != nil {
		t.Fatal(err)
	}
	tarWriter := tar.NewWriter(compressor)
	identity := cacheTestIdentityHeader(name, osName, arch) + adapterExtra
	members := append([]cacheArchiveMember{{Name: name + "/autolib-import.cmake", Body: identity}}, contents...)
	for _, member := range members {
		kind := member.Kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: member.Name, Mode: 0644, Typeflag: kind, Linkname: member.Link}
		if kind == tar.TypeReg {
			header.Size = int64(len(member.Body))
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			if _, err := tarWriter.Write([]byte(member.Body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := errors.Join(tarWriter.Close(), compressor.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func cacheTestIdentityHeader(name, osName, arch string) string {
	return fmt.Sprintf("set(AUTOLIB_PACKAGE_NAME \"%s\")\nset(AUTOLIB_OS \"%s\")\nset(AUTOLIB_ARCH \"%s\")\n", name, osName, arch)
}

func cacheTestPlatform() (string, string) {
	platformOS := runtime.GOOS
	if platformOS == "darwin" {
		platformOS = "linux"
	}
	platformArch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	return platformOS, platformArch
}

func mustReadText(t *testing.T, filename string) string {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
