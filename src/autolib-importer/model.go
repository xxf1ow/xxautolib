package main

import (
	"fmt"
	"regexp"
)

type packageIdentity struct{ Name, OS, Arch string }
type preparedPackage struct{ Name, Directory string }
type importerRecord struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Asset  string `json:"asset"`
	SHA256 string `json:"sha256"`
}
type packageRecord struct {
	Name   string `json:"name"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Asset  string `json:"asset"`
	SHA256 string `json:"sha256"`
}
type manifest struct {
	Importers []importerRecord `json:"importers"`
	Packages  []packageRecord  `json:"packages"`
}

var packageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.+\-]*$`)

func validateIdentity(id packageIdentity) error {
	if !packageName.MatchString(id.Name) {
		return fmt.Errorf("invalid package name %q", id.Name)
	}
	if id.OS == "any" || id.Arch == "any" {
		if id.OS == "any" && id.Arch == "any" {
			return nil
		}
		return fmt.Errorf("unsupported platform %s/%s", id.OS, id.Arch)
	}
	return validatePlatform(id.OS, id.Arch)
}

func matchesPackageIdentity(actual, requested packageIdentity) bool {
	if actual.Name != requested.Name || validatePlatform(requested.OS, requested.Arch) != nil {
		return false
	}
	return (actual.OS == requested.OS && actual.Arch == requested.Arch) || (actual.OS == "any" && actual.Arch == "any")
}

func validatePlatform(system, arch string) error {
	if (system != "windows" && system != "linux") || (arch != "x86_64" && arch != "aarch64") {
		return fmt.Errorf("unsupported platform %s/%s", system, arch)
	}
	return nil
}
