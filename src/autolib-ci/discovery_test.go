package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func discoveryBuilder(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "builder"+executableSuffix())
	cmd := exec.Command("cmake", "-P", filepath.Join(root, "src/autolib-builder/go-sdk.cmake"), "--", "build", "-o", output, "./src/autolib-builder")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture builder: %v\n%s", err, out)
	}
	return output
}

func discoveryRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, file := range []string{"go.mod", "go.sum", "CMakeLists.txt", "src/autolib-builder/fixture.go"} {
		writeCIFixture(t, root, file, "")
	}
	return root
}

func discoveryModule(t *testing.T, root, name string, adapter bool, depends string) {
	t.Helper()
	declaration := "name='" + name + "'\ntype='cmake'\n" + depends + "\n[source]\nGIT_REPOSITORY='fixture'\nGIT_TAG='fixed'\n"
	writeCIFixture(t, root, "src/dependencies/"+name+"/module.toml", declaration)
	if adapter {
		writeCIFixture(t, root, "src/dependencies/"+name+"/autolib-import.cmake", "message(FATAL_ERROR \"planning must not execute adapters\")\n")
	}
}

func TestCIDiscoveryFiltering(t *testing.T) {
	builder := discoveryBuilder(t)
	t.Run("workflow default discovers adapter-backed modules", func(t *testing.T) {
		data, err := os.ReadFile("../../.github/workflows/build-platform.yml")
		if err != nil {
			t.Fatal(err)
		}
		document := strings.ReplaceAll(string(data), "\r\n", "\n")
		input := workflowSection(t, document, "      modules:")
		selection := strings.Trim(workflowField(t, input, "default"), "'\"")
		root := discoveryRoot(t)
		discoveryModule(t, root, "fresh", true, "")
		discoveryModule(t, root, "independent", true, "")
		discoveryModule(t, root, "local", false, "")
		plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Platforms: stringPointer("linux-x86_64"), Modules: &selection})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(plan.RequestedModules, []string{"fresh", "independent"}) || len(plan.Platforms) != 1 || !reflect.DeepEqual(plan.Platforms[0].Layers, [][]string{{"fresh", "independent"}}) {
			t.Fatalf("workflow default did not plan all adapter-backed modules: %+v", plan)
		}
	})
	for _, kind := range []string{"missing dependencies directory", "source path is file", "unreadable dependencies directory", "unreadable module directory"} {
		t.Run(kind, func(t *testing.T) {
			root := discoveryRoot(t)
			switch kind {
			case "source path is file":
				root = filepath.Join(t.TempDir(), "source")
				if err := os.WriteFile(root, nil, 0644); err != nil {
					t.Fatal(err)
				}
			case "unreadable dependencies directory", "unreadable module directory":
				discoveryModule(t, root, "fresh", true, "")
				directory := filepath.Join(root, "src/dependencies")
				if kind == "unreadable module directory" {
					directory = filepath.Join(directory, "fresh")
				}
				if err := os.Chmod(directory, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(directory, 0755) })
				if _, err := os.ReadDir(directory); err == nil {
					t.Skip("host permissions do not make mode 000 directory unreadable")
				}
			}
			if _, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: "missing-builder"}); err == nil {
				t.Fatal("unreadable discovery source accepted as no work")
			}
		})
	}
	t.Run("empty dependencies directory is no work", func(t *testing.T) {
		root := discoveryRoot(t)
		if err := os.MkdirAll(filepath.Join(root, "src/dependencies"), 0755); err != nil {
			t.Fatal(err)
		}
		plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: "missing-builder"})
		if err != nil || len(plan.Platforms) != 0 || len(plan.RequestedModules) != 0 {
			t.Fatalf("empty module directory: %+v %v", plan, err)
		}
	})
	t.Run("publisher follows effective universal module", func(t *testing.T) {
		root := discoveryRoot(t)
		discoveryModule(t, root, "universal", true, "")
		file := filepath.Join(root, "src/dependencies/universal/module.toml")
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, append([]byte("platform_independent=true\n"), data...), 0644); err != nil {
			t.Fatal(err)
		}
		plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder})
		if err != nil || plan.Publisher != "linux-x86_64" {
			t.Fatalf("publisher depends on package name: %+v %v", plan, err)
		}
	})
	t.Run("no candidates need no builder invocation", func(t *testing.T) {
		root := discoveryRoot(t)
		discoveryModule(t, root, "local", false, "")
		plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: "missing-builder"})
		if err != nil || len(plan.Platforms) != 0 || len(plan.RequestedModules) != 0 {
			t.Fatalf("empty discovery invoked builder: %+v %v", plan, err)
		}
	})
	for _, selection := range []string{"", "fresh,local,blocked,independent", "local", "blocked"} {
		t.Run("selection="+selection, func(t *testing.T) {
			root := discoveryRoot(t)
			discoveryModule(t, root, "shared", true, "")
			discoveryModule(t, root, "fresh", true, "depends=['shared']\n")
			discoveryModule(t, root, "independent", true, "depends=['shared']\n")
			discoveryModule(t, root, "local", false, "")
			discoveryModule(t, root, "middle", true, "depends=['local','orphan']\n")
			discoveryModule(t, root, "orphan", true, "")
			discoveryModule(t, root, "blocked", true, "depends=['middle']\n")
			plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Platforms: stringPointer("linux-x86_64"), Modules: &selection})
			if err != nil {
				t.Fatal(err)
			}
			if selection == "local" || selection == "blocked" {
				if len(plan.Platforms) != 0 || plan.Publisher != "" {
					t.Fatalf("filtered work retained: %+v", plan)
				}
				if err := finalizeCI(plan, ciRunIdentity{}, nil); err != nil {
					t.Fatal(err)
				}
				return
			}
			want := [][]string{{"shared"}, {"fresh", "independent"}}
			if selection == "" {
				want = [][]string{{"orphan", "shared"}, {"fresh", "independent"}}
			}
			if len(plan.Platforms) != 1 || !reflect.DeepEqual(plan.Platforms[0].Layers, want) {
				t.Fatalf("effective closure: %+v", plan)
			}
		})
	}
	for _, kind := range []string{"unknown", "empty item", "broken declaration", "broken local declaration", "directory adapter", "unreadable adapter"} {
		t.Run(kind, func(t *testing.T) {
			root := discoveryRoot(t)
			discoveryModule(t, root, "fresh", true, "")
			selection := "fresh"
			switch kind {
			case "unknown":
				selection = "missing"
			case "empty item":
				selection = "fresh,"
			case "broken declaration":
				writeCIFixture(t, root, "src/dependencies/fresh/module.toml", "broken=[")
			case "broken local declaration":
				if err := os.Remove(filepath.Join(root, "src/dependencies/fresh/autolib-import.cmake")); err != nil {
					t.Fatal(err)
				}
				writeCIFixture(t, root, "src/dependencies/fresh/module.toml", "broken=[")
			case "unreadable adapter":
				file := filepath.Join(root, "src/dependencies/fresh/autolib-import.cmake")
				if err := os.Chmod(file, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(file, 0644) })
				if f, err := os.Open(file); err == nil {
					f.Close()
					t.Skip("host permissions do not make mode 000 unreadable")
				}
			case "directory adapter":
				file := filepath.Join(root, "src/dependencies/fresh/autolib-import.cmake")
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(file, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Modules: &selection}); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}
