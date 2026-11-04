package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
	writeCIFixture(t, root, "install/autolib.cmake", "# installed entry for driver argument fixture\n")
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
	packages := filepath.Join(root, "installed")
	libraryRoot := filepath.Join(packages, "fresh", "1", "lib")
	run := filepath.Join(root, "validation", "run-fixture")
	build := filepath.Join(run, "build")
	for _, directory := range []string{libraryRoot, build} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeCIFixture(t, root, "consumer.cpp", "#include <cstdio>\nint main() { return std::puts(\"ABI fixture\") < 0; }\n")
	writeCIFixture(t, root, "library.cpp", ciABIFixtureLibrary)
	for _, args := range [][]string{
		{filepath.Join(root, "consumer.cpp"), "-o", filepath.Join(build, "autolib_smoke")},
		{"-fPIC", "-shared", filepath.Join(root, "library.cpp"), "-o", filepath.Join(libraryRoot, "libfresh.so")},
	} {
		if output, err := exec.Command("g++", args...).CombinedOutput(); err != nil {
			t.Fatalf("compile ABI fixture: %v\n%s", err, output)
		}
	}
	// This version is required by getentropy inside the library, not by the executable.
	_, consumerRequirements, err := linuxObjdumpVersions(filepath.Join(build, "autolib_smoke"))
	if err != nil || consumerRequirements["GLIBC_2.25"] {
		t.Fatalf("consumer cannot isolate library-only GLIBC_2.25 requirement: %v %v", consumerRequirements, err)
	}
	writeCIFixture(t, packages, "unrelated/1/lib/libunrelated.so", "not an ELF library\n")
	writeCIFixture(t, packages, "fresh/2/lib/libfresh.so", "unselected version is not an ELF library\n")
	packageRoots := []string{filepath.Join(packages, "fresh", "1")}
	if err := verifyLinuxABIEvidence(run, packageRoots, "2.31"); err != nil {
		t.Fatal(err)
	}
	report := mustReadText(t, filepath.Join(run, "abi-requirements.txt"))
	for _, prefix := range []string{"GLIBC_", "GLIBCXX_", "CXXABI_", "GCC_"} {
		if !strings.Contains(report, prefix) {
			t.Errorf("real consumer/library %s requirements missing from ABI report: %q", prefix, report)
		}
	}
	if !slices.Contains(strings.Fields(report), "GLIBC_2.25") {
		t.Errorf("library-only GLIBC_2.25 missing from ABI report: %q", report)
	}
	if err := verifyLinuxABIEvidence(run, packageRoots, "2.24"); err == nil || !strings.Contains(err.Error(), "GLIBC_2.25") {
		t.Fatalf("accepted library-only GLIBC requirement above the configured maximum: %v", err)
	}
}

const ciABIFixtureLibrary = `#include <cstdio>
#include <string>
#include <unistd.h>
extern "C" int abi_fixture() {
    char random;
    if (getentropy(&random, 1) != 0) return -1;
    std::string text(128, 'x');
    return std::puts(text.c_str()) < 0 ? -1 : 1;
}
`

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
	t.Run("does not require an archive release root", func(t *testing.T) {
		o := ciValidationPathFixture(t)
		if _, err := validateCIValidationPaths(o); err != nil {
			t.Fatalf("installed validation still requires archive inputs: %v", err)
		}
	})

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
		filepath.Join(source, "build", "evidence"),
	} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	return ciValidationOptions{
		SourceRoot: source, InstallRoot: filepath.Join(source, "install"),
		BuildRoot:    filepath.Join(source, "build"),
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

func TestCIWindowsPackageInputsRequirePreparedDirectLZ4(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "installed")
	flannLink := filepath.Join(packageRoot, "flann", "1", "lib", "flann.lib")
	lz4Link := filepath.Join(packageRoot, "lz4", "1", "lib", "lz4.lib")
	for _, path := range []string{flannLink, lz4Link} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	run := filepath.Join(root, "validation", "run-fixture")
	build := filepath.Join(run, "build")
	if err := os.MkdirAll(build, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(build, "link-inputs-Release.txt"), []byte(flannLink+"\n"+lz4Link+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	properties := "flann::flann\tINTERFACE_LINK_LIBRARIES\t" + filepath.Join(packageRoot, "flann", "1", "..", "..", "lz4", "1", "lib", "lz4.lib") + "\n"
	propertiesPath := filepath.Join(build, "imported-properties.txt")
	if err := os.WriteFile(propertiesPath, []byte(properties), 0644); err != nil {
		t.Fatal(err)
	}
	packageRoots := []string{filepath.Join(packageRoot, "flann", "1"), filepath.Join(packageRoot, "lz4", "1")}
	if err := verifyWindowsPackageInputs(run, packageRoots); err != nil {
		t.Fatalf("rejected prepared FLANN and direct LZ4 link inputs: %v", err)
	}
	for _, test := range []struct{ name, input, direct string }{
		{"external link", filepath.Join(t.TempDir(), "external.lib"), lz4Link},
		{"external LZ4", lz4Link, filepath.Join(t.TempDir(), "lz4.lib")},
		{"unselected installed version", filepath.Join(packageRoot, "lz4", "2", "lib", "lz4.lib"), lz4Link},
		{"unselected installed LZ4", lz4Link, filepath.Join(packageRoot, "lz4", "2", "lib", "lz4.lib")},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, path := range []string{test.input, test.direct} {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0644); err != nil {
					t.Fatal(err)
				}
			}
			writeCIFixture(t, root, filepath.Join("validation", "run-fixture", "build", "link-inputs-Release.txt"), flannLink+"\n"+test.input+"\n")
			if err := os.WriteFile(propertiesPath, []byte("flann::flann\tINTERFACE_LINK_LIBRARIES\t"+test.direct+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := verifyWindowsPackageInputs(run, packageRoots); err == nil || !strings.Contains(err.Error(), "must resolve inside selected installed packages") {
				t.Fatalf("did not reject link input outside the selected installed packages: %v", err)
			}
		})
	}
}

func TestCIImporterSmoke(t *testing.T) {
	root := t.TempDir()
	importer := buildTestImporter(t)
	if err := verifyImporterNoCRuntime(importer); err != nil {
		t.Fatal(err)
	}
	osName, arch := testPlatform()
	command := exec.Command(importer, "prepare", "--cache-root="+root, "--os="+osName, "--arch="+arch, "--result="+filepath.Join(root, "result.cmake"), "--package=ci-smoke")
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(output), "missing package input") {
		t.Fatalf("host importer did not reject controlled missing input: %v %s", err, output)
	}
}
