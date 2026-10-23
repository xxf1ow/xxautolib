package main

import (
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
	if config.Ubuntu.Image != "ubuntu:20.04@sha256:8feb4d8ca5354def3d8fce243717141ce31e2c428701f6682bd2fafe15388214" || len(config.Ubuntu.PlatformDigests) != 2 {
		t.Fatalf("Ubuntu index or architecture coverage: %+v", config.Ubuntu)
	}
	if config.Make.Version != "4.4.1" || config.CMake.Version != "3.31.12" || len(config.CMake.Assets) != 3 {
		t.Fatalf("tool versions/assets: make=%+v cmake=%+v", config.Make, config.CMake)
	}
	if err := validateCIEnvironment(config); err != nil {
		t.Fatal(err)
	}
	missingArchitecture := config
	missingArchitecture.Ubuntu.PlatformDigests = map[string]string{"linux/amd64": config.Ubuntu.PlatformDigests["linux/amd64"]}
	if err := validateCIEnvironment(missingArchitecture); err == nil || !strings.Contains(err.Error(), "linux/arm64/v8") {
		t.Fatalf("missing Ubuntu architecture accepted: %v", err)
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
