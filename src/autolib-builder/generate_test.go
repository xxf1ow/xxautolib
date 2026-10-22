package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGenerationWrites(t *testing.T) {
	f := newEntryFixture(t)
	dep := definitionFile(t, f.root, "dep", "cmake", testSource+"[cmake.cache]\nVALUE='one'\n")
	definitionFile(t, f.root, "leaf", "cmake", "depends=['dep']\n"+testSource)
	definitionFile(t, f.root, "unused", "cmake", testSource)
	if err := generate(f.context, []string{"leaf"}); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(f.context.BuildRoot, "generated/inputs.cmake")
	before := readFile(t, manifest)
	stat, err := os.Stat(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"dep/module.toml", "leaf/module.toml", "go.mod", "go.sum", "cmake.go", "apply_patch.cmake", "configure_dependencies.cmake"} {
		if !strings.Contains(before, part) {
			t.Fatalf("missing input %s", part)
		}
	}
	if strings.Contains(before, "unused/module.toml") {
		t.Fatal("unselected input")
	}
	if err := generate(f.context, []string{"leaf"}); err != nil {
		t.Fatal(err)
	}
	now, _ := os.Stat(manifest)
	if !stat.ModTime().Equal(now.ModTime()) {
		t.Fatal("unchanged output rewritten")
	}
	generated := filepath.Join(f.context.BuildRoot, "generated/dep.cmake")
	old := readFile(t, generated)
	putFile(t, dep, strings.ReplaceAll(readFile(t, dep), "VALUE='one'", "VALUE='two'"))
	if err := generate(f.context, []string{"leaf"}); err != nil {
		t.Fatal(err)
	}
	if readFile(t, generated) == old || !strings.Contains(readFile(t, generated), "VALUE:STRING=two") {
		t.Fatal("dependency change ignored")
	}
	putFile(t, dep, "name=[broken TOML")
	if err := generate(f.context, []string{"leaf"}); err == nil {
		t.Fatal("accepted malformed input")
	}
	if readFile(t, manifest) != before {
		t.Fatal("failed generation replaced inputs")
	}
	now, _ = os.Stat(manifest)
	if !stat.ModTime().Equal(now.ModTime()) {
		t.Fatal("failed generation rewrote inputs")
	}
	output := filepath.Join(f.root, "output.cmake")
	if changed, err := writeIfChanged(output, "one"); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if changed, err := writeIfChanged(output, "one"); err != nil || changed {
		t.Fatal(changed, err)
	}
	if changed, err := writeIfChanged(output, "two"); err != nil || !changed {
		t.Fatal(changed, err)
	}
}

func TestModuleClosureStable(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ name, depends string }{{"shared", "[]"}, {"left", "['shared']"}, {"right", "['shared']"}, {"leaf", "['left','right']"}, {"unused", "[]"}} {
		definitionFile(t, root, tc.name, "cmake", "depends="+tc.depends+"\n"+testSource)
	}
	modules, err := loadModuleClosure(root, []string{"leaf", "leaf"}, "Linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, m := range modules {
		names = append(names, m.Name)
	}
	if !reflect.DeepEqual(names, []string{"shared", "left", "right", "leaf"}) {
		t.Fatal(names)
	}
}

func TestModuleClosureErrors(t *testing.T) {
	for _, tc := range []struct{ left, right, chain string }{{"missing", "", "left -> missing"}, {"left", "", "left -> left"}, {"right", "left", "left -> right -> left"}} {
		t.Run(tc.chain, func(t *testing.T) {
			root := t.TempDir()
			definitionFile(t, root, "left", "cmake", "depends=['"+tc.left+"']\n"+testSource)
			if tc.right != "" {
				definitionFile(t, root, "right", "cmake", "depends=['"+tc.right+"']\n"+testSource)
			}
			_, err := loadModuleClosure(root, []string{"left"}, "Linux", "amd64")
			if err == nil || !strings.Contains(err.Error(), tc.chain) || !strings.Contains(err.Error(), "module.toml") || !strings.Contains(err.Error(), root) {
				t.Fatal(err)
			}
		})
	}
	if _, err := loadModuleClosure(t.TempDir(), []string{"../escape"}, "Linux", ""); err == nil || !strings.Contains(err.Error(), "module name") {
		t.Fatal(err)
	}
}
