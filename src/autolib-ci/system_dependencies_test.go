package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func init() {
	root := os.Getenv("AUTOLIB_CI_DEP_HELPER")
	if root == "" {
		return
	}
	if os.Getenv("GITHUB_TOKEN") != "" || os.Getenv("GITEE_TOKEN") != "" {
		os.Exit(90)
	}
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	args := os.Args[1:]
	f, err := os.OpenFile(filepath.Join(root, "events"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		os.Exit(91)
	}
	json.NewEncoder(f).Encode(append([]string{name}, args...))
	f.Close()
	if len(args) > 1 && args[0] == "--prefix" {
		fmt.Println(filepath.Join(root, "prefix", args[1]))
		os.Exit(0)
	}
	for _, arg := range args {
		if arg == "bad" {
			fmt.Fprintln(os.Stderr, "controlled dependency failure")
			os.Exit(72)
		}
	}
	fmt.Println("controlled installed package version=1")
	os.Exit(0)
}

func TestCISystemDependencyIsolation(t *testing.T) {
	for _, platform := range []string{"linux-x86_64", "windows-x86_64", "darwin-aarch64"} {
		t.Run(platform, func(t *testing.T) {
			if runtime.GOOS == "windows" && platform == "darwin-aarch64" {
				t.Skip("brew prefix validation requires Unix paths")
			}
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0755); err != nil {
				t.Fatal(err)
			}
			binary, err := os.ReadFile(testExecutable(t))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"apt-get", "dpkg-query", "choco", "brew"} {
				if err := os.WriteFile(filepath.Join(bin, name+executableSuffix()), binary, 0755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("AUTOLIB_CI_DEP_HELPER", root)
			t.Setenv("GITHUB_TOKEN", "must-not-reach-installer")
			t.Setenv("GITEE_TOKEN", "must-not-reach-installer")
			manager := map[string]string{"linux-x86_64": "apt", "windows-x86_64": "choco", "darwin-aarch64": "brew"}[platform]
			b := describedModule{Name: "b", Version: "1", Depends: []string{}, SystemDependencies: map[string][]string{manager: {"bad"}}}
			a := describedModule{Name: "a", Version: "1", Depends: []string{}, SystemDependencies: map[string][]string{manager: {"good"}}}
			c := describedModule{Name: "c", Version: "1", Depends: []string{"a@1"}, SystemDependencies: map[string][]string{}}
			d := describedModule{Name: "d", Version: "1", Depends: []string{}, SystemDependencies: map[string][]string{}}
			binding := describedModule{Name: "pybind11", Version: "1", Depends: []string{}, SystemDependencies: map[string][]string{}}
			modules := map[string]describedModule{}
			for _, module := range []describedModule{a, b, c, d, binding} {
				modules[moduleIdentity(module)] = module
			}
			o := ciRunOptions{Platform: platform, WorkRoot: filepath.Join(root, "work")}
			state := ciDependencyState{}
			prepare := func(module describedModule, phase string) ([]string, error) {
				closure, err := ciModuleClosure(moduleIdentity(module), modules)
				if err != nil {
					return nil, err
				}
				return prepareCIItemDependencies(context.Background(), o, closure, modules, phase, state)
			}
			for range 2 {
				if _, err := prepare(b, "build"); err == nil {
					t.Fatal("dependency failure swallowed")
				}
			}
			if _, err := prepare(d, "build"); err != nil {
				t.Fatal("unrelated item blocked", err)
			}
			for range 2 {
				if _, err := prepare(a, "build"); err != nil {
					t.Fatal(err)
				}
			}
			environment, err := prepare(c, "verify")
			if err != nil {
				t.Fatal(err)
			}
			if manager == "brew" {
				text := strings.Join(environment, "\n")
				if !strings.Contains(text, filepath.Join(root, "prefix/good")) || strings.Contains(text, "bad") {
					t.Fatalf("wrong closure environment: %v", environment)
				}
			} else if len(environment) != 0 {
				t.Fatal("unexpected environment overrides")
			}
			clean, err := prepare(d, "verify")
			if err != nil || len(clean) != 0 {
				t.Fatal("formula environment leaked to independent item")
			}
			if _, err := prepare(binding, "build"); err != nil {
				t.Fatal(err)
			}
			before := mustReadText(t, filepath.Join(root, "events"))
			if strings.Contains(before, "python3-dev") {
				t.Fatal("Python headers installed during build")
			}
			if _, err := prepare(binding, "verify"); err != nil {
				t.Fatal(err)
			}
			events := mustReadText(t, filepath.Join(root, "events"))
			installs := map[string]int{}
			for _, line := range strings.Split(strings.TrimSpace(events), "\n") {
				var args []string
				if err := json.Unmarshal([]byte(line), &args); err != nil {
					t.Fatal(err)
				}
				if len(args) > 1 && args[1] == "install" {
					for _, arg := range args[2:] {
						installs[arg]++
					}
				}
			}
			if installs["good"] != 1 || installs["bad"] != 1 {
				t.Fatalf("shared dependency not memoized: %v", installs)
			}
			if (installs["python3-dev"] == 1) != (manager == "apt") {
				t.Fatal("Python verify prerequisite applied to wrong platform")
			}
			for _, invalid := range []string{"--bad", "bad;echo", "tap/formula", ""} {
				a.SystemDependencies[manager] = []string{"new-good", invalid}
				if _, err := prepare(a, "build"); err == nil {
					t.Fatal("invalid name accepted")
				}
			}
			if mustReadText(t, filepath.Join(root, "events")) != events {
				t.Fatal("invalid declaration started a manager")
			}
			if _, err := prepare(a, "unknown"); err == nil {
				t.Fatal("unknown phase accepted")
			}
		})
	}
}
