package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testSource = "\n[source]\nGIT_REPOSITORY='unused'\nGIT_TAG='fixed'\n"

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
	return putFile(t, filepath.Join(root, "src", "dependencies", name, "module.toml"), "name='"+name+"'\ntype='"+kind+"'\n"+body)
}

func TestDefinitionPlatformSelection(t *testing.T) {
	root := t.TempDir()
	path := definitionFile(t, root, "demo", "cmake", `depends=['left','left','right']
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
[platforms.linux.source]
URL=['https://one.invalid/linux.tar.xz','https://two.invalid/linux.tar.xz']
URL_HASH='SHA256=`+strings.Repeat("a", 64)+`'
[platforms.linux.resources.extra]
URL=['extra.tar.xz']
URL_HASH='SHA256=`+strings.Repeat("b", 64)+`'
DOWNLOAD_NAME='extra.tar.xz'
[platforms.linux.cmake.cache]
A='linux'
C=true
[[platforms.linux.steps]]
name='linux'
COMMAND=[['cmake','-E','echo','one'],['cmake','-E','echo','two']]
[platforms.linux.architectures.aarch64.source]
GIT_REPOSITORY='arm'
GIT_TAG='arm-tag'
[platforms.linux.architectures.aarch64.resources.extra]
GIT_REPOSITORY='arm-extra'
GIT_TAG='extra-tag'
[platforms.windows]
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
	if linux.PlatformIndependent {
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
	if !reflect.DeepEqual(linux.Depends, []string{"left", "right"}) || linux.SourceSubdir != "build/cmake" || linux.PkgconfigDir != "lib/pkgconfig" {
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
	path = definitionFile(t, root, "demo", "cmake", "[platforms.windows.architectures.x86_64.source]\nGIT_REPOSITORY='win'\nGIT_TAG='fixed'\n")
	for _, arch := range []string{"x64", "AMD64", "x86_64"} {
		if _, err := loadDefinition(path, "Windows", arch); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range [][2]string{{"Linux", "amd64"}, {"Windows", "arm64"}, {"Windows", ""}} {
		if _, err := loadDefinition(path, target[0], target[1]); err == nil || !strings.Contains(err.Error(), "source") {
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
		{"cmake", testSource + "[platforms.windows]\nfilter=true\n", "filter"},
		{"cmake", testSource + "[platforms.windows.architectures.mips]\n", "mips"},
		{"cmake", testSource + "[platforms.windows.architectures.aarch64]\ncache={}\n", "cache"},
		{"cmake", testSource + "[platforms.windows.architectures.x86_64.source]\nGIT_REPOSITORY='bad'\n", "GIT_TAG"},
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
	m, err := loadDefinition(path, "Linux", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Patches[0].File != patch || m.Patches[0].Resource != "extra" || m.Steps[0].Depends[0] != filepath.ToSlash(filepath.Join(filepath.Dir(path), "change.cmake")) || m.Steps[0].Depends[1] != "<SOURCE_DIR>/input" || m.Steps[0].WorkingDirectory != "<SOURCE_DIR>" {
		t.Fatal(m)
	}
	body, _ := os.ReadFile(path)
	for _, change := range []struct{ from, to, field string }{{"resource='extra'", "resource='missing'", "patches.resource"}, {"SOURCE_DIR:extra", "SOURCE_DIR:missing", "EXTRA"}} {
		putFile(t, path, strings.ReplaceAll(string(body), change.from, change.to))
		if _, err := loadDefinition(path, "Linux", ""); err == nil || !strings.Contains(err.Error(), change.field) {
			t.Fatal(err)
		}
	}
	putFile(t, path, string(body))
	if err := os.Remove(patch); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDefinition(path, "Linux", ""); err == nil || !strings.Contains(err.Error(), "change.patch") {
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
	m, err = loadDefinition(path, "Linux", "")
	if err != nil || m.Source != nil || len(m.Copies) != 1 || m.Copies[0].Directories[0] != "." {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestDefinitionPreservesInput(t *testing.T) {
	path := definitionFile(t, t.TempDir(), "demo", "cmake", "# 中文说明\nversion='1.0'\n"+testSource)
	before, _ := os.ReadFile(path)
	stat, _ := os.Stat(path)
	if _, err := loadDefinition(path, "Linux", ""); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	now, _ := os.Stat(path)
	if string(before) != string(after) || !stat.ModTime().Equal(now.ModTime()) {
		t.Fatal("declaration modified")
	}
}
