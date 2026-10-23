package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

type ciEnvironment struct {
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
}

var digestValue = regexp.MustCompile(`^[0-9a-f]{64}$`)

func loadCIEnvironment(path string) (ciEnvironment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ciEnvironment{}, err
	}
	var config ciEnvironment
	if err := json.Unmarshal(data, &config); err != nil {
		return ciEnvironment{}, err
	}
	if err := validateCIEnvironment(config); err != nil {
		return ciEnvironment{}, err
	}
	return config, nil
}

func validateCIEnvironment(config ciEnvironment) error {
	const image = "ubuntu:20.04@sha256:8feb4d8ca5354def3d8fce243717141ce31e2c428701f6682bd2fafe15388214"
	if config.Ubuntu.Image != image {
		return fmt.Errorf("ubuntu.image: expected fixed Ubuntu 20.04 index digest, got %q", config.Ubuntu.Image)
	}
	wantPlatforms := map[string]string{
		"linux/amd64":    "sha256:c664f8f86ed5a386b0a340d981b8f81714e21a8b9c73f658c4bea56aa179d54a",
		"linux/arm64/v8": "sha256:722ea796ac2d57eeb3627c58a582fc1acc58be51faf815e1bce1682ae5c092f7",
	}
	for platform, expected := range wantPlatforms {
		value := strings.TrimPrefix(config.Ubuntu.PlatformDigests[platform], "sha256:")
		if !digestValue.MatchString(value) || config.Ubuntu.PlatformDigests[platform] != expected {
			return fmt.Errorf("ubuntu.platform_digests.%s: expected sha256 digest", platform)
		}
	}
	if len(config.Ubuntu.PlatformDigests) != 2 {
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
	return nil
}
