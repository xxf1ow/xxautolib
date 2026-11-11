package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
)

var gitCommit = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var systemPackagePatterns = map[string]*regexp.Regexp{
	"apt":   regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`),
	"choco": regexp.MustCompile(`^[A-Za-z0-9.][A-Za-z0-9.-]*$`),
	"brew":  regexp.MustCompile(`^[a-z0-9][a-z0-9+_.@-]*$`),
}

func parsePatches(value any, where string) ([]patchDefinition, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected array of tables", where)
	}
	result := make([]patchDefinition, 0, len(items))
	for i, value := range items {
		at := fmt.Sprintf("%s[%d]", where, i)
		item, err := table(value, at, "file", "resource")
		if err != nil {
			return nil, err
		}
		patch := patchDefinition{}
		if patch.File, err = relative(item["file"], at+".file"); err != nil {
			return nil, err
		}
		if value, ok := item["resource"]; ok {
			if patch.Resource, err = name(value, at+".resource"); err != nil {
				return nil, err
			}
		}
		result = append(result, patch)
	}
	return result, nil
}

func parseCopies(value any, where string) ([]copyDefinition, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected array of tables", where)
	}
	result := make([]copyDefinition, 0, len(items))
	for i, value := range items {
		at := fmt.Sprintf("%s[%d]", where, i)
		item, err := table(value, at, "files", "directories", "destination", "resource")
		if err != nil {
			return nil, err
		}
		mapping := copyDefinition{}
		if value, ok := item["resource"]; ok {
			if mapping.Resource, err = name(value, at+".resource"); err != nil {
				return nil, err
			}
		}
		_, files := item["files"]
		_, directories := item["directories"]
		if files == directories {
			return nil, fmt.Errorf("%s: specify either files or directories", at)
		}
		field := "files"
		if directories {
			field = "directories"
		}
		paths, err := stringArray(item[field], at+"."+field)
		if err != nil {
			return nil, err
		}
		if len(paths) == 0 {
			return nil, fmt.Errorf("%s.%s: expected at least one path", at, field)
		}
		for _, path := range paths {
			if _, err := relative(path, at+"."+field); err != nil {
				return nil, err
			}
		}
		if mapping.Destination, err = relative(item["destination"], at+".destination"); err != nil {
			return nil, err
		}
		if files {
			mapping.Files = paths
		} else {
			mapping.Directories = paths
		}
		result = append(result, mapping)
	}
	return result, nil
}

func validateSystemDependency(manager, name string) error {
	pattern, ok := systemPackagePatterns[manager]
	if !ok || !pattern.MatchString(name) {
		return fmt.Errorf("system_dependencies.%s: invalid package name %q", manager, name)
	}
	return nil
}

// 局部校验覆盖所有分支；来源需求和资源引用由有效配方检查。
func validateDefinitionFields(raw map[string]any, where string, override bool) error {
	allowed := []string{"source", "resources", "cmake", "patches", "steps", "copy", "sentinel", "config_dir", "pkgconfig_dir", "package_name"}
	if !override {
		allowed = append(allowed, "type", "platforms", "depends", "system_dependencies", "package_platforms")
	}
	if _, err := table(raw, where, allowed...); err != nil {
		return err
	}
	if _, _, err := sources(raw, where); err != nil {
		return err
	}
	if v, ok := raw["cmake"]; ok {
		cache, _, err := parseCache(v, where+".cmake")
		if err != nil {
			return err
		}
		for _, key := range slices.Sorted(maps.Keys(cache)) {
			for _, value := range cacheStrings(cache[key]) {
				if err := validatePlaceholders(value, where+".cmake.cache."+key, nil); err != nil {
					return err
				}
			}
		}

	}
	if _, err := metadata(raw, where, nil); err != nil {
		return err
	}
	if v, ok := raw["steps"]; ok {
		if _, err := parseSteps(v, ".", where+".steps"); err != nil {
			return err
		}
	}
	if v, ok := raw["patches"]; ok {
		if _, err := parsePatches(v, where+".patches"); err != nil {
			return err
		}
	}
	if v, ok := raw["copy"]; ok {
		if _, err := parseCopies(v, where+".copy"); err != nil {
			return err
		}
	}
	if !override {
		kind, _ := raw["type"].(string)
		checkKind := func(fields map[string]any, at string) error {
			if _, ok := fields["cmake"]; ok && kind != "cmake" {
				return fmt.Errorf("%s.cmake: requires type=cmake", at)
			}
			if _, ok := fields["copy"]; ok && kind != "copy" {
				return fmt.Errorf("%s.copy: requires type=copy", at)
			}
			return nil
		}
		if err := checkKind(raw, where); err != nil {
			return err
		}
		if value, ok := raw["platforms"]; ok {
			platforms, err := table(value, where+".platforms", recipePlatforms...)
			if err != nil {
				return err
			}
			for _, id := range slices.Sorted(maps.Keys(platforms)) {
				fields, ok := platforms[id].(map[string]any)
				if !ok {
					return fmt.Errorf("%s.platforms.%s: expected table", where, id)
				}
				at := where + ".platforms." + id
				if err := validateDefinitionFields(fields, at, true); err != nil {
					return err
				}
				if err := checkKind(fields, at); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateSystemDependencies(value any) (map[string][]string, error) {
	fields, err := table(value, "system_dependencies", "apt", "choco", "brew")
	if err != nil {
		return nil, err
	}
	result := map[string][]string{}
	for _, manager := range slices.Sorted(maps.Keys(fields)) {
		items, err := stringArray(fields[manager], "system_dependencies."+manager)
		if err != nil {
			return nil, err
		}
		for i, item := range items {
			if err := validateSystemDependency(manager, item); err != nil {
				return nil, err
			}
			if slices.Contains(items[:i], item) {
				return nil, fmt.Errorf("system_dependencies.%s: duplicate package %q", manager, item)
			}
		}
		result[manager] = items
	}
	return result, nil
}
