package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

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
	if err := verifyWindowsPackageInputs(root); err != nil {
		t.Fatalf("rejected relocated FLANN and direct LZ4 link inputs: %v", err)
	}
	if err := os.WriteFile(propertiesPath, []byte("flann::flann\tINTERFACE_LINK_LIBRARIES\t"+filepath.Join(t.TempDir(), "lz4", "lib", "lz4.lib")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsPackageInputs(root); err == nil {
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
	asset := "queue.tar.zst"
	archive := testQueueArchive(t, targetOS, targetArch)
	if err := os.WriteFile(filepath.Join(release, asset), archive, 0644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	manifest, err := json.Marshal(map[string]any{
		"importers": []any{},
		"packages":  []any{map[string]string{"name": "queue", "os": targetOS, "arch": targetArch, "asset": asset, "sha256": hex.EncodeToString(digest[:])}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "manifest.json"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(root, "result.cmake")
	command := exec.Command(importer, "prepare", "--uri="+release, "--work-root="+filepath.Join(root, "work"), "--os="+targetOS, "--arch="+targetArch, "--result="+result, "--package=queue")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("local importer prepare failed: %v\n%s", err, output)
	}
	if data, err := os.ReadFile(result); err != nil || !strings.Contains(string(data), "AUTOLIB_PREPARED_PACKAGES [[queue]]") {
		t.Fatalf("prepare did not produce the queue result: %v\n%s", err, data)
	}
	if _, err := os.Stat(filepath.Join(root, "work", sourceKeyForTest(release), "packages", targetOS+"-"+targetArch, "queue", "autolib-import.cmake")); err != nil {
		t.Fatalf("prepare did not install the local package: %v", err)
	}
}

func sourceKeyForTest(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}
	data := sha256.Sum256([]byte(filepath.ToSlash(filepath.Clean(absolute))))
	return hex.EncodeToString(data[:])
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
		"queue/autolib-import.cmake":              "set(AUTOLIB_PACKAGE_NAME \"queue\")\nset(AUTOLIB_OS \"" + osName + "\")\nset(AUTOLIB_ARCH \"" + arch + "\")\n",
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
