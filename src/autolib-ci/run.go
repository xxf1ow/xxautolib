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

func runCILayer(ctx context.Context, o ciRunOptions) error {
	return runCILayerWithCache(ctx, o, nil)
}

func ciItemReady(status string) bool {
	return status == "stored" || status == "reused" || status == "local_ready"
}

func runCILayerWithCache(ctx context.Context, o ciRunOptions, cache *ciCacheClient) (failure error) {
	index := -1
	if o.Layer != "prepare" && o.Layer != "verify" {
		value, ok := strings.CutPrefix(o.Layer, "layer")
		var err error
		index, err = strconv.Atoi(value)
		if !ok || err != nil || index < 0 || strconv.Itoa(index) != value {
			return errors.New("CI layer must be prepare, layer<N> or verify")
		}
	}
	for name, value := range map[string]string{"importer file": o.ImporterFile, "CMake command": o.CMakeCommand, "cache snapshot": o.CacheFile} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("CI run %s is required", name)
		}
	}
	plan, run, platform, source, work, err := loadCIWork(ctx, o)
	if err != nil {
		return err
	}
	o.SourceRoot, o.WorkRoot = source, work
	var snapshot ciCacheSnapshot
	if err := decodeStrictJSONFile(o.CacheFile, &snapshot); err != nil {
		return fmt.Errorf("read cache snapshot: %w", err)
	}
	if snapshot.Repository != o.Repository || snapshot.ReleaseID <= 0 || snapshot.Assets == nil || snapshot.Records == nil || snapshot.Inputs.Fingerprints == nil || snapshot.Inputs.Failures == nil {
		return errors.New("cache snapshot does not describe prepared repository inputs")
	}
	if cache == nil {
		cache, err = newCICacheClient(o.Repository, os.Getenv("GITHUB_TOKEN"))
		if err != nil {
			return err
		}
	}
	if cache.Repository != o.Repository {
		return errors.New("cache client repository differs from current source")
	}
	config, err := loadCIEnvironment(filepath.Join(source, ".github/ci-tools.json"))
	if err != nil {
		return err
	}
	production, err := ciProductionForPlatform(o.Platform, config)
	if err != nil {
		return err
	}
	if err := validateCIProductionIdentity(production, config); err != nil {
		return err
	}
	resultFile := filepath.Join(work, "results", o.Platform+".json")
	result := ciPlatformResult{Run: run, Platform: o.Platform, Packages: []ciSlotResult{}, Roots: []ciRootResult{}, FailedRoots: slices.Clone(platform.FailedRoots), SystemDependencies: ciDependencyState{}}
	install := filepath.Join(work, "install")
	if o.Layer != "prepare" {
		if err := decodeStrictJSONFile(resultFile, &result); err != nil {
			return fmt.Errorf("read current platform result: %w", err)
		}
		if result.Run != run || result.Platform != o.Platform || result.Error != "" || result.Processed || !result.Importer.Verified || !ciItemReady(result.Importer.Status) || result.Importer.File != ciImporterExecutable(o.Platform) || result.SystemDependencies == nil {
			return errors.New("platform result does not describe current prepared work")
		}
		seen := map[string]bool{}
		for _, item := range result.Packages {
			ref := item.Slot.Name + "@" + item.Slot.Version
			if seen[ref] || (!ciItemReady(item.Status) && item.Status != "failed" && item.Status != "dependency_failed") {
				return errors.New("invalid package terminal result")
			}
			seen[ref] = true
		}
		o.PreparedImporter = filepath.Join(install, ciImporterExecutable(o.Platform))
	} else if _, err := os.Lstat(resultFile); !os.IsNotExist(err) {
		return errors.New("prepare requires a fresh run work root")
	}
	defer func() {
		if failure != nil && !result.Processed {
			result.Error = failure.Error()
			result.Complete = false
		}
		failure = errors.Join(failure, writeCIJSON(resultFile, result))
	}()
	topTool := ciCacheKey{Kind: "importer", Name: "autolib-importer", OS: production.OS, Arch: production.Arch}
	if !digestValue.MatchString(snapshot.Inputs.Fingerprints[topTool.archiveName()]) {
		return errors.New("cache snapshot lacks current tool fingerprint")
	}
	if o.Layer == "prepare" {
		if err := os.MkdirAll(work, 0755); err != nil {
			return err
		}
		if err := os.Mkdir(install, 0755); err != nil {
			return fmt.Errorf("prepare requires a fresh isolated installation: %w", err)
		}
		expected, err := filepath.Abs(o.ImporterFile)
		if err != nil {
			return err
		}
		if expected != filepath.Join(install, ciImporterExecutable(o.Platform)) {
			return errors.New("importer must be the current isolated installation's host tool")
		}
		var record ciCacheRecord
		hit := false
		if candidate, ok := snapshot.Records[topTool.archiveName()]; ok && validateCICacheRecord(candidate, o.Repository, topTool, snapshot.Inputs.Fingerprints[topTool.archiveName()], map[string]string{}, snapshot.Assets) == nil {
			asset, err := cache.Download(ctx, candidate, filepath.Join(work, "downloads"))
			if err != nil {
				return err
			}
			if err := restoreCICacheItem(ctx, o, asset); err != nil {
				return err
			}
			record = candidate
			hit = true
			o.PreparedImporter = o.ImporterFile
		}
		if err := configureCIProduction(ctx, o, production, nil, install, filepath.Join(work, "logs/prepare")); err != nil {
			return err
		}
		result.Entry, err = verifyCIEntry(source, install)
		if err != nil {
			return err
		}
		if err := verifyImporterNoCRuntime(o.ImporterFile); err != nil {
			return err
		}
		smoke := filepath.Join(work, "importer-smoke")
		if err := os.Mkdir(smoke, 0755); err != nil {
			return err
		}
		command := ciExternalCommand(ctx, o.ImporterFile, "prepare", "--cache-root", smoke, "--os", production.OS, "--arch", production.Arch, "--result", filepath.Join(smoke, "result.cmake"), "--package=ci-smoke")
		output, err := command.CombinedOutput()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(output), "missing package input") {
			return fmt.Errorf("importer smoke did not reject controlled missing package: %v\n%s", err, output)
		}
		status := "reused"
		if !hit {
			record = ciCacheRecord{Schema: 1, Kind: "importer", Key: topTool, Origin: run, Fingerprint: snapshot.Inputs.Fingerprints[topTool.archiveName()], DependencyFingerprints: map[string]string{}, Verification: ciCacheVerification{Platform: o.Platform, Context: production, Evidence: []string{"logs/prepare/configure.log", "importer-smoke"}, Passed: true}}
			asset, err := packageCIItem(ctx, o, run, platform, plan.Publishers, record)
			if err != nil {
				return err
			}
			record, err = cache.Put(ctx, asset)
			if err != nil {
				return err
			}
			status = "stored"
		}
		result.Importer = ciImporterResult{File: ciImporterExecutable(o.Platform), Verified: true, Status: status, Record: &record}
		if (production.OS == "linux" || production.OS == "darwin") && len(platform.Roots) > 0 {
			evidence, err := probeJobserver(ctx, ciJobserverOptions{SourceRoot: source, WorkRoot: filepath.Join(work, "jobserver"), MakeProgram: o.MakeProgram, CMakeCommand: o.CMakeCommand, Budget: o.Budget})
			if err != nil {
				return err
			}
			if err := writeCIJSON(filepath.Join(work, "jobserver.json"), evidence); err != nil {
				return err
			}
		}
		fmt.Printf("%s importer: %s\n", o.Platform, status)
		return nil
	}
	modules := map[string]describedModule{}
	for _, module := range platform.Description.Modules {
		modules[moduleIdentity(module)] = module
	}
	find := func(ref string) *ciSlotResult {
		for i := range result.Packages {
			item := &result.Packages[i]
			if item.Slot.Name+"@"+item.Slot.Version == ref {
				return item
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
			return errors.New("entry differs from prepared work")
		}
		if err := verifyImporterNoCRuntime(o.ImporterFile); err != nil {
			return err
		}
		failures := []error{}
		for _, failed := range result.FailedRoots {
			failures = append(failures, fmt.Errorf("%s: %s (%s)", failed.Root, failed.Message, strings.Join(failed.Chain, " -> ")))
		}
		for ref, module := range modules {
			item := find(ref)
			if item == nil || item.Slot != ciModuleSlot(module, o.Platform) {
				failures = append(failures, fmt.Errorf("unprocessed package %s", ref))
				continue
			}
			if !ciItemReady(item.Status) || !item.Verified {
				failures = append(failures, fmt.Errorf("%s: %s: %s", ref, item.Status, item.Error))
			}
		}
		if len(result.Packages) != len(modules) {
			failures = append(failures, errors.New("package results do not match selected work"))
		}
		for _, root := range platform.Roots {
			item := find(root)
			r := ciRootResult{Root: root}
			if item != nil {
				r.Verified = ciItemReady(item.Status) && item.Verified
				r.EvidenceDirectory = item.EvidenceDirectory
			}
			result.Roots = append(result.Roots, r)
		}
		result.Processed = true
		result.Complete = len(failures) == 0
		return errors.Join(failures...)
	}
	if index >= len(platform.Layers) || len(platform.Layers[index]) == 0 {
		return errors.New("requested dependency layer is empty")
	}
	for position, ref := range platform.Layers[index] {
		if err := ctx.Err(); err != nil {
			return err
		}
		if find(ref) != nil {
			return fmt.Errorf("package %s was already processed", ref)
		}
		module, ok := modules[ref]
		if !ok {
			return fmt.Errorf("layer contains unselected module %s", ref)
		}
		item := ciSlotResult{Slot: ciModuleSlot(module, o.Platform), DependencyChain: []string{}}
		for _, dep := range module.Depends {
			ready := find(dep)
			if ready == nil || !ciItemReady(ready.Status) || !ready.Verified {
				item.Status = "dependency_failed"
				item.Error = "dependency " + dep + " is unavailable"
				item.DependencyChain = []string{ref, dep}
				if ready != nil && len(ready.DependencyChain) > 0 {
					item.DependencyChain = append([]string{ref}, ready.DependencyChain...)
				}
				break
			}
		}
		key := ciModuleCacheKey(module)
		fingerprint := snapshot.Inputs.Fingerprints[key.archiveName()]
		if item.Status == "" {
			if failed, ok := snapshot.Inputs.Failures[key.archiveName()]; ok {
				item.Status = "failed"
				if len(failed.DependencyChain) > 1 {
					item.Status = "dependency_failed"
				}
				item.Error = failed.Error
				item.DependencyChain = slices.Clone(failed.DependencyChain)
			} else if !digestValue.MatchString(fingerprint) {
				return fmt.Errorf("cache snapshot lacks current fingerprint for %s", ref)
			}
		}
		if item.Status == "" {
			logs := filepath.Join(work, "logs", o.Layer, strconv.Itoa(position+1))
			err := func() error {
				closure, err := ciModuleClosure(ref, modules)
				if err != nil {
					return err
				}
				deps := map[string]string{}
				for _, dep := range module.Depends {
					deps[dep] = snapshot.Inputs.Fingerprints[ciModuleCacheKey(modules[dep]).archiveName()]
				}
				var record ciCacheRecord
				hit := false
				if candidate, ok := snapshot.Records[key.archiveName()]; ok && validateCICacheRecord(candidate, o.Repository, key, fingerprint, deps, snapshot.Assets) == nil {
					asset, err := cache.Download(ctx, candidate, filepath.Join(work, "downloads"))
					if err != nil {
						return err
					}
					if err := restoreCICacheItem(ctx, o, asset); err != nil {
						return err
					}
					record = candidate
					hit = true
				}
				if !hit {
					environment, err := prepareCIItemDependencies(ctx, o, closure, modules, "build", result.SystemDependencies)
					if err != nil {
						return err
					}
					buildOptions := o
					buildOptions.Environment = environment
					for _, dependency := range closure {
						if ready := find(dependency); ready != nil && ready.Status == "reused" && ready.Verified {
							buildOptions.RestoredPackages = append(buildOptions.RestoredPackages, dependency)
						}
					}
					if err := buildCIModule(ctx, buildOptions, production, module, install, logs); err != nil {
						return err
					}
					item.Built = true
				}
				environment, err := prepareCIItemDependencies(ctx, o, closure, modules, "verify", result.SystemDependencies)
				if err != nil {
					return err
				}
				evidence, err := validateCIRelease(ctx, ciValidationOptions{SourceRoot: source, InstallRoot: install, BuildRoot: work, CMakeCommand: o.CMakeCommand, MakeProgram: o.MakeProgram, EvidenceDirectory: filepath.Join(work, "evidence", ref), Modules: closure, RootPackages: []string{ref}, Context: production, Environment: environment})
				if err != nil {
					return err
				}
				item.Verified = true
				item.EvidenceDirectory = evidence.EvidenceDirectory
				if hit {
					item.Status = "reused"
					item.Record = &record
					return nil
				}
				if key.OS == "any" && plan.Publishers[ref] != o.Platform {
					item.Status = "local_ready"
					return nil
				}
				record = ciCacheRecord{Schema: 1, Kind: "library", Key: key, Origin: run, Fingerprint: fingerprint, DependencyFingerprints: deps, Verification: ciCacheVerification{Platform: o.Platform, Context: production, Evidence: []string{evidence.EvidenceDirectory}, Passed: true}}
				asset, err := packageCIItem(ctx, o, run, platform, plan.Publishers, record)
				if err != nil {
					return err
				}
				record, err = cache.Put(ctx, asset)
				if err != nil {
					return err
				}
				item.Status = "stored"
				item.Record = &record
				return nil
			}()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				item.Status = "failed"
				item.Error = err.Error()
			}
		}
		result.Packages = append(result.Packages, item)
		if err := writeCIJSON(resultFile, result); err != nil {
			return err
		}
		fmt.Printf("%s %s: %s %s\n", o.Platform, ref, item.Status, item.Error)
	}
	return nil
}

func ciModuleClosure(root string, modules map[string]describedModule) ([]string, error) {
	result := []string{}
	seen := map[string]bool{}
	active := map[string]bool{}
	var visit func(string) error
	visit = func(ref string) error {
		if active[ref] {
			return fmt.Errorf("dependency cycle at %s", ref)
		}
		if seen[ref] {
			return nil
		}
		module, ok := modules[ref]
		if !ok {
			return fmt.Errorf("missing dependency %s", ref)
		}
		active[ref] = true
		for _, dep := range module.Depends {
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(active, ref)
		seen[ref] = true
		result = append(result, ref)
		return nil
	}
	err := visit(root)
	return result, err
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
			output, err := ciExternalCommand(context.Background(), "xcrun", args...).Output()
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
	command := ciExternalCommand(ctx, executable, args...)
	command.Dir = directory
	if environment != nil {
		command.Env = ciSafeEnvironment(environment)
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
		args := []string{}
		if o.PreparedImporter != "" {
			args = append(args, "-DAUTOLIB_PREPARED_IMPORTER="+o.PreparedImporter)
		}
		args = append(args, "-DAUTOLIB_SOURCE_ROOT="+o.SourceRoot, "-DAUTOLIB_INSTALL_ROOT="+install, "-P", filepath.Join(o.SourceRoot, "src", "autolib-builder", "prepare-tools.cmake"))
		return ciCommand(ctx, o.CMakeCommand, args, o.SourceRoot, ciItemEnvironment(o.Environment), filepath.Join(logs, "configure.log"))
	}
	build := filepath.Join(o.WorkRoot, "build")
	args := []string{"-S", o.SourceRoot, "-B", build, "-G", c.Generator, "-DBUILD_LIST=" + strings.Join(modules, ","), "-DCMAKE_BUILD_TYPE=" + c.Configuration}
	args = append(args, "-DAUTOLIB_RESTORED_PACKAGES="+strings.Join(o.RestoredPackages, ";"))
	if o.PreparedImporter != "" {
		args = append(args, "-DAUTOLIB_PREPARED_IMPORTER="+o.PreparedImporter)
	}
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
	environment := withoutEnvironment(ciItemEnvironment(o.Environment), "AUTOLIB_INSTALL_ROOT", "MAKEFLAGS", "CMAKE_BUILD_PARALLEL_LEVEL")
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
	environment := withoutEnvironment(ciItemEnvironment(o.Environment), "AUTOLIB_INSTALL_ROOT", "MAKEFLAGS", "CMAKE_BUILD_PARALLEL_LEVEL")
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
	}{{"repository", &o.Repository}, {"source-root", &o.SourceRoot}, {"plan", &o.PlanFile}, {"cache", &o.CacheFile}, {"run-identity", &o.RunIdentityFile}, {"platform", &o.Platform}, {"layer", &o.Layer}, {"work-root", &o.WorkRoot}, {"importer-file", &o.ImporterFile}, {"cmake-command", &o.CMakeCommand}, {"make-program", &o.MakeProgram}} {
		flags.StringVar(item.value, item.key, "", item.key)
	}
	flags.IntVar(&o.Budget, "budget", 0, "Native Make worker budget")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if o.PlanFile == "" || o.CacheFile == "" || flags.NArg() != 0 {
		return fail(errors.New("run requires --plan, --cache and explicit run/tool paths without positional arguments"))
	}
	if err := runCILayer(context.Background(), o); err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "%s %s passed\n", o.Platform, o.Layer)
	return 0
}

func ciSafeEnvironment(environment []string) []string {
	if environment == nil {
		environment = os.Environ()
	}
	return withoutEnvironment(environment, "GITHUB_TOKEN", "GH_TOKEN", "GITEE_TOKEN", "AUTOLIB_CACHE_ASKPASS", "AUTOLIB_CACHE_GIT_LOGIN", "AUTOLIB_CACHE_GIT_TOKEN", "GIT_ASKPASS", "SSH_ASKPASS")
}
func ciItemEnvironment(overrides []string) []string {
	environment := os.Environ()
	for _, value := range overrides {
		key, _, ok := strings.Cut(value, "=")
		if ok {
			environment = append(withoutEnvironment(environment, key), value)
		}
	}
	return ciSafeEnvironment(environment)
}
func ciExternalCommand(ctx context.Context, executable string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = ciSafeEnvironment(nil)
	return command
}
