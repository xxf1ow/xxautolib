package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVersionedClosureRoots(t *testing.T) {
	root := t.TempDir()
	definitionVersionFile(t, root, "eigen", "3.4.0", "cmake", testSource)
	definitionVersionFile(t, root, "eigen", "5.0.1", "cmake", testSource)
	definitionVersionFile(t, root, "left", "1", "cmake", "depends=['eigen@3.4.0']\n"+testSource)
	definitionVersionFile(t, root, "right", "1", "cmake", "depends=['eigen@5.0.1']\n"+testSource)
	modules, err := loadModuleClosure(root, []string{"left@1", "right@1", "left@1"}, "Linux", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	refs := []string{}
	for _, module := range modules {
		refs = append(refs, module.Name+"@"+module.Version)
	}
	if !reflect.DeepEqual(refs, []string{"eigen@3.4.0", "left@1", "eigen@5.0.1", "right@1"}) {
		t.Fatalf("independent roots must retain both versions with stable exact deduplication: %v", refs)
	}
	definitionVersionFile(t, root, "peer", "1", "cmake", "depends=['eigen@3.4.0']\n"+testSource)
	modules, err = loadModuleClosure(root, []string{"left@1", "peer@1"}, "Linux", "x86_64")
	if err != nil || len(modules) != 3 {
		t.Fatalf("shared exact dependency must appear once: %+v, %v", modules, err)
	}
	definitionVersionFile(t, root, "diamond", "1", "cmake", "depends=['left@1','right@1']\n"+testSource)
	for _, roots := range [][]string{{"diamond@1"}, {"left@1", "right@1", "diamond@1"}} {
		_, err := loadModuleClosure(root, roots, "Linux", "x86_64")
		for _, part := range []string{"diamond@1", "eigen@3.4.0", "eigen@5.0.1", "diamond@1 -> left@1 -> eigen@3.4.0", "diamond@1 -> right@1 -> eigen@5.0.1"} {
			if err == nil || !strings.Contains(err.Error(), part) {
				t.Fatalf("conflict must identify root, versions and both chains (%v): %v", roots, err)
			}
		}
	}
}

func TestVersionedClosureFreeze(t *testing.T) {
	root := t.TempDir()
	definitionVersionFile(t, root, "eigen", "3.4.0", "cmake", testSource)
	definitionVersionFile(t, root, "eigen", "5.0.1", "cmake", testSource)
	definitionVersionFile(t, root, "app", "1", "cmake", "depends=['eigen','eigen@3.4.0']\n"+testSource)
	metadata := filepath.Join(root, "src", "dependencies", "eigen", "package.toml")
	putFile(t, metadata, "latest='3.4.0'\n")
	modules, err := loadModuleClosure(root, []string{"app"}, "Linux", "x86_64")
	if err != nil || len(modules) != 2 || !reflect.DeepEqual(modules[1].Depends, []string{"eigen@3.4.0"}) {
		t.Fatalf("bare dependencies must freeze and deduplicate to current latest: %+v, %v", modules, err)
	}
	putFile(t, metadata, "latest='5.0.1'\n")
	if !reflect.DeepEqual(modules[1].Depends, []string{"eigen@3.4.0"}) {
		t.Fatal("a resolved dependency changed after latest changed")
	}
	definitionVersionFile(t, root, "app", "1", "cmake", "depends=['eigen']\n"+testSource)
	modules, err = loadModuleClosure(root, []string{"app"}, "Linux", "x86_64")
	if err != nil || !reflect.DeepEqual(modules[len(modules)-1].Depends, []string{"eigen@5.0.1"}) {
		t.Fatalf("new resolutions must read current latest: %+v, %v", modules, err)
	}
	modules, err = loadModuleClosure(root, nil, "Linux", "x86_64")
	if err != nil || len(modules) != 3 {
		t.Fatalf("empty selection must discover every recipe version: %+v, %v", modules, err)
	}
}

func TestVersionedDescribe(t *testing.T) {
	var outputs [][]byte
	for range 2 {
		root := t.TempDir()
		describeRootInputs(t, root)
		definitionVersionFile(t, root, "eigen", "3.4.0", "cmake", testSource)
		definitionVersionFile(t, root, "app", "1", "cmake", "depends=['eigen']\n"+testSource)
		putFile(t, filepath.Join(root, "src/dependencies/app/1/autolib-import.cmake"), "# adapter\n")
		var stdout, stderr bytes.Buffer
		code := run([]string{"describe", "--source-root", root, "--system-name", "Linux", "--architecture", "x86_64", "--module", "app@1"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("describe failed: %d: %s", code, stderr.String())
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		var modules []map[string]json.RawMessage
		if err := json.Unmarshal(raw["modules"], &modules); err != nil {
			t.Fatal(err)
		}
		if len(modules) != 2 {
			t.Fatalf("wrong closure: %s", stdout.String())
		}
		app := modules[1]
		for key, want := range map[string]string{"name": `"app"`, "version": `"1"`, "depends": `["eigen@3.4.0"]`, "adapter_file": `"src/dependencies/app/1/autolib-import.cmake"`} {
			var compact bytes.Buffer
			if err := json.Compact(&compact, app[key]); err != nil || compact.String() != want {
				t.Fatalf("%s: got %s, want %s (%v)", key, app[key], want, err)
			}
		}
		var inputs []string
		if err := json.Unmarshal(app["input_files"], &inputs); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(inputs, []string{"src/dependencies/app/1/autolib-import.cmake", "src/dependencies/app/1/module.toml", "src/dependencies/app/package.toml"}) {
			t.Fatalf("missing recipe, adapter or latest metadata inputs: %v", inputs)
		}
		outputs = append(outputs, bytes.Clone(stdout.Bytes()))
	}
	if !bytes.Equal(outputs[0], outputs[1]) {
		t.Fatalf("checkout path affected description:\n%s\n%s", outputs[0], outputs[1])
	}
}
