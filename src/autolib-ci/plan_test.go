package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCIPlanInputs(t *testing.T) {
	root := fakePlanRoot(t)
	builder, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTOLIB_CI_HELPER_MODE", "valid")
	plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan.RequestedModules, []string{"flann", "lz4", "queue"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("requested modules: want %v, got %v", want, got)
	}
	if len(plan.Platforms) != 3 || plan.Publisher != "linux-x86_64" {
		t.Fatalf("unexpected plan defaults: %+v", plan)
	}
	for _, platform := range plan.Platforms {
		if !reflect.DeepEqual(platform.Layers, [][]string{{"lz4", "queue"}, {"flann"}}) {
			t.Fatalf("%s layers: %v", platform.ID, platform.Layers)
		}
		if platform.Description.SystemName == "" || platform.Description.Architecture == "" {
			t.Fatalf("platform target was not retained: %+v", platform.Description)
		}
	}

	modules, platforms := "flann, flann", "linux-aarch64"
	subset, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Modules: &modules, Platforms: &platforms})
	if err != nil {
		t.Fatal(err)
	}
	if subset.Publisher != "" || len(subset.Platforms) != 1 || !reflect.DeepEqual(subset.Platforms[0].Layers, [][]string{{"lz4"}, {"flann"}}) {
		t.Fatalf("unexpected selected plan: %+v", subset)
	}
	if !reflect.DeepEqual(subset.RequestedModules, []string{"flann"}) {
		t.Fatalf("requested modules were not deduplicated: %v", subset.RequestedModules)
	}
	for _, value := range []string{"", " \t "} {
		t.Run("blank modules "+value, func(t *testing.T) {
			got, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Modules: &value})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, plan) {
				t.Fatalf("blank modules should select discovered candidates: %+v", got)
			}
		})
	}
	all, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Platforms: stringPointer("all")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(all, plan) {
		t.Fatalf("all platforms should select the default platform set: %+v", all)
	}
}

func TestCIPlanRejectsInvalidSelection(t *testing.T) {
	builder, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := fakePlanRoot(t)
	t.Setenv("AUTOLIB_CI_HELPER_MODE", "valid")
	for _, tc := range []struct {
		name      string
		platforms *string
		modules   *string
	}{
		{name: "empty platforms", platforms: stringPointer("")},
		{name: "empty platform item", platforms: stringPointer("linux-x86_64,")},
		{name: "unknown platform", platforms: stringPointer("linux-x64")},
		{name: "empty module item", modules: stringPointer("queue,,lz4")},
		{name: "unsupported module", modules: stringPointer("opencv")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Platforms: tc.platforms, Modules: tc.modules}); err == nil {
				t.Fatal("invalid selection accepted")
			}
		})
	}
}

func stringPointer(value string) *string { return &value }

func TestCIPublisher(t *testing.T) {
	builder, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTOLIB_CI_HELPER_MODE", "valid")
	root := fakePlanRoot(t)
	for _, tc := range []struct {
		platforms string
		modules   string
		want      string
	}{
		{"windows-x86_64,linux-aarch64,linux-x86_64", "queue", "linux-x86_64"},
		{"windows-x86_64,linux-aarch64", "queue", "linux-aarch64"},
		{"windows-x86_64", "queue", "windows-x86_64"},
		{"windows-x86_64,linux-x86_64", "lz4", ""},
	} {
		platforms, modules := tc.platforms, tc.modules
		got, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Platforms: &platforms, Modules: &modules})
		if err != nil {
			t.Fatal(err)
		}
		if got.Publisher != tc.want {
			t.Errorf("platforms %q modules %q: want publisher %q, got %q", tc.platforms, tc.modules, tc.want, got.Publisher)
		}
	}
}

func TestCICLI(t *testing.T) {
	builder, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTOLIB_CI_HELPER_MODE", "valid")
	root := fakePlanRoot(t)
	output := filepath.Join(root, "ci plan.json")
	var stdout, stderr bytes.Buffer
	args := []string{"plan", "--source-root", root, "--builder-file", builder, "--output", output, "--platforms", "linux-aarch64,linux-x86_64", "--modules", "flann"}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("run failed: %d %s", code, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var plan ciPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Publisher != "" || !reflect.DeepEqual(plan.RequestedModules, []string{"flann"}) || len(plan.Platforms) != 2 {
		t.Fatalf("unexpected CLI plan: %+v", plan)
	}
	if !strings.HasSuffix(string(data), "\n") || stdout.String() != output+"\n" {
		t.Fatalf("unexpected CLI output: stdout=%q json=%q", stdout.String(), data)
	}
	validOutput := string(data)
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"plan", "--source-root", root, "--builder-file", builder, "--output", output, "--platforms=all", "--modules="}, &stdout, &stderr); code != 0 {
		t.Fatalf("blank modules failed: %d %s", code, stderr.String())
	}
	data, err = os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.RequestedModules, []string{"flann", "lz4", "queue"}) || len(plan.Platforms) != 3 {
		t.Fatalf("blank CLI modules did not select all: %+v", plan)
	}
	validOutput = string(data)
	for _, bad := range [][]string{
		{"plan", "--source-root", root, "--builder-file", builder, "--output", output, "--platforms="},
		{"plan", "--source-root", root, "--builder-file", builder, "--output", output, "--force=true"},
		append(append([]string{}, args...), "unexpected"),
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(bad, &stdout, &stderr); code == 0 || stderr.Len() == 0 {
			t.Fatalf("accepted invalid CLI input %q: %d %q", bad, code, stderr.String())
		}
		if got, err := os.ReadFile(output); err != nil || string(got) != validOutput {
			t.Fatalf("invalid plan changed successful output: %q, %v", got, err)
		}
	}
}

func fakePlanRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"queue", "lz4", "flann"} {
		writeCIFixture(t, root, "src/dependencies/"+name+"/module.toml", "")
		writeCIFixture(t, root, "src/dependencies/"+name+"/autolib-import.cmake", "# fixture\n")
	}
	return root
}
