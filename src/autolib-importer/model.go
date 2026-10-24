package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type packageIdentity struct{ Name, OS, Arch string }
type packageMetadata struct {
	Identity     packageIdentity
	Dependencies []string
}
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

func validatePackageName(value string) error {
	stem := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	reserved := slices.Contains([]string{"CON", "PRN", "AUX", "NUL"}, stem) ||
		(len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9')
	if !packageName.MatchString(value) || strings.HasSuffix(value, ".") || reserved {
		return fmt.Errorf("invalid package name %q", value)
	}
	return nil
}

func validateIdentity(id packageIdentity) error {
	if err := validatePackageName(id.Name); err != nil {
		return err
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
