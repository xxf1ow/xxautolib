package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestCIValidationMode(t *testing.T) {
	// Ignoring Mode would run a second consumer; inferring readiness would invoke packaging.
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(source, "build", "ci-test")
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "validation-mode-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	importer := filepath.Join(root, "importer"+executableSuffix())
	if err := copyCIFile(buildTestImporter(t), importer); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(importer, 0755); err != nil {
		t.Fatal(err)
	}
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	environment, err := loadCIEnvironment(filepath.Join(source, ".github", "ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	osName, arch := testPlatform()
	production := ciProductionContext{OS: osName, Arch: arch, Configuration: "Release", CMakeDefaults: environment.Production.Common.CMakeDefaults}
	if osName == "windows" {
		w := environment.Production.Windows
		production.Generator, production.Platform, production.Toolset, production.Runtime, production.MultiConfig = w.Generator, w.Platform, w.Toolset, w.Runtime, true
	} else {
		production.Generator = environment.Production.Linux.Generator
	}
	archive := testPackageArchive(t, "queue", "any", "any", []testArchiveMember{{Name: "queue/include/queue/readerwriterqueue.h", Body: "#pragma once\nnamespace moodycamel { template<class T> class ReaderWriterQueue { T value{}; public: bool enqueue(T v) { value=v; return true; } bool try_dequeue(T& v) { v=value; return true; } }; }\n"}}, "add_library(autolib::queue INTERFACE IMPORTED GLOBAL)\nset_target_properties(autolib::queue PROPERTIES INTERFACE_INCLUDE_DIRECTORIES \"${CMAKE_CURRENT_LIST_DIR}/include\")\n")
	for _, mode := range []string{"single", "relocate"} {
		t.Run(mode, func(t *testing.T) {
			work := filepath.Join(root, mode)
			release := filepath.Join(work, "release")
			if err := os.MkdirAll(release, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(release, "queue-any-any.tar.zst"), archive, 0644); err != nil {
				t.Fatal(err)
			}
			tool := filepath.Join(release, "autolib-importer-"+osName+"-"+arch+executableSuffix())
			if err := copyCIFile(importer, tool); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(tool, 0755); err != nil {
				t.Fatal(err)
			}
			o := ciValidationOptions{SourceRoot: source, InstallRoot: filepath.Join(work, "install"), BuildRoot: work, ReleaseRoot: release, ImporterFile: importer, CMakeCommand: cmake, EvidenceDirectory: filepath.Join(work, "evidence"), Modules: []string{"queue"}, Context: production, Mode: mode}
			result, err := validateCIRelease(context.Background(), o)
			if err != nil || !result.Passed {
				t.Fatalf("validation failed: %+v %v", result, err)
			}
			first := mustReadText(t, filepath.Join(result.EvidenceDirectory, "first-ctest.log"))
			if !strings.Contains(first, "100% tests passed") {
				t.Fatalf("first consumer did not run: %s", first)
			}
			_, secondErr := os.Stat(filepath.Join(result.EvidenceDirectory, "moved-ctest.log"))
			if mode == "single" && !os.IsNotExist(secondErr) || mode == "relocate" && secondErr != nil {
				t.Fatalf("unexpected second consumer: %v", secondErr)
			}
			if err := os.Remove(filepath.Join(release, "queue-any-any.tar.zst")); err != nil {
				t.Fatal(err)
			}
			failed, err := validateCIRelease(context.Background(), o)
			if err == nil || failed.Passed || !strings.Contains(err.Error(), "RELEASE_READY=ON requires") {
				t.Fatalf("missing asset did not fail directly: %+v %v", failed, err)
			}
		})
	}
}

func TestCIConsumerSelection(t *testing.T) {
	for _, test := range []struct {
		modules []string
		valid   bool
	}{
		{[]string{"queue"}, true},
		{[]string{"lz4"}, true},
		{[]string{"lz4", "flann"}, true},
		{[]string{"flann"}, false},
		{[]string{"queue", "queue"}, false},
		{[]string{"opencv"}, false},
	} {
		err := validateCIConsumerModules(test.modules)
		if (err == nil) != test.valid {
			t.Errorf("validateCIConsumerModules(%v) error=%v, want valid=%v", test.modules, err, test.valid)
		}
	}
}

func TestCIABIGate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	required := []string{"GLIBC_2.32", "GLIBCXX_3.4.28", "CXXABI_1.3.14", "GCC_4.9.0"}
	if err := verifyLinuxABIVersions(required, "2.31"); err == nil || !strings.Contains(err.Error(), "GLIBC_2.32") {
		t.Fatalf("accepted a consumer GLIBC requirement above the configured limit: %v", err)
	}
	required[0] = "GLIBC_2.31"
	if err := verifyLinuxABIVersions(required, "2.31"); err != nil {
		t.Fatalf("rejected compatible GLIBC with recorded C++ ABI requirements: %v", err)
	}
}

func TestCILinuxABIRecordsRealSymbolRequirements(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires native Linux compiler and objdump")
	}
	root := t.TempDir()
	packages := filepath.Join(root, "packages")
	libraryRoot := filepath.Join(packages, "flann", "lib")
	first := filepath.Join(root, "first")
	for _, directory := range []string{libraryRoot, first} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeCIFixture(t, root, "first/prepared-root.txt", packages+"\n")
	writeCIFixture(t, root, "consumer.cpp", "#include <cstdio>\nint main() { return std::puts(\"ABI fixture\") < 0; }\n")
	writeCIFixture(t, root, "library.cpp", "#include <cstdio>\n#include <string>\nextern \"C\" int abi_fixture() { std::string text(128, 'x'); return std::puts(text.c_str()); }\n")
	for _, args := range [][]string{
		{filepath.Join(root, "consumer.cpp"), "-o", filepath.Join(first, "autolib_smoke")},
		{"-fPIC", "-shared", filepath.Join(root, "library.cpp"), "-o", filepath.Join(libraryRoot, "libflann.so")},
	} {
		if output, err := exec.Command("g++", args...).CombinedOutput(); err != nil {
			t.Fatalf("compile ABI fixture: %v\n%s", err, output)
		}
	}
	if err := verifyLinuxABIEvidence(root, "2.99", "single"); err != nil {
		t.Fatal(err)
	}
	report := mustReadText(t, filepath.Join(root, "abi-requirements.txt"))
	for _, prefix := range []string{"GLIBC_", "GLIBCXX_", "CXXABI_", "GCC_"} {
		if !strings.Contains(report, prefix) {
			t.Errorf("real consumer/library %s requirements missing from ABI report: %q", prefix, report)
		}
	}
	if err := verifyLinuxABIEvidence(root, "2.0", "single"); err == nil {
		t.Fatal("accepted real GLIBC requirements above the configured maximum")
	}
}

func TestCIWindowsIdentity(t *testing.T) {
	config, err := loadCIEnvironment(filepath.Join("..", "..", ".github", "ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	context := ciProductionContext{OS: "windows", Arch: "x86_64", Generator: config.Production.Windows.Generator, Platform: config.Production.Windows.Platform, Toolset: "v142,host=x64", Configuration: config.Production.Common.Configuration, Runtime: config.Production.Windows.Runtime, MultiConfig: true, CMakeDefaults: config.Production.Common.CMakeDefaults}
	if err := validateCIProductionIdentity(context, config); err == nil || !strings.Contains(strings.ToLower(err.Error()), "configuration") {
		t.Fatalf("accepted a CI consumer identity that differs from ci-tools.json: %v", err)
	}
}

func TestCIValidationPathsResolveExecutableSymlinks(t *testing.T) {
	t.Run("accepts internal targets", func(t *testing.T) {
		o := ciValidationPathFixture(t)
		link := filepath.Join(o.SourceRoot, "tools", "importer-link.exe")
		target := filepath.Join(o.SourceRoot, "tools", "importer-real.exe")
		createCIExecutableSymlink(t, link, target)
		o.ImporterFile = link
		if _, err := validateCIValidationPaths(o); err != nil {
			t.Fatalf("rejected executable symlinks resolving inside source root: %v", err)
		}
	})

	t.Run("rejects external importer target", func(t *testing.T) {
		o := ciValidationPathFixture(t)
		outside := filepath.Join(filepath.Dir(o.SourceRoot), "external-importer.exe")
		link := filepath.Join(o.SourceRoot, "tools", "importer-link.exe")
		createCIExecutableSymlink(t, link, outside)
		o.ImporterFile = link
		if _, err := validateCIValidationPaths(o); err == nil || !strings.Contains(err.Error(), "importer file must resolve inside source root") {
			t.Fatalf("accepted importer symlink resolving outside source root: %v", err)
		}
	})
}

func ciValidationPathFixture(t *testing.T) ciValidationOptions {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	for _, path := range []string{
		filepath.Join(source, "tools"), filepath.Join(source, "install"),
		filepath.Join(source, "build", "release"), filepath.Join(source, "build", "evidence"),
	} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	return ciValidationOptions{
		SourceRoot: source, InstallRoot: filepath.Join(source, "install"),
		BuildRoot: filepath.Join(source, "build"), ReleaseRoot: filepath.Join(source, "build", "release"),
		ImporterFile: filepath.Join(source, "tools", "importer.exe"),
		CMakeCommand: filepath.Join(source, "cmake.exe"), EvidenceDirectory: filepath.Join(source, "build", "evidence"),
	}
}

func createCIExecutableSymlink(t *testing.T, link, target string) {
	t.Helper()
	if err := os.WriteFile(target, []byte("fixture executable"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		if errors.Is(err, os.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "privilege") {
			t.Skipf("host does not permit creating symlink fixtures: %v", err)
		}
		t.Fatalf("create executable symlink: %v", err)
	}
}

func TestCIWindowsPackageInputsRequireRelocatedDirectLZ4(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "moved packages")
	flannLink := filepath.Join(packageRoot, "flann", "lib", "flann.lib")
	lz4Link := filepath.Join(packageRoot, "lz4", "lib", "lz4.lib")
	for _, path := range []string{flannLink, lz4Link} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	second := filepath.Join(root, "second")
	if err := os.MkdirAll(second, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "link-inputs-Release.txt"), []byte(flannLink+"\n"+lz4Link+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	properties := "flann::flann\tINTERFACE_LINK_LIBRARIES\t" + filepath.Join(packageRoot, "flann", "lib", "cmake", "flann", "..", "..", "..", "..", "lz4", "lib", "lz4.lib") + "\n"
	propertiesPath := filepath.Join(second, "imported-properties.txt")
	if err := os.WriteFile(propertiesPath, []byte(properties), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsPackageInputs(root, "relocate"); err != nil {
		t.Fatalf("rejected relocated FLANN and direct LZ4 link inputs: %v", err)
	}
	if err := os.WriteFile(propertiesPath, []byte("flann::flann\tINTERFACE_LINK_LIBRARIES\t"+filepath.Join(t.TempDir(), "lz4", "lib", "lz4.lib")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsPackageInputs(root, "relocate"); err == nil {
		t.Fatal("accepted FLANN's direct LZ4 link input outside the relocated packages")
	}
}

func TestCIImporterSmoke(t *testing.T) {
	root := t.TempDir()
	goCommand := filepath.Join(os.Getenv("GOROOT"), "bin", "go")
	if runtime.GOOS == "windows" {
		goCommand += ".exe"
	}
	importer := filepath.Join(root, "autolib-importer")
	if runtime.GOOS == "windows" {
		importer += ".exe"
	}
	build := exec.Command(goCommand, "build", "-mod=readonly", "-trimpath", "-o", importer, "../autolib-importer")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off", "GOTOOLCHAIN=local")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build importer fixture: %v\n%s", err, output)
	}
	if err := verifyImporterNoCRuntime(importer); err != nil {
		t.Fatal(err)
	}
	targetOS := runtime.GOOS
	targetArch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if targetOS != "windows" && targetOS != "linux" || targetArch == "" {
		t.Skipf("unsupported importer smoke host %s/%s", targetOS, targetArch)
	}
	release := filepath.Join(root, "release")
	if err := os.MkdirAll(release, 0755); err != nil {
		t.Fatal(err)
	}
	asset := "queue-any-any.tar.zst"
	archive := testQueueArchive(t, "any", "any")
	if err := os.WriteFile(filepath.Join(release, asset), archive, 0644); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(root, "result.cmake")
	command := exec.Command(importer, "prepare", "--local-root="+release, "--work-root="+filepath.Join(root, "work"), "--os="+targetOS, "--arch="+targetArch, "--result="+result, "--package=queue")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("local importer prepare failed: %v\n%s", err, output)
	}
	if data, err := os.ReadFile(result); err != nil || !strings.Contains(string(data), "AUTOLIB_PREPARED_PACKAGES [[queue]]") {
		t.Fatalf("prepare did not produce the queue result: %v\n%s", err, data)
	}
	if _, err := os.Stat(filepath.Join(release, "packages", targetOS+"-"+targetArch, "queue", "autolib-import.cmake")); err != nil {
		t.Fatalf("prepare did not install the local package: %v", err)
	}
}

func testQueueArchive(t *testing.T, osName, arch string) []byte {
	t.Helper()
	var data bytes.Buffer
	encoder, err := zstd.NewWriter(&data)
	if err != nil {
		t.Fatal(err)
	}
	archive := tar.NewWriter(encoder)
	files := map[string]string{
		"queue/autolib-import.cmake":              testIdentityHeader("queue", osName, arch),
		"queue/include/queue/readerwriterqueue.h": "// fixture header\n",
	}
	for name, body := range files {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
