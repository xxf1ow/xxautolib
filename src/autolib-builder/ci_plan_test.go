package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestCIPlanInputs(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planCI(ciPlanOptions{SourceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"windows-x86_64", "linux-x86_64", "linux-aarch64"}; !reflect.DeepEqual(plan.Platforms, want) {
		t.Fatalf("platforms: want %v, got %v", want, plan.Platforms)
	}
	if want := []string{"queue", "lz4", "flann"}; !reflect.DeepEqual(plan.Modules, want) {
		t.Fatalf("modules: want %v, got %v", want, plan.Modules)
	}
	if want := [][]string{{"lz4", "queue"}, {"flann"}}; !reflect.DeepEqual(plan.Layers, want) {
		t.Fatalf("layers: want %v, got %v", want, plan.Layers)
	}
	if plan.Publisher != "linux-x86_64" || plan.Force || plan.SystemDependencies == nil || len(plan.SystemDependencies) != 0 {
		t.Fatalf("unexpected defaults: %+v", plan)
	}

	for _, tc := range []struct {
		name      string
		modules   string
		platform  string
		wantMods  []string
		wantPlat  []string
		wantLayer [][]string
	}{
		{"flann closure", " flann, flann ", " linux-aarch64 ", []string{"lz4", "flann"}, []string{"linux-aarch64"}, [][]string{{"lz4"}, {"flann"}}},
		{"lz4 only", "lz4", "windows-x86_64", []string{"lz4"}, []string{"windows-x86_64"}, [][]string{{"lz4"}}},
		{"queue only", "queue", "linux-x86_64", []string{"queue"}, []string{"linux-x86_64"}, [][]string{{"queue"}}},
		{"trim and deduplicate", "queue, lz4,queue", "windows-x86_64, linux-x86_64,windows-x86_64", []string{"queue", "lz4"}, []string{"windows-x86_64", "linux-x86_64"}, [][]string{{"lz4", "queue"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modules, platforms := tc.modules, tc.platform
			got, err := planCI(ciPlanOptions{SourceRoot: root, Modules: &modules, Platforms: &platforms})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Modules, tc.wantMods) || !reflect.DeepEqual(got.Platforms, tc.wantPlat) || !reflect.DeepEqual(got.Layers, tc.wantLayer) {
				t.Fatalf("want modules %v platforms %v layers %v; got %+v", tc.wantMods, tc.wantPlat, tc.wantLayer, got)
			}
		})
	}

	for _, tc := range []struct {
		name      string
		platforms *string
		modules   *string
		force     string
	}{
		{name: "explicit empty platforms", platforms: stringPointer("")},
		{name: "empty platform item", platforms: stringPointer("linux-x86_64,")},
		{name: "unknown platform", platforms: stringPointer("linux-x64")},
		{name: "explicit empty modules", modules: stringPointer("")},
		{name: "empty module item", modules: stringPointer("queue,,lz4")},
		{name: "unknown module", modules: stringPointer("opencv")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := ciPlanOptions{SourceRoot: root, Platforms: tc.platforms, Modules: tc.modules}
			if tc.force != "" {
				o.Force = true
			}
			if _, err := planCI(o); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	forced, err := planCI(ciPlanOptions{SourceRoot: root, Force: true})
	if err != nil || !forced.Force {
		t.Fatalf("explicit force was not retained: %+v, %v", forced, err)
	}
}

func TestCIPublisher(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		platforms string
		modules   string
		want      string
	}{
		{"windows-x86_64,linux-aarch64,linux-x86_64", "queue", "linux-x86_64"},
		{"windows-x86_64,linux-aarch64", "queue", "linux-aarch64"},
		{"windows-x86_64", "queue", "windows-x86_64"},
		{"windows-x86_64,linux-x86_64", "lz4", ""},
		{"windows-x86_64,linux-x86_64", "flann", ""},
	} {
		platforms, modules := tc.platforms, tc.modules
		got, err := planCI(ciPlanOptions{SourceRoot: root, Platforms: &platforms, Modules: &modules})
		if err != nil {
			t.Fatal(err)
		}
		if got.Publisher != tc.want {
			t.Errorf("platforms %q modules %q: want publisher %q, got %q", tc.platforms, tc.modules, tc.want, got.Publisher)
		}
	}
}

func stringPointer(value string) *string { return &value }
