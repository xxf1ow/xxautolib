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
	if got, want := plan.RequestedModules, []string{"flann@1.0", "lz4@1.0", "queue@1.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("requested modules: want %v, got %v", want, got)
	}
	if len(plan.Platforms) != 3 || plan.Publisher != "linux-x86_64" {
		t.Fatalf("unexpected plan defaults: %+v", plan)
	}
	for _, platform := range plan.Platforms {
		if !reflect.DeepEqual(platform.Layers, [][]string{{"lz4@1.0", "queue@1.0"}, {"flann@1.0"}}) {
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
	if subset.Publisher != "" || len(subset.Platforms) != 1 || !reflect.DeepEqual(subset.Platforms[0].Layers, [][]string{{"lz4@1.0"}, {"flann@1.0"}}) {
		t.Fatalf("unexpected selected plan: %+v", subset)
	}
	if !reflect.DeepEqual(subset.RequestedModules, []string{"flann@1.0"}) {
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
	if plan.Publisher != "" || !reflect.DeepEqual(plan.RequestedModules, []string{"flann@1.0"}) || len(plan.Platforms) != 2 {
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
	if !reflect.DeepEqual(plan.RequestedModules, []string{"flann@1.0", "lz4@1.0", "queue@1.0"}) || len(plan.Platforms) != 3 {
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

func TestCIVersionedPlan(t *testing.T) {
	builder := discoveryBuilder(t)
	root := discoveryRoot(t)
	discoveryVersion(t, root, "eigen", "3.4.0", true, "platform_independent=true\n")
	discoveryVersion(t, root, "eigen", "5.0.1", true, "platform_independent=true\n")
	discoveryModule(t, root, "shared", true, "")
	discoveryModule(t, root, "left", true, "depends=['shared','eigen@3.4.0']\n")
	discoveryModule(t, root, "right", true, "depends=['shared','eigen@5.0.1']\n")
	discoveryModule(t, root, "local", false, "")
	discoveryModule(t, root, "middle", true, "depends=['local']\n")
	discoveryModule(t, root, "blocked", true, "depends=['middle']\n")
	for _, tc := range []struct {
		name, modules string
		wantRoots     []string
		wantLayers    [][]string
	}{
		{"independent closures", "left,right,blocked,local", []string{"left@1.0", "right@1.0"}, [][]string{{"eigen@3.4.0", "eigen@5.0.1", "shared@1.0"}, {"left@1.0", "right@1.0"}}},
		{"all filtered", "blocked,local", []string{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Platforms: stringPointer("linux-aarch64"), Modules: &tc.modules})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(plan.RequestedModules, tc.wantRoots) {
				t.Fatalf("requested roots: %v want %v", plan.RequestedModules, tc.wantRoots)
			}
			if tc.wantLayers == nil {
				if len(plan.Platforms) != 0 || plan.Publisher != "" {
					t.Fatalf("filtered plan: %+v", plan)
				}
				return
			}
			if len(plan.Platforms) != 1 || plan.Publisher != "linux-aarch64" || !reflect.DeepEqual(plan.Platforms[0].Layers, tc.wantLayers) {
				t.Fatalf("versioned plan: %+v", plan)
			}
			data, err := json.Marshal(plan.Platforms[0])
			if err != nil {
				t.Fatal(err)
			}
			var platform struct {
				Roots []string `json:"roots"`
			}
			if err := json.Unmarshal(data, &platform); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(platform.Roots, tc.wantRoots) {
				t.Fatalf("effective platform roots: %v", platform.Roots)
			}
		})
	}
	t.Run("three allowed platforms", func(t *testing.T) {
		plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Modules: stringPointer("eigen@3.4.0")})
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, platform := range plan.Platforms {
			ids = append(ids, platform.ID)
		}
		if !reflect.DeepEqual(ids, []string{"windows-x86_64", "linux-x86_64", "linux-aarch64"}) {
			t.Fatalf("platforms: %v", ids)
		}
		if _, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Platforms: stringPointer("windows-aarch64")}); err == nil {
			t.Fatal("unsupported platform accepted")
		}
	})
}
