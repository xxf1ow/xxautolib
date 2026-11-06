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
	byRef := map[string]*moduleDefinition{}
	for _, m := range definitions {
		byRef[(moduleRef{m.Name, m.Version}).String()] = m
	}
	type output struct{ path, content string }
	outputs := []output{}
	inputs := []string{slashJoin(context.SourceRoot, "go.mod"), slashJoin(context.SourceRoot, "go.sum")}
	for _, m := range definitions {
		dependencies := []*moduleDefinition{}
		for _, ref := range m.Depends {
			dependencies = append(dependencies, byRef[ref])
		}
		content, err := renderModule(m, context, dependencies)
		if err != nil {
			return err
		}
		outputs = append(outputs, output{slashJoin(context.BuildRoot, "generated", m.Name, m.Version+".cmake"), content})
		inputs = append(inputs, filepath.ToSlash(m.Path))
		inputs = append(inputs, slashJoin(filepath.Dir(filepath.Dir(m.Path)), "package.toml"))
		adapter := slashJoin(filepath.Dir(m.Path), "autolib-import.cmake")
		if _, err := os.Stat(adapter); err == nil {
			inputs = append(inputs, adapter)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, tool := range []string{"autolib-builder", "autolib-importer", "internal/packagemetadata"} {
		directory := filepath.Join(context.SourceRoot, "src", tool)
		files, err := os.ReadDir(directory)
		if err != nil {
			return fmt.Errorf("%s: %w", directory, err)
		}
		for _, file := range files {
			name := file.Name()
			if !file.IsDir() && ((strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")) || strings.HasSuffix(name, ".cmake")) {
				inputs = append(inputs, slashJoin(directory, name))
			}
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
	selection, err := selectModuleClosure(sourceRoot, names, systemName, architecture, len(names) == 0)
	if err != nil {
		return nil, err
	}
	if len(names) != 0 && len(selection.FilteredRoots) != 0 {
		reason := selection.FilteredRoots[0]
		return nil, fmt.Errorf("root %s: unsupported platform %s-%s; dependency chain: %s", reason.Root, strings.ToLower(systemName), normalizeArchitecture(architecture), strings.Join(reason.Chain, " -> "))
	}
	return selection.Modules, nil
}
