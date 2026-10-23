package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIIdentityOwnership(t *testing.T) {
	root, description, inputs := identityFixture(t)
	inputs.SourceRoot = root
	initial := identitiesForTest(t, inputs, description)
	writeIdentityFixture(t, root, "src/dependencies/lz4/autolib-import.cmake", "adapter changed")
	adapterChanged := identitiesForTest(t, inputs, description)
	if adapterChanged["lz4"].BuildID == initial["lz4"].BuildID || adapterChanged["lz4"].BuildInputsID != initial["lz4"].BuildInputsID {
		t.Fatal("module adapter must change BuildID but not BuildInputsID")
	}
	if adapterChanged["flann"] != initial["flann"] {
		t.Fatal("dependency adapter must not propagate to its consumer")
	}
	writeIdentityFixture(t, root, "README.md", "documentation changed")
	writeIdentityFixture(t, root, "src/autolib-ci/identity_test.go", "test source changed")
	writeIdentityFixture(t, root, "src/autolib-ci/README.md", "CI notes changed")
	if got := identitiesForTest(t, inputs, description); got["flann"] != adapterChanged["flann"] {
		t.Fatal("documentation and test source must not change production identities")
	}
}

func TestCIIdentityPropagation(t *testing.T) {
	root, description, inputs := identityFixture(t)
	inputs.SourceRoot = root
	initial := identitiesForTest(t, inputs, description)
	description.Modules[0].Configuration = json.RawMessage(`{"Name":"lz4","Version":"2","Cache":{"BUILD_SHARED_LIBS":"OFF"}}`)
	configurationChanged := identitiesForTest(t, inputs, description)
	if configurationChanged["lz4"].BuildInputsID == initial["lz4"].BuildInputsID || configurationChanged["flann"].BuildInputsID == initial["flann"].BuildInputsID {
		t.Fatal("module configuration change must invalidate its production inputs and consumers")
	}
	description.Modules[0].Configuration = json.RawMessage(`{"Name":"lz4","Version":"1"}`)
	writeIdentityFixture(t, root, "src/dependencies/lz4/fix.patch", "patch changed")
	patchChanged := identitiesForTest(t, inputs, description)
	if patchChanged["lz4"].BuildInputsID == initial["lz4"].BuildInputsID || patchChanged["flann"].BuildInputsID == initial["flann"].BuildInputsID {
		t.Fatal("dependency production input must invalidate itself and its consumer")
	}
	writeIdentityFixture(t, root, "src/autolib-builder/generate.go", "builder code changed")
	builderChanged := identitiesForTest(t, inputs, description)
	if builderChanged["lz4"].BuildInputsID == patchChanged["lz4"].BuildInputsID {
		t.Fatal("builder production source must invalidate production inputs")
	}
	writeIdentityFixture(t, root, ".github/ci-tools.json", identityBaseline("3.32.0", "9.5"))
	baselineChanged := identitiesForTest(t, inputs, description)
	if baselineChanged["lz4"].BuildInputsID == builderChanged["lz4"].BuildInputsID || baselineChanged["flann"].BuildInputsID == builderChanged["flann"].BuildInputsID {
		t.Fatal("current platform production baseline must invalidate machine identities")
	}
	writeIdentityFixture(t, root, "go.mod", "module fixture\n\ngo 1.27.4\n")
	goChanged := identitiesForTest(t, inputs, description)
	if goChanged["lz4"].BuildInputsID == baselineChanged["lz4"].BuildInputsID || goChanged["flann"].BuildInputsID == baselineChanged["flann"].BuildInputsID {
		t.Fatal("Go module input change must invalidate production identities")
	}
	writeIdentityFixture(t, root, "src/autolib-ci/identity.go", "changed CI identity algorithm\n")
	algorithmChanged := identitiesForTest(t, inputs, description)
	if algorithmChanged["lz4"].BuildInputsID == goChanged["lz4"].BuildInputsID || algorithmChanged["flann"].BuildInputsID == goChanged["flann"].BuildInputsID {
		t.Fatal("identity algorithm source change must invalidate production identities")
	}
	baselineWithPublisher := strings.Replace(identityBaseline("3.32.0", "9.5"), `}},"make"`, `},"publisher":"windows-x86_64"},"make"`, 1)
	writeIdentityFixture(t, root, ".github/ci-tools.json", baselineWithPublisher)
	gotPublisher := identitiesForTest(t, inputs, description)
	if gotPublisher["lz4"] != algorithmChanged["lz4"] || gotPublisher["flann"] != algorithmChanged["flann"] {
		t.Fatal("publisher-only baseline change must not invalidate production identities")
	}
	writeIdentityFixture(t, root, "src/autolib-ci/logging.go", "log formatting changed\n")
	writeIdentityFixture(t, root, "src/autolib-builder/publish.go", "publisher behavior changed\n")
	writeIdentityFixture(t, root, "src/autolib-ci/identity_test.go", "test changed\n")
	if got := identitiesForTest(t, inputs, description); got["lz4"] != algorithmChanged["lz4"] || got["flann"] != algorithmChanged["flann"] {
		t.Fatal("logging, publisher, or test source change must not invalidate production identities")
	}
	otherRoot, otherDescription, otherInputs := identityFixture(t)
	otherInputs.SourceRoot = otherRoot
	if got := identitiesForTest(t, otherInputs, otherDescription); got["lz4"] != initial["lz4"] || got["flann"] != initial["flann"] {
		t.Fatal("moving identical source inputs to another checkout path changed identities")
	}
	queue := description
	queue.Modules = []describedModule{identityModule("queue", nil, true)}
	queueInputs := inputs
	queueBefore := identitiesForTest(t, queueInputs, queue)
	platformOnlyBaseline := strings.Replace(identityBaseline("3.32.0", "9.5"), `"gcc_version":"9.5"`, `"gcc_version":"9.6"`, 1)
	writeIdentityFixture(t, root, ".github/ci-tools.json", platformOnlyBaseline)
	if got := identitiesForTest(t, queueInputs, queue); got["queue"] != queueBefore["queue"] {
		t.Fatal("platform-specific production change must not invalidate platform-independent package")
	}
	writeIdentityFixture(t, root, ".github/ci-tools.json", identityBaseline("3.33.0", "9.5"))
	queueAfter := identitiesForTest(t, queueInputs, queue)
	if queueAfter["queue"] == queueBefore["queue"] {
		t.Fatal("common production baseline must invalidate platform-independent package")
	}
}

func identityFixture(t *testing.T) (string, moduleDescription, ciBuildInputs) {
	t.Helper()
	root := t.TempDir()
	for path, value := range map[string]string{
		"go.mod":                          "module fixture\n",
		"go.sum":                          "",
		"CMakeLists.txt":                  "cmake_minimum_required(VERSION 3.20)\n",
		"src/autolib-builder/generate.go": "generate source\n",
		"src/dependencies/lz4/autolib-import.cmake":   "lz4 adapter\n",
		"src/dependencies/lz4/module.toml":            "lz4 declaration\n",
		"src/dependencies/lz4/fix.patch":              "patch bytes\n",
		"src/dependencies/flann/autolib-import.cmake": "flann adapter\n",
		"src/dependencies/flann/module.toml":          "flann declaration\n",
		"src/dependencies/flann/prepare.cmake":        "step bytes\n",
		"src/dependencies/queue/autolib-import.cmake": "queue adapter\n",
		"src/dependencies/queue/module.toml":          "queue declaration\n",
		"src/autolib-ci/identity.go":                  "identity algorithm\n",
		".github/ci-tools.json":                       identityBaseline("3.31.12", "9.4"),
	} {
		writeIdentityFixture(t, root, path, value)
	}
	files := []string{"src/autolib-builder/generate.go"}
	description := moduleDescription{SystemName: "Linux", Architecture: "x86_64", GoModuleFiles: []string{"go.mod", "go.sum"}, BuilderFiles: files, Modules: []describedModule{
		identityModule("lz4", nil, false),
		identityModule("flann", []string{"lz4"}, false),
	}}
	description.Modules[0].InputFiles = []string{"src/dependencies/lz4/fix.patch"}
	description.Modules[1].InputFiles = []string{"src/dependencies/flann/prepare.cmake"}
	description.Modules[0].Configuration = json.RawMessage(`{"Name":"lz4","Version":"1"}`)
	description.Modules[1].Configuration = json.RawMessage(`{"Name":"flann","Version":"1"}`)
	inputs := ciBuildInputs{SourceRoot: root, BaselineFile: ".github/ci-tools.json", Context: ciProductionContext{OS: "linux", Arch: "x86_64", Generator: "Unix Makefiles", Configuration: "Release", CMakeDefaults: map[string]string{"CMAKE_INSTALL_LIBDIR": "lib"}}}
	return root, description, inputs
}

func identityModule(name string, dependencies []string, independent bool) describedModule {
	if dependencies == nil {
		dependencies = []string{}
	}
	return describedModule{Name: name, Depends: dependencies, PlatformIndependent: independent, SystemDependencies: map[string][]string{"apt": {}, "choco": {}}, Configuration: json.RawMessage(`{}`), InputFiles: []string{}, AdapterFile: "src/dependencies/" + name + "/autolib-import.cmake"}
}

func identityBaseline(cmakeVersion, gccVersion string) string {
	return `{"production":{"common":{"go_version":"1.27.3","cmake_version":"` + cmakeVersion + `","configuration":"Release","cmake_defaults":{"CMAKE_INSTALL_LIBDIR":"lib"}},"linux":{"distribution":"ubuntu-20.04","gcc_version":"` + gccVersion + `","glibc_max":"2.31","make_version":"4.4.1","generator":"Unix Makefiles"}},"make":{"version":"4.4.1","url":"https://example.invalid/make.tar.gz","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"cmake":{"checksums_url":"https://example.invalid/checksums","assets":[{"platform":"linux-x86_64","url":"https://example.invalid/cmake.tar.gz","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}}`
}

func writeIdentityFixture(t *testing.T, root, path, value string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func identitiesForTest(t *testing.T, inputs ciBuildInputs, description moduleDescription) map[string]ciIdentity {
	t.Helper()
	got, err := computeBuildIdentities(inputs, description)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
