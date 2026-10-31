package main

import "testing"

func TestUniversalIdentity(t *testing.T) {
	actual := packageIdentity{Name: "queue", Version: "1.0", OS: "any", Arch: "any"}
	for _, requested := range []packageIdentity{
		{Name: "queue", Version: "1.0", OS: "windows", Arch: "x86_64"},
		{Name: "queue", Version: "1.0", OS: "linux", Arch: "x86_64"},
		{Name: "queue", Version: "1.0", OS: "linux", Arch: "aarch64"},
	} {
		if !matchesPackageIdentity(actual, requested) {
			t.Fatalf("universal identity did not match %+v", requested)
		}
	}
	for _, requested := range []packageIdentity{
		{Name: "other", Version: "1.0", OS: "linux", Arch: "x86_64"},
		{Name: "queue", Version: "1.0", OS: "linux", Arch: "mips"},
		{Name: "queue", Version: "1.0", OS: "linux", Arch: "any"},
	} {
		if matchesPackageIdentity(actual, requested) {
			t.Fatalf("universal identity matched unsupported request %+v", requested)
		}
	}
}

func TestUniversalEligibility(t *testing.T) {
	if err := validatePlatform("any", "any"); err == nil {
		t.Fatal("universal identity became a supported target platform")
	}
	if err := validateIdentity(packageIdentity{Name: "queue", Version: "1.0", OS: "any", Arch: "linux"}); err == nil {
		t.Fatal("accepted half-universal identity")
	}
}
