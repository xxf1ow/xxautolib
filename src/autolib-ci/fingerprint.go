package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type ciFingerprintFailure struct {
	Error           string   `json:"error"`
	DependencyChain []string `json:"dependency_chain"`
}

type ciFingerprintReport struct {
	Fingerprints map[string]string               `json:"fingerprints"`
	Failures     map[string]ciFingerprintFailure `json:"failures"`
}

var errCIFingerprintBoundary = errors.New("fingerprint input escapes source root")

// File names are encoded together with their digests; JSON gives every field an unambiguous boundary.
func ciFingerprintValue(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return ciSHA256(data), nil
}

func ciFingerprintFiles(root string, paths []string) (map[string]string, error) {
	result := map[string]string{}
	active := map[string]bool{}
	var visit func(string) error
	visit = func(rel string) error {
		if err := validateDescriptionPaths([]string{rel}); err != nil {
			return fmt.Errorf("%w: %s", errCIFingerprintBoundary, rel)
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("fingerprint %s: %w", rel, err)
		}
		inside, err := filepath.Rel(root, resolved)
		if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
			return fmt.Errorf("%w: %s", errCIFingerprintBoundary, rel)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if active[resolved] {
				return fmt.Errorf("fingerprint directory cycle: %s", rel)
			}
			active[resolved] = true
			defer delete(active, resolved)
			result[rel+"/"] = "directory"
			entries, err := os.ReadDir(resolved)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := visit(filepath.ToSlash(filepath.Join(rel, entry.Name()))); err != nil {
					return err
				}
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("fingerprint input %s is not a regular file", rel)
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return err
		}
		result[rel] = ciSHA256(data)
		return nil
	}
	for _, rel := range paths {
		if err := visit(rel); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func ciProductionFiles(root, directory string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(directory)))
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && !strings.HasSuffix(name, "_test.go") && (strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".cmake")) {
			files = append(files, directory+"/"+name)
		}
	}
	return files, nil
}

func ciLocalGoInputs(root string, inputs []string) ([]string, error) {
	result := slices.Clone(inputs)
	seen := map[string]bool{}
	for i := 0; i < len(result); i++ {
		rel := result[i]
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		if _, err := ciFingerprintFiles(root, []string{rel}); err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, filepath.FromSlash(rel)), nil, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		for _, imp := range file.Imports {
			value, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return nil, err
			}
			if !strings.HasPrefix(value, "xxautolib/src/internal/") {
				continue
			}
			dir := strings.TrimPrefix(value, "xxautolib/")
			if seen[dir] {
				continue
			}
			seen[dir] = true
			files, err := ciProductionFiles(root, dir)
			if err != nil {
				return nil, err
			}
			result = append(result, files...)
		}
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}

func ciFingerprintContext(platform string, config ciEnvironmentConfig) (map[string]any, error) {
	context := map[string]any{"common": config.Production.Common}
	if platform == "any-any" {
		return context, nil
	}
	osName, arch, err := ciTarget(platform)
	if err != nil {
		return nil, err
	}
	context["platform"] = platform
	context["cmake_version"] = config.CMake.Version
	context["make"] = config.Make
	for _, asset := range config.CMake.Assets {
		if asset.Platform == platform {
			context["cmake_asset"] = asset
		}
	}
	switch osName {
	case "Linux":
		context["production"] = config.Production.Linux
		architecture := map[string]string{"x86_64": "linux/amd64", "aarch64": "linux/arm64/v8"}[arch]
		context["container"] = config.Ubuntu.PlatformDigests[architecture]
	case "Windows":
		context["production"] = config.Production.Windows
	case "Darwin":
		context["production"] = config.Production.Darwin
	}
	return context, nil
}

func ciModuleCacheKey(module describedModule) ciCacheKey {
	osName, arch, _ := strings.Cut(module.PackagePlatform, "-")
	return ciCacheKey{Kind: "library", Name: module.Name, Version: module.Version, OS: osName, Arch: arch}
}

func computeCIFingerprints(sourceRoot string, plan ciPlan) (ciFingerprintReport, error) {
	report := ciFingerprintReport{Fingerprints: map[string]string{}, Failures: map[string]ciFingerprintFailure{}}
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return report, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return report, err
	}
	config, err := loadCIEnvironment(filepath.Join(root, ".github/ci-tools.json"))
	if err != nil {
		return report, err
	}
	builderFiles, err := ciProductionFiles(root, "src/autolib-builder")
	if err != nil {
		return report, err
	}
	builderFiles = append(builderFiles, "go.mod", "go.sum", "CMakeLists.txt", "src/autolib-importer/fixup-metadata.cmake")
	for _, platform := range plan.Platforms {
		builderFiles = append(builderFiles, platform.Description.BuilderFiles...)
		builderFiles = append(builderFiles, platform.Description.GoModuleFiles...)
	}
	builderFiles, err = ciLocalGoInputs(root, builderFiles)
	if err != nil {
		return report, err
	}
	shared, err := ciFingerprintFiles(root, builderFiles)
	if err != nil {
		return report, err
	}
	importerFiles, err := ciProductionFiles(root, "src/autolib-importer")
	if err != nil {
		return report, err
	}
	importerFiles, err = ciLocalGoInputs(root, append(importerFiles, "src/autolib.cmake"))
	if err != nil {
		return report, err
	}
	toolInputs, err := ciFingerprintFiles(root, importerFiles)
	if err != nil {
		return report, err
	}
	for _, platform := range plan.Platforms {
		context, err := ciFingerprintContext(platform.ID, config)
		if err != nil {
			return report, err
		}
		osName, arch, _ := strings.Cut(platform.ID, "-")
		key := ciCacheKey{Kind: "importer", Name: "autolib-importer", OS: osName, Arch: arch}
		tool, err := ciFingerprintValue(struct {
			Key            ciCacheKey
			Shared, Inputs map[string]string
			Context        map[string]any
		}{key, shared, toolInputs, context})
		if err != nil {
			return report, err
		}
		report.Fingerprints[key.archiveName()] = tool
		modules := map[string]describedModule{}
		for _, module := range platform.Description.Modules {
			modules[moduleIdentity(module)] = module
		}
		if _, err := ciLayers(platform.Description.Modules); err != nil {
			return report, err
		}
		computed := map[string]bool{}
		var visit func(string) error
		visit = func(ref string) error {
			if computed[ref] {
				return nil
			}
			computed[ref] = true
			module := modules[ref]
			key := ciModuleCacheKey(module)
			name := key.archiveName()
			if err := key.validate(); err != nil {
				return err
			}
			deps := map[string]string{}
			for _, dep := range module.Depends {
				if err := visit(dep); err != nil {
					return err
				}
				depName := ciModuleCacheKey(modules[dep]).archiveName()
				if failed, ok := report.Failures[depName]; ok {
					report.Failures[name] = ciFingerprintFailure{failed.Error, append([]string{ref}, failed.DependencyChain...)}
					delete(report.Fingerprints, name)
					return nil
				}
				deps[depName] = report.Fingerprints[depName]
			}
			paths := []string{}
			for _, input := range module.InputFiles {
				if input != "src/dependencies/"+module.Name+"/package.toml" {
					paths = append(paths, input)
				}
			}
			inputs, err := ciFingerprintFiles(root, paths)
			if err != nil {
				if errors.Is(err, errCIFingerprintBoundary) {
					return err
				}
				report.Failures[name] = ciFingerprintFailure{err.Error(), []string{ref}}
				delete(report.Fingerprints, name)
				return nil
			}
			context, err := ciFingerprintContext(module.PackagePlatform, config)
			if err != nil {
				return err
			}
			fingerprint, err := ciFingerprintValue(struct {
				Key                          ciCacheKey
				Shared, Inputs, Dependencies map[string]string
				Context                      map[string]any
			}{key, shared, inputs, deps, context})
			if err != nil {
				return err
			}
			if previous, ok := report.Fingerprints[name]; ok && previous != fingerprint {
				return fmt.Errorf("cache identity %s has inconsistent inputs across platforms", name)
			}
			report.Fingerprints[name] = fingerprint
			return nil
		}
		for _, module := range platform.Description.Modules {
			if err := visit(moduleIdentity(module)); err != nil {
				return report, err
			}
		}
	}
	return report, nil
}
