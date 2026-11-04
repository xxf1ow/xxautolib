package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCIValidateReleaseScansInstalledBinaryClosure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires native Linux compiler and objdump")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(repository, "build", "ci-test")
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "installed binary 中文 ")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	for _, file := range []string{".github/ci-tools.json", "src/autolib.cmake", "tests/autolib/run-installed.cmake", "tests/autolib/CMakeLists.txt"} {
		writeCIFixture(t, root, file, mustReadText(t, filepath.Join(repository, file)))
	}
	install := filepath.Join(root, "installed")
	writeCIFixture(t, install, "autolib.cmake", mustReadText(t, filepath.Join(root, "src/autolib.cmake")))
	osName, arch := testPlatform()
	importer := filepath.Join(install, ciImporterExecutable(osName+"-"+arch))
	data, err := os.ReadFile(buildTestImporter(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(importer, data, 0755); err != nil {
		t.Fatal(err)
	}
	writeCIFixture(t, root, "library.cpp", ciABIFixtureLibrary)
	library := filepath.Join(install, "fresh", "1", "lib", "libfresh.so")
	if err := os.MkdirAll(filepath.Dir(library), 0755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("g++", "-fPIC", "-shared", filepath.Join(root, "library.cpp"), "-o", library).CombinedOutput(); err != nil {
		t.Fatalf("compile installed library: %v\n%s", err, output)
	}
	header := fmt.Sprintf("set(AUTOLIB_PACKAGE_NAME \"fresh\")\nset(AUTOLIB_PACKAGE_VERSION \"1\")\nset(AUTOLIB_OS \"linux\")\nset(AUTOLIB_ARCH \"%s\")\nset(AUTOLIB_DEPENDENCIES \"\")\n", arch)
	writeCIFixture(t, install, "fresh/1/autolib-import.cmake", header+`add_library(autolib::fresh SHARED IMPORTED GLOBAL)
set_target_properties(autolib::fresh PROPERTIES IMPORTED_LOCATION_RELEASE "${CMAKE_CURRENT_LIST_DIR}/lib/libfresh.so")
`)
	// Invalid ELF sentinels make an over-broad installation scan observable.
	writeCIFixture(t, install, "unrelated/1/lib/libunrelated.so", "not in the current closure\n")
	writeCIFixture(t, install, "fresh/2/lib/libfresh.so", "not the selected version\n")
	writeCIFixture(t, root, "tests/autolib/smoke.cpp", "extern \"C\" int abi_fixture();\nint main() { return abi_fixture() == 1 ? 0 : 1; }\n")
	config, err := loadCIEnvironment(filepath.Join(root, ".github", "ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	production, err := ciProductionForPlatform(osName+"-"+arch, config)
	if err != nil {
		t.Fatal(err)
	}
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	o := ciValidationOptions{SourceRoot: root, InstallRoot: install, BuildRoot: filepath.Join(root, "build", "validation"), CMakeCommand: cmake, EvidenceDirectory: filepath.Join(root, "build", "evidence"), Modules: []string{"fresh@1"}, RootPackages: []string{"fresh@1"}, Context: production}
	before := ciInstalledBinarySnapshot(t, install)
	result, err := validateCIRelease(context.Background(), o)
	if err != nil || !result.Passed {
		t.Fatalf("direct installed binary validation failed: %+v %v", result, err)
	}
	report := mustReadText(t, filepath.Join(result.EvidenceDirectory, "abi-requirements.txt"))
	if !slices.Contains(strings.Fields(report), "GLIBC_2.25") {
		t.Errorf("validateCIRelease omitted library-only GLIBC_2.25: %q", report)
	}
	_, consumerRequirements, err := linuxObjdumpVersions(filepath.Join(result.EvidenceDirectory, "build", "autolib_smoke"))
	if err != nil || consumerRequirements["GLIBC_2.25"] {
		t.Fatalf("consumer cannot isolate library-only GLIBC requirement: %v %v", consumerRequirements, err)
	}
	inputs := mustReadText(t, filepath.Join(result.EvidenceDirectory, "build", "link-inputs-Release.txt"))
	if !strings.Contains(inputs, library) || strings.Contains(inputs, "consumer/3rdparty") {
		t.Fatalf("consumer did not link the direct installed library: %q", inputs)
	}
	configFile := filepath.Join(root, ".github", "ci-tools.json")
	writeCIFixture(t, root, ".github/ci-tools.json", strings.Replace(mustReadText(t, configFile), `"glibc_max": "2.31"`, `"glibc_max": "2.24"`, 1))
	if _, err := validateCIRelease(context.Background(), o); err == nil || !strings.Contains(err.Error(), "GLIBC_2.25") {
		t.Fatalf("validateCIRelease accepted library ABI above configured baseline: %v", err)
	}
	if after := ciInstalledBinarySnapshot(t, install); !equalStringMap(before, after) {
		t.Fatal("CI validation changed installed binary inputs")
	}
}

func ciInstalledBinarySnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files[path] = testDigest(data) + ":" + info.Mode().String()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}
