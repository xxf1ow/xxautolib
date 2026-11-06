package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestToolBoundaryFailures(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name string
		mode string
	}{
		{"missing executable", "missing"},
		{"nonzero exit", "nonzero"},
		{"empty output", "empty"},
		{"truncated JSON", "truncated"},
		{"multiple JSON values", "multiple"},
		{"wrong platform", "wrong-platform"},
		{"missing dependency description", "missing-dependency"},
		{"module outside closure", "extra-module"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			builder := filepath.Join(root, "missing-builder")
			if tc.mode != "missing" {
				builder, _ = os.Executable()
			}
			t.Setenv("AUTOLIB_CI_HELPER_MODE", tc.mode)
			if _, err := readModuleDescription(context.Background(), builder, root, "Linux", "x86_64", []string{"lz4"}); err == nil {
				t.Fatal("accepted invalid builder describe result")
			}
		})
	}
}

func init() {
	mode := os.Getenv("AUTOLIB_CI_HELPER_MODE")
	if mode == "" {
		return
	}
	switch mode {
	case "nonzero":
		fmt.Fprint(os.Stdout, `{"system_name":"Linux"}`)
		os.Exit(2)
	case "empty":
		os.Exit(0)
	case "truncated":
		fmt.Fprint(os.Stdout, `{"system_name":`)
		os.Exit(0)
	case "multiple":
		fmt.Fprint(os.Stdout, `{"system_name":"Linux"} {}`)
		os.Exit(0)
	case "wrong-platform":
		fmt.Fprint(os.Stdout, `{"system_name":"Windows","architecture":"x86_64","roots":[],"modules":[],"go_module_files":[],"builder_files":[],"requested_roots":[],"filtered_roots":[]}`)
		os.Exit(0)
	case "missing-dependency":
		fmt.Fprint(os.Stdout, `{"system_name":"Linux","architecture":"x86_64","roots":["flann@1.0"],"modules":[{"name":"flann","version":"1.0","target":"fixture-flann","depends":["lz4@1.0"],"package_platform":"linux-x86_64","system_dependencies":{},"configuration":{},"input_files":[],"adapter_file":""}],"go_module_files":[],"builder_files":[],"requested_roots":["flann@1.0"],"filtered_roots":[]}`)
		os.Exit(0)
	case "extra-module":
		fmt.Fprint(os.Stdout, `{"system_name":"Linux","architecture":"x86_64","roots":["lz4@1.0"],"modules":[{"name":"lz4","version":"1.0","target":"fixture-lz4","depends":[],"package_platform":"linux-x86_64","system_dependencies":{},"configuration":{},"input_files":[],"adapter_file":""},{"name":"queue","version":"1.0","target":"fixture-queue","depends":[],"package_platform":"any-any","system_dependencies":{},"configuration":{},"input_files":[],"adapter_file":""}],"go_module_files":[],"builder_files":[],"requested_roots":["lz4@1.0"],"filtered_roots":[]}`)
		os.Exit(0)
	case "valid":
		var systemName, architecture string
		requested := []string{}
		for i := 1; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "--system-name":
				systemName = os.Args[i+1]
			case "--architecture":
				architecture = os.Args[i+1]
			case "--module":
				requested = append(requested, os.Args[i+1])
			case "--discover":
				requested = []string{"flann", "lz4", "queue"}
			}
		}
		byName := map[string]describedModule{}
		var add func(string)
		add = func(name string) {
			name = strings.TrimSuffix(name, "@1.0")
			if _, ok := byName[name]; ok {
				return
			}
			depends := []string{}
			if name == "flann" {
				add("lz4")
				depends = []string{"lz4@1.0"}
			}
			byName[name] = describedModule{Name: name, Version: "1.0", Target: "fixture-" + name, Depends: depends, PackagePlatform: map[bool]string{true: "any-any", false: strings.ToLower(systemName) + "-" + architecture}[name == "queue"], SystemDependencies: map[string][]string{"apt": {}, "choco": {}}, Configuration: []byte(`{}`), InputFiles: []string{}, AdapterFile: "src/dependencies/" + name + "/autolib-import.cmake"}
		}
		for _, name := range requested {
			add(name)
		}
		modules := []describedModule{}
		for _, name := range []string{"lz4", "queue", "flann"} {
			if module, ok := byName[name]; ok {
				modules = append(modules, module)
			}
		}
		roots := []string{}
		for _, request := range requested {
			root := strings.TrimSuffix(request, "@1.0") + "@1.0"
			if !slices.Contains(roots, root) {
				roots = append(roots, root)
			}
		}
		data, _ := json.Marshal(moduleDescription{SystemName: systemName, Architecture: architecture, Roots: roots, RequestedRoots: roots, FilteredRoots: []filteredRoot{}, Modules: modules, GoModuleFiles: []string{"go.mod", "go.sum"}, BuilderFiles: []string{"CMakeLists.txt"}})
		fmt.Fprintln(os.Stdout, string(data))
		os.Exit(0)
	default:
		os.Exit(3)
	}
}
