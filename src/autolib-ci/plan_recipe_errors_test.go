package main

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func TestCIPlanRecipeErrors(t *testing.T) {
	builder := discoveryBuilder(t)
	for _, fault := range []string{"dangling latest", "bad TOML", "dependency cycle"} {
		for _, selection := range []string{"all", "bad@1,a@1,healthy@1", "bad,a,healthy"} {
			t.Run(fault+"/"+selection, func(t *testing.T) {
				root := discoveryRoot(t)
				discoveryVersion(t, root, "bad", "1", true, "")
				discoveryVersion(t, root, "a", "1", true, "depends=['bad@1']\n")
				discoveryVersion(t, root, "healthy", "1", true, "")
				switch fault {
				case "dangling latest":
					writeCIFixture(t, root, "src/dependencies/bad/package.toml", "latest='deleted'\n")
				case "bad TOML":
					writeCIFixture(t, root, "src/dependencies/bad/1/module.toml", "broken=[")
				case "dependency cycle":
					discoveryVersion(t, root, "bad", "1", true, "depends=['bad@1']\n")
				}
				plan, err := planCI(context.Background(), ciPlanOptions{SourceRoot: root, BuilderFile: builder, Platforms: stringPointer("linux-x86_64"), Modules: &selection})
				if err != nil {
					t.Fatalf("recipe error discarded healthy plan: %v", err)
				}
				if len(plan.Platforms) != 1 || !slices.Equal(plan.Platforms[0].Roots, []string{"healthy@1"}) || len(plan.Platforms[0].FilteredRoots) != 0 {
					t.Fatalf("wrong healthy partition: %+v", plan)
				}
				if len(plan.Platforms[0].Description.Modules) != 1 || moduleIdentity(plan.Platforms[0].Description.Modules[0]) != "healthy@1" || len(plan.Publishers) != 0 {
					t.Fatalf("failed closure leaked into executable work: %+v", plan)
				}
				data, err := json.Marshal(plan.Platforms[0])
				if err != nil {
					t.Fatal(err)
				}
				var wire struct {
					Failed []struct {
						Root, Code, Message string
						Chain               []string
					} `json:"failed_roots"`
				}
				if err := json.Unmarshal(data, &wire); err != nil {
					t.Fatal(err)
				}
				if len(wire.Failed) != 2 {
					t.Fatalf("plan omitted recipe failures: %s", data)
				}
				for _, failed := range wire.Failed {
					if failed.Code != "recipe_error" || failed.Root == "" || failed.Message == "" || len(failed.Chain) == 0 || failed.Chain[0] != failed.Root {
						t.Fatalf("incomplete failure: %+v", failed)
					}
				}
			})
		}
	}
}
