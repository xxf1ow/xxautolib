package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCIBuildIdentity(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module fixture\n\ngo 1.27.0\n")
	write("go.sum", "")
	write(".github/ci-tools.json", `{"tools":{"cmake":"3.31.12"}}`)
	write("src/autolib-builder/ci_identity.go", "identity implementation")
	write("src/autolib-builder/ci_plan.go", "planning implementation")
	write("src/autolib-builder/generate.go", "shared generation implementation")
	write("src/autolib-builder/package.go", "shared packaging implementation")
	write("src/autolib-builder/stage.go", "shared staging implementation")
	write("src/dependencies/base/module.toml", "name = \"base\"\ntype = \"cmake\"\nversion = \"1\"\n[source]\nGIT_REPOSITORY = \"https://example.invalid/base\"\nGIT_TAG = \"v1\"\n")
	write("src/dependencies/base/autolib-import.cmake", "set(BASE_ADAPTER 1)\n")
	write("src/dependencies/app/module.toml", "name = \"app\"\ntype = \"cmake\"\nversion = \"1\"\ndepends = [\"base\"]\npatches = [{ file = \"fix.patch\" }]\n[[steps]]\nname = \"prepare\"\nCOMMAND = [\"cmake\", \"-P\", \"prepare.cmake\"]\nDEPENDS = [\"prepare.cmake\"]\n[source]\nGIT_REPOSITORY = \"https://example.invalid/app\"\nGIT_TAG = \"v1\"\n")
	write("src/dependencies/app/autolib-import.cmake", "set(APP_ADAPTER 1)\n")
	write("src/dependencies/app/fix.patch", "patch version 1\n")
	write("src/dependencies/app/prepare.cmake", "prepare version 1\n")
	write("src/dependencies/other/fix.patch", "unrelated host patch\n")
	write("src/dependencies/queue/module.toml", "name = \"queue\"\ntype = \"copy\"\nplatform_independent = true\n[source]\nGIT_REPOSITORY = \"https://example.invalid/queue\"\nGIT_TAG = \"v1\"\n[[copy]]\nfiles = [\"queue.h\"]\ndestination = \"include\"\n")
	write("src/dependencies/queue/autolib-import.cmake", "set(QUEUE_ADAPTER 1)\n")
	write("README.md", "unrelated documentation\n")
	write("src/dependencies/base/README.md", "not a production input\n")
	base, err := loadDefinition(filepath.Join(root, "src/dependencies/base/module.toml"), "Linux", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	app, err := loadDefinition(filepath.Join(root, "src/dependencies/app/module.toml"), "Linux", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	otherRoot := filepath.Join(root, "another-checkout")
	otherApp := *app
	otherApp.Path = filepath.Join(otherRoot, "src/dependencies/app/module.toml")
	otherApp.Patches = append([]patchDefinition(nil), app.Patches...)
	otherApp.Patches[0].File = filepath.Join(otherRoot, "src/dependencies/app/fix.patch")
	otherApp.Steps = append([]stepDefinition(nil), app.Steps...)
	otherApp.Steps[0].Depends = []string{filepath.Join(otherRoot, "src/dependencies/app/prepare.cmake")}
	firstConfig, err := ciModuleConfiguration(root, app)
	if err != nil {
		t.Fatal(err)
	}
	secondConfig, err := ciModuleConfiguration(otherRoot, &otherApp)
	if err != nil || firstConfig != secondConfig {
		t.Fatalf("normalized module config contains a checkout path: %v", err)
	}
	queue, err := loadDefinition(filepath.Join(root, "src/dependencies/queue/module.toml"), "Linux", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	context := buildContext{Generator: "Unix Makefiles", MultiConfig: false, CMakeDefaults: map[string]string{"CMAKE_POLICY_VERSION_MINIMUM": "3.5"}}
	inputs := ciBuildInputs{SourceRoot: root, OS: "linux", Arch: "x86_64", Context: context, BaselineFile: ".github/ci-tools.json"}
	identities := func() map[string]ciIdentity {
		t.Helper()
		got, err := computeBuildIdentities(inputs, []*moduleDefinition{base, app, queue})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	initial := identities()
	if initial["base"].BuildID == "" || initial["base"].BuildInputsID == "" || initial["app"].BuildID == "" || initial["app"].BuildInputsID == "" {
		t.Fatalf("empty identity: %+v", initial)
	}

	write("README.md", "changed unrelated documentation\n")
	write("src/dependencies/base/README.md", "changed unrelated module documentation\n")
	write("src/dependencies/other/fix.patch", "changed unrelated host patch\n")
	write("src/autolib-builder/ci_identity_test.go", "changed test source\n")
	t.Setenv("UNRELATED_CI_TEST_VARIABLE", "before")
	if got := identities(); got["base"] != initial["base"] || got["app"] != initial["app"] {
		t.Fatalf("unrelated documentation changed identities: before %+v after %+v", initial, got)
	}

	write("src/dependencies/base/autolib-import.cmake", "set(BASE_ADAPTER 2)\n")
	adapter := identities()
	if adapter["base"].BuildID == initial["base"].BuildID || adapter["base"].BuildInputsID != initial["base"].BuildInputsID || adapter["app"] != initial["app"] {
		t.Fatalf("dependency adapter must invalidate only its package: before %+v after %+v", initial, adapter)
	}

	write("src/dependencies/base/module.toml", "name = \"base\"\ntype = \"cmake\"\nversion = \"2\"\n[source]\nGIT_REPOSITORY = \"https://example.invalid/base\"\nGIT_TAG = \"v2\"\n")
	base, err = loadDefinition(filepath.Join(root, "src/dependencies/base/module.toml"), "Linux", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	changedDependency := identities()
	if changedDependency["base"].BuildID == adapter["base"].BuildID || changedDependency["app"].BuildID == adapter["app"].BuildID {
		t.Fatalf("dependency build input must propagate: before %+v after %+v", adapter, changedDependency)
	}

	write("src/dependencies/app/fix.patch", "patch version 2\n")
	patchChanged := identities()
	if patchChanged["app"].BuildID == changedDependency["app"].BuildID {
		t.Fatal("referenced patch change did not invalidate module")
	}
	write("src/dependencies/app/prepare.cmake", "prepare version 2\n")
	scriptChanged := identities()
	if scriptChanged["app"].BuildID == patchChanged["app"].BuildID {
		t.Fatal("referenced step input change did not invalidate module")
	}

	write(".github/ci-tools.json", `{"tools":{"cmake":"3.31.13"}}`)
	baselineChanged := identities()
	if baselineChanged["base"].BuildID == scriptChanged["base"].BuildID || baselineChanged["app"].BuildID == scriptChanged["app"].BuildID {
		t.Fatal("baseline change did not invalidate package")
	}

	write("go.mod", "module fixture\n\ngo 1.27.1\n")
	goChanged := identities()
	if goChanged["base"].BuildID == baselineChanged["base"].BuildID || goChanged["app"].BuildID == baselineChanged["app"].BuildID {
		t.Fatal("Go module change did not invalidate package")
	}

	context.Generator = "Visual Studio 17 2022"
	context.Toolset = "v143,host=x64"
	inputs.Context = context
	inputs.OS = "windows"
	inputs.Arch = "x86_64"
	machineChanged := identities()
	if machineChanged["base"].BuildID == goChanged["base"].BuildID || machineChanged["queue"] != goChanged["queue"] {
		t.Fatal("machine context must affect machine package identity only")
	}

	t.Setenv("UNRELATED_CI_TEST_VARIABLE", "changed")
	if got := identities(); got["base"] != machineChanged["base"] || got["app"] != machineChanged["app"] {
		t.Fatal("unrelated environment variable changed identities")
	}

	for _, path := range []string{"src/autolib-builder/ci_identity.go", "src/autolib-builder/ci_plan.go", "src/autolib-builder/generate.go", "src/autolib-builder/package.go", "src/autolib-builder/stage.go"} {
		before := identities()
		write(path, "changed production input\n")
		after := identities()
		if after["base"].BuildID == before["base"].BuildID || after["app"].BuildID == before["app"].BuildID || after["queue"].BuildID == before["queue"].BuildID {
			t.Errorf("shared production input %s did not invalidate every selected package", path)
		}
	}
}
