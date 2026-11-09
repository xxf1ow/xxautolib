package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type ciDependencyResult struct {
	Ready       bool     `json:"ready"`
	Error       string   `json:"error"`
	Environment []string `json:"environment"`
}
type ciDependencyState map[string]ciDependencyResult

var ciSystemPackagePatterns = map[string]*regexp.Regexp{
	"apt":   regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`),
	"choco": regexp.MustCompile(`^[A-Za-z0-9.][A-Za-z0-9.-]*$`),
	"brew":  regexp.MustCompile(`^[a-z0-9][a-z0-9+_.@-]*$`),
}

func prepareCIItemDependencies(ctx context.Context, o ciRunOptions, module describedModule, phase string, state ciDependencyState) ([]string, error) {
	if phase != "build" && phase != "verify" {
		return nil, errors.New("dependency phase must be build or verify")
	}
	osName, _, err := ciTarget(o.Platform)
	if err != nil {
		return nil, err
	}
	manager := map[string]string{"Linux": "apt", "Windows": "choco", "Darwin": "brew"}[osName]
	if state == nil {
		return nil, errors.New("dependency preparation requires current run state")
	}
	var plan ciPlan
	if err := decodeStrictJSONFile(o.PlanFile, &plan); err != nil {
		return nil, err
	}
	modules := map[string]describedModule{}
	selected := 0
	for _, platform := range plan.Platforms {
		if platform.ID == o.Platform {
			selected++
			for _, m := range platform.Description.Modules {
				modules[moduleIdentity(m)] = m
			}
		}
	}
	if selected != 1 {
		return nil, errors.New("dependency preparation requires one selected platform")
	}
	packages := []string{}
	active := map[string]bool{}
	seen := map[string]bool{}
	var visit func(describedModule) error
	visit = func(m describedModule) error {
		ref := moduleIdentity(m)
		if active[ref] {
			return fmt.Errorf("dependency cycle at %s", ref)
		}
		if seen[ref] {
			return nil
		}
		active[ref] = true
		for _, dep := range m.Depends {
			child, ok := modules[dep]
			if !ok {
				return fmt.Errorf("missing dependency description %s", dep)
			}
			if err := visit(child); err != nil {
				return err
			}
		}
		delete(active, ref)
		seen[ref] = true
		packages = append(packages, m.SystemDependencies[manager]...)
		if manager == "apt" && phase == "verify" && m.Name == "pybind11" {
			packages = append(packages, "python3-dev")
		}
		return nil
	}
	if err := visit(module); err != nil {
		return nil, err
	}
	for _, name := range packages {
		if !ciSystemPackagePatterns[manager].MatchString(name) {
			return nil, fmt.Errorf("unsafe %s package name %q", manager, name)
		}
	}
	slices.Sort(packages)
	packages = slices.Compact(packages)
	environment := []string{}
	prefixes := map[string][]string{}
	for _, name := range packages {
		key := manager + ":" + name
		result, exists := state[key]
		if !exists {
			result.Environment, err = installCIItemDependency(ctx, o, manager, name)
			result.Ready = err == nil
			if err != nil {
				result.Error = err.Error()
			}
			state[key] = result
		}
		if !result.Ready {
			return nil, fmt.Errorf("system dependency %s is unavailable: %s", key, result.Error)
		}
		for _, value := range result.Environment {
			key, value, ok := strings.Cut(value, "=")
			if !ok {
				return nil, errors.New("invalid dependency environment")
			}
			prefixes[key] = append(prefixes[key], value)
		}
	}
	for _, key := range sortedKeysSliceMap(prefixes) {
		values := prefixes[key]
		if existing := os.Getenv(key); existing != "" {
			values = append(values, existing)
		}
		environment = append(environment, key+"="+strings.Join(values, ":"))
	}
	return environment, nil
}

func sortedKeysSliceMap(values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func installCIItemDependency(ctx context.Context, o ciRunOptions, manager, name string) ([]string, error) {
	commands := map[string][]string{"apt": {"apt-get", "install", "-y", "--no-install-recommends", name}, "choco": {"choco", "install", name, "--yes", "--no-progress", "--limit-output"}, "brew": {"brew", "install", name}}
	run := func(suffix string, command []string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		environment := withoutEnvironment(os.Environ(), "GITHUB_TOKEN", "GH_TOKEN", "GITEE_TOKEN")
		if manager == "brew" {
			environment = append(withoutEnvironment(environment, "HOMEBREW_NO_AUTO_UPDATE"), "HOMEBREW_NO_AUTO_UPDATE=1")
		}
		if manager == "apt" {
			environment = append(withoutEnvironment(environment, "DEBIAN_FRONTEND"), "DEBIAN_FRONTEND=noninteractive")
		}
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		cmd.Env = environment
		var out, diagnostic bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &diagnostic
		err := cmd.Run()
		log := filepath.Join(o.WorkRoot, "logs", "system-dependencies", manager+"-"+name+"-"+suffix+".log")
		if mkdirErr := os.MkdirAll(filepath.Dir(log), 0755); mkdirErr != nil {
			return "", mkdirErr
		}
		args, _ := json.Marshal(command)
		data := append(append(args, '\n'), out.Bytes()...)
		data = append(data, diagnostic.Bytes()...)
		if writeErr := os.WriteFile(log, data, 0644); writeErr != nil {
			return "", writeErr
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil {
			return "", fmt.Errorf("%s %s failed; see %s: %w", manager, name, log, err)
		}
		return strings.TrimSpace(out.String()), nil
	}
	if _, err := run("install", commands[manager]); err != nil {
		return nil, err
	}
	if manager == "apt" {
		_, err := run("version", []string{"dpkg-query", "-W", "-f=${binary:Package}=${Version}\n", name})
		return nil, err
	}
	if manager != "brew" {
		return []string{}, nil
	}
	if _, err := run("version", []string{"brew", "list", "--versions", name}); err != nil {
		return nil, err
	}
	prefix, err := run("prefix", []string{"brew", "--prefix", name})
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(prefix) || strings.ContainsAny(prefix, "\r\n:;") {
		return nil, fmt.Errorf("invalid brew formula prefix for %s", name)
	}
	return []string{"CMAKE_PREFIX_PATH=" + prefix, "PKG_CONFIG_PATH=" + filepath.Join(prefix, "lib/pkgconfig") + ":" + filepath.Join(prefix, "share/pkgconfig")}, nil
}
