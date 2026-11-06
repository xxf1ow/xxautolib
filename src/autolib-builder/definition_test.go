package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const testSource = "\n[source]\nGIT_REPOSITORY='unused'\nGIT_TAG='fixed'\n"

func TestPackagePlatforms(t *testing.T) {
	for _, tc := range []struct {
		declaration string
		valid       bool
	}{
		{"", false}, {"package_platforms=[]\n", false},
		{"package_platforms=['linux-x86_64','linux-x86_64']\n", false},
		{"package_platforms=['macos-arm64']\n", false},
		{"package_platforms=[42]\n", false},
		{"package_platforms=['windows-x86_64','linux-x86_64','linux-aarch64','darwin-aarch64']\n", true},
	} {
		t.Run(tc.declaration, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "src/dependencies/demo/1/module.toml")
			putFile(t, filepath.Join(root, "src/dependencies/demo/package.toml"), "latest='1'")
			putFile(t, path, "type='cmake'\n"+tc.declaration+testSource)
			_, err := loadDefinition(path, "Linux", "x86_64")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, got %v", tc.valid, err)
			}
		})
	}
}

func putFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func definitionFile(t *testing.T, root, name, kind, body string) string {
	t.Helper()
	version := "1.0"
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "version=") {
			version = strings.Trim(strings.TrimPrefix(line, "version="), "'\"")
			body = strings.Replace(body, line+"\n", "", 1)
			break
		}
	}
	return definitionVersionFile(t, root, name, version, kind, body)
}

func definitionVersionFile(t *testing.T, root, name, version, kind, body string) string {
	t.Helper()
	if !strings.Contains(body, "package_platforms") {
		body = "package_platforms=['windows-x86_64','linux-x86_64','linux-aarch64','darwin-aarch64']\n" + body
	}
	putFile(t, filepath.Join(root, "src", "dependencies", name, "package.toml"), "latest='"+version+"'\n")
	return putFile(t, filepath.Join(root, "src", "dependencies", name, version, "module.toml"), "type='"+kind+"'\n"+body)
}

func TestDefinitionPlatformSelection(t *testing.T) {
	root := t.TempDir()
	path := definitionFile(t, root, "demo", "cmake", `depends=['left@1.0','right@1.0']
config_dir='lib/cmake/demo'
pkgconfig_dir='lib/pkgconfig'
package_name='Demo'
sentinel='include/demo.h'
`+testSource+`
[resources.extra]
GIT_REPOSITORY='common-extra'
GIT_TAG='fixed'
[cmake]
SOURCE_SUBDIR='build/cmake'
[cmake.cache]
A='common'
B=42
LIST=['one','two']
[[steps]]
name='common'
COMMAND=['cmake','-E','echo','common']
[platforms.linux-x86_64.source]
URL=['https://one.invalid/linux.tar.xz','https://two.invalid/linux.tar.xz']
URL_HASH='SHA256=`+strings.Repeat("a", 64)+`'
[platforms.linux-x86_64.resources.extra]
URL=['extra.tar.xz']
URL_HASH='SHA256=`+strings.Repeat("b", 64)+`'
DOWNLOAD_NAME='extra.tar.xz'
[platforms.linux-x86_64.cmake.cache]
A='linux'
C=true
[[platforms.linux-x86_64.steps]]
name='linux'
COMMAND=[['cmake','-E','echo','one'],['cmake','-E','echo','two']]
[platforms.linux-aarch64.source]
GIT_REPOSITORY='arm'
GIT_TAG='arm-tag'
[platforms.linux-aarch64.resources.extra]
GIT_REPOSITORY='arm-extra'
GIT_TAG='extra-tag'
[platforms.windows-x86_64]
config_dir='lib'
package_name='WinDemo'
sentinel='lib/DemoConfig.cmake'
`)
	linux, err := loadDefinition(path, "Linux", "AMD64")
	if err != nil {
		t.Fatal(err)
	}
	if linux.Source.GitRepository != "" || !reflect.DeepEqual(linux.Source.URL, []string{"https://one.invalid/linux.tar.xz", "https://two.invalid/linux.tar.xz"}) {
		t.Fatalf("source not replaced: %+v", linux.Source)
	}
	if slices.Contains(linux.PackagePlatforms, "any-any") {
		t.Fatal("platform_independent default must be false")
	}
	if linux.Resources["extra"].GitRepository != "" || linux.Resources["extra"].DownloadName != "extra.tar.xz" {
		t.Fatal(linux.Resources)
	}
	if !reflect.DeepEqual(linux.Cache, map[string]any{"A": "linux", "B": int64(42), "LIST": []string{"one", "two"}, "C": true}) {
		t.Fatal(linux.Cache)
	}
	if len(linux.Steps) != 2 || linux.Steps[0].Name != "common" || linux.Steps[1].Commands[1][3] != "two" {
		t.Fatal(linux.Steps)
	}
	if !reflect.DeepEqual(linux.Depends, []string{"left@1.0", "right@1.0"}) || linux.SourceSubdir != "build/cmake" || linux.PkgconfigDir != "lib/pkgconfig" {
		t.Fatal(linux)
	}
	for _, arch := range []string{"ARM64", "arm64", "aarch64"} {
		arm, err := loadDefinition(path, "Linux", arch)
		if err != nil {
			t.Fatal(err)
		}
		if arm.Source.GitRepository != "arm" || len(arm.Source.URL) != 0 || arm.Resources["extra"].GitRepository != "arm-extra" {
			t.Fatal(arm)
		}
	}
	win, err := loadDefinition(path, "Windows", "x64")
	if err != nil {
		t.Fatal(err)
	}
	if win.Source.GitRepository != "unused" || win.Cache["A"] != "common" || len(win.Steps) != 1 || win.ConfigDir != "lib" || win.PackageName != "WinDemo" || win.Sentinel != "lib/DemoConfig.cmake" {
		t.Fatal(win)
	}
	path = definitionFile(t, root, "demo", "cmake", "[platforms.windows-x86_64.source]\nGIT_REPOSITORY='win'\nGIT_TAG='fixed'\n")
	for _, arch := range []string{"x64", "AMD64", "x86_64"} {
		if _, err := loadDefinition(path, "Windows", arch); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range [][2]string{{"Linux", "amd64"}, {"Windows", "arm64"}, {"Windows", ""}} {
		if _, err := loadDefinition(path, target[0], target[1]); err == nil {
			t.Fatalf("missing source: %v", err)
		}
	}
	if normalizeArchitecture("mips") != "mips" {
		t.Fatal("unknown architecture changed")
	}
}

func TestDefinitionRejectsInvalidData(t *testing.T) {
	archive := "URL=['one','two']\nURL_HASH='SHA256=" + strings.Repeat("A", 64) + "'\n"
	cases := []struct{ kind, body, field string }{
		{"cmake", "unknown=true\n" + testSource, "unknown"},
		{"copy", "platform_independent='true'\n" + testSource + "[[copy]]\nresource='extra'\nfiles=['a']\ndestination='.'\n", "platform_independent"},
		{"cmake", "depends=[1]\n" + testSource, "depends"},
		{"cmake", "depends=['../escape']\n" + testSource, "depends"},
		{"cmake", "package_name='Demo'\n" + testSource, "package_name"},
		{"cmake", testSource + "[platforms.macos]\n", "macos"},
		{"cmake", testSource + "[platforms.windows-x86_64]\nfilter=true\n", "filter"},
		{"cmake", testSource + "[platforms.windows-x86_64.architectures.mips]\n", "mips"},
		{"cmake", testSource + "[platforms.windows-x86_64.architectures.aarch64]\ncache={}\n", "cache"},
		{"cmake", testSource + "[platforms.windows-x86_64.source]\nGIT_REPOSITORY='bad'\n", "GIT_TAG"},
		{"cmake", testSource + "[cmake]\ncach={}\n", "cach"},
		{"cmake", testSource + "[cmake.cache]\nA=1.5\n", "A"},
		{"cmake", testSource + "[cmake.cache]\nA=['a',1]\n", "A"},
		{"cmake", testSource + "[cmake.cache]\n'bad-key'=1\n", "bad-key"},
		{"cmake", testSource + "[[steps]]\nname='s'\nCOMMAND='echo no'\n", "COMMAND"},
		{"cmake", testSource + "[[steps]]\nname='s'\nCOMMAND=[['echo'],1]\n", "COMMAND"},
		{"cmake", testSource + "[[steps]]\nname='s'\nCOMMAND=[[]]\n", "COMMAND"},
		{"cmake", testSource + "[[steps]]\nname='s'\nCOMMAND=['']\n", "COMMAND"},
		{"cmake", testSource + "[[steps]]\nname='s'\nCOMMAND=['echo']\nBEFORE='build'\n", "BEFORE"},
		{"cmake", testSource + "[[steps]]\nname='s'\nCOMMAND=['echo']\n[[steps]]\nname='s'\nCOMMAND=['echo']\n", "steps.name"},
		{"cmake", testSource + "[[copy]]\nfiles=['a']\ndestination='.'\n", "copy"},
		{"copy", "[[copy]]\nfiles=['a']\ndestination='.'\n", "copy"},
		{"copy", testSource + "[[copy]]\nresource='missing'\nfiles=['a']\ndestination='.'\n", "copy.resource"},
		{"copy", testSource + "[cmake]\nSOURCE_SUBDIR='sub'\n[[copy]]\nfiles=['a']\ndestination='.'\n", "SOURCE_SUBDIR"},
	}
	for _, value := range []string{"../escape", "/absolute", "C:drive", `a\..\b`, `\root`, `\\host\share`} {
		for _, field := range []string{"sentinel", "config_dir", "pkgconfig_dir"} {
			cases = append(cases, struct{ kind, body, field string }{"cmake", field + "='" + value + "'\n" + testSource, field})
		}
		cases = append(cases, struct{ kind, body, field string }{"cmake", testSource + "[cmake]\nSOURCE_SUBDIR='" + value + "'\n", "SOURCE_SUBDIR"})
	}
	for _, mapping := range []string{"files=['a']\ndirectories=['.']", "directories=[]", "directories=['../escape']", "directories=['C:drive']"} {
		cases = append(cases, struct{ kind, body, field string }{"copy", testSource + "[[copy]]\n" + mapping + "\ndestination='.'\n", "copy"})
	}
	for _, table := range []string{"source", "resources.extra"} {
		for _, item := range []struct{ body, field string }{
			{strings.ReplaceAll(archive, "['one','two']", "'one'"), "URL"},
			{strings.ReplaceAll(archive, "['one','two']", "[]"), "URL"},
			{strings.ReplaceAll(archive, "['one','two']", "['']"), "URL"},
			{strings.ReplaceAll(archive, "['one','two']", "[1]"), "URL"},
			{"URL=['a']\n", "URL_HASH"}, {"URL_HASH='SHA256=" + strings.Repeat("a", 64) + "'\n", "URL"},
			{strings.ReplaceAll(archive, "SHA256=", "MD5="), "URL_HASH"},
			{strings.ReplaceAll(archive, strings.Repeat("A", 64), strings.Repeat("g", 64)), "URL_HASH"},
			{strings.ReplaceAll(archive, strings.Repeat("A", 64), strings.Repeat("a", 63)), "URL_HASH"},
			{archive + "GIT_REPOSITORY='repo'\n", "source"}, {archive + "GIT_TAG='tag'\n", "source"},
			{archive + "UNKNOWN=true\n", "UNKNOWN"}, {"GIT_REPOSITORY='repo'\n", "GIT_TAG"}, {"GIT_TAG='tag'\n", "GIT_REPOSITORY"},
		} {
			body := "[" + table + "]\n" + item.body + "[[copy]]\nresource='extra'\nfiles=['a']\ndestination='.'\n"
			cases = append(cases, struct{ kind, body, field string }{"copy", body, item.field})
		}
		for _, name := range []string{"''", "1", "'dir/a.xz'", `'dir\a.xz'`, "'.'", "'..'", "'C:file.xz'"} {
			cases = append(cases, struct{ kind, body, field string }{"copy", "[" + table + "]\n" + archive + "DOWNLOAD_NAME=" + name + "\n", "DOWNLOAD_NAME"})
		}
	}
	for _, tc := range cases {
		t.Run(tc.field+"/"+tc.body, func(t *testing.T) {
			path := definitionFile(t, t.TempDir(), "demo", tc.kind, tc.body)
			_, err := loadDefinition(path, "Linux", "amd64")
			if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("want path and %s; got %v", tc.field, err)
			}
		})
	}
}

func TestDefinitionLocalInputs(t *testing.T) {
	root := t.TempDir()
	path := definitionFile(t, root, "demo", "cmake", testSource+`
[resources.extra]
GIT_REPOSITORY='not-downloaded'
GIT_TAG='fixed'
[cmake.cache]
EXTRA=['<RESOURCE_SOURCE_DIR:extra>/modules','suffix']
[[patches]]
file='change.patch'
resource='extra'
[[steps]]
name='change'
COMMAND=['cmake','-P','change.cmake']
DEPENDS=['change.cmake','<SOURCE_DIR>/input']
WORKING_DIRECTORY='<SOURCE_DIR>'
`)
	patch := putFile(t, filepath.Join(filepath.Dir(path), "change.patch"), "patch input")
	m, err := loadDefinition(path, "Linux", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if m.Patches[0].File != patch || m.Patches[0].Resource != "extra" || m.Steps[0].Depends[0] != filepath.ToSlash(filepath.Join(filepath.Dir(path), "change.cmake")) || m.Steps[0].Depends[1] != "<SOURCE_DIR>/input" || m.Steps[0].WorkingDirectory != "<SOURCE_DIR>" {
		t.Fatal(m)
	}
	body, _ := os.ReadFile(path)
	for _, change := range []struct{ from, to, field string }{{"resource='extra'", "resource='missing'", "patches.resource"}, {"SOURCE_DIR:extra", "SOURCE_DIR:missing", "EXTRA"}} {
		putFile(t, path, strings.ReplaceAll(string(body), change.from, change.to))
		if _, err := loadDefinition(path, "Linux", "x86_64"); err == nil || !strings.Contains(err.Error(), change.field) {
			t.Fatal(err)
		}
	}
	putFile(t, path, string(body))
	if err := os.Remove(patch); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDefinition(path, "Linux", "x86_64"); err == nil || !strings.Contains(err.Error(), "change.patch") {
		t.Fatal(err)
	}
	path = definitionFile(t, root, "demo", "copy", `[resources.extra]
GIT_REPOSITORY='unused'
GIT_TAG='fixed'
[[copy]]
resource='extra'
directories=['.']
destination='.'
`)
	m, err = loadDefinition(path, "Linux", "x86_64")
	if err != nil || m.Source != nil || len(m.Copies) != 1 || m.Copies[0].Directories[0] != "." {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestDefinitionPreservesInput(t *testing.T) {
	path := definitionFile(t, t.TempDir(), "demo", "cmake", "# 中文说明\nversion='1.0'\n"+testSource)
	before, _ := os.ReadFile(path)
	stat, _ := os.Stat(path)
	if _, err := loadDefinition(path, "Linux", "x86_64"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	now, _ := os.Stat(path)
	if string(before) != string(after) || !stat.ModTime().Equal(now.ModTime()) {
		t.Fatal("declaration modified")
	}
}

func TestSystemDependencies(t *testing.T) {
	path := definitionFile(t, t.TempDir(), "demo", "cmake", testSource+"[system_dependencies]\napt=['pkg-config','libgomp1','pkg-config']\nchoco=['pkgconfiglite']\n")
	definition, err := loadDefinition(path, "Linux", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(definition.SystemDependencies["apt"], ","), "pkg-config,libgomp1"; got != want {
		t.Fatalf("APT list was not deduplicated: got %q, want %q", got, want)
	}
	for _, invalid := range []string{
		"apt=['pkg-config']\nyum=['yum-only']\n",
		"apt='pkg-config'\n",
		"apt=['pkg-config; arbitrary-command']\n",
		"apt=['']\n",
		"apt=['-unsafe-option']\n",
		"apt=['not a package']\n",
	} {
		path := definitionFile(t, t.TempDir(), "demo", "cmake", testSource+"[system_dependencies]\n"+invalid)
		if _, err := loadDefinition(path, "Linux", "x86_64"); err == nil {
			t.Fatalf("unsafe or unsupported system dependency accepted: %s", invalid)
		}
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	flann, err := loadDefinition(filepath.Join(root, "src", "dependencies", "flann", "f9caaf609d8b", "module.toml"), "Linux", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(flann.SystemDependencies["apt"], ","), "pkg-config,libgomp1"; got != want {
		t.Fatalf("flann APT dependencies: got %q, want %q", got, want)
	}
	if got, want := strings.Join(flann.SystemDependencies["choco"], ","), "pkgconfiglite"; got != want {
		t.Fatalf("flann Chocolatey dependencies: got %q, want %q", got, want)
	}
}

func contractRecipe(t *testing.T, body string) (string, string) {
	t.Helper()
	root := t.TempDir()
	putFile(t, filepath.Join(root, "src/dependencies/demo/package.toml"), "latest='1.0'\n")
	path := putFile(t, filepath.Join(root, "src/dependencies/demo/1.0/module.toml"), body)
	return root, path
}

func TestPackagePlatformsContract(t *testing.T) {
	for _, tc := range []struct {
		declaration string
		valid       bool
	}{
		{"", false}, {"package_platforms=[]", false},
		{"package_platforms=['linux-x86_64','linux-x86_64']", false},
		{"package_platforms=['macos-arm64']", false}, {"package_platforms=[42]", false},
		{"package_platforms=['any-any','darwin-aarch64']", false},
		{"package_platforms=['any-any']", true}, {"package_platforms=['darwin-aarch64']", true},
		{"package_platforms=['any-any']\nname='demo'", false},
		{"package_platforms=['any-any']\nversion='1.0'", false},
		{"package_platforms=['any-any']\nsupported_platforms=['darwin-aarch64']", false},
		{"package_platforms=['any-any']\nplatform_independent=true", false},
	} {
		t.Run(tc.declaration, func(t *testing.T) {
			_, path := contractRecipe(t, "type='cmake'\n"+tc.declaration+"\n"+testSource)
			got, err := loadDefinition(path, "Darwin", "arm64")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
			if err == nil && (got.Name != "demo" || got.Version != "1.0") {
				t.Fatal(got)
			}
			if err == nil && tc.declaration == "package_platforms=['any-any']" {
				osName, arch := packagePlatform(got, "darwin", "aarch64")
				if osName != "any" || arch != "any" {
					t.Fatal(osName, arch)
				}
			}
		})
	}
}

func TestDirectoryIdentityAndPinnedDepends(t *testing.T) {
	base := "type='cmake'\npackage_platforms=['any-any']\n"
	for _, depends := range []string{"['dep']", "['dep@1','dep@1']", "['dep@1','dep@2']"} {
		t.Run(depends, func(t *testing.T) {
			_, path := contractRecipe(t, base+"depends="+depends+"\n"+testSource)
			if _, err := loadDefinition(path, "Linux", "x86_64"); err == nil {
				t.Fatal("accepted invalid direct dependencies")
			}
		})
	}
	for _, latest := range []string{"", "latest='missing'", "latest='1.0'\nextra=true"} {
		t.Run("latest/"+latest, func(t *testing.T) {
			root, path := contractRecipe(t, base+testSource)
			putFile(t, filepath.Join(root, "src/dependencies/demo/package.toml"), latest)
			if _, err := loadDefinition(path, "Linux", "x86_64"); err == nil {
				t.Fatal("accepted invalid package latest")
			}
		})
	}
	for _, component := range []string{"CON", "trailing."} {
		t.Run(component, func(t *testing.T) {
			root := t.TempDir()
			putFile(t, filepath.Join(root, "src/dependencies", component, "package.toml"), "latest='1.0'")
			path := putFile(t, filepath.Join(root, "src/dependencies", component, "1.0/module.toml"), base+testSource)
			if _, err := loadDefinition(path, "Linux", "x86_64"); err == nil {
				t.Fatal("accepted unsafe directory identity")
			}
		})
	}
}

func TestFlatPlatformOverrides(t *testing.T) {
	root, path := contractRecipe(t, `type='cmake'
package_platforms=['linux-x86_64','darwin-aarch64']
config_dir='common/config'
[source]
GIT_REPOSITORY='common'
GIT_TAG='0123456789012345678901234567890123456789'
[resources.extra]
GIT_REPOSITORY='common-extra'
GIT_TAG='0123456789012345678901234567890123456789'
[cmake]
SOURCE_SUBDIR='common'
[cmake.cache]
A='common'
B=42
[[steps]]
name='common-step'
COMMAND=[['cmake','-E','echo','common']]
DEPENDEES=['download']
[[patches]]
file='common.patch'
[platforms.darwin-aarch64]
config_dir='target/config'
[platforms.darwin-aarch64.source]
URL=['archive.zip']
URL_HASH='SHA256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
[platforms.darwin-aarch64.resources.extra]
GIT_REPOSITORY='target-extra'
GIT_TAG='0123456789012345678901234567890123456789'
[platforms.darwin-aarch64.cmake]
SOURCE_SUBDIR='target'
[platforms.darwin-aarch64.cmake.cache]
A='target'
[[platforms.darwin-aarch64.steps]]
name='target-step'
COMMAND=[['cmake','-E','echo','target']]
DEPENDEES=['common-step']
[[platforms.darwin-aarch64.patches]]
file='target.patch'
`)
	putFile(t, filepath.Join(root, "src/dependencies/demo/1.0/common.patch"), "patch")
	putFile(t, filepath.Join(root, "src/dependencies/demo/1.0/target.patch"), "patch")
	got, err := loadDefinition(path, "Darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source.GitRepository != "" || !reflect.DeepEqual(got.Source.URL, []string{"archive.zip"}) {
		t.Fatal(got.Source)
	}
	if got.Resources["extra"].GitRepository != "target-extra" || got.Cache["A"] != "target" || got.Cache["B"] != int64(42) {
		t.Fatal(got)
	}
	if got.SourceSubdir != "target" || got.ConfigDir != "target/config" || len(got.Steps) != 2 || len(got.Patches) != 2 {
		t.Fatal(got)
	}
	for _, override := range []string{"[platforms.linux]\n", "[platforms.windows-x86_64]\n", "[platforms.darwin-aarch64]\ndepends=['dep@1']\n"} {
		_, invalid := contractRecipe(t, "type='cmake'\npackage_platforms=['darwin-aarch64']\n"+testSource+override)
		if _, err := loadDefinition(invalid, "Darwin", "arm64"); err == nil {
			t.Fatal("accepted invalid override", override)
		}
	}
	_, copyPath := contractRecipe(t, "type='copy'\npackage_platforms=['any-any']\n"+testSource+`
[[copy]]
files=['common.h']
destination='include'
[[platforms.darwin-aarch64.copy]]
files=['target.h']
destination='include'
`)
	copied, err := loadDefinition(copyPath, "Darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if len(copied.Copies) != 1 || !reflect.DeepEqual(copied.Copies[0].Files, []string{"target.h"}) {
		t.Fatal(copied.Copies)
	}
}

func TestDescribePackagePlatform(t *testing.T) {
	root, _ := contractRecipe(t, "type='cmake'\npackage_platforms=['any-any']\n"+testSource)
	for _, file := range []string{"go.mod", "go.sum", "src/autolib-builder/index.cmake"} {
		putFile(t, filepath.Join(root, file), "")
	}
	got, err := describeModules(root, "Darwin", "arm64", []string{"demo@1.0"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"package_platform":"any-any"`) || strings.Contains(string(encoded), "platform_independent") {
		t.Fatal(string(encoded))
	}
}
