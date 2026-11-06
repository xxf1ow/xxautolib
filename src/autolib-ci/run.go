package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

func runCILayer(ctx context.Context, o ciRunOptions) (failure error) {
	index := -1
	if o.Layer != "prepare" && o.Layer != "verify" {
		value, ok := strings.CutPrefix(o.Layer, "layer")
		var err error
		index, err = strconv.Atoi(value)
		if !ok || err != nil || index < 0 || strconv.Itoa(index) != value {
			return errors.New("CI layer must be prepare, layer<N> or verify")
		}
	}
	for name, value := range map[string]string{"importer file": o.ImporterFile, "CMake command": o.CMakeCommand} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("CI run %s is required", name)
		}
	}
	_, run, platform, source, work, err := loadCIWork(ctx, o)
	if err != nil {
		return err
	}
	environment, err := loadCIEnvironment(filepath.Join(source, ".github", "ci-tools.json"))
	if err != nil {
		return err
	}
	production, err := ciProductionForPlatform(o.Platform, environment)
	if err != nil {
		return err
	}
	if production.OS == "darwin" && o.Budget == 0 {
		output, err := exec.CommandContext(ctx, "sysctl", "-n", "hw.logicalcpu").Output()
		if err != nil {
			return fmt.Errorf("query Darwin CPU budget: %w", err)
		}
		o.Budget, err = strconv.Atoi(strings.TrimSpace(string(output)))
		if err != nil {
			return fmt.Errorf("parse Darwin CPU budget: %w", err)
		}
		if o.Budget < 1 {
			o.Budget = 1
		}
	}
	if err := validateCIProductionIdentity(production, environment); err != nil {
		return err
	}
	resultFile := filepath.Join(work, "results", o.Platform+".json")
	result := ciPlatformResult{Run: run, Platform: o.Platform, Packages: []ciSlotResult{}, Roots: []ciRootResult{}}
	if o.Layer != "prepare" {
		if err := decodeStrictJSONFile(resultFile, &result); err != nil {
			return fmt.Errorf("read current platform result: %w", err)
		}
		if result.Run != run || result.Platform != o.Platform || result.Error != "" || result.Complete || !result.Importer.Verified || result.Importer.File != ciImporterExecutable(o.Platform) {
			return errors.New("platform result does not describe current prepared work")
		}
		seen := map[string]bool{}
		for _, item := range result.Packages {
			key := item.Slot.Name + "@" + item.Slot.Version
			if seen[key] || !item.Built {
				return errors.New("package result does not describe a current build")
			}
			seen[key] = true
		}
	} else if _, err := os.Lstat(resultFile); !os.IsNotExist(err) {
		return errors.New("prepare requires a fresh run work root")
	}
	defer func() {
		if failure != nil {
			result.Error = failure.Error()
			result.Complete = false
		}
		failure = errors.Join(failure, writeCIJSON(resultFile, result))
	}()
	install := filepath.Join(work, "install")
	if o.Layer == "prepare" {
		if err := os.MkdirAll(work, 0755); err != nil {
			return err
		}
		if err := os.Mkdir(install, 0755); err != nil {
			return fmt.Errorf("prepare requires a fresh isolated installation: %w", err)
		}
		if err := configureCIProduction(ctx, o, production, platform.Roots, install, filepath.Join(work, "logs", "prepare")); err != nil {
			return err
		}
		result.Entry, err = verifyCIEntry(source, install)
		if err != nil {
			return err
		}
		expectedImporter, err := filepath.Abs(o.ImporterFile)
		if err != nil {
			return err
		}
		if expectedImporter != filepath.Join(install, ciImporterExecutable(o.Platform)) {
			return errors.New("importer must be the current isolated installation's host tool")
		}
		if err := verifyImporterNoCRuntime(o.ImporterFile); err != nil {
			return err
		}
		smoke := filepath.Join(work, "importer-smoke")
		if err := os.Mkdir(smoke, 0755); err != nil {
			return err
		}
		command := exec.CommandContext(ctx, o.ImporterFile, "prepare", "--cache-root", smoke, "--os", production.OS, "--arch", production.Arch, "--result", filepath.Join(smoke, "result.cmake"), "--package=ci-smoke")
		output, err := command.CombinedOutput()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(output), "missing package input") {
			return fmt.Errorf("importer smoke did not reject controlled missing package: %v\n%s", err, output)
		}
		if (production.OS == "linux" || production.OS == "darwin") && len(platform.Roots) > 0 {
			evidence, err := probeJobserver(ctx, ciJobserverOptions{SourceRoot: source, WorkRoot: filepath.Join(work, "jobserver"), MakeProgram: o.MakeProgram, CMakeCommand: o.CMakeCommand, Budget: o.Budget})
			if err != nil {
				return err
			}
			if err := writeCIJSON(filepath.Join(work, "jobserver.json"), evidence); err != nil {
				return err
			}
			fmt.Printf("Make FIFO budget=%d peak=%d overlap=%t\n", evidence.Budget, evidence.Peak, evidence.Overlap)
		}
		result.Importer = ciImporterResult{File: ciImporterExecutable(o.Platform), Verified: true}
		return nil
	}
	modules := map[string]describedModule{}
	for _, module := range platform.Description.Modules {
		modules[moduleIdentity(module)] = module
	}
	findResult := func(key string) *ciSlotResult {
		for i := range result.Packages {
			if result.Packages[i].Slot.Name+"@"+result.Packages[i].Slot.Version == key {
				return &result.Packages[i]
			}
		}
		return nil
	}
	if o.Layer == "verify" {
		entry, err := verifyCIEntry(source, install)
		if err != nil {
			return err
		}
		if entry != result.Entry {
			return errors.New("entry does not match verified prepare result")
		}
		if err := verifyImporterNoCRuntime(o.ImporterFile); err != nil {
			return err
		}
		if len(result.Packages) != len(modules) {
			return errors.New("verify requires all selected package results")
		}
		for _, module := range platform.Description.Modules {
			item := findResult(moduleIdentity(module))
			if item == nil || item.Slot != ciModuleSlot(module, o.Platform) || !item.Built {
				return fmt.Errorf("missing exact built package %s", moduleIdentity(module))
			}
		}
		for _, root := range platform.Roots {
			closure := []string{}
			seen := map[string]bool{}
			var visit func(string) error
			visit = func(key string) error {
				if seen[key] {
					return nil
				}
				module, ok := modules[key]
				if !ok {
					return fmt.Errorf("unknown root dependency %s", key)
				}
				seen[key] = true
				for _, dependency := range module.Depends {
					if err := visit(dependency); err != nil {
						return err
					}
				}
				closure = append(closure, key)
				return nil
			}
			if err := visit(root); err != nil {
				return err
			}
			evidence, err := validateCIRelease(ctx, ciValidationOptions{SourceRoot: source, InstallRoot: install, BuildRoot: work, CMakeCommand: o.CMakeCommand, EvidenceDirectory: filepath.Join(work, "evidence", root), Modules: closure, RootPackages: []string{root}, Context: production, MakeProgram: o.MakeProgram})
			if err != nil {
				return err
			}
			result.Roots = append(result.Roots, ciRootResult{Root: root, Verified: true, EvidenceDirectory: evidence.EvidenceDirectory})
		}
		for i := range result.Packages {
			result.Packages[i].Verified = true
		}
		result.Complete = true
		return nil
	}
	if index >= len(platform.Layers) || len(platform.Layers[index]) == 0 {
		return errors.New("requested dependency layer is empty")
	}
	for previous := 0; previous < index; previous++ {
		for _, key := range platform.Layers[previous] {
			if findResult(key) == nil {
				return fmt.Errorf("previous layer package %s is not ready", key)
			}
		}
	}
	for position, key := range platform.Layers[index] {
		if findResult(key) != nil {
			return fmt.Errorf("package %s was already processed", key)
		}
		module, ok := modules[key]
		if !ok {
			return fmt.Errorf("layer contains unselected module %s", key)
		}
		for _, dependency := range module.Depends {
			if findResult(dependency) == nil {
				return fmt.Errorf("dependency %s is not built", dependency)
			}
		}
		logs := filepath.Join(work, "logs", o.Layer, strconv.Itoa(position+1))
		if err := buildCIModule(ctx, o, production, module, install, logs); err != nil {
			return err
		}
		result.Packages = append(result.Packages, ciSlotResult{Slot: ciModuleSlot(module, o.Platform), Built: true})
		if err := writeCIJSON(resultFile, result); err != nil {
			return err
		}
	}
	return nil
}

func ciRunNumber(value string) bool {
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == value
}

func ciProductionForPlatform(platform string, config ciEnvironmentConfig) (ciProductionContext, error) {
	osName, arch, err := ciTarget(platform)
	if err != nil {
		return ciProductionContext{}, err
	}
	c := ciProductionContext{OS: strings.ToLower(osName), Arch: arch, Configuration: config.Production.Common.Configuration, CMakeDefaults: maps.Clone(config.Production.Common.CMakeDefaults)}
	if c.OS == "windows" {
		w := config.Production.Windows
		c.Generator, c.Platform, c.Toolset, c.Runtime, c.MultiConfig = w.Generator, w.Platform, w.Toolset, w.Runtime, true
	} else if c.OS == "darwin" {
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			return ciProductionContext{}, fmt.Errorf("Darwin CI requires native darwin-arm64 host")
		}
		c.Generator = config.Production.Darwin.Generator
		c.CMakeDefaults["CMAKE_OSX_ARCHITECTURES"] = config.Production.Darwin.Architectures
		c.CMakeDefaults["CMAKE_OSX_DEPLOYMENT_TARGET"] = config.Production.Darwin.DeploymentTarget
		for key, args := range map[string][]string{"CMAKE_C_COMPILER": {"--find", "clang"}, "CMAKE_CXX_COMPILER": {"--find", "clang++"}, "CMAKE_OSX_SYSROOT": {"--sdk", "macosx", "--show-sdk-path"}} {
			output, err := exec.Command("xcrun", args...).Output()
			if err != nil {
				return ciProductionContext{}, fmt.Errorf("resolve Darwin %s: %w", key, err)
			}
			value := strings.TrimSpace(string(output))
			if !filepath.IsAbs(value) {
				return ciProductionContext{}, fmt.Errorf("Darwin %s must be absolute", key)
			}
			c.CMakeDefaults[key] = value
		}
	} else {
		c.Generator = config.Production.Linux.Generator
	}
	return c, nil
}

func ciCommand(ctx context.Context, executable string, args []string, directory string, environment []string, log string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := os.MkdirAll(filepath.Dir(log), 0755); err != nil {
		return err
	}
	file, err := os.Create(log)
	if err != nil {
		return err
	}
	defer file.Close()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = directory
	if environment != nil {
		command.Env = environment
	}
	encoded, _ := json.Marshal(append([]string{executable}, args...))
	fmt.Println(string(encoded))
	fmt.Fprintln(file, string(encoded))
	command.Stdout = io.MultiWriter(os.Stdout, file)
	command.Stderr = io.MultiWriter(os.Stderr, file)
	if err := command.Run(); err != nil {
		return fmt.Errorf("command failed; see %s: %w", log, err)
	}
	return nil
}

func configureCIProduction(ctx context.Context, o ciRunOptions, c ciProductionContext, modules []string, install, logs string) error {
	if len(modules) == 0 {
		args := []string{"-DAUTOLIB_SOURCE_ROOT=" + o.SourceRoot, "-DAUTOLIB_INSTALL_ROOT=" + install, "-P", filepath.Join(o.SourceRoot, "src", "autolib-builder", "prepare-tools.cmake")}
		return ciCommand(ctx, o.CMakeCommand, args, o.SourceRoot, nil, filepath.Join(logs, "configure.log"))
	}
	build := filepath.Join(o.WorkRoot, "build")
	args := []string{"-S", o.SourceRoot, "-B", build, "-G", c.Generator, "-DBUILD_LIST=" + strings.Join(modules, ","), "-DCMAKE_BUILD_TYPE=" + c.Configuration}
	if c.OS == "windows" {
		args = append(args, "-A", c.Platform, "-T", c.Toolset, "-DCMAKE_MSVC_RUNTIME_LIBRARY="+c.Runtime)
	} else {
		if o.Budget < 1 || o.MakeProgram == "" {
			return errors.New("Unix Makefiles build requires Make program and positive budget")
		}
		args = append(args, "-DCMAKE_MAKE_PROGRAM="+o.MakeProgram)
	}
	for _, key := range sortedKeysStringMap(c.CMakeDefaults) {
		args = append(args, "-D"+key+"="+c.CMakeDefaults[key])
	}
	environment := withoutEnvironment(os.Environ(), "AUTOLIB_INSTALL_ROOT", "MAKEFLAGS", "CMAKE_BUILD_PARALLEL_LEVEL")
	environment = append(environment, "AUTOLIB_INSTALL_ROOT="+install)
	if c.OS == "linux" || c.OS == "darwin" {
		environment = prependPath(environment, filepath.Dir(o.MakeProgram))
	}
	return ciCommand(ctx, o.CMakeCommand, args, o.SourceRoot, environment, filepath.Join(logs, "configure.log"))
}

func buildCIModule(ctx context.Context, o ciRunOptions, c ciProductionContext, module describedModule, install, logs string) error {
	name := moduleIdentity(module)
	if err := configureCIProduction(ctx, o, c, []string{name}, install, logs); err != nil {
		return err
	}
	environment := withoutEnvironment(os.Environ(), "AUTOLIB_INSTALL_ROOT", "MAKEFLAGS", "CMAKE_BUILD_PARALLEL_LEVEL")
	environment = append(environment, "AUTOLIB_INSTALL_ROOT="+install)
	if c.OS == "linux" || c.OS == "darwin" {
		environment = prependPath(environment, filepath.Dir(o.MakeProgram))
	}
	args := []string{"--build", filepath.Join(o.WorkRoot, "build"), "--config", c.Configuration, "--target", module.Target, "--verbose"}
	if c.OS == "linux" || c.OS == "darwin" {
		args = append(args, "--", fmt.Sprintf("-j%d", o.Budget), "--jobserver-style=fifo")
	}
	return ciCommand(ctx, o.CMakeCommand, args, o.SourceRoot, environment, filepath.Join(logs, "build.log"))
}

func sortedKeysStringMap(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func runCIRun(args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var o ciRunOptions
	for _, item := range []struct {
		key   string
		value *string
	}{{"repository", &o.Repository}, {"source-root", &o.SourceRoot}, {"plan", &o.PlanFile}, {"run-identity", &o.RunIdentityFile}, {"platform", &o.Platform}, {"layer", &o.Layer}, {"work-root", &o.WorkRoot}, {"importer-file", &o.ImporterFile}, {"cmake-command", &o.CMakeCommand}, {"make-program", &o.MakeProgram}} {
		flags.StringVar(item.value, item.key, "", item.key)
	}
	flags.IntVar(&o.Budget, "budget", 0, "Native Make worker budget")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if o.PlanFile == "" || flags.NArg() != 0 {
		return fail(errors.New("run requires --plan and explicit run/tool paths without positional arguments"))
	}
	if err := runCILayer(context.Background(), o); err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "%s %s passed\n", o.Platform, o.Layer)
	return 0
}
