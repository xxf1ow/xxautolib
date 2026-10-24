package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIEnvironmentConfig(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	config, err := loadCIEnvironment(filepath.Join(root, ".github", "ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(config.Ubuntu.Image, "ubuntu:20.04@sha256:") || len(config.Ubuntu.PlatformDigests) != 2 {
		t.Fatalf("Ubuntu index or architecture coverage: %+v", config.Ubuntu)
	}
	if config.Make.Version == "" || config.CMake.Version == "" || len(config.CMake.Assets) != 3 {
		t.Fatalf("tool versions/assets: make=%+v cmake=%+v", config.Make, config.CMake)
	}
	if err := validateCIEnvironment(config); err != nil {
		t.Fatal(err)
	}
	missingMakeDigest := config
	missingMakeDigest.Make.SHA256 = ""
	if err := validateCIEnvironment(missingMakeDigest); err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatalf("missing Make digest accepted: %v", err)
	}
	if config.Production.Common.Configuration == "" {
		t.Fatalf("production common baseline: %+v", config.Production.Common)
	}
	if config.Production.Linux.GCCVersion == "" || config.Production.Linux.GlibcMax == "" || config.Production.Linux.Generator == "" {
		t.Fatalf("production Linux baseline: %+v", config.Production.Linux)
	}
	if config.Production.Windows.Generator == "" || config.Production.Windows.Platform == "" || config.Production.Windows.Toolset == "" || config.Production.Windows.Runtime == "" {
		t.Fatalf("production Windows baseline: %+v", config.Production.Windows)
	}
	missingArchitecture := config
	missingArchitecture.Ubuntu.PlatformDigests = map[string]string{"linux/amd64": config.Ubuntu.PlatformDigests["linux/amd64"]}
	if err := validateCIEnvironment(missingArchitecture); err == nil || !strings.Contains(err.Error(), "linux/arm64/v8") {
		t.Fatalf("missing Ubuntu architecture accepted: %v", err)
	}
	missingAMD64 := config
	missingAMD64.Ubuntu.PlatformDigests = map[string]string{"linux/arm64/v8": config.Ubuntu.PlatformDigests["linux/arm64/v8"]}
	if err := validateCIEnvironment(missingAMD64); err == nil || !strings.Contains(err.Error(), "linux/amd64") {
		t.Fatalf("missing Ubuntu amd64 architecture accepted: %v", err)
	}
	driftingTag := config
	driftingTag.Ubuntu.Image = "ubuntu:20.04"
	if err := validateCIEnvironment(driftingTag); err == nil || !strings.Contains(err.Error(), "ubuntu.image") {
		t.Fatalf("drifting Ubuntu tag accepted: %v", err)
	}
	config.CMake.Assets[0].SHA256 = ""
	if err := validateCIEnvironment(config); err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatalf("missing asset digest accepted: %v", err)
	}
}

func TestCIEnvironmentRejectsMissingProductionFields(t *testing.T) {
	for _, field := range []struct{ section, name string }{
		{"common", "configuration"}, {"common", "cmake_defaults"},
		{"linux", "distribution"}, {"linux", "gcc_version"}, {"linux", "glibc_max"}, {"linux", "generator"},
		{"windows", "generator"}, {"windows", "platform"}, {"windows", "toolset"}, {"windows", "runtime"},
	} {
		t.Run(field.section+"/"+field.name, func(t *testing.T) {
			document := environmentFixture(t)
			production := document["production"].(map[string]any)
			delete(production[field.section].(map[string]any), field.name)
			if err := loadEnvironmentDocument(t, document); err == nil {
				t.Fatalf("missing production.%s.%s was accepted", field.section, field.name)
			}
		})
	}
}

func TestCIEnvironmentRejectsUnsupportedDefaults(t *testing.T) {
	document := environmentFixture(t)
	production := document["production"].(map[string]any)
	production["common"].(map[string]any)["cmake_defaults"] = map[string]any{"CMAKE_INSTALL_LIBDIR": "lib64", "CMAKE_POLICY_VERSION_MINIMUM": "3.5"}
	if err := loadEnvironmentDocument(t, document); err == nil {
		t.Fatal("unsupported production.common.cmake_defaults was accepted")
	}
}

func TestCIEnvironmentUsesConfiguredToolVersions(t *testing.T) {
	document := environmentFixture(t)
	cmake := document["cmake"].(map[string]any)
	cmake["version"] = "3.32.0"
	cmake["checksums_url"] = "https://github.com/Kitware/CMake/releases/download/v3.32.0/cmake-3.32.0-SHA-256.txt"
	for _, asset := range cmake["assets"].([]any) {
		entry := asset.(map[string]any)
		filename := strings.Replace(entry["filename"].(string), "3.31.12", "3.32.0", 1)
		entry["filename"] = filename
		entry["url"] = strings.ReplaceAll(entry["url"].(string), "3.31.12", "3.32.0")
	}
	make := document["make"].(map[string]any)
	make["version"] = "4.5.0"
	make["url"] = "https://ftp.gnu.org/gnu/make/make-4.5.0.tar.gz"
	if err := loadEnvironmentDocument(t, document); err != nil {
		t.Fatalf("rejected tool versions consistently selected in configuration: %v", err)
	}
}

func TestCIEnvironmentDigestsAreConfigurationOwned(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	config, err := loadCIEnvironment(filepath.Join(root, ".github", "ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	config.Ubuntu.Image = "ubuntu:20.04@sha256:" + strings.Repeat("a", 64)
	config.Ubuntu.PlatformDigests = map[string]string{
		"linux/amd64":    "sha256:" + strings.Repeat("b", 64),
		"linux/arm64/v8": "sha256:" + strings.Repeat("c", 64),
	}
	config.Make.SHA256 = strings.Repeat("d", 64)
	for i := range config.CMake.Assets {
		config.CMake.Assets[i].SHA256 = strings.Repeat("e", 64)
	}
	if err := validateCIEnvironment(config); err != nil {
		t.Fatalf("valid digests owned by ci-tools.json were rejected: %v", err)
	}
}

func environmentFixture(t *testing.T) map[string]any {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".github", "ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["production"] = map[string]any{
		"common": map[string]any{
			"configuration":  "Release",
			"cmake_defaults": map[string]any{"CMAKE_INSTALL_LIBDIR": "lib", "CMAKE_POLICY_VERSION_MINIMUM": "3.5"},
		},
		"linux": map[string]any{
			"distribution": "ubuntu-20.04", "gcc_version": "9.4", "glibc_max": "2.31", "generator": "Unix Makefiles",
		},
		"windows": map[string]any{
			"generator": "Visual Studio 17 2022", "platform": "x64", "toolset": "v143,host=x64", "runtime": "MultiThreadedDLL",
		},
	}
	return document
}

func loadEnvironmentDocument(t *testing.T, document map[string]any) error {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ci-tools.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = loadCIEnvironment(path)
	return err
}
