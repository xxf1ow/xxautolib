package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	case "run-fixture":
		runLayerCommandFixture()
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
		fmt.Fprint(os.Stdout, `{"system_name":"Windows","architecture":"x86_64","modules":[],"go_module_files":[],"builder_files":[]}`)
		os.Exit(0)
	case "missing-dependency":
		fmt.Fprint(os.Stdout, `{"system_name":"Linux","architecture":"x86_64","modules":[{"name":"flann","depends":["lz4"],"platform_independent":false,"system_dependencies":{},"configuration":{},"input_files":[],"adapter_file":""}],"go_module_files":[],"builder_files":[]}`)
		os.Exit(0)
	case "extra-module":
		fmt.Fprint(os.Stdout, `{"system_name":"Linux","architecture":"x86_64","modules":[{"name":"lz4","depends":[],"platform_independent":false,"system_dependencies":{},"configuration":{},"input_files":[],"adapter_file":""},{"name":"queue","depends":[],"platform_independent":true,"system_dependencies":{},"configuration":{},"input_files":[],"adapter_file":""}],"go_module_files":[],"builder_files":[]}`)
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
			}
		}
		byName := map[string]describedModule{}
		var add func(string)
		add = func(name string) {
			if _, ok := byName[name]; ok {
				return
			}
			depends := []string{}
			if name == "flann" {
				add("lz4")
				depends = []string{"lz4"}
			}
			byName[name] = describedModule{Name: name, Depends: depends, SystemDependencies: map[string][]string{"apt": {}, "choco": {}}, Configuration: []byte(`{}`), InputFiles: []string{}, AdapterFile: "src/dependencies/" + name + "/autolib-import.cmake"}
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
		data, _ := json.Marshal(moduleDescription{SystemName: systemName, Architecture: architecture, Modules: modules, GoModuleFiles: []string{"go.mod", "go.sum"}, BuilderFiles: []string{"CMakeLists.txt"}})
		fmt.Fprintln(os.Stdout, string(data))
		os.Exit(0)
	default:
		os.Exit(3)
	}
}
