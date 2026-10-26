package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func runCILayer(ctx context.Context, o ciRunOptions) error {
	return runCILayerWithClient(ctx, o, productionGitHubClient(os.Getenv("GITHUB_TOKEN")))
}

func runCILayerWithClient(ctx context.Context, o ciRunOptions, client *githubClient) (failure error) {
	if !slices.Contains([]string{"prepare", "layer0", "layer1", "verify"}, o.Layer) {
		return errors.New("CI layer must be prepare, layer0, layer1 or verify")
	}
	for name, value := range map[string]string{"source root": o.SourceRoot, "plan": o.PlanFile, "run identity": o.RunIdentityFile, "platform": o.Platform, "work root": o.WorkRoot, "builder file": o.BuilderFile, "importer file": o.ImporterFile, "CMake command": o.CMakeCommand} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("CI run %s is required", name)
		}
	}
	var plan ciPlan
	var run ciRunIdentity
	if err := decodeStrictJSONFile(o.PlanFile, &plan); err != nil {
		return err
	}
	if err := decodeStrictJSONFile(o.RunIdentityFile, &run); err != nil {
		return err
	}
	if !ciRunNumber(run.RunID) || !ciRunNumber(run.Attempt) {
		return errors.New("run ID and attempt must be positive integers")
	}
	auth, err := authorizeCIWrite(ctx, client, o.SourceRoot, run)
	if err != nil {
		return err
	}
	source, err := filepath.Abs(o.SourceRoot)
	if err != nil {
		return err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	work, err := resolveExistingPrefix(o.WorkRoot)
	if err != nil {
		return err
	}
	expected := filepath.Join(source, "build", "ci", run.RunID+"-"+run.Attempt, o.Platform)
	if work != expected {
		return fmt.Errorf("CI work root must be %s", expected)
	}
	var platform *ciPlatformPlan
	for i := range plan.Platforms {
		if plan.Platforms[i].ID == o.Platform {
			if platform != nil {
				return errors.New("duplicate selected platform")
			}
			platform = &plan.Platforms[i]
		}
	}
	if platform == nil {
		return fmt.Errorf("platform %s is not selected", o.Platform)
	}
	environment, err := loadCIEnvironment(filepath.Join(source, ".github", "ci-tools.json"))
	if err != nil {
		return err
	}
	production, err := ciProductionForPlatform(o.Platform, environment)
	if err != nil {
		return err
	}
	if err := validateCIProductionIdentity(production, environment); err != nil {
		return err
	}
	resultFile := filepath.Join(work, "results", o.Platform+".json")
	result := ciPlatformResult{Run: run, Platform: o.Platform, Packages: []ciSlotResult{}}
	if o.Layer != "prepare" {
		if err := decodeStrictJSONFile(resultFile, &result); err != nil {
			return fmt.Errorf("read current platform result: %w", err)
		}
		if result.Run != run || result.Platform != o.Platform || result.Error != "" || result.Complete || !result.Importer.Verified || !result.Importer.Uploaded {
			return errors.New("platform result does not describe current prepared work")
		}
		for _, item := range result.Packages {
			if !item.Built || !item.Verified {
				return errors.New("package result does not describe a current verified build")
			}
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
	releaseRoot := filepath.Join(work, "release")
	if err := os.MkdirAll(releaseRoot, 0755); err != nil {
		return err
	}
	if o.Layer == "prepare" {
		if err := os.Mkdir(install, 0755); err != nil {
			return fmt.Errorf("prepare requires a fresh isolated installation: %w", err)
		}
		selected := make([]string, 0, len(platform.Description.Modules))
		for _, module := range platform.Description.Modules {
			selected = append(selected, module.Name)
		}
		if err := configureCIProduction(ctx, o, production, selected, install); err != nil {
			return err
		}
		if err := verifyImporterNoCRuntime(o.ImporterFile); err != nil {
			return err
		}
		// A controlled missing package exercises the executable without producing another package format.
		smoke := filepath.Join(work, "importer-smoke")
		if err := os.Mkdir(smoke, 0755); err != nil {
			return err
		}
		command := exec.CommandContext(ctx, o.ImporterFile, "prepare", "--local-root", smoke, "--work-root", filepath.Join(smoke, "work"), "--os", production.OS, "--arch", production.Arch, "--result", filepath.Join(smoke, "result.cmake"), "--package=ci-smoke")
		output, err := command.CombinedOutput()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(output), "ci-smoke") {
			return fmt.Errorf("importer smoke did not reject the controlled missing package: %v\n%s", err, output)
		}
		if production.OS == "linux" {
			evidence, err := probeJobserver(ctx, ciJobserverOptions{SourceRoot: source, WorkRoot: filepath.Join(work, "jobserver"), MakeProgram: o.MakeProgram, Budget: o.Budget})
			if err != nil {
				return err
			}
			if err := writeCIJSON(filepath.Join(work, "jobserver.json"), evidence); err != nil {
				return err
			}
			fmt.Printf("Make FIFO budget=%d peak=%d overlap=%t\n", evidence.Budget, evidence.Peak, evidence.Overlap)
		}
		tool := filepath.Join(releaseRoot, ciImporterAsset(o.Platform))
		if err := copyCIFile(o.ImporterFile, tool); err != nil {
			return err
		}
		if err := os.Chmod(tool, 0755); err != nil {
			return err
		}
		digest, err := ciFileSHA256(tool)
		if err != nil {
			return err
		}
		result.Importer = ciImporterResult{Asset: filepath.Base(tool), SHA256: digest, Verified: true}
		if err := writeCIJSON(resultFile, result); err != nil {
			return err
		}
		release, err := existingCIRelease(ctx, client)
		if err != nil {
			return err
		}
		if _, err := uploadCIAsset(ctx, client, auth, release, ciUploadOptions{File: tool, Name: filepath.Base(tool), SHA256: digest}); err != nil {
			return err
		}
		result.Importer.Uploaded = true
		return nil
	}
	findResult := func(name string) *ciSlotResult {
		for i := range result.Packages {
			if result.Packages[i].Slot.Name == name {
				return &result.Packages[i]
			}
		}
		return nil
	}
	modules := map[string]describedModule{}
	for _, module := range platform.Description.Modules {
		modules[module.Name] = module
	}
	if o.Layer == "verify" {
		if len(result.Packages) != len(modules) {
			return errors.New("verify requires all selected package results")
		}
		if len(modules) == 3 && modules["queue"].Name != "" && modules["lz4"].Name != "" && modules["flann"].Name != "" {
			if _, err := validateCIRelease(ctx, ciValidationOptions{SourceRoot: source, InstallRoot: install, BuildRoot: work, ReleaseRoot: releaseRoot, ImporterFile: o.ImporterFile, CMakeCommand: o.CMakeCommand, EvidenceDirectory: filepath.Join(work, "evidence", "relocate"), Modules: []string{"queue", "lz4", "flann"}, Context: production, Mode: "relocate"}); err != nil {
				return err
			}
		}
		result.Complete = true
		return nil
	}
	index := 0
	if o.Layer == "layer1" {
		index = 1
	}
	if index >= len(platform.Layers) || len(platform.Layers[index]) == 0 {
		return errors.New("requested dependency layer is empty")
	}
	for previous := 0; previous < index; previous++ {
		for _, name := range platform.Layers[previous] {
			if findResult(name) == nil {
				return fmt.Errorf("previous layer package %s is not ready", name)
			}
		}
	}
	for _, name := range platform.Layers[index] {
		if findResult(name) != nil {
			return fmt.Errorf("package %s was already processed", name)
		}
		module, ok := modules[name]
		if !ok {
			return fmt.Errorf("layer contains unselected module %s", name)
		}
		slot := ciModuleSlot(module, o.Platform)
		item := ciSlotResult{Slot: slot, Asset: ciPackageAssetName(slot)}
		consumerModules := []string{}
		for _, dependency := range module.Depends {
			ready := findResult(dependency)
			if ready == nil || !ready.Verified {
				return fmt.Errorf("dependency %s is not verified", dependency)
			}
			consumerModules = append(consumerModules, dependency)
		}
		consumerModules = append(consumerModules, name)
		archive := filepath.Join(releaseRoot, item.Asset)
		if err := buildCIModule(ctx, o, production, name, install); err != nil {
			return err
		}
		packageRoot, err := os.MkdirTemp(work, "package-"+name+"-")
		if err != nil {
			return err
		}
		args := []string{"package", "--source-root", source, "--install-root", install, "--output-root", packageRoot, "--os", production.OS, "--arch", production.Arch, "--host-os", production.OS, "--host-arch", production.Arch, "--importer-file", o.ImporterFile, "--cmake-command", o.CMakeCommand}
		args = append(args, "--module", name)
		if err := ciCommand(ctx, o.BuilderFile, args, source, nil, filepath.Join(work, "logs", "package-"+name+".log")); err != nil {
			return err
		}
		if err := copyCIFile(filepath.Join(packageRoot, item.Asset), archive); err != nil {
			return err
		}
		item.Built = true
		digest, err := ciFileSHA256(archive)
		if err != nil {
			return err
		}
		item.SHA256 = digest
		if _, err := validateCIRelease(ctx, ciValidationOptions{SourceRoot: source, InstallRoot: install, BuildRoot: work, ReleaseRoot: releaseRoot, ImporterFile: o.ImporterFile, CMakeCommand: o.CMakeCommand, EvidenceDirectory: filepath.Join(work, "evidence", name), Modules: consumerModules, RootPackages: []string{name}, Context: production, Mode: "single"}); err != nil {
			return err
		}
		item.Verified = true
		result.Packages = append(result.Packages, item)
		if err := writeCIJSON(resultFile, result); err != nil {
			return err
		}
		if !module.PlatformIndependent || plan.Publisher == o.Platform {
			release, err := existingCIRelease(ctx, client)
			if err != nil {
				return err
			}
			if _, err := uploadCIAsset(ctx, client, auth, release, ciUploadOptions{File: archive, Name: item.Asset, SHA256: digest}); err != nil {
				return err
			}
			result.Packages[len(result.Packages)-1].Uploaded = true
		}
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
	c := ciProductionContext{OS: strings.ToLower(osName), Arch: arch, Configuration: config.Production.Common.Configuration, CMakeDefaults: config.Production.Common.CMakeDefaults}
	if c.OS == "windows" {
		w := config.Production.Windows
		c.Generator, c.Platform, c.Toolset, c.Runtime, c.MultiConfig = w.Generator, w.Platform, w.Toolset, w.Runtime, true
	} else {
		c.Generator = config.Production.Linux.Generator
	}
	return c, nil
}

func existingCIRelease(ctx context.Context, client *githubClient) (githubRelease, error) {
	release, found, err := client.release(ctx)
	if err != nil {
		return githubRelease{}, err
	}
	if !found {
		return githubRelease{}, errors.New("fixed Release is missing; run init before platform work")
	}
	return release, nil
}

func ciFileSHA256(file string) (string, error) {
	input, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer input.Close()
	return hashReader(input)
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

func configureCIProduction(ctx context.Context, o ciRunOptions, c ciProductionContext, modules []string, install string) error {
	build := filepath.Join(o.WorkRoot, "build")
	args := []string{"-S", o.SourceRoot, "-B", build, "-G", c.Generator, "-DBUILD_LIST=" + strings.Join(modules, ","), "-DCMAKE_BUILD_TYPE=" + c.Configuration}
	if c.OS == "windows" {
		args = append(args, "-A", c.Platform, "-T", c.Toolset, "-DCMAKE_MSVC_RUNTIME_LIBRARY="+c.Runtime)
	} else {
		if o.Budget < 1 || o.MakeProgram == "" {
			return errors.New("Linux build requires Make program and positive budget")
		}
		args = append(args, "-DCMAKE_MAKE_PROGRAM="+o.MakeProgram)
	}
	for _, key := range sortedKeysStringMap(c.CMakeDefaults) {
		args = append(args, "-D"+key+"="+c.CMakeDefaults[key])
	}
	environment := withoutEnvironment(os.Environ(), "AUTOLIB_INSTALL_ROOT", "MAKEFLAGS", "CMAKE_BUILD_PARALLEL_LEVEL")
	environment = append(environment, "AUTOLIB_INSTALL_ROOT="+install)
	if c.OS == "linux" {
		environment = prependPath(environment, filepath.Dir(o.MakeProgram))
	}
	return ciCommand(ctx, o.CMakeCommand, args, o.SourceRoot, environment, filepath.Join(o.WorkRoot, "logs", "configure-"+strings.Join(modules, "+")+".log"))
}

func buildCIModule(ctx context.Context, o ciRunOptions, c ciProductionContext, name, install string) error {
	if err := configureCIProduction(ctx, o, c, []string{name}, install); err != nil {
		return err
	}
	environment := withoutEnvironment(os.Environ(), "AUTOLIB_INSTALL_ROOT", "MAKEFLAGS", "CMAKE_BUILD_PARALLEL_LEVEL")
	environment = append(environment, "AUTOLIB_INSTALL_ROOT="+install)
	if c.OS == "linux" {
		environment = prependPath(environment, filepath.Dir(o.MakeProgram))
	}
	args := []string{"--build", filepath.Join(o.WorkRoot, "build"), "--config", c.Configuration, "--target", name, "--verbose"}
	if c.OS == "linux" {
		args = append(args, "--", fmt.Sprintf("-j%d", o.Budget), "--jobserver-style=fifo")
	}
	return ciCommand(ctx, o.CMakeCommand, args, o.SourceRoot, environment, filepath.Join(o.WorkRoot, "logs", "build-"+name+".log"))
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
	}{{"source-root", &o.SourceRoot}, {"plan", &o.PlanFile}, {"run-identity", &o.RunIdentityFile}, {"platform", &o.Platform}, {"layer", &o.Layer}, {"work-root", &o.WorkRoot}, {"builder-file", &o.BuilderFile}, {"importer-file", &o.ImporterFile}, {"cmake-command", &o.CMakeCommand}, {"make-program", &o.MakeProgram}} {
		flags.StringVar(item.value, item.key, "", item.key)
	}
	flags.IntVar(&o.Budget, "budget", 0, "Linux Make worker budget")
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
