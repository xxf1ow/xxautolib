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
		outputs = append(outputs, output{slashJoin(context.BuildRoot, "generated", m.Name+".cmake"), content})
		inputs = append(inputs, filepath.ToSlash(m.Path))
		inputs = append(inputs, slashJoin(filepath.Dir(filepath.Dir(m.Path)), "package.toml"))
		adapter := slashJoin(filepath.Dir(m.Path), "autolib-import.cmake")
		if _, err := os.Stat(adapter); err == nil {
			inputs = append(inputs, adapter)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, tool := range []string{"autolib-builder", "autolib-importer"} {
		directory := filepath.Join(context.SourceRoot, "src", tool)
		files, err := os.ReadDir(directory)
		if err != nil {
			return fmt.Errorf("%s: %w", directory, err)
		}
		for _, file := range files {
			name := file.Name()
			if !file.IsDir() && ((strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")) || (tool == "autolib-builder" && strings.HasSuffix(name, ".cmake"))) {
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
	if len(names) == 0 {
		refs, err := discoverModuleRefs(sourceRoot)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			names = append(names, ref.String())
		}
	}
	resolved := map[string]moduleRef{}
	resolve := func(value string) (moduleRef, error) {
		if ref, ok := resolved[value]; ok {
			return ref, nil
		}
		ref, err := parseModuleRef(value)
		if err != nil {
			return moduleRef{}, err
		}
		ref, err = resolveModuleRef(sourceRoot, ref)
		if err == nil {
			resolved[value] = ref
			resolved[ref.String()] = ref
		}
		return ref, err
	}
	definitions := map[string]*moduleDefinition{}
	merged := map[string]bool{}
	result := []*moduleDefinition{}
	for _, request := range names {
		root, err := resolve(request)
		if err != nil {
			return nil, fmt.Errorf("root %s: %w", request, err)
		}
		type selection struct {
			ref   moduleRef
			chain string
		}
		selected := map[string]selection{}
		complete := map[string]bool{}
		stack := []string{}
		closure := []*moduleDefinition{}
		var visit func(moduleRef) error
		visit = func(ref moduleRef) error {
			key := ref.String()
			path := filepath.Join(sourceRoot, "src", "dependencies", ref.Name, ref.Version, "module.toml")
			chain := strings.Join(append(slices.Clone(stack), key), " -> ")
			if previous, ok := selected[ref.Name]; ok && previous.ref.Version != ref.Version {
				return fmt.Errorf("root %s: conflicting dependency versions %s and %s; dependency chains: %s; %s", root, previous.ref, ref, previous.chain, chain)
			}
			if slices.Contains(stack, key) {
				return fmt.Errorf("%s: dependency cycle: %s", path, chain)
			}
			if complete[key] {
				return nil
			}
			selected[ref.Name] = selection{ref, chain}
			module, ok := definitions[key]
			if !ok {
				var err error
				module, err = loadDefinition(path, systemName, architecture)
				if err != nil {
					return fmt.Errorf("%w; dependency chain: %s", err, chain)
				}
				depends := []string{}
				for _, dependency := range module.Depends {
					dep, err := resolve(dependency)
					if err != nil {
						return fmt.Errorf("%s: %w; dependency chain: %s -> %s", path, err, chain, dependency)
					}
					depends = append(depends, dep.String())
				}
				module.Depends = unique(depends)
				definitions[key] = module
			}
			stack = append(stack, key)
			defer func() { stack = stack[:len(stack)-1] }()
			for _, dependency := range module.Depends {
				if err := visit(resolved[dependency]); err != nil {
					return err
				}
			}
			complete[key] = true
			closure = append(closure, module)
			return nil
		}
		if err := visit(root); err != nil {
			return nil, err
		}
		for _, module := range closure {
			key := (moduleRef{module.Name, module.Version}).String()
			if !merged[key] {
				merged[key] = true
				result = append(result, module)
			}
		}
	}
	return result, nil
}
