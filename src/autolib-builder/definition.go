package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var targetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.+\-]*$`)
var cacheKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var sha256Hash = regexp.MustCompile(`^SHA256=[0-9a-fA-F]{64}$`)
var resourceReference = regexp.MustCompile(`<RESOURCE_SOURCE_DIR:([^>]*)>`)

func normalizeArchitecture(value string) string {
	switch strings.ToLower(value) {
	case "x64", "amd64", "x86_64":
		return "x86_64"
	case "arm64", "aarch64":
		return "aarch64"
	default:
		return value
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func table(value any, where string, allowed ...string) (map[string]any, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected a table", where)
	}
	for _, key := range sortedKeys(m) {
		if !slices.Contains(allowed, key) {
			return nil, fmt.Errorf("%s: unknown field %s", where, key)
		}
	}
	return m, nil
}

func text(value any, where string) (string, error) {
	s, ok := value.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("%s: expected a nonempty string", where)
	}
	return s, nil
}

func name(value any, where string) (string, error) {
	s, err := text(value, where)
	if err != nil {
		return "", err
	}
	if !targetName.MatchString(s) {
		return "", fmt.Errorf("%s: invalid target name %q", where, s)
	}
	return s, nil
}

func stringArray(value any, where string) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected an array of strings", where)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s: expected an array of strings", where)
		}
		result = append(result, s)
	}
	return result, nil
}

func relative(value any, where string) (string, error) {
	s, err := text(value, where)
	if err != nil {
		return "", err
	}
	// 同时按 Windows 和 POSIX 识别边界，不能因运行宿主不同而接受盘符路径。
	unified := strings.ReplaceAll(s, `\`, "/")
	if strings.HasPrefix(unified, "/") || (len(s) >= 2 && s[1] == ':') || slices.Contains(strings.Split(unified, "/"), "..") {
		return "", fmt.Errorf("%s: expected a path within its declared directory", where)
	}
	return s, nil
}

func parseSource(value any, where string) (*sourceDefinition, error) {
	m, err := table(value, where, "GIT_REPOSITORY", "GIT_TAG", "URL", "URL_HASH", "DOWNLOAD_NAME")
	if err != nil {
		return nil, err
	}
	_, repo := m["GIT_REPOSITORY"]
	_, tag := m["GIT_TAG"]
	_, url := m["URL"]
	_, hash := m["URL_HASH"]
	_, download := m["DOWNLOAD_NAME"]
	if (repo || tag) && (url || hash || download) {
		return nil, fmt.Errorf("%s: Git and URL archive fields are mutually exclusive", where)
	}
	s := &sourceDefinition{}
	if !url && !hash && !download {
		if s.GitRepository, err = text(m["GIT_REPOSITORY"], where+".GIT_REPOSITORY"); err != nil {
			return nil, err
		}
		if s.GitTag, err = text(m["GIT_TAG"], where+".GIT_TAG"); err != nil {
			return nil, err
		}
		return s, nil
	}
	if s.URL, err = stringArray(m["URL"], where+".URL"); err != nil {
		return nil, err
	}
	if len(s.URL) == 0 || slices.Contains(s.URL, "") {
		return nil, fmt.Errorf("%s.URL: expected a nonempty array of nonempty strings", where)
	}
	if s.URLHash, err = text(m["URL_HASH"], where+".URL_HASH"); err != nil {
		return nil, err
	}
	if !sha256Hash.MatchString(s.URLHash) {
		return nil, fmt.Errorf("%s.URL_HASH: expected SHA256=<64 hexadecimal digits>", where)
	}
	if download {
		if s.DownloadName, err = relative(m["DOWNLOAD_NAME"], where+".DOWNLOAD_NAME"); err != nil {
			return nil, err
		}
		if s.DownloadName == "." || strings.ContainsAny(s.DownloadName, `/\`) {
			return nil, fmt.Errorf("%s.DOWNLOAD_NAME: expected a single filename", where)
		}
	}
	return s, nil
}

func sources(m map[string]any, where string) (*sourceDefinition, map[string]sourceDefinition, error) {
	var primary *sourceDefinition
	var err error
	if value, ok := m["source"]; ok {
		primary, err = parseSource(value, where+".source")
		if err != nil {
			return nil, nil, err
		}
	}
	result := map[string]sourceDefinition{}
	if value, ok := m["resources"]; ok {
		resources, ok := value.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("%s.resources: expected a table", where)
		}
		for _, key := range sortedKeys(resources) {
			if _, err = name(key, where+".resources"); err != nil {
				return nil, nil, err
			}
			source, err := parseSource(resources[key], where+".resources."+key)
			if err != nil {
				return nil, nil, err
			}
			result[key] = *source
		}
	}
	return primary, result, nil
}

func parseCache(value any, where string, allowSubdir bool) (map[string]any, string, error) {
	allowed := []string{"cache"}
	if allowSubdir {
		allowed = append(allowed, "SOURCE_SUBDIR")
	}
	m, err := table(value, where, allowed...)
	if err != nil {
		return nil, "", err
	}
	subdir := ""
	if v, ok := m["SOURCE_SUBDIR"]; ok {
		subdir, err = relative(v, where+".SOURCE_SUBDIR")
		if err != nil {
			return nil, "", err
		}
	}
	result := map[string]any{}
	if v, ok := m["cache"]; ok {
		cache, ok := v.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("%s.cache: expected a table", where)
		}
		for _, key := range sortedKeys(cache) {
			at := where + ".cache." + key
			if !cacheKey.MatchString(key) {
				return nil, "", fmt.Errorf("%s: invalid cache key", at)
			}
			switch item := cache[key].(type) {
			case string, bool, int64:
				result[key] = item
			case []any:
				result[key], err = stringArray(item, at)
				if err != nil {
					return nil, "", err
				}
			default:
				return nil, "", fmt.Errorf("%s: expected string, bool, integer or string array", at)
			}
		}
	}
	return result, subdir, nil
}

func inputPath(value, directory string) string {
	if strings.Contains(value, "<") {
		return value
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(directory, value)
	}
	return filepath.ToSlash(filepath.Clean(value))
}

func parseSteps(value any, directory, where string) ([]stepDefinition, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected an array of tables", where)
	}
	result := []stepDefinition{}
	for i, item := range items {
		at := fmt.Sprintf("%s[%d]", where, i)
		m, err := table(item, at, "name", "COMMAND", "DEPENDEES", "DEPENDERS", "DEPENDS", "WORKING_DIRECTORY")
		if err != nil {
			return nil, err
		}
		s := stepDefinition{}
		if s.Name, err = name(m["name"], at+".name"); err != nil {
			return nil, err
		}
		commands, ok := m["COMMAND"].([]any)
		if !ok || len(commands) == 0 {
			return nil, fmt.Errorf("%s.COMMAND: expected argv or an array of argv arrays", at)
		}
		if _, flat := commands[0].(string); flat {
			commands = []any{commands}
		}
		for _, command := range commands {
			argv, err := stringArray(command, at+".COMMAND")
			if err != nil {
				return nil, err
			}
			if len(argv) == 0 || argv[0] == "" {
				return nil, fmt.Errorf("%s.COMMAND: command executable cannot be empty", at)
			}
			s.Commands = append(s.Commands, argv)
		}
		for _, field := range []struct {
			key  string
			dest *[]string
		}{{"DEPENDEES", &s.Dependees}, {"DEPENDERS", &s.Dependers}, {"DEPENDS", &s.Depends}} {
			if v, ok := m[field.key]; ok {
				*field.dest, err = stringArray(v, at+"."+field.key)
				if err != nil {
					return nil, err
				}
			}
		}
		for i, p := range s.Depends {
			s.Depends[i] = inputPath(p, directory)
		}
		if v, ok := m["WORKING_DIRECTORY"]; ok {
			p, err := text(v, at+".WORKING_DIRECTORY")
			if err != nil {
				return nil, err
			}
			s.WorkingDirectory = inputPath(p, directory)
		}
		result = append(result, s)
	}
	return result, nil
}

func metadata(m map[string]any, where string, base map[string]string) (map[string]string, error) {
	result := map[string]string{}
	for key, value := range base {
		result[key] = value
	}
	for _, key := range []string{"config_dir", "pkgconfig_dir", "sentinel", "package_name"} {
		if v, ok := m[key]; ok {
			parse := relative
			if key == "package_name" {
				parse = name
			}
			value, err := parse(v, where+"."+key)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
	}
	if result["package_name"] != "" && result["config_dir"] == "" {
		return nil, fmt.Errorf("%s.package_name: requires config_dir", where)
	}
	return result, nil
}

func optional(m map[string]any, key string, fallback any) any {
	if value, ok := m[key]; ok {
		return value
	}
	return fallback
}

func loadDefinition(path, systemName, architecture string) (definition *moduleDefinition, err error) {
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = fmt.Errorf("%s: %w", path, err)
		}
	}()
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err = toml.Unmarshal(content, &raw); err != nil {
		return nil, err
	}
	m, err := table(raw, "module", "name", "type", "version", "source", "resources", "cmake", "patches", "steps", "copy", "platforms", "sentinel", "depends", "config_dir", "pkgconfig_dir", "package_name")
	if err != nil {
		return nil, err
	}
	d := &moduleDefinition{Path: path}
	if d.Name, err = name(m["name"], "name"); err != nil {
		return nil, err
	}
	if d.Name != filepath.Base(filepath.Dir(path)) {
		return nil, fmt.Errorf("name: must match the module directory name")
	}
	d.Kind, _ = m["type"].(string)
	if d.Kind != "cmake" && d.Kind != "copy" {
		return nil, fmt.Errorf("type: expected cmake or copy")
	}
	if v, ok := m["version"]; ok {
		if d.Version, err = text(v, "version"); err != nil {
			return nil, err
		}
	}
	depends, err := stringArray(optional(m, "depends", []any{}), "depends")
	if err != nil {
		return nil, err
	}
	for _, dep := range depends {
		if _, err = name(dep, "depends"); err != nil {
			return nil, err
		}
		if !slices.Contains(d.Depends, dep) {
			d.Depends = append(d.Depends, dep)
		}
	}
	common, err := metadata(m, "module", nil)
	if err != nil {
		return nil, err
	}
	selectedMetadata := common
	if d.Source, d.Resources, err = sources(m, "module"); err != nil {
		return nil, err
	}
	if d.Cache, d.SourceSubdir, err = parseCache(optional(m, "cmake", map[string]any{}), "cmake", true); err != nil {
		return nil, err
	}
	if d.SourceSubdir != "" && d.Kind != "cmake" {
		return nil, fmt.Errorf("cmake.SOURCE_SUBDIR: requires type=cmake")
	}
	if d.Steps, err = parseSteps(optional(m, "steps", []any{}), filepath.Dir(path), "steps"); err != nil {
		return nil, err
	}
	platforms, err := table(optional(m, "platforms", map[string]any{}), "platforms", "windows", "linux")
	if err != nil {
		return nil, err
	}
	for _, platform := range sortedKeys(platforms) {
		at := "platforms." + platform
		options, err := table(platforms[platform], at, "cmake", "steps", "source", "resources", "architectures", "config_dir", "pkgconfig_dir", "package_name", "sentinel")
		if err != nil {
			return nil, err
		}
		meta, err := metadata(options, at, common)
		if err != nil {
			return nil, err
		}
		cache, _, err := parseCache(optional(options, "cmake", map[string]any{}), at+".cmake", false)
		if err != nil {
			return nil, err
		}
		steps, err := parseSteps(optional(options, "steps", []any{}), filepath.Dir(path), at+".steps")
		if err != nil {
			return nil, err
		}
		primary, resources, err := sources(options, at)
		if err != nil {
			return nil, err
		}
		selected := map[string]string{"Windows": "windows", "Linux": "linux"}[systemName] == platform
		merge := func(primary *sourceDefinition, resources map[string]sourceDefinition) {
			if primary != nil {
				d.Source = primary
			}
			for key, value := range resources {
				d.Resources[key] = value
			}
		}
		if selected {
			selectedMetadata = meta
			for key, value := range cache {
				d.Cache[key] = value
			}
			d.Steps = append(d.Steps, steps...)
			merge(primary, resources)
		}
		arches, err := table(optional(options, "architectures", map[string]any{}), at+".architectures", "x86_64", "aarch64")
		if err != nil {
			return nil, err
		}
		for _, arch := range sortedKeys(arches) {
			archAt := at + ".architectures." + arch
			inputs, err := table(arches[arch], archAt, "source", "resources")
			if err != nil {
				return nil, err
			}
			primary, resources, err := sources(inputs, archAt)
			if err != nil {
				return nil, err
			}
			if selected && normalizeArchitecture(architecture) == arch {
				merge(primary, resources)
			}
		}
	}
	d.ConfigDir = selectedMetadata["config_dir"]
	d.PkgconfigDir = selectedMetadata["pkgconfig_dir"]
	d.PackageName = selectedMetadata["package_name"]
	d.Sentinel = selectedMetadata["sentinel"]
	if d.Kind == "cmake" && d.Source == nil {
		return nil, fmt.Errorf("source: no primary source for %s/%s", systemName, architecture)
	}
	for _, key := range sortedKeys(d.Cache) {
		var texts []string
		switch value := d.Cache[key].(type) {
		case string:
			texts = []string{value}
		case []string:
			texts = value
		}
		for _, value := range texts {
			for _, match := range resourceReference.FindAllStringSubmatch(value, -1) {
				if _, ok := d.Resources[match[1]]; !ok {
					return nil, fmt.Errorf("cmake.cache.%s: unknown resource %q", key, match[1])
				}
			}
		}
	}
	names := map[string]bool{}
	for _, step := range d.Steps {
		if names[step.Name] {
			return nil, fmt.Errorf("steps.name: duplicate step names")
		}
		names[step.Name] = true
	}
	patches, ok := optional(m, "patches", []any{}).([]any)
	if !ok {
		return nil, fmt.Errorf("patches: expected an array of tables")
	}
	resource := func(m map[string]any, where string) (string, error) {
		v, exists := m["resource"]
		if !exists {
			return "", nil
		}
		s, ok := v.(string)
		if _, found := d.Resources[s]; !ok || !found {
			return "", fmt.Errorf("%s.resource: unknown resource %v", where, v)
		}
		return s, nil
	}
	for _, item := range patches {
		p, err := table(item, "patches", "file", "resource")
		if err != nil {
			return nil, err
		}
		r, err := resource(p, "patches")
		if err != nil {
			return nil, err
		}
		file, err := text(p["file"], "patches.file")
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(filepath.Dir(path), file)
		}
		stat, err := os.Stat(file)
		if err != nil || !stat.Mode().IsRegular() {
			return nil, fmt.Errorf("patches.file: file not found: %s", file)
		}
		d.Patches = append(d.Patches, patchDefinition{File: filepath.Clean(file), Resource: r})
	}
	copies, ok := optional(m, "copy", []any{}).([]any)
	if !ok || (d.Kind == "copy" && len(copies) == 0) {
		return nil, fmt.Errorf("copy: expected nonempty installation mappings for copy modules")
	}
	if d.Kind == "cmake" && len(copies) > 0 {
		return nil, fmt.Errorf("copy: installation mappings require type=copy")
	}
	for _, item := range copies {
		c, err := table(item, "copy", "resource", "files", "directories", "destination")
		if err != nil {
			return nil, err
		}
		r, err := resource(c, "copy")
		if err != nil {
			return nil, err
		}
		if r == "" && d.Source == nil {
			return nil, fmt.Errorf("copy: mapping requires a source or resource")
		}
		_, files := c["files"]
		_, dirs := c["directories"]
		if files == dirs {
			return nil, fmt.Errorf("copy: specify either files or directories")
		}
		key := "files"
		if dirs {
			key = "directories"
		}
		entries, err := stringArray(c[key], "copy."+key)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return nil, fmt.Errorf("copy.%s: expected at least one path", key)
		}
		for _, entry := range entries {
			if _, err := relative(entry, "copy."+key); err != nil {
				return nil, err
			}
		}
		destination, err := relative(c["destination"], "copy.destination")
		if err != nil {
			return nil, err
		}
		mapping := copyDefinition{Resource: r, Destination: destination}
		if files {
			mapping.Files = entries
		} else {
			mapping.Directories = entries
		}
		d.Copies = append(d.Copies, mapping)
	}
	return d, nil
}
