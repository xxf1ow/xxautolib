package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var directoryTag = regexp.MustCompile(`^<(SOURCE_DIR|BINARY_DIR|INSTALL_DIR|RESOURCE_SOURCE_DIR:[A-Za-z0-9][A-Za-z0-9_.+\-]*)>`)
var placeholderTag = regexp.MustCompile(`<([A-Z][A-Z0-9_]*)(:[^>]*)?>`)
var nativeSteps = []string{"mkdir", "download", "update", "patch", "configure", "build", "install", "test"}

func validateRecipePath(value, where string, allowTags bool) error {
	if strings.Contains(value, "$<") {
		return fmt.Errorf("%s: generator expressions are not directory paths", where)
	}
	if allowTags {
		if tag := directoryTag.FindString(value); tag != "" {
			suffix := strings.TrimPrefix(value, tag)
			if suffix == "" {
				return nil
			}
			if !strings.HasPrefix(suffix, "/") && !strings.HasPrefix(suffix, `\`) {
				return fmt.Errorf("%s: expected a path below directory tag", where)
			}
			_, err := relative(suffix[1:], where)
			return err
		}
	}
	if placeholderTag.MatchString(value) {
		return fmt.Errorf("%s: unknown or misplaced directory placeholder in %q", where, value)
	}
	_, err := relative(value, where)
	return err
}

func validatePlaceholders(value, where string, resources map[string]sourceDefinition) error {
	for _, bounds := range placeholderTag.FindAllStringIndex(value, -1) {
		if bounds[0] > 0 && value[bounds[0]-1] == '$' {
			continue
		}
		tag := value[bounds[0]:bounds[1]]
		if tag == "<SOURCE_DIR>" || tag == "<BINARY_DIR>" || tag == "<INSTALL_DIR>" {
			continue
		}
		if match := resourceReference.FindStringSubmatch(tag); match != nil {
			if _, ok := resources[match[1]]; ok || resources == nil {
				continue
			}
		}
		return fmt.Errorf("%s: unknown directory placeholder %s", where, tag)
	}
	return nil
}

func validateCacheOwnership(module *moduleDefinition, platform string, dependencies []*moduleDefinition) error {
	reserved := []string{"CMAKE_INSTALL_PREFIX", "CMAKE_BUILD_TYPE", "CMAKE_MSVC_RUNTIME_LIBRARY", "CMAKE_GENERATOR", "CMAKE_GENERATOR_PLATFORM", "CMAKE_GENERATOR_TOOLSET", "CMAKE_GENERATOR_INSTANCE", "CMAKE_MAKE_PROGRAM", "CMAKE_C_COMPILER", "CMAKE_CXX_COMPILER", "CMAKE_TOOLCHAIN_FILE", "CMAKE_SYSTEM_NAME", "CMAKE_SYSTEM_VERSION", "CMAKE_SYSTEM_PROCESSOR", "CMAKE_SYSROOT", "CMAKE_C_COMPILER_TARGET", "CMAKE_CXX_COMPILER_TARGET", "CMAKE_C_COMPILER_EXTERNAL_TOOLCHAIN", "CMAKE_CXX_COMPILER_EXTERNAL_TOOLCHAIN"}
	if platform == "darwin-aarch64" {
		reserved = append(reserved, "CMAKE_OSX_ARCHITECTURES", "CMAKE_OSX_DEPLOYMENT_TARGET", "CMAKE_OSX_SYSROOT")
	}
	for _, dep := range dependencies {
		if dep.ConfigDir != "" {
			n := dep.PackageName
			if n == "" {
				n = dep.Name
			}
			reserved = append(reserved, n+"_DIR")
		}
	}
	for _, key := range reserved {
		if _, ok := module.Cache[key]; ok {
			return fmt.Errorf("%s: cmake.cache.%s is owned by the execution entry or direct dependency", module.Path, key)
		}
	}
	return nil
}

func validateEffectiveExecution(module *moduleDefinition) error {
	for _, key := range sortedKeys(module.Cache) {
		for _, value := range cacheStrings(module.Cache[key]) {
			if err := validatePlaceholders(value, "cmake.cache."+key, module.Resources); err != nil {
				return err
			}
		}
	}
	names := slices.Clone(nativeSteps)
	for _, step := range module.Steps {
		if slices.Contains(names, step.Name) {
			return fmt.Errorf("steps.name: duplicate or native step %q", step.Name)
		}
		names = append(names, step.Name)
	}
	directory, err := filepath.EvalSymlinks(filepath.Dir(module.Path))
	if err != nil {
		return err
	}
	for _, step := range module.Steps {
		for _, ref := range append(slices.Clone(step.Dependees), step.Dependers...) {
			if !slices.Contains(names, ref) {
				return fmt.Errorf("steps.%s: unknown step %q", step.Name, ref)
			}
		}
		values := []string{step.WorkingDirectory}
		values = append(values, step.Depends...)
		for _, argv := range step.Commands {
			values = append(values, argv...)
		}
		for _, value := range values {
			if err := validatePlaceholders(value, "steps."+step.Name, module.Resources); err != nil {
				return err
			}
		}
		for _, input := range step.Depends {
			if directoryTag.MatchString(input) {
				continue
			}
			resolved, err := filepath.EvalSymlinks(input)
			if err != nil {
				return fmt.Errorf("steps.%s.DEPENDS: %w", step.Name, err)
			}
			rel, err := filepath.Rel(directory, resolved)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("steps.%s.DEPENDS: path escapes recipe directory", step.Name)
			}
			info, err := os.Stat(resolved)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("steps.%s.DEPENDS: expected an existing file %s", step.Name, input)
			}
		}
	}
	return nil
}
