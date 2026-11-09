package main

import (
	"strings"
	"testing"
)

const pinnedSource = "\n[source]\nGIT_REPOSITORY='repo'\nGIT_TAG='0123456789012345678901234567890123456789'\n"

func TestSourceContract(t *testing.T) {
	for _, tag := range []string{"main", "v1.0", "abcdef0", strings.Repeat("g", 40)} {
		_, path := contractRecipe(t, "type='cmake'\npackage_platforms=['linux-x86_64']\n[source]\nGIT_REPOSITORY='repo'\nGIT_TAG='"+tag+"'\n")
		if _, err := loadDefinition(path, "Linux", "amd64"); err == nil {
			t.Errorf("accepted Git tag %q", tag)
		}
	}
	for _, body := range []string{
		"source=42\n", "[source]\nGIT_REPOSITORY='repo'\n", "[source]\nURL=[]\nURL_HASH='SHA256=" + strings.Repeat("a", 64) + "'\n",
		"[source]\nURL=['']\nURL_HASH='SHA256=" + strings.Repeat("a", 64) + "'\n", "[source]\nURL=['a']\nURL_HASH='bad'\n",
		"[source]\nURL=['a']\nURL_HASH='SHA256=" + strings.Repeat("a", 64) + "'\nDOWNLOAD_NAME='dir/file'\n",
		"[source]\nGIT_REPOSITORY='repo'\nGIT_TAG='0123456789012345678901234567890123456789'\nURL=['a']\n",
	} {
		_, path := contractRecipe(t, "type='cmake'\npackage_platforms=['linux-x86_64']\n"+body)
		if _, err := loadDefinition(path, "Linux", "amd64"); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestRecipeFieldContract(t *testing.T) {
	cases := []struct{ kind, body string }{
		{"copy", "[cmake]\n"}, {"copy", "[cmake.cache]\nIGNORED=true\n"}, {"cmake", "copy=[]\n"},
		{"cmake", "[platforms.windows-x86_64]\ncopy=[{files=[],destination='.'}]\n"},
		{"cmake", "[platforms.windows-x86_64]\npatches=[{file=42}]\n"},
		{"cmake", "[platforms.windows-x86_64]\npatches=[{file='missing.patch',typo=true}]\n"},
		{"cmake", "[platforms.windows-x86_64.cmake.cache]\nCMAKE_PREFIX_PATH=true\n"},
		{"cmake", "[platforms.windows-x86_64.resources.extra]\nGIT_REPOSITORY='repo'\nGIT_TAG='main'\n"},
		{"cmake", "[platforms.windows-x86_64]\nsteps=[{name='bad',COMMAND=42}]\n"},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			body := "type='" + tc.kind + "'\npackage_platforms=['linux-x86_64','windows-x86_64']\n"
			if tc.kind == "copy" {
				body += "copy=[{files=['x.h'],destination='include'}]\n"
			}
			_, path := contractRecipe(t, body+tc.body+pinnedSource)
			if _, err := loadDefinition(path, "Linux", "amd64"); err == nil {
				t.Fatal("invalid field accepted")
			}
		})
	}
	// A resource-only copy has no primary source requirement.
	_, path := contractRecipe(t, "type='copy'\npackage_platforms=['any-any']\ncopy=[{resource='extra',directories=['.'],destination='.'}]\n[resources.extra]\nGIT_REPOSITORY='repo'\nGIT_TAG='0123456789012345678901234567890123456789'\n")
	if _, err := loadDefinition(path, "Linux", "amd64"); err != nil {
		t.Fatal(err)
	}
}

func TestSystemDependencyNames(t *testing.T) {
	for _, tc := range []struct {
		manager, entry string
		valid          bool
	}{
		{"apt", "libfoo++-dev", true}, {"choco", "CMake.1", true}, {"brew", "llvm@18", true},
		{"apt", "lib:amd64", false}, {"apt", "PKG", false}, {"choco", "-option", false},
		{"brew", "tap/pkg", false}, {"brew", "--cask", false}, {"brew", "a;touch x", false}, {"yum", "pkg", false},
	} {
		t.Run(tc.manager+tc.entry, func(t *testing.T) {
			_, path := contractRecipe(t, "type='cmake'\npackage_platforms=['any-any']\n[system_dependencies]\n"+tc.manager+"=['"+tc.entry+"']\n"+pinnedSource)
			_, err := loadDefinition(path, "Linux", "amd64")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
	_, path := contractRecipe(t, "type='cmake'\npackage_platforms=['any-any']\n[system_dependencies]\napt=['pkg','pkg']\n"+pinnedSource)
	if _, err := loadDefinition(path, "Linux", "amd64"); err == nil {
		t.Fatal("duplicate packages accepted")
	}
}

func TestRecipePatchRequiresSource(t *testing.T) {
	root, path := contractRecipe(t, "type='copy'\npackage_platforms=['any-any']\ncopy=[{resource='extra',files=['x.h'],destination='include'}]\npatches=[{file='change.patch'}]\n[resources.extra]\nGIT_REPOSITORY='repo'\nGIT_TAG='0123456789012345678901234567890123456789'\n")
	putFile(t, root+"/src/dependencies/demo/1.0/change.patch", "patch")
	if _, err := loadDefinition(path, "Linux", "amd64"); err == nil {
		t.Fatal("primary patch accepted without primary source")
	}
}

func TestRecipeMetadataAfterOverride(t *testing.T) {
	_, path := contractRecipe(t, "type='cmake'\npackage_platforms=['linux-x86_64']\npackage_name='Demo'\n[platforms.linux-x86_64]\nconfig_dir='lib/cmake/Demo'\n"+pinnedSource)
	m, err := loadDefinition(path, "Linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if m.ConfigDir != "lib/cmake/Demo" || m.PackageName != "Demo" {
		t.Fatal(m)
	}
}
