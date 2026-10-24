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

func TestCICacheAssetSelection(t *testing.T) {
	valid := `{"id":7,"name":"queue-any-any.tar.zst","state":"uploaded","label":"{\"build_id\":\"build-1\",\"commit\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"}","digest":"sha256:` + strings.Repeat("A", 64) + `"}`
	for _, test := range []struct {
		name, asset   string
		miss, failure bool
	}{
		{"standalone asset", valid, false, false},
		{"missing", "", true, false},
		{"missing label", strings.Replace(valid, `\"build-1\"`, `\"\"`, 1), true, false},
		{"invalid label", strings.Replace(valid, `build_id`, `unknown`, 1), true, false},
		{"invalid commit", strings.Replace(valid, strings.Repeat("a", 40), "bad", 1), true, false},
		{"build ID mismatch", strings.Replace(valid, "build-1", "build-2", 1), true, false},
		{"incomplete upload", strings.Replace(valid, "uploaded", "starter", 1), true, false},
		{"missing digest prefix", strings.Replace(valid, "sha256:", "", 1), true, false},
		{"duplicate", valid + "," + valid, false, true},
		{"mixed slot", valid + `,{"id":8,"name":"queue-windows-x86_64.tar.zst"}`, false, true},
		{"unrelated invalid label", valid + `,{"id":8,"name":"other.tar.zst","label":"broken"}`, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var assets []githubAsset
			if err := json.Unmarshal([]byte("["+test.asset+"]"), &assets); err != nil {
				t.Fatal(err)
			}
			got, reason, err := selectCICacheAsset(assets, ciSlot{Name: "queue", OS: "any", Arch: "any"}, "build-1")
			if (err != nil) != test.failure {
				t.Fatalf("asset=%+v reason=%q error=%v", got, reason, err)
			}
			if test.failure {
				return
			}
			if test.miss {
				if got.ID != 0 || reason == "" {
					t.Fatalf("invalid candidate selected: %+v %q", got, reason)
				}
				return
			}
			if got.ID != 7 || got.Name != "queue-any-any.tar.zst" || reason != "" {
				t.Fatalf("standalone asset not selected: %+v %q", got, reason)
			}
		})
	}
}

func TestCICacheIntegrity(t *testing.T) {
	archive := cacheTestPackageArchive(t, "queue", "any", "any", []cacheArchiveMember{{Name: "queue/payload", Body: "valid"}})
	importer := buildCacheTestImporter(t)
	for _, test := range []struct {
		name, buildID, digest string
		hit                   bool
	}{
		{"standalone valid package", "build-1", cacheTestDigest(archive), true},
		{"build ID mismatch", "different", cacheTestDigest(archive), false},
		{"actual byte mismatch", "build-1", strings.Repeat("b", 64), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := cacheTestOptions(t, test.buildID)
			o.SourceRoot, o.ImporterFile, o.CMakeCommand = cacheTestSourceRoot(t), importer, "cmake"
			got, err := lookupCICache(context.Background(), cacheTestClient(t, archive, "queue", "any", "any", "build-1", test.digest), o)
			if err != nil || got.Hit != test.hit {
				t.Fatalf("hit=%v want %v: %+v %v", got.Hit, test.hit, got, err)
			}
			if !test.hit && got.Reason == "" {
				t.Fatal("cache miss has no reason")
			}
		})
	}
}

func newCacheTestGitHub(t *testing.T, asset githubAsset, archive []byte) *githubClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/xxf1ow/xxautolib/releases/tags/autolib-release":
			fmt.Fprintf(w, `{"id":9,"tag_name":"autolib-release","assets_url":"%s/repos/xxf1ow/xxautolib/releases/9/assets"}`, "http://"+r.Host)
		case "/repos/xxf1ow/xxautolib/releases/9/assets":
			_ = json.NewEncoder(w).Encode([]githubAsset{asset})
		case "/repos/xxf1ow/xxautolib/releases/assets/1":
			w.Write(archive)
		default:
			t.Errorf("unexpected cache request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &githubClient{BaseURL: server.URL, HTTP: server.Client()}
}

func cacheTestOptions(t *testing.T, buildID string) ciCacheOptions {
	t.Helper()
	root := t.TempDir()
	osName, arch := cacheTestPlatform()
	return ciCacheOptions{SourceRoot: root, WorkRoot: filepath.Join(root, "work"), InstallRoot: filepath.Join(root, "install"), ReleaseRoot: filepath.Join(root, "release"), ImporterFile: "unused", CMakeCommand: "unused", BuildID: buildID, TargetOS: osName, TargetArch: arch, Slot: ciSlot{Name: "queue", OS: "any", Arch: "any"}}
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
			options.TargetOS, options.TargetArch = targetOS, targetArch
			client := cacheTestClient(t, item.archive, "queue", "any", "any", "build-1", cacheTestDigest(item.archive))
			got, err := lookupCICache(context.Background(), client, options)
			if got.Hit || (err == nil && strings.HasPrefix(item.name, "missing")) {
				t.Fatalf("missing execution tool must fail without reporting a hit: %+v %v", got, err)
			}
			if _, statErr := os.Stat(filepath.Join(options.InstallRoot, "queue")); !os.IsNotExist(statErr) {
				t.Fatalf("failed preparation restored a package: %v", statErr)
			}
		})
	}
	t.Run("old importer cache does not bypass candidate validation", func(t *testing.T) {
		options := cacheTestOptions(t, "build-1")
		options.SourceRoot = cacheTestSourceRoot(t)
		options.ImporterFile, options.CMakeCommand = importer, "cmake"
		options.TargetOS, options.TargetArch = targetOS, targetArch
		oldPackage := filepath.Join(options.ReleaseRoot, "packages", targetOS+"-"+targetArch, "queue")
		if err := os.MkdirAll(oldPackage, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(oldPackage, "autolib-import.cmake"), []byte(cacheTestIdentityHeader("queue", targetOS, targetArch)), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(oldPackage, "stale.txt"), []byte("stale importer cache"), 0644); err != nil {
			t.Fatal(err)
		}
		badArchive := []byte("not a zstd archive")
		client := cacheTestClient(t, badArchive, "queue", "any", "any", "build-1", cacheTestDigest(badArchive))
		got, err := lookupCICache(context.Background(), client, options)
		if err != nil || got.Hit || got.Reason == "" {
			t.Fatalf("invalid new candidate archive bypassed validation: %+v %v", got, err)
		}
		if _, statErr := os.Stat(filepath.Join(options.InstallRoot, "queue", "stale.txt")); !os.IsNotExist(statErr) {
			t.Fatalf("old importer result was restored: %v", statErr)
		}
	})
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
	cmakeLists := "cmake_minimum_required(VERSION 3.20)\nproject(fixture LANGUAGES C)\nadd_library(fixture STATIC fixture.c)\ntarget_include_directories(fixture PUBLIC \"${CMAKE_CURRENT_SOURCE_DIR}\")\nset_target_properties(fixture PROPERTIES OUTPUT_NAME fixture-selected-by-cmake ARCHIVE_OUTPUT_DIRECTORY \"${PACKAGE_LIB}\")\nfile(GENERATE OUTPUT \"${CMAKE_CURRENT_BINARY_DIR}/fixture-location-$<CONFIG>.txt\" CONTENT \"$<TARGET_FILE:fixture>\")\n"
	if err := os.WriteFile(filepath.Join(cmakeSource, "CMakeLists.txt"), []byte(cmakeLists), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmakeSource, "fixture.c"), []byte("int fixture_value(void) { return 42; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	configure := exec.Command("cmake", "-S", cmakeSource, "-B", buildRoot, "-DCMAKE_BUILD_TYPE=Release", "-DPACKAGE_LIB="+filepath.Join(packageRoot, "lib"))
	if output, err := configure.CombinedOutput(); err != nil {
		t.Fatalf("configure dependency fixture: %v\n%s", err, output)
	}
	build := exec.Command("cmake", "--build", buildRoot, "--config", "Release")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dependency fixture: %v\n%s", err, output)
	}
	libraryPathData, err := os.ReadFile(filepath.Join(buildRoot, "fixture-location-Release.txt"))
	if err != nil {
		t.Fatalf("read CMake-selected fixture library path: %v", err)
	}
	library := strings.TrimSpace(string(libraryPathData))
	contents := []cacheArchiveMember{
		{Name: "queue/include/fixture.h", Body: "int fixture_value(void);\n"},
		{Name: "queue/lib/" + filepath.Base(library), Body: mustReadText(t, library)},
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
	archive := cacheTestPackageArchiveWithAdapterExtra(t, "queue", "any", "any", contents, "# archived adapter must not be copied\n", "support")
	dependencyFile := filepath.Join(root, "support-any-any.tar.zst")
	if err := os.WriteFile(dependencyFile, cacheTestPackageArchive(t, "support", "any", "any", nil), 0644); err != nil {
		t.Fatal(err)
	}
	options.DependencyArchives = []ciReadyArchive{{Name: "support", File: dependencyFile}}
	client := cacheTestClient(t, archive, "queue", "any", "any", "build-1", cacheTestDigest(archive))
	options.TargetOS, options.TargetArch = targetOS, targetArch
	got, err := lookupCICache(context.Background(), client, options)
	if err != nil || !got.Hit {
		t.Fatalf("restore verified fixture: %+v %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(options.InstallRoot, "queue", "autolib-import.cmake")); !os.IsNotExist(err) {
		t.Fatalf("restore mixed a root adapter into the installation payload: %v", err)
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
	consumerSource := "cmake_minimum_required(VERSION 3.20)\nproject(consumer LANGUAGES C)\nenable_testing()\nadd_library(AutoLib::queue STATIC IMPORTED GLOBAL)\nset_target_properties(AutoLib::queue PROPERTIES IMPORTED_LOCATION \"${PACKAGE_ROOT}/lib/" + filepath.Base(library) + "\" INTERFACE_INCLUDE_DIRECTORIES \"${PACKAGE_ROOT}/include\")\nadd_executable(consumer main.c)\ntarget_link_libraries(consumer PRIVATE AutoLib::queue)\nadd_test(NAME restored_dependency COMMAND consumer)\n"
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
	linkConsumer := exec.Command("cmake", "--build", consumerBuild, "--config", "Release")
	if output, err := linkConsumer.CombinedOutput(); err != nil {
		t.Fatalf("link restored dependency consumer: %v\n%s", err, output)
	}
	if output, err := exec.Command("ctest", "--test-dir", consumerBuild, "-C", "Release", "--output-on-failure").CombinedOutput(); err != nil {
		t.Fatalf("run restored dependency consumer: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(options.ReleaseRoot, "queue-any-any.tar.zst")); err != nil {
		t.Fatalf("original archive was not retained: %v", err)
	}
}

func cacheTestSourceRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range []string{"src/autolib-ci"} {
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
	label, _ := json.Marshal(ciAssetLabel{BuildID: buildID, Commit: strings.Repeat("a", 40)})
	return newCacheTestGitHub(t, githubAsset{ID: 1, Name: name + "-" + osName + "-" + arch + ".tar.zst", State: "uploaded", Label: string(label), Digest: "sha256:" + digest}, archive)
}

type cacheArchiveMember struct {
	Name, Body, Link string
	Kind             byte
}

func cacheTestPackageArchive(t *testing.T, name, osName, arch string, contents []cacheArchiveMember) []byte {
	return cacheTestPackageArchiveWithAdapterExtra(t, name, osName, arch, contents, "")
}

func cacheTestPackageArchiveWithAdapterExtra(t *testing.T, name, osName, arch string, contents []cacheArchiveMember, adapterExtra string, dependencies ...string) []byte {
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
	identity := cacheTestIdentityHeader(name, osName, arch, dependencies...) + adapterExtra
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

func cacheTestIdentityHeader(name, osName, arch string, dependencies ...string) string {
	return fmt.Sprintf("set(AUTOLIB_PACKAGE_NAME \"%s\")\nset(AUTOLIB_OS \"%s\")\nset(AUTOLIB_ARCH \"%s\")\nset(AUTOLIB_DEPENDENCIES \"%s\")\n", name, osName, arch, strings.Join(dependencies, ";"))
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
