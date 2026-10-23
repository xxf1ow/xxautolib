package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

type ciEnvironmentConfig struct {
	Ubuntu struct {
		Image           string            `json:"image"`
		PlatformDigests map[string]string `json:"platform_digests"`
	} `json:"ubuntu"`
	Make struct {
		Version string `json:"version"`
		URL     string `json:"url"`
		SHA256  string `json:"sha256"`
	} `json:"make"`
	CMake struct {
		Version      string `json:"version"`
		ChecksumsURL string `json:"checksums_url"`
		Assets       []struct {
			Platform string `json:"platform"`
			Filename string `json:"filename"`
			URL      string `json:"url"`
			SHA256   string `json:"sha256"`
		} `json:"assets"`
	} `json:"cmake"`
	Production struct {
		Common struct {
			GoVersion     string            `json:"go_version"`
			CMakeVersion  string            `json:"cmake_version"`
			Configuration string            `json:"configuration"`
			CMakeDefaults map[string]string `json:"cmake_defaults"`
		} `json:"common"`
		Linux struct {
			Distribution string `json:"distribution"`
			GCCVersion   string `json:"gcc_version"`
			GlibcMax     string `json:"glibc_max"`
			MakeVersion  string `json:"make_version"`
			Generator    string `json:"generator"`
		} `json:"linux"`
		Windows struct {
			Generator string `json:"generator"`
			Platform  string `json:"platform"`
			Toolset   string `json:"toolset"`
			Runtime   string `json:"runtime"`
		} `json:"windows"`
	} `json:"production"`
}

var digestValue = regexp.MustCompile(`^[0-9a-f]{64}$`)

func loadCIEnvironment(path string) (ciEnvironmentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ciEnvironmentConfig{}, err
	}
	var config ciEnvironmentConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return ciEnvironmentConfig{}, err
	}
	if err := validateCIEnvironment(config); err != nil {
		return ciEnvironmentConfig{}, err
	}
	return config, nil
}

func validateCIEnvironment(config ciEnvironmentConfig) error {
	const imagePrefix = "ubuntu:20.04@sha256:"
	if !strings.HasPrefix(config.Ubuntu.Image, imagePrefix) || !digestValue.MatchString(strings.TrimPrefix(config.Ubuntu.Image, imagePrefix)) {
		return fmt.Errorf("ubuntu.image: expected Ubuntu 20.04 with an index SHA256 digest, got %q", config.Ubuntu.Image)
	}
	wantPlatforms := []string{"linux/amd64", "linux/arm64/v8"}
	for _, platform := range wantPlatforms {
		digest, exists := config.Ubuntu.PlatformDigests[platform]
		if !exists {
			return fmt.Errorf("ubuntu.platform_digests.%s: missing required platform digest", platform)
		}
		if !strings.HasPrefix(digest, "sha256:") || !digestValue.MatchString(strings.TrimPrefix(digest, "sha256:")) {
			return fmt.Errorf("ubuntu.platform_digests.%s: expected SHA256 digest", platform)
		}
	}
	if len(config.Ubuntu.PlatformDigests) != len(wantPlatforms) {
		return fmt.Errorf("ubuntu.platform_digests: expected exactly amd64 and arm64/v8")
	}
	if config.Make.Version != "4.4.1" || config.Make.URL != "https://ftp.gnu.org/gnu/make/make-4.4.1.tar.gz" || !digestValue.MatchString(config.Make.SHA256) {
		return fmt.Errorf("make: expected pinned GNU Make 4.4.1 source and SHA256")
	}
	if config.CMake.Version != "3.31.12" || config.CMake.ChecksumsURL != "https://github.com/Kitware/CMake/releases/download/v3.31.12/cmake-3.31.12-SHA-256.txt" {
		return fmt.Errorf("cmake: expected pinned Kitware CMake 3.31.12 checksums")
	}
	want := map[string]string{"linux-aarch64": "cmake-3.31.12-linux-aarch64.tar.gz", "linux-x86_64": "cmake-3.31.12-linux-x86_64.tar.gz", "windows-x86_64": "cmake-3.31.12-windows-x86_64.zip"}
	if len(config.CMake.Assets) != len(want) {
		return fmt.Errorf("cmake.assets: expected Linux aarch64/x86_64 and Windows x86_64 assets")
	}
	for _, asset := range config.CMake.Assets {
		filename, ok := want[asset.Platform]
		if !ok || filename != asset.Filename {
			return fmt.Errorf("cmake.assets: unexpected platform or drifting asset %q", asset.Filename)
		}
		if asset.URL != "https://github.com/Kitware/CMake/releases/download/v3.31.12/"+filename || !digestValue.MatchString(asset.SHA256) {
			return fmt.Errorf("cmake.assets.%s: expected official URL and SHA256", asset.Platform)
		}
		delete(want, asset.Platform)
	}
	if len(want) != 0 {
		missing := make([]string, 0, len(want))
		for platform := range want {
			missing = append(missing, platform)
		}
		slices.Sort(missing)
		return fmt.Errorf("cmake.assets: missing platform(s) %v", missing)
	}
	return validateProductionEnvironment(config)
}

func validateProductionEnvironment(config ciEnvironmentConfig) error {
	common := config.Production.Common
	if common.GoVersion != "1.27.3" || common.CMakeVersion != "3.31.12" || common.Configuration != "Release" {
		return fmt.Errorf("production.common: fixed Go, CMake, and configuration values are required")
	}
	wantDefaults := map[string]string{"CMAKE_INSTALL_LIBDIR": "lib", "CMAKE_POLICY_VERSION_MINIMUM": "3.5"}
	if !equalStringMap(common.CMakeDefaults, wantDefaults) {
		return fmt.Errorf("production.common.cmake_defaults: expected fixed CMake defaults")
	}
	linux := config.Production.Linux
	if linux.Distribution != "ubuntu-20.04" || linux.GCCVersion != "9.4" || linux.GlibcMax != "2.31" || linux.MakeVersion != "4.4.1" || linux.Generator != "Unix Makefiles" {
		return fmt.Errorf("production.linux: fixed Ubuntu, GCC, glibc, Make, and generator values are required")
	}
	windows := config.Production.Windows
	if windows.Generator != "Visual Studio 17 2022" || windows.Platform != "x64" || windows.Toolset != "v143,host=x64" || windows.Runtime != "MultiThreadedDLL" {
		return fmt.Errorf("production.windows: fixed Visual Studio generator, platform, toolset, and runtime are required")
	}
	return nil
}

func equalStringMap(actual, expected map[string]string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}
