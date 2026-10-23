package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func computeBuildIdentities(o ciBuildInputs, description moduleDescription) (map[string]ciIdentity, error) {
	root, err := filepath.Abs(o.SourceRoot)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(description.SystemName, o.Context.OS) || normalizeCIArch(description.Architecture) != normalizeCIArch(o.Context.Arch) {
		return nil, fmt.Errorf("identity context %s/%s does not match description %s/%s", o.Context.OS, o.Context.Arch, description.SystemName, description.Architecture)
	}
	shared, err := ciSharedInputs(root, description)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]describedModule, len(description.Modules))
	for _, module := range description.Modules {
		if _, exists := byName[module.Name]; exists {
			return nil, fmt.Errorf("duplicate module description %q", module.Name)
		}
		byName[module.Name] = module
	}
	inputs := make(map[string]string, len(description.Modules))
	identities := make(map[string]ciIdentity, len(description.Modules))
	visiting := map[string]bool{}
	var build func(describedModule) error
	build = func(module describedModule) error {
		if _, ok := identities[module.Name]; ok {
			return nil
		}
		if visiting[module.Name] {
			return fmt.Errorf("module dependency cycle involving %s", module.Name)
		}
		visiting[module.Name] = true
		dependencyInputs := map[string]string{}
		for _, name := range module.Depends {
			dependency, ok := byName[name]
			if !ok {
				return fmt.Errorf("module %s is missing dependency description %s", module.Name, name)
			}
			if err := build(dependency); err != nil {
				return err
			}
			dependencyInputs[name] = inputs[name]
		}
		fileHashes, err := ciHashFiles(root, module.InputFiles)
		if err != nil {
			return err
		}
		config, err := normalizedJSON(module.Configuration)
		if err != nil {
			return fmt.Errorf("module %s configuration: %w", module.Name, err)
		}
		baseline, err := ciProductionBaseline(root, o.BaselineFile, o.Context, module.PlatformIndependent)
		if err != nil {
			return err
		}
		baseContext := map[string]any{
			"configuration":  o.Context.Configuration,
			"cmake_defaults": nonNilMap(o.Context.CMakeDefaults),
		}
		if !module.PlatformIndependent {
			baseContext["os"] = strings.ToLower(o.Context.OS)
			baseContext["arch"] = normalizeCIArch(o.Context.Arch)
			baseContext["generator"] = o.Context.Generator
			baseContext["platform"] = o.Context.Platform
			baseContext["toolset"] = o.Context.Toolset
			baseContext["runtime"] = o.Context.Runtime
			baseContext["multi_config"] = o.Context.MultiConfig
		}
		inputID, err := ciHash(map[string]any{
			"module": module.Name, "configuration": config, "input_files": fileHashes,
			"builder_inputs": shared, "baseline": baseline, "context": baseContext,
			"dependencies": dependencyInputs,
		})
		if err != nil {
			return err
		}
		inputs[module.Name] = inputID
		adapterName := module.AdapterFile
		if adapterName == "" {
			return fmt.Errorf("module %s has no adapter file", module.Name)
		}
		adapterHashes, err := ciHashFiles(root, []string{adapterName})
		if err != nil {
			return err
		}
		buildID, err := ciHash(map[string]any{"inputs": inputID, "adapter": adapterHashes[adapterName]})
		if err != nil {
			return err
		}
		identities[module.Name] = ciIdentity{BuildID: buildID, BuildInputsID: inputID}
		delete(visiting, module.Name)
		return nil
	}
	for _, module := range description.Modules {
		if err := build(module); err != nil {
			return nil, err
		}
	}
	return identities, nil
}

func ciSharedInputs(root string, description moduleDescription) (map[string]string, error) {
	paths := append(slices.Clone(description.GoModuleFiles), description.BuilderFiles...)
	paths = append(paths, "src/autolib-ci/identity.go")
	return ciHashFiles(root, paths)
}

func ciHashFiles(root string, paths []string) (map[string]string, error) {
	result := make(map[string]string, len(paths))
	for _, name := range paths {
		clean, data, err := ciReadSourceFile(root, name)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		result[clean] = hex.EncodeToString(digest[:])
	}
	return result, nil
}

func ciReadSourceFile(root, name string) (string, []byte, error) {
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") {
		return "", nil, fmt.Errorf("CI input path %q must be source-root relative POSIX path", name)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(name)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", nil, fmt.Errorf("CI input %q is outside the source root", name)
	}
	full := filepath.Join(root, filepath.FromSlash(clean))
	if err := rejectSymlinkComponents(root, clean); err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", nil, fmt.Errorf("read CI input %s: %w", clean, err)
	}
	return filepath.ToSlash(clean), data, nil
}

func rejectSymlinkComponents(root, relative string) error {
	current := root
	for _, component := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("read CI input %s: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("CI input %q traverses a symlink", relative)
		}
	}
	return nil
}

func ciProductionBaseline(root, filename string, context ciProductionContext, platformIndependent bool) (map[string]any, error) {
	_, data, err := ciReadSourceFile(root, filename)
	if err != nil {
		return nil, err
	}
	var document struct {
		Production map[string]json.RawMessage `json:"production"`
		Make       struct {
			URL    string `json:"url"`
			SHA256 string `json:"sha256"`
		} `json:"make"`
		CMake struct {
			ChecksumsURL string `json:"checksums_url"`
			Assets       []struct {
				Platform string `json:"platform"`
				URL      string `json:"url"`
				SHA256   string `json:"sha256"`
			} `json:"assets"`
		} `json:"cmake"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode production baseline %s: %w", filename, err)
	}
	commonRaw, ok := document.Production["common"]
	if !ok {
		return nil, fmt.Errorf("production baseline is missing production.common")
	}
	var common map[string]any
	if err := json.Unmarshal(commonRaw, &common); err != nil || common == nil {
		return nil, fmt.Errorf("production.common must be an object")
	}
	result := map[string]any{"common": common}
	if platformIndependent {
		return result, nil
	}
	platformName := strings.ToLower(context.OS)
	platformRaw, ok := document.Production[platformName]
	if !ok {
		return nil, fmt.Errorf("production baseline is missing production.%s", platformName)
	}
	var platform map[string]any
	if err := json.Unmarshal(platformRaw, &platform); err != nil || platform == nil {
		return nil, fmt.Errorf("production.%s must be an object", platformName)
	}
	result[platformName] = platform
	assetFound := false
	for _, asset := range document.CMake.Assets {
		if asset.Platform == strings.ToLower(context.OS)+"-"+normalizeCIArch(context.Arch) {
			result["cmake_asset"] = map[string]string{"url": asset.URL, "sha256": asset.SHA256}
			assetFound = true
			break
		}
	}
	if !assetFound {
		return nil, fmt.Errorf("production baseline has no CMake asset for %s-%s", strings.ToLower(context.OS), normalizeCIArch(context.Arch))
	}
	if document.CMake.ChecksumsURL == "" {
		return nil, fmt.Errorf("production baseline is missing cmake.checksums_url")
	}
	if strings.EqualFold(context.OS, "linux") {
		if document.Make.URL == "" || document.Make.SHA256 == "" {
			return nil, fmt.Errorf("production baseline is missing make asset URL or SHA256")
		}
		result["make_asset"] = map[string]string{"url": document.Make.URL, "sha256": document.Make.SHA256}
	}
	return result, nil
}

func ciHash(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func normalizedJSON(data json.RawMessage) (any, error) {
	var value any
	if len(data) == 0 || !json.Valid(data) {
		return nil, fmt.Errorf("missing or invalid JSON")
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func nonNilMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

func normalizeCIArch(value string) string {
	if value == "amd64" || value == "x64" {
		return "x86_64"
	}
	if value == "arm64" {
		return "aarch64"
	}
	return value
}
