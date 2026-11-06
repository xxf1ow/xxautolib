package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPlatformSelection(t *testing.T) {
	root := t.TempDir()
	describeRootInputs(t, root)
	for _, item := range []struct{ name, extra, platforms string }{
		{"shared", "", "'linux-x86_64','darwin-aarch64'"},
		{"good", "depends=['shared@1.0']\n", "'linux-x86_64','darwin-aarch64'"},
		{"bad", "depends=['shared@1.0','blocked@1.0']\n", "'darwin-aarch64'"},
		{"blocked", "", "'linux-x86_64'"},
	} {
		definitionFile(t, root, item.name, "cmake", "package_platforms=["+item.platforms+"]\n"+item.extra+testSource)
	}
	got, err := describeSelection(root, "Darwin", "arm64", []string{"bad", "good", "blocked"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Roots, []string{"good@1.0"}) || len(got.Modules) != 2 || got.Modules[0].Name != "shared" {
		t.Fatalf("effective closure: %+v", got)
	}
	data, _ := json.Marshal(got)
	var wire struct {
		Requested []string `json:"requested_roots"`
		Filtered  []struct {
			Root, Code string
			Chain      []string
		} `json:"filtered_roots"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(wire.Requested, []string{"bad@1.0", "good@1.0", "blocked@1.0"}) || len(wire.Filtered) != 2 || wire.Filtered[0].Code != "unsupported_dependency_platform" || !slices.Equal(wire.Filtered[0].Chain, []string{"bad@1.0", "blocked@1.0"}) || wire.Filtered[1].Code != "unsupported_platform" {
		t.Fatalf("filter reasons: %s", data)
	}
	if _, err := loadModuleClosure(root, []string{"bad"}, "Darwin", "arm64"); err == nil || !strings.Contains(err.Error(), "bad@1.0 -> blocked@1.0") {
		t.Fatalf("explicit unsupported request: %v", err)
	}
	build := filepath.Join(root, "build-explicit")
	if err := generate(buildContext{SourceRoot: root, BuildRoot: build, SystemName: "Darwin", Architecture: "arm64"}, []string{"bad"}); err == nil {
		t.Fatal("explicit generation accepted unsupported dependency")
	}
	if _, err := os.Stat(filepath.Join(build, "generated")); !os.IsNotExist(err) {
		t.Fatalf("unsupported request generated build targets: %v", err)
	}
	all, err := loadModuleClosure(root, nil, "Darwin", "arm64")
	if err != nil || len(all) != 2 {
		t.Fatalf("discovery must filter: %v, %v", all, err)
	}
	for _, kind := range []string{"missing dependency", "cycle", "bad TOML", "version conflict", "unknown root"} {
		t.Run(kind, func(t *testing.T) {
			r := t.TempDir()
			describeRootInputs(t, r)
			body := "package_platforms=['linux-x86_64']\n"
			request := "broken"
			switch kind {
			case "missing dependency":
				body += "depends=['absent@1.0']\n"
			case "cycle":
				body += "depends=['broken@1.0']\n"
			case "bad TOML":
				body += "unknown=true\n"
			case "version conflict":
				body += "depends=['dep@1','dep@2']\n"
				for _, v := range []string{"1", "2"} {
					definitionVersionFile(t, r, "dep", v, "cmake", "package_platforms=['linux-x86_64']\n"+testSource)
				}
			case "unknown root":
				request = "absent"
			}
			definitionFile(t, r, "broken", "cmake", body+testSource)
			if _, err := describeSelection(r, "Darwin", "arm64", []string{request}, false); err == nil {
				t.Fatal("invalid graph was swallowed as unsupported")
			}
		})
	}
	t.Run("real ONNX missing Darwin source", func(t *testing.T) {
		repository, _ := filepath.Abs(filepath.Join("..", ".."))
		got, err := describeSelection(repository, "Darwin", "arm64", []string{"onnxruntime", "queue", "lz4"}, false)
		if err != nil || !slices.Equal(got.Roots, []string{"queue@45db9a8b4431", "lz4@1.10.0"}) {
			t.Fatalf("platform must precede source requirements: %+v %v", got, err)
		}
	})
}

func TestDescribeParity(t *testing.T) {
	f := newFixture(t)
	describeRootInputs(t, f.root)
	definitionFile(t, f.root, "lz4", "cmake", testSource)
	definitionFile(t, f.root, "flann", "cmake", "depends=['lz4@1.0']\n[system_dependencies]\napt=['pkg-config']\n"+testSource)
	got, err := describeModules(f.root, "Linux", "x86_64", []string{"flann"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Modules) != 2 || got.Modules[0].Name != "lz4" || got.Modules[1].Name != "flann" {
		t.Fatalf("closure: %+v", got.Modules)
	}
	if got.Modules[1].Version != "1.0" || !reflect.DeepEqual(got.Modules[1].Depends, []string{"lz4@1.0"}) {
		t.Fatalf("imprecise dependency description: %+v", got.Modules[1])
	}
	if !reflect.DeepEqual(got.Modules[1].SystemDependencies["apt"], []string{"pkg-config"}) || got.Modules[1].SystemDependencies["choco"] == nil {
		t.Fatalf("system dependencies were not normalized: %+v", got.Modules[1].SystemDependencies)
	}
	other := f.root + "-other"
	describeRootInputs(t, other)
	definitionFile(t, other, "lz4", "cmake", testSource)
	definitionFile(t, other, "flann", "cmake", "depends=['lz4@1.0']\n[system_dependencies]\napt=['pkg-config']\n"+testSource)
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
	moduleDir := filepath.Join(f.root, "src", "dependencies", "plain", "1.0")
	putFile(t, filepath.Join(moduleDir, "fix.patch"), "patch\n")
	putFile(t, filepath.Join(moduleDir, "run.cmake"), "step input\n")
	body := "version='1.0'\nconfig_dir='config/linux'\n" + testSource + "[resources.assets]\nGIT_REPOSITORY='assets'\nGIT_TAG='0123456789012345678901234567890123456789'\n[cmake.cache]\nVALUE='selected'\n" +
		"[platforms.linux-x86_64.cmake.cache]\nPLATFORM='linux'\n[[steps]]\nname='prepare'\nCOMMAND=['cmake','-P','run.cmake']\nDEPENDS=['run.cmake','<RESOURCE_SOURCE_DIR:assets>/run.cmake']\nWORKING_DIRECTORY='<RESOURCE_SOURCE_DIR:assets>'\n[[patches]]\nfile='fix.patch'\n"
	definitionFile(t, f.root, "plain", "cmake", body)
	putFile(t, filepath.Join(moduleDir, "autolib-import.cmake"), "# adapter\n")
	description, err := describeModules(f.root, "Linux", "x86_64", []string{"plain"})
	if err != nil {
		t.Fatal(err)
	}
	module := description.Modules[0]
	if !reflect.DeepEqual(module.InputFiles, []string{"src/dependencies/plain/1.0/autolib-import.cmake", "src/dependencies/plain/1.0/fix.patch", "src/dependencies/plain/1.0/module.toml", "src/dependencies/plain/1.0/run.cmake", "src/dependencies/plain/package.toml"}) || module.AdapterFile != "src/dependencies/plain/1.0/autolib-import.cmake" {
		t.Fatalf("module inputs/adapter: %+v", module)
	}
	var configuration map[string]any
	if err := json.Unmarshal(module.Configuration, &configuration); err != nil {
		t.Fatal(err)
	}
	patches := configuration["Patches"].([]any)
	steps := configuration["Steps"].([]any)
	resources := configuration["Resources"].(map[string]any)
	if configuration["Cache"].(map[string]any)["PLATFORM"] != "linux" ||
		patches[0].(map[string]any)["File"] != "src/dependencies/plain/1.0/fix.patch" ||
		steps[0].(map[string]any)["WorkingDirectory"] != "<RESOURCE_SOURCE_DIR:assets>" ||
		steps[0].(map[string]any)["Depends"].([]any)[0] != "src/dependencies/plain/1.0/run.cmake" ||
		steps[0].(map[string]any)["Depends"].([]any)[1] != "<RESOURCE_SOURCE_DIR:assets>/run.cmake" ||
		resources["assets"] == nil ||
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
			body := "version='1.0'\n" + testSource + "[resources.assets]\nGIT_REPOSITORY='assets'\nGIT_TAG='0123456789012345678901234567890123456789'\n" +
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
	definitionFile(t, f.root, "cycle-a", "cmake", "depends=['cycle-b@1.0']\n"+testSource)
	definitionFile(t, f.root, "cycle-b", "cmake", "depends=['cycle-a@1.0']\n"+testSource)
	definitionFile(t, f.root, "invalid", "cmake", "unknown=true\n"+testSource)
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

func TestDescribeCISelection(t *testing.T) {
	root := t.TempDir()
	describeRootInputs(t, root)
	definitionVersionFile(t, root, "eigen", "5.0.1", "copy", testSource+"[[copy]]\nfiles=['Eigen']\ndestination='include'\n")
	definitionVersionFile(t, root, "eigen", "3.4.0", "copy", testSource+"[[copy]]\nfiles=['Eigen']\ndestination='include'\n")
	definitionFile(t, root, "old", "cmake", "depends=['eigen@3.4.0']\n"+testSource)
	definitionFile(t, root, "new", "cmake", "depends=['eigen@5.0.1']\n"+testSource)
	definitionFile(t, root, "conflict", "cmake", "depends=['old@1.0','new@1.0']\n"+testSource)
	// Discovery includes every recipe, so keep the intentionally invalid root explicit.
	for _, tc := range []struct {
		name      string
		modules   []string
		discover  bool
		want      []string
		wantError bool
	}{
		{"latest", []string{"eigen"}, false, []string{"eigen@3.4.0"}, false},
		{"unknown exact", []string{"eigen@9.9.9"}, false, nil, true},
		{"independent closures", []string{"old", "new", "eigen@3.4.0"}, false, []string{"old@1.0", "new@1.0", "eigen@3.4.0"}, false},
		{"same root conflict", []string{"conflict"}, false, nil, true},
		{"mutually exclusive", []string{"eigen"}, true, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"describe", "--source-root", root, "--system-name", "Linux", "--architecture", "x86_64"}
			if tc.discover {
				args = append(args, "--discover")
			}
			for _, module := range tc.modules {
				args = append(args, "--module", module)
			}
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr)
			if tc.wantError {
				if code == 0 || stdout.Len() != 0 {
					t.Fatalf("accepted selection: %s", stdout.String())
				}
				if tc.name == "same root conflict" && !strings.Contains(stderr.String(), "conflicting dependency versions") {
					t.Fatalf("wrong conflict failure: %s", stderr.String())
				}
				if tc.name == "mutually exclusive" && !strings.Contains(stderr.String(), "mutually exclusive") {
					t.Fatalf("wrong selection failure: %s", stderr.String())
				}
				return
			}
			if code != 0 {
				t.Fatalf("describe: %s", stderr.String())
			}
			var got struct {
				Roots   []string `json:"roots"`
				Modules []struct {
					Name, Version, Target string
					Depends               []string
				} `json:"modules"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Roots, tc.want) {
				t.Fatalf("roots: got %v want %v", got.Roots, tc.want)
			}
			seen := map[string]bool{}
			for _, module := range got.Modules {
				ref := module.Name + "@" + module.Version
				if seen[ref] || module.Target == "" {
					t.Fatalf("duplicate or missing target: %+v", module)
				}
				seen[ref] = true
				if ref == "eigen@3.4.0" && module.Target != "_al_pkg_656967656e_332e342e30" {
					t.Fatalf("build target: %s", module.Target)
				}
				if module.Name == "old" && !reflect.DeepEqual(module.Depends, []string{"eigen@3.4.0"}) {
					t.Fatalf("old dependencies: %v", module.Depends)
				}
				if module.Name == "new" && !reflect.DeepEqual(module.Depends, []string{"eigen@5.0.1"}) {
					t.Fatalf("new dependencies: %v", module.Depends)
				}
			}
			if tc.name == "independent closures" && (!seen["eigen@3.4.0"] || !seen["eigen@5.0.1"] || len(seen) != 4) {
				t.Fatalf("merged closure: %v", seen)
			}
		})
	}
	t.Run("discover all eigen versions", func(t *testing.T) {
		root := t.TempDir()
		describeRootInputs(t, root)
		for _, version := range []string{"3.4.0", "5.0.1"} {
			definitionVersionFile(t, root, "eigen", version, "cmake", testSource)
		}
		var stdout, stderr bytes.Buffer
		if code := run([]string{"describe", "--source-root", root, "--system-name", "Linux", "--architecture", "x86_64", "--discover"}, &stdout, &stderr); code != 0 {
			t.Fatalf("discover: %s", stderr.String())
		}
		var got struct {
			Roots []string `json:"roots"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Roots, []string{"eigen@3.4.0", "eigen@5.0.1"}) {
			t.Fatalf("discovered roots: %v", got.Roots)
		}
	})
}
