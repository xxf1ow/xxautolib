package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCIInstalledDriverProductionInputs(t *testing.T) {
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(repository, "build", "ci-test")
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "driver-inputs-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	writeCIFixture(t, root, "src/autolib.cmake", "# unused entry for driver argument fixture\n")
	writeCIFixture(t, root, "tests/autolib/CMakeLists.txt", `cmake_minimum_required(VERSION 3.20)
project(driver_inputs NONE)
enable_testing()
file(WRITE "${CMAKE_BINARY_DIR}/inputs.txt" "${CMAKE_MSVC_RUNTIME_LIBRARY}\n${CMAKE_INSTALL_LIBDIR}\n${CMAKE_POLICY_VERSION_MINIMUM}\n")
file(WRITE "${CMAKE_BINARY_DIR}/prepared-state.cmake" "")
add_test(NAME forwarded COMMAND "${CMAKE_COMMAND}" -E true)
`)
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	generator := "Unix Makefiles"
	if runtime.GOOS == "windows" {
		generator = "Visual Studio 17 2022"
	}
	output, err := exec.Command(cmake, "-DSOURCE_ROOT="+root, "-DINSTALL_ROOT="+filepath.Join(root, "install"), "-DVALIDATION_ROOT="+filepath.Join(root, "build", "validation"), "-DGENERATOR="+generator, "-DCMAKE_MSVC_RUNTIME_LIBRARY=MultiThreadedDLL", "-DCMAKE_INSTALL_LIBDIR=fixture-lib", "-DCMAKE_POLICY_VERSION_MINIMUM=3.5", "-P", filepath.Join(repository, "tests", "autolib", "run-installed.cmake")).CombinedOutput()
	if err != nil {
		t.Fatalf("driver failed: %v %s", err, output)
	}
	runRoot, err := validationRunFromOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustReadText(t, filepath.Join(runRoot, "build", "inputs.txt")); got != "MultiThreadedDLL\nfixture-lib\n3.5\n" {
		t.Fatalf("driver lost production inputs: %q", got)
	}
}

func TestCIConsumerSelection(t *testing.T) {
	for _, test := range []struct {
		modules []string
		valid   bool
	}{
		{[]string{"queue@1"}, true},
		{[]string{"lz4@1"}, true},
		{[]string{"lz4@1", "flann@1"}, true},
		{[]string{"flann@1"}, true},
		{[]string{"queue@1", "queue@1"}, false},
		{[]string{"opencv@1"}, true},
		{[]string{"bad/name"}, false},
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
	libraryRoot := filepath.Join(packages, "fresh", "lib")
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
		{"-fPIC", "-shared", filepath.Join(root, "library.cpp"), "-o", filepath.Join(libraryRoot, "libfresh.so")},
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
	importer := buildTestImporter(t)
	if err := verifyImporterNoCRuntime(importer); err != nil {
		t.Fatal(err)
	}
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	osName, arch := testPlatform()
	command := exec.Command(importer, "prepare", "--local-root="+root, "--cache-root="+filepath.Join(root, "cache"), "--work-root="+filepath.Join(root, "work"), "--cmake-command="+cmake, "--os="+osName, "--arch="+arch, "--result="+filepath.Join(root, "result.cmake"), "--package=ci-smoke")
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(output), "missing package input") {
		t.Fatalf("host importer did not reject controlled missing input: %v %s", err, output)
	}
}
