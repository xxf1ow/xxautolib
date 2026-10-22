package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func writeIfChanged(path, content string) (bool, error) {
	old, err := os.ReadFile(path)
	if err == nil && string(old) == content {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return true, nil
}

func generate(context buildContext, names []string) error {
	definitions, err := loadModuleClosure(context.SourceRoot, names, context.SystemName, context.Architecture)
	if err != nil {
		return err
	}
	byName := map[string]*moduleDefinition{}
	for _, m := range definitions {
		byName[m.Name] = m
	}
	type output struct{ path, content string }
	outputs := []output{}
	inputs := []string{slashJoin(context.SourceRoot, "go.mod"), slashJoin(context.SourceRoot, "go.sum")}
	for _, m := range definitions {
		dependencies := []*moduleDefinition{}
		for _, name := range m.Depends {
			dependencies = append(dependencies, byName[name])
		}
		content, err := renderModule(m, context, dependencies)
		if err != nil {
			return err
		}
		outputs = append(outputs, output{slashJoin(context.BuildRoot, "generated", m.Name+".cmake"), content})
		inputs = append(inputs, filepath.ToSlash(m.Path))
	}
	engine := filepath.Join(context.SourceRoot, "src/autolib-builder")
	files, err := os.ReadDir(engine)
	if err != nil {
		return fmt.Errorf("%s: %w", engine, err)
	}
	for _, file := range files {
		name := file.Name()
		if !file.IsDir() && ((strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")) || strings.HasSuffix(name, ".cmake")) {
			inputs = append(inputs, slashJoin(engine, name))
		}
	}
	slices.Sort(inputs)
	var manifest strings.Builder
	manifest.WriteString("set(XXAUTOLIB_GENERATED_MODULES\n")
	for _, out := range outputs {
		manifest.WriteString("    " + cmakeArgument(out.path) + "\n")
	}
	manifest.WriteString(")\nset(XXAUTOLIB_CONFIGURE_INPUTS\n")
	for _, input := range unique(inputs) {
		manifest.WriteString("    " + cmakeArgument(input) + "\n")
	}
	manifest.WriteString(")\n")
	// 全部声明和渲染通过后才更新结果，失败时入口不能读取残留文件。
	for _, out := range outputs {
		if _, err := writeIfChanged(out.path, out.content); err != nil {
			return err
		}
	}
	_, err = writeIfChanged(filepath.Join(context.BuildRoot, "generated/inputs.cmake"), manifest.String())
	return err
}

func loadModuleClosure(sourceRoot string, names []string, systemName, architecture string) ([]*moduleDefinition, error) {
	result := []*moduleDefinition{}
	complete := map[string]bool{}
	stack := []string{}
	var visit func(string) error
	visit = func(moduleName string) error {
		path := filepath.Join(sourceRoot, "src", "dependencies", moduleName, "module.toml")
		chain := strings.Join(append(slices.Clone(stack), moduleName), " -> ")
		if slices.Contains(stack, moduleName) {
			return fmt.Errorf("%s: dependency cycle: %s", path, chain)
		}
		if complete[moduleName] {
			return nil
		}
		if _, err := name(moduleName, "module name"); err != nil {
			return fmt.Errorf("%s: %w; dependency chain: %s", path, err, chain)
		}
		module, err := loadDefinition(path, systemName, architecture)
		if err != nil {
			return fmt.Errorf("%w; dependency chain: %s", err, chain)
		}
		stack = append(stack, moduleName)
		defer func() { stack = stack[:len(stack)-1] }()
		for _, dependency := range module.Depends {
			if err := visit(dependency); err != nil {
				return fmt.Errorf("%s: %w", module.Path, err)
			}
		}
		complete[moduleName] = true
		result = append(result, module)
		return nil
	}
	for _, moduleName := range names {
		if err := visit(moduleName); err != nil {
			return nil, err
		}
	}
	return result, nil
}
