package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVersionReference(t *testing.T) {
	for _, value := range []string{"3.4.0", "v1.2.0", "1.0.0+cpu", "V1", strings.Repeat("a", 12)} {
		t.Run(value, func(t *testing.T) {
			if err := validatePackageVersion(value); err != nil {
				t.Fatal(err)
			}
			ref, err := parseModuleRef("eigen@" + value)
			if err != nil || ref != (moduleRef{Name: "eigen", Version: value}) || ref.String() != "eigen@"+value {
				t.Fatalf("unexpected exact reference: %+v, %v", ref, err)
			}
		})
	}
	ref, err := parseModuleRef("eigen")
	if err != nil || ref != (moduleRef{Name: "eigen"}) || ref.String() != "eigen" {
		t.Fatalf("unexpected bare reference: %+v, %v", ref, err)
	}
	for _, value := range []string{"", "../x", "a/b", `a\b`, "a@b", "CON.txt", "Lpt1", "1.", "PACKAGE.TOML", "PRN", "aux.data", "nul", "com9.log", "lpt9", "a b", " a", "é", "-1"} {
		t.Run("invalid/"+value, func(t *testing.T) {
			if err := validatePackageVersion(value); err == nil {
				t.Fatalf("accepted invalid version %q", value)
			}
			if _, err := parseModuleRef("eigen@" + value); err == nil {
				t.Fatalf("accepted invalid exact reference %q", value)
			}
		})
	}
	for _, value := range []string{"", "@1", "eigen@", "eigen@@1", "eigen@1@2", " eigen", "eigen ", "a/b", "a b@1"} {
		if _, err := parseModuleRef(value); err == nil {
			t.Errorf("accepted invalid reference %q", value)
		}
	}
}

func versionRecipe(t *testing.T, root, name, version string) string {
	t.Helper()
	return putFile(t, filepath.Join(root, "src", "dependencies", name, version, "module.toml"), "name='"+name+"'\nversion='"+version+"'\ntype='cmake'\n"+testSource)
}

func TestVersionCatalog(t *testing.T) {
	t.Run("all recipes and exact selection", func(t *testing.T) {
		root := t.TempDir()
		for _, ref := range [][2]string{{"zlib", "1"}, {"eigen", "v1.2.0"}, {"eigen", "3.4.0"}, {"eigen", strings.Repeat("a", 12)}} {
			versionRecipe(t, root, ref[0], ref[1])
		}
		putFile(t, filepath.Join(root, "src", "dependencies", "eigen", "notes", "README.md"), "not a recipe")
		pointer := filepath.Join(root, "src", "dependencies", "eigen", "package.toml")
		putFile(t, pointer, "latest='v1.2.0'\n")
		got, err := discoverModuleRefs(root)
		want := []moduleRef{{"eigen", "3.4.0"}, {"eigen", strings.Repeat("a", 12)}, {"eigen", "v1.2.0"}, {"zlib", "1"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("catalog: got %+v, %v; want %+v", got, err, want)
		}
		for _, tc := range []struct{ request, want moduleRef }{
			{moduleRef{"eigen", ""}, moduleRef{"eigen", "v1.2.0"}},
			{moduleRef{"eigen", "3.4.0"}, moduleRef{"eigen", "3.4.0"}},
		} {
			got, err := resolveModuleRef(root, tc.request)
			if err != nil || got != tc.want {
				t.Fatalf("resolve %+v: %+v, %v", tc.request, got, err)
			}
		}
		for _, request := range []moduleRef{{"eigen", "V1.2.0"}, {"Eigen", "3.4.0"}, {"eigen", "aaaaaaa"}, {"zlib", ""}, {"missing", "1"}, {"eigen", "../x"}} {
			if got, err := resolveModuleRef(root, request); err == nil {
				t.Errorf("unexpected resolution %+v -> %+v", request, got)
			}
		}
		for _, body := range []string{"", "latest=1\n", "latest=''\n", "latest='../x'\n", "latest='absent'\n", "latest='V1.2.0'\n", "latest='v1.2.0'\nunknown=true\n", "[latest]\nversion='v1.2.0'\n"} {
			putFile(t, pointer, body)
			if _, err := resolveModuleRef(root, moduleRef{Name: "eigen"}); err == nil {
				t.Errorf("accepted package metadata %q", body)
			}
			if got, err := resolveModuleRef(root, moduleRef{"eigen", "3.4.0"}); err != nil || got != (moduleRef{"eigen", "3.4.0"}) {
				t.Fatalf("exact request read latest: %+v, %v", got, err)
			}
			if _, err := discoverModuleRefs(root); err != nil {
				t.Fatalf("discovery resolved unused latest: %v", err)
			}
		}
	})
	t.Run("case collision", func(t *testing.T) {
		root := t.TempDir()
		versionRecipe(t, root, "demo", "V1")
		entries, err := os.ReadDir(filepath.Join(root, "src", "dependencies", "demo"))
		if err != nil || len(entries) != 1 || entries[0].Name() != "V1" {
			t.Fatalf("fixture directory: %v, %v", entries, err)
		}
		versionRecipe(t, root, "demo", "v1")
		entries, err = os.ReadDir(filepath.Join(root, "src", "dependencies", "demo"))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Skip("filesystem cannot represent case-colliding directories")
		}
		if _, err := discoverModuleRefs(root); err == nil {
			t.Fatal("discovery accepted case-colliding versions")
		}
		if _, err := resolveModuleRef(root, moduleRef{"demo", "V1"}); err == nil {
			t.Fatal("exact selection accepted case-colliding versions")
		}
		putFile(t, filepath.Join(root, "src", "dependencies", "demo", "package.toml"), "latest='V1'\n")
		if _, err := resolveModuleRef(root, moduleRef{Name: "demo"}); err == nil {
			t.Fatal("latest accepted case-colliding versions")
		}
	})
	t.Run("invalid recipe version", func(t *testing.T) {
		root := t.TempDir()
		versionRecipe(t, root, "demo", "a@b")
		if _, err := discoverModuleRefs(root); err == nil {
			t.Fatal("discovery accepted invalid version directory")
		}
	})
	t.Run("invalid structured package name", func(t *testing.T) {
		root := t.TempDir()
		versionRecipe(t, root, "eigen@3.4.0", "1")
		putFile(t, filepath.Join(root, "src", "dependencies", "eigen@3.4.0", "package.toml"), "latest='1'\n")
		if _, err := resolveModuleRef(root, moduleRef{Name: "eigen@3.4.0"}); err == nil {
			t.Fatal("accepted @ inside a structured package name")
		}
	})
}

func TestVersionPathIdentity(t *testing.T) {
	root := t.TempDir()
	path := versionRecipe(t, root, "demo", "v1")
	body := readFile(t, path)
	putFile(t, path, strings.Replace(body, "type='cmake'", "type='cmake'\ndepends=['eigen@3.4.0','zlib','eigen@3.4.0']", 1))
	got, err := loadDefinition(path, "Linux", "x86_64")
	if err != nil || got.Name != "demo" || got.Version != "v1" || !reflect.DeepEqual(got.Depends, []string{"eigen@3.4.0", "zlib"}) {
		t.Fatalf("valid versioned recipe: %+v, %v", got, err)
	}
	for _, tc := range []struct{ name, body string }{
		{"name mismatch", strings.Replace(body, "name='demo'", "name='other'", 1)},
		{"version mismatch", strings.Replace(body, "version='v1'", "version='V1'", 1)},
		{"missing version", strings.Replace(body, "version='v1'\n", "", 1)},
		{"invalid dependency", strings.Replace(body, "type='cmake'", "type='cmake'\ndepends=['eigen@']", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			putFile(t, path, tc.body)
			if _, err := loadDefinition(path, "Linux", "x86_64"); err == nil {
				t.Fatal("accepted invalid recipe identity or dependency")
			}
		})
	}
	putFile(t, path, body)
	for _, wrong := range []string{
		filepath.Join(root, "src", "dependencies", "Demo", "v1", "module.toml"),
		filepath.Join(filepath.Dir(path), "MODULE.TOML"),
		putFile(t, filepath.Join(root, "src", "dependencies", "old", "module.toml"), "name='old'\nversion='v1'\ntype='cmake'\n"+testSource),
	} {
		if _, err := loadDefinition(wrong, "Linux", "x86_64"); err == nil {
			t.Errorf("accepted wrong recipe path %s", wrong)
		}
	}
}
