package main

import (
	"fmt"
	"regexp"
	"slices"
)

var gitCommit = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var systemPackagePatterns = map[string]*regexp.Regexp{
	"apt":   regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`),
	"choco": regexp.MustCompile(`^[A-Za-z0-9.][A-Za-z0-9.-]*$`),
	"brew":  regexp.MustCompile(`^[a-z0-9][a-z0-9+_.@-]*$`),
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
		if _, _, err := parseCache(v, where+".cmake", true); err != nil {
			return err
		}
	}
	for _, key := range []string{"config_dir", "pkgconfig_dir", "sentinel", "package_name"} {
		if v, ok := raw[key]; ok {
			parse := relative
			if key == "package_name" {
				parse = name
			}
			if _, err := parse(v, where+"."+key); err != nil {
				return err
			}
		}
	}
	if v, ok := raw["steps"]; ok {
		if _, err := parseSteps(v, ".", where+".steps"); err != nil {
			return err
		}
	}
	for _, key := range []string{"patches", "copy"} {
		v, ok := raw[key]
		if !ok {
			continue
		}
		items, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s.%s: expected array of tables", where, key)
		}
		for i, v := range items {
			at := fmt.Sprintf("%s.%s[%d]", where, key, i)
			keys := []string{"file", "resource"}
			if key == "copy" {
				keys = []string{"files", "directories", "destination", "resource"}
			}
			item, err := table(v, at, keys...)
			if err != nil {
				return err
			}
			if r, ok := item["resource"]; ok {
				if _, err := name(r, at+".resource"); err != nil {
					return err
				}
			}
			if key == "patches" {
				if _, err := relative(item["file"], at+".file"); err != nil {
					return err
				}
				continue
			}
			_, files := item["files"]
			_, dirs := item["directories"]
			if files == dirs {
				return fmt.Errorf("%s: specify either files or directories", at)
			}
			field := "files"
			if dirs {
				field = "directories"
			}
			paths, err := stringArray(item[field], at+"."+field)
			if err != nil {
				return err
			}
			if len(paths) == 0 {
				return fmt.Errorf("%s.%s: expected at least one path", at, field)
			}
			for _, p := range paths {
				if _, err := relative(p, at+"."+field); err != nil {
					return err
				}
			}
			if _, err := relative(item["destination"], at+".destination"); err != nil {
				return err
			}
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
			for _, id := range sortedKeys(platforms) {
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
	for _, manager := range sortedKeys(fields) {
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
