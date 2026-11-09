package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDescribeReportIsolation(t *testing.T) {
	for _, fault := range []string{"dangling latest", "bad TOML", "dependency cycle"} {
		for _, discover := range []bool{false, true} {
			t.Run(fault+map[bool]string{false: "/explicit", true: "/discover"}[discover], func(t *testing.T) {
				root := t.TempDir()
				describeRootInputs(t, root)
				definitionVersionFile(t, root, "bad", "1", "cmake", testSource)
				definitionVersionFile(t, root, "a", "1", "cmake", "depends=['bad@1']\n"+testSource)
				definitionVersionFile(t, root, "healthy", "1", "cmake", testSource)
				switch fault {
				case "dangling latest":
					putFile(t, filepath.Join(root, "src/dependencies/bad/package.toml"), "latest='deleted'\n")
				case "bad TOML":
					putFile(t, filepath.Join(root, "src/dependencies/bad/1/module.toml"), "broken=[")
				case "dependency cycle":
					definitionVersionFile(t, root, "bad", "1", "cmake", "depends=['bad@1']\n"+testSource)
				}
				args := []string{"--source-root", root, "--system-name", "Linux", "--architecture", "x86_64"}
				if discover {
					args = append(args, "--discover")
				} else {
					args = append(args, "--module", "bad@1", "--module", "a@1", "--module", "healthy@1")
				}
				var stdout, stderr bytes.Buffer
				if code := runDescribe(append(slices.Clone(args), "--report-errors"), &stdout, &stderr); code != 0 {
					t.Fatalf("report discarded healthy work: code=%d stderr=%s", code, &stderr)
				}
				var report struct {
					Roots          []string
					RequestedRoots []string `json:"requested_roots"`
					Failed         []struct {
						Root, Code, Message string
						Chain               []string
					} `json:"failed_roots"`
					Filtered []json.RawMessage `json:"filtered_roots"`
					Modules  []describedModule
				}
				if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(report.Roots, []string{"healthy@1"}) || len(report.Modules) != 1 || report.Modules[0].Name != "healthy" {
					t.Fatalf("healthy closure lost or failed closure leaked: %s", &stdout)
				}
				if len(report.Failed) != 2 || len(report.Filtered) != 0 || len(report.RequestedRoots) != 3 {
					t.Fatalf("wrong failure partition: %s", &stdout)
				}
				for _, failed := range report.Failed {
					if !slices.Contains([]string{"bad@1", "a@1"}, failed.Root) || failed.Code != "recipe_error" || failed.Message == "" || len(failed.Chain) == 0 || failed.Chain[0] != failed.Root {
						t.Fatalf("missing named failure: %+v", failed)
					}
				}
				stdout.Reset()
				stderr.Reset()
				if code := runDescribe(args, &stdout, &stderr); code == 0 {
					t.Fatal("strict describe swallowed a recipe error")
				}
			})
		}
	}
}

func TestDescribeReportGlobalErrors(t *testing.T) {
	for _, fault := range []string{"request syntax", "platform", "shared input", "escaping input"} {
		t.Run(fault, func(t *testing.T) {
			root := t.TempDir()
			describeRootInputs(t, root)
			definitionVersionFile(t, root, "healthy", "1", "cmake", testSource)
			definitionVersionFile(t, root, "bad", "1", "cmake", testSource)
			args := []string{"--report-errors", "--source-root", root, "--system-name", "Linux", "--architecture", "x86_64", "--module", "healthy@1"}
			switch fault {
			case "request syntax":
				args = append(args, "--module", "bad@@1")
			case "platform":
				args = append(args, "--architecture", "unsupported")
			case "shared input":
				if err := os.Remove(filepath.Join(root, "go.mod")); err != nil {
					t.Fatal(err)
				}
			case "escaping input":
				external := filepath.Join(t.TempDir(), "external.patch")
				putFile(t, external, "patch")
				if err := os.Symlink(external, filepath.Join(root, "src/dependencies/bad/1/escape.patch")); err != nil {
					t.Fatal(err)
				}
				definitionVersionFile(t, root, "bad", "1", "cmake", "patches=[{file='escape.patch'}]\n"+testSource)
				args = append(args, "--module", "bad@1")
			}
			var stdout, stderr bytes.Buffer
			if code := runDescribe(args, &stdout, &stderr); code == 0 {
				t.Fatalf("global error became a recipe failure: %s", &stdout)
			}
		})
	}
}
