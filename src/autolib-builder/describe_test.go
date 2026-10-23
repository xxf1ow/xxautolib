package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestDescribeParity(t *testing.T) {
	f := newFixture(t)
	describeRootInputs(t, f.root)
	definitionFile(t, f.root, "lz4", "cmake", testSource)
	definitionFile(t, f.root, "flann", "cmake", "depends=['lz4']\n[system_dependencies]\napt=['pkg-config']\n"+testSource)
	got, err := describeModules(f.root, "Linux", "x86_64", []string{"flann"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Modules) != 2 || got.Modules[0].Name != "lz4" || got.Modules[1].Name != "flann" {
		t.Fatalf("closure: %+v", got.Modules)
	}
	if !reflect.DeepEqual(got.Modules[1].SystemDependencies["apt"], []string{"pkg-config"}) || got.Modules[1].SystemDependencies["choco"] == nil {
		t.Fatalf("system dependencies were not normalized: %+v", got.Modules[1].SystemDependencies)
	}
	other := f.root + "-other"
	describeRootInputs(t, other)
	definitionFile(t, other, "lz4", "cmake", testSource)
	definitionFile(t, other, "flann", "cmake", "depends=['lz4']\n[system_dependencies]\napt=['pkg-config']\n"+testSource)
	first, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	second, err := describeModules(other, "Linux", "x86_64", []string{"flann"})
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(secondJSON) {
		t.Fatalf("checkout prefix affected description:\n%s\n%s", first, secondJSON)
	}
}

func TestDescribeInputs(t *testing.T) {
	f := newFixture(t)
	describeRootInputs(t, f.root)
	moduleDir := filepath.Join(f.root, "src", "dependencies", "plain")
	putFile(t, filepath.Join(moduleDir, "fix.patch"), "patch\n")
	putFile(t, filepath.Join(moduleDir, "run.cmake"), "step input\n")
	body := "version='1.0'\nconfig_dir='config/linux'\n" + testSource + "[resources.assets]\nGIT_REPOSITORY='assets'\nGIT_TAG='fixed'\n[cmake.cache]\nVALUE='selected'\n" +
		"[platforms.linux.cmake.cache]\nPLATFORM='linux'\n[[steps]]\nname='prepare'\nCOMMAND=['cmake','-P','run.cmake']\nDEPENDS=['run.cmake','<RESOURCE_SOURCE_DIR:assets>/run.cmake']\nWORKING_DIRECTORY='<RESOURCE_SOURCE_DIR:assets>'\n[[patches]]\nfile='fix.patch'\n[[copy]]\nfiles=['plain.h']\ndestination='include'\n"
	definitionFile(t, f.root, "plain", "copy", body)
	putFile(t, filepath.Join(moduleDir, "autolib-import.cmake"), "# adapter\n")
	description, err := describeModules(f.root, "Linux", "x86_64", []string{"plain"})
	if err != nil {
		t.Fatal(err)
	}
	module := description.Modules[0]
	if !reflect.DeepEqual(module.InputFiles, []string{"src/dependencies/plain/fix.patch", "src/dependencies/plain/run.cmake"}) || module.AdapterFile != "src/dependencies/plain/autolib-import.cmake" {
		t.Fatalf("module inputs/adapter: %+v", module)
	}
	var configuration map[string]any
	if err := json.Unmarshal(module.Configuration, &configuration); err != nil {
		t.Fatal(err)
	}
	patches := configuration["Patches"].([]any)
	steps := configuration["Steps"].([]any)
	resources := configuration["Resources"].(map[string]any)
	copies := configuration["Copies"].([]any)
	if configuration["Cache"].(map[string]any)["PLATFORM"] != "linux" ||
		patches[0].(map[string]any)["File"] != "src/dependencies/plain/fix.patch" ||
		steps[0].(map[string]any)["WorkingDirectory"] != "<RESOURCE_SOURCE_DIR:assets>" ||
		steps[0].(map[string]any)["Depends"].([]any)[0] != "src/dependencies/plain/run.cmake" ||
		steps[0].(map[string]any)["Depends"].([]any)[1] != "<RESOURCE_SOURCE_DIR:assets>/run.cmake" ||
		resources["assets"] == nil || len(copies) != 1 ||
		len(description.BuilderFiles) == 0 || !slices.Contains(description.BuilderFiles, "CMakeLists.txt") ||
		slices.ContainsFunc(description.BuilderFiles, func(name string) bool { return strings.Contains(name, "ci_") }) {
		t.Fatalf("effective configuration or production files missing: %+v / %+v", configuration, description)
	}
	definitionFile(t, f.root, "unadapted", "cmake", testSource)
	plainNoAdapter, err := describeModules(f.root, "Linux", "x86_64", []string{"unadapted"})
	if err != nil || plainNoAdapter.Modules[0].AdapterFile != "" {
		t.Fatalf("legal module without adapter was rejected: %+v, %v", plainNoAdapter, err)
	}
	for _, tc := range []struct{ system, architecture string }{{"Other", "x86_64"}, {"Linux", "invalid"}} {
		if _, err := describeModules(f.root, tc.system, tc.architecture, []string{"plain"}); err == nil {
			t.Fatalf("accepted target %s/%s", tc.system, tc.architecture)
		}
	}
	if _, err := describeModules(f.root, "Linux", "x86_64", nil); err == nil {
		t.Fatal("accepted empty module list")
	}
	outside := putFile(t, f.root+"-outside.cmake", "outside\n")
	definitionFile(t, f.root, "unsafe", "cmake", testSource+"[[steps]]\nname='unsafe'\nCOMMAND=['cmake','-P','x']\nDEPENDS=['"+outside+"']\n")
	if _, err := describeModules(f.root, "Linux", "x86_64", []string{"unsafe"}); err == nil || !strings.Contains(err.Error(), "escapes source root") {
		t.Fatalf("accepted a local input outside the source root: %v", err)
	}
}

func TestDescribeRejectsAbsoluteResourcePrefixes(t *testing.T) {
	f := newFixture(t)
	describeRootInputs(t, f.root)
	outside := filepath.ToSlash(filepath.Join(f.root, "outside")) + "/<RESOURCE_SOURCE_DIR:assets>/run.cmake"
	for _, field := range []string{"DEPENDS", "WORKING_DIRECTORY"} {
		t.Run(field, func(t *testing.T) {
			module := "unsafe-" + strings.ToLower(field)
			fieldValue := "['" + outside + "']"
			if field == "WORKING_DIRECTORY" {
				fieldValue = "'" + outside + "'"
			}
			body := "version='1.0'\n" + testSource + "[resources.assets]\nGIT_REPOSITORY='assets'\nGIT_TAG='fixed'\n" +
				"[[steps]]\nname='prepare'\nCOMMAND=['cmake','-P','x']\n" + field + "=" + fieldValue + "\n"
			definitionFile(t, f.root, module, "cmake", body)
			var stdout, stderr bytes.Buffer
			args := []string{"describe", "--source-root", f.root, "--system-name", "Linux", "--architecture", "x86_64", "--module", module}
			if code := run(args, &stdout, &stderr); code == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "escapes source root") {
				t.Fatalf("accepted external absolute %s path: code=%d stdout=%q stderr=%q", field, code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestDescribeCLI(t *testing.T) {
	f := newFixture(t)
	describeRootInputs(t, f.root)
	definitionFile(t, f.root, "plain", "cmake", testSource)
	definitionFile(t, f.root, "cycle-a", "cmake", "depends=['cycle-b']\n"+testSource)
	definitionFile(t, f.root, "cycle-b", "cmake", "depends=['cycle-a']\n"+testSource)
	putFile(t, filepath.Join(f.root, "src", "dependencies", "invalid", "module.toml"), "name='invalid'\ntype='cmake'\nunknown=true\n"+testSource)
	var stdout, stderr bytes.Buffer
	args := []string{"describe", "--source-root", f.root, "--system-name", "Linux", "--architecture", "x86_64", "--module", "plain"}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("describe failed: %d %s", code, stderr.String())
	}
	var result moduleDescription
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SystemName != "Linux" || result.Architecture != "x86_64" || len(result.Modules) != 1 {
		t.Fatalf("unexpected describe result: %+v", result)
	}
	good := stdout.String()
	for _, bad := range [][]string{
		{"describe"},
		{"describe", "--source-root", f.root, "--system-name", "Linux", "--architecture", "x86_64"},
		{"describe", "--source-root", f.root, "--system-name", "Linux", "--architecture", "x86_64", "--module", "plain", "--module", "missing"},
		{"describe", "--source-root", f.root, "--system-name", "Linux", "--architecture", "x86_64", "--module", "cycle-a"},
		{"describe", "--source-root", f.root, "--system-name", "Linux", "--architecture", "x86_64", "--module", "invalid"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(bad, &stdout, &stderr); code == 0 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("accepted invalid arguments %q: code=%d stdout=%q stderr=%q", bad, code, stdout.String(), stderr.String())
		}
	}
	if strings.TrimSpace(good) == "" || filepath.IsAbs(result.Modules[0].AdapterFile) {
		t.Fatalf("unexpected description: %s", good)
	}
}

func describeRootInputs(t *testing.T, root string) {
	t.Helper()
	for name, content := range map[string]string{
		"go.mod":                                           "module fixture\n",
		"go.sum":                                           "",
		"CMakeLists.txt":                                   "cmake_minimum_required(VERSION 3.20)\n",
		"src/autolib-builder/main.go":                      "package main\n",
		"src/autolib-builder/model.go":                     "package main\n",
		"src/autolib-builder/index.cmake":                  "# fixture\n",
		"src/autolib-builder/apply_patch.cmake":            "# fixture\n",
		"src/autolib-builder/configure_dependencies.cmake": "# fixture\n",
	} {
		putFile(t, filepath.Join(root, filepath.FromSlash(name)), content)
	}
}
