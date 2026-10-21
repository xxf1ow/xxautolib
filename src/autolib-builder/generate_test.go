package main

import (
	"reflect"
	"strings"
	"testing"
)

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
