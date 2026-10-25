package main

import (
	"context"
	"debug/elf"
	"debug/pe"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

type ciValidationOptions struct {
	SourceRoot, InstallRoot, BuildRoot, ReleaseRoot string
	ImporterFile, CMakeCommand                      string
	EvidenceDirectory                               string
	Mode                                            string
	Modules                                         []string
	RootPackages                                    []string
	Context                                         ciProductionContext
}

type ciValidationResult struct {
	Passed            bool   `json:"passed"`
	EvidenceDirectory string `json:"evidence_directory"`
}

func validateCIRelease(ctx context.Context, o ciValidationOptions) (ciValidationResult, error) {
	if o.Mode != "single" && o.Mode != "relocate" {
		return ciValidationResult{}, errors.New("validation mode must be single or relocate")
	}
	if err := validateCIConsumerModules(o.Modules); err != nil {
		return ciValidationResult{}, err
	}
	environment, err := loadCIEnvironment(filepath.Join(o.SourceRoot, ".github", "ci-tools.json"))
	if err != nil {
		return ciValidationResult{}, fmt.Errorf("load CI production configuration: %w", err)
	}
	if err := validateCIProductionIdentity(o.Context, environment); err != nil {
		return ciValidationResult{}, err
	}
	roots, err := validateCIValidationPaths(o)
	if err != nil {
		return ciValidationResult{}, err
	}
	if err := os.MkdirAll(roots.evidence, 0755); err != nil {
		return ciValidationResult{}, fmt.Errorf("create evidence directory: %w", err)
	}
	args := []string{
		"-DSOURCE_ROOT=" + roots.source,
		"-DINSTALL_ROOT=" + roots.install,
		"-DRELEASE_DIR=" + roots.release,
		"-DVALIDATION_ROOT=" + roots.build,
		"-DEVIDENCE_DIRECTORY=" + roots.evidence,
		"-DIMPORTER_FILE=" + o.ImporterFile,
		"-DCMAKE_COMMAND=" + o.CMakeCommand,
		"-DTARGET_OS=" + o.Context.OS,
		"-DTARGET_ARCH=" + o.Context.Arch,
		"-DGENERATOR=" + o.Context.Generator,
		"-DGENERATOR_PLATFORM=" + o.Context.Platform,
		"-DGENERATOR_TOOLSET=" + o.Context.Toolset,
		"-DCONFIGURATION=" + o.Context.Configuration,
		"-DCMAKE_MSVC_RUNTIME_LIBRARY=" + o.Context.Runtime,
		"-DMULTI_CONFIG=" + onOff(o.Context.MultiConfig),
		"-DMODULES=" + strings.Join(o.Modules, ";"),
		"-DROOT_PACKAGES=" + strings.Join(o.RootPackages, ";"),
		"-DRELEASE_READY=ON",
		"-DVALIDATION_MODE=" + o.Mode,
	}
	defaultNames := make([]string, 0, len(o.Context.CMakeDefaults))
	for name := range o.Context.CMakeDefaults {
		defaultNames = append(defaultNames, name)
	}
	slices.Sort(defaultNames)
	for _, name := range defaultNames {
		args = append(args, "-D"+name+"="+o.Context.CMakeDefaults[name])
	}
	args = append(args, "-P", filepath.Join(roots.source, "tests", "autolib", "run-local.cmake"))
	command := exec.CommandContext(ctx, o.CMakeCommand, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return ciValidationResult{EvidenceDirectory: roots.evidence}, fmt.Errorf("real consumer validation failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	runRoot, err := validationRunFromOutput(output)
	if err != nil {
		return ciValidationResult{EvidenceDirectory: roots.evidence}, err
	}
	if o.Context.OS == "windows" {
		if err := verifyWindowsPackageInputs(runRoot, o.Mode); err != nil {
			return ciValidationResult{EvidenceDirectory: roots.evidence}, err
		}
	} else {
		if err := verifyLinuxABIEvidence(runRoot, environment.Production.Linux.GlibcMax, o.Mode); err != nil {
			return ciValidationResult{EvidenceDirectory: roots.evidence}, err
		}
	}
	return ciValidationResult{Passed: true, EvidenceDirectory: runRoot}, nil
}

func validateCIConsumerModules(modules []string) error {
	if len(modules) == 0 {
		return errors.New("CI consumer modules are required")
	}
	seen := map[string]bool{}
	for _, name := range modules {
		if !slices.Contains(ciModules, name) || seen[name] {
			return fmt.Errorf("invalid CI consumer module %q", name)
		}
		seen[name] = true
	}
	if seen["flann"] && !seen["lz4"] {
		return errors.New("CI consumer selection for flann must include dependency lz4")
	}
	return nil
}

func validateCIProductionIdentity(c ciProductionContext, environment ciEnvironmentConfig) error {
	common := environment.Production.Common
	if c.Configuration != common.Configuration {
		return fmt.Errorf("CI consumer configuration %q does not match production configuration %q", c.Configuration, common.Configuration)
	}
	if !equalStringMap(c.CMakeDefaults, common.CMakeDefaults) {
		return errors.New("CI consumer CMake defaults do not match the production configuration")
	}
	switch c.OS {
	case "windows":
		windows := environment.Production.Windows
		if c.Arch != "x86_64" || c.Generator != windows.Generator || c.Platform != windows.Platform || c.Toolset != windows.Toolset || c.Runtime != windows.Runtime || !c.MultiConfig {
			return errors.New("Windows CI consumer identity does not match the production configuration")
		}
		if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
			return fmt.Errorf("Windows CI consumer must run natively on windows-x86_64, got %s-%s", runtime.GOOS, runtime.GOARCH)
		}
	case "linux":
		linux := environment.Production.Linux
		if c.Generator != linux.Generator || c.Platform != "" || c.Toolset != "" || c.Runtime != "" || c.MultiConfig {
			return errors.New("Linux CI consumer requires single-config Unix Makefiles without a Windows runtime")
		}
		if runtime.GOOS != "linux" {
			return fmt.Errorf("Linux ABI validation must run on Linux, got %s", runtime.GOOS)
		}
		wantArch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
		if c.Arch != wantArch {
			return fmt.Errorf("Linux ABI validation must run natively on %s, got %s", c.Arch, wantArch)
		}
	default:
		return fmt.Errorf("unsupported CI consumer OS %q", c.OS)
	}
	return nil
}

type ciValidationRoots struct{ source, install, build, release, evidence string }

func validateCIValidationPaths(o ciValidationOptions) (ciValidationRoots, error) {
	paths := []struct {
		name, value string
	}{
		{"source root", o.SourceRoot}, {"install root", o.InstallRoot}, {"validation root", o.BuildRoot},
		{"release root", o.ReleaseRoot}, {"importer file", o.ImporterFile},
		{"CMake command", o.CMakeCommand}, {"evidence directory", o.EvidenceDirectory},
	}
	for _, item := range paths {
		if strings.TrimSpace(item.value) == "" {
			return ciValidationRoots{}, fmt.Errorf("%s cannot be empty", item.name)
		}
	}
	source, err := filepath.Abs(o.SourceRoot)
	if err != nil {
		return ciValidationRoots{}, err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return ciValidationRoots{}, fmt.Errorf("resolve source root: %w", err)
	}
	resolved := make([]string, len(paths)-1)
	for i, item := range paths[1:] {
		absolute, err := filepath.Abs(item.value)
		if err != nil {
			return ciValidationRoots{}, err
		}
		if item.name != "CMake command" {
			absolute, err = resolveExistingPrefix(absolute)
			if err != nil {
				return ciValidationRoots{}, fmt.Errorf("resolve %s: %w", item.name, err)
			}
		}
		resolved[i] = absolute
		if item.name != "CMake command" {
			if err := requirePathWithin(source, absolute, item.name); err != nil {
				return ciValidationRoots{}, err
			}
		}
	}
	return ciValidationRoots{source: source, install: resolved[0], build: resolved[1], release: resolved[2], evidence: resolved[5]}, nil
}

func resolveExistingPrefix(path string) (string, error) {
	current := path
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func requirePathWithin(root, path, label string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("%s must resolve inside source root", label)
	}
	return nil
}

func onOff(value bool) string {
	if value {
		return "ON"
	}
	return "OFF"
}

func validationRunFromOutput(output []byte) (string, error) {
	for _, line := range strings.Split(string(output), "\n") {
		const prefix = "-- Local binary validation: "
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), prefix)), nil
		}
	}
	return "", errors.New("validation driver did not report its evidence directory")
}

func verifyImporterNoCRuntime(path string) error {
	if runtime.GOOS == "windows" {
		file, err := pe.Open(path)
		if err != nil {
			return fmt.Errorf("inspect importer PE imports: %w", err)
		}
		defer file.Close()
		if file.FileHeader.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
			return fmt.Errorf("importer architecture is not x86_64")
		}
		libraries, err := file.ImportedLibraries()
		if err != nil {
			return fmt.Errorf("inspect importer PE imports: %w", err)
		}
		for _, library := range libraries {
			name := strings.ToLower(library)
			if strings.Contains(name, "msvcp") || strings.Contains(name, "vcruntime") || strings.Contains(name, "ucrt") || strings.Contains(name, "libstdc++") || strings.Contains(name, "libgcc") {
				return fmt.Errorf("pure-Go importer depends on C/C++ runtime %s", library)
			}
		}
		return nil
	}
	file, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("inspect importer ELF dependencies: %w", err)
	}
	defer file.Close()
	want := elf.EM_X86_64
	if runtime.GOARCH == "arm64" {
		want = elf.EM_AARCH64
	}
	if file.Machine != want {
		return fmt.Errorf("importer ELF architecture %v does not match host %s", file.Machine, runtime.GOARCH)
	}
	libraries, err := file.ImportedLibraries()
	if err != nil {
		return fmt.Errorf("inspect importer ELF dependencies: %w", err)
	}
	if len(libraries) != 0 {
		return fmt.Errorf("pure-Go importer has dynamic C/C++ runtime dependencies: %v", libraries)
	}
	return nil
}

func verifyWindowsPackageInputs(root, mode string) error {
	phase, packageRoot, err := validationPackagePaths(root, mode)
	if err != nil {
		return err
	}
	linkData, err := os.ReadFile(filepath.Join(root, phase, "link-inputs-Release.txt"))
	if err != nil {
		return fmt.Errorf("read Windows consumer package link inputs: %w", err)
	}
	linkText := string(linkData)
	for _, input := range strings.Split(strings.TrimSpace(linkText), "\n") {
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if err := requirePathWithin(packageRoot, filepath.Clean(input), "Windows consumer link input"); err != nil {
			return err
		}
	}
	if !strings.Contains(strings.ToLower(linkText), "flann") {
		return nil
	}
	properties, err := os.ReadFile(filepath.Join(root, phase, "imported-properties.txt"))
	if err != nil {
		return fmt.Errorf("read Windows consumer imported properties: %w", err)
	}
	flannDirectLZ4 := false
	for _, line := range strings.Split(string(properties), "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 || !strings.EqualFold(fields[0], "flann::flann") || !strings.EqualFold(fields[1], "INTERFACE_LINK_LIBRARIES") {
			continue
		}
		for _, link := range strings.Split(fields[2], ";") {
			if !strings.Contains(strings.ToLower(link), "lz4") {
				continue
			}
			if err := requirePathWithin(packageRoot, filepath.Clean(link), "FLANN direct LZ4 link input"); err != nil {
				return err
			}
			flannDirectLZ4 = true
		}
	}
	if !flannDirectLZ4 {
		return errors.New("Windows consumer does not record FLANN's direct lz4 link input from the relocated package")
	}
	return nil
}

func verifyLinuxABIEvidence(root, glibcMax, mode string) error {
	required, err := collectLinuxABIVersions(root, mode)
	if err != nil {
		return err
	}
	if err := verifyLinuxABIVersions(required, glibcMax); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "abi-requirements.txt"), []byte(strings.Join(required, "\n")+"\n"), 0644); err != nil {
		return fmt.Errorf("write consumer ABI requirements: %w", err)
	}
	return nil
}

var abiVersionPattern = regexp.MustCompile(`(?:^|\s)\(?(GLIBC_[0-9.]+|GLIBCXX_[0-9.]+|CXXABI_[0-9.]+|GCC_[0-9.]+)\)?(?:\s|$)`)

func validationPackagePaths(root, mode string) (string, string, error) {
	if mode == "single" {
		data, err := os.ReadFile(filepath.Join(root, "first", "prepared-root.txt"))
		if err != nil {
			return "", "", err
		}
		return "first", filepath.Clean(strings.TrimSpace(string(data))), nil
	}
	return "second", filepath.Join(root, "moved packages"), nil
}

func collectLinuxABIVersions(root, mode string) ([]string, error) {
	phase, packageRoot, err := validationPackagePaths(root, mode)
	if err != nil {
		return nil, err
	}
	consumerFiles := []string{filepath.Join(root, "first", "autolib_smoke")}
	if phase == "second" {
		consumerFiles = append(consumerFiles, filepath.Join(root, "second", "autolib_smoke"))
	}
	flannRoot := filepath.Join(packageRoot, "flann", "lib")
	if info, err := os.Stat(flannRoot); err == nil && info.IsDir() {
		err = filepath.WalkDir(flannRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.Contains(entry.Name(), ".so") {
				consumerFiles = append(consumerFiles, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	required := map[string]bool{}
	for _, file := range consumerFiles {
		versions, undefined, err := linuxObjdumpVersions(file)
		if err != nil {
			return nil, fmt.Errorf("inspect consumer ABI symbols in %s: %w", file, err)
		}
		for version := range versions {
			if undefined[version] {
				required[version] = true
			}
		}
	}
	return sortedKeys(required), nil
}

func linuxObjdumpVersions(path string) (map[string]bool, map[string]bool, error) {
	output, err := exec.Command("objdump", "-T", path).CombinedOutput()
	if err != nil {
		return nil, nil, err
	}
	versions, undefined := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		for _, match := range abiVersionPattern.FindAllStringSubmatch(line, -1) {
			versions[match[1]] = true
			if strings.Contains(line, "*UND*") {
				undefined[match[1]] = true
			}
		}
	}
	return versions, undefined, nil
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func verifyLinuxABIVersions(required []string, glibcMax string) error {
	limit := "GLIBC_" + glibcMax
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+$`).MatchString(glibcMax) {
		return fmt.Errorf("invalid configured glibc maximum %q", glibcMax)
	}
	for _, version := range required {
		if strings.HasPrefix(version, "GLIBC_") && compareVersion(version, limit) > 0 {
			return fmt.Errorf("consumer requires %s, above the configured %s release limit", version, limit)
		}
	}
	return nil
}

func compareVersion(left, right string) int {
	l := strings.TrimPrefix(left, "GLIBC_")
	r := strings.TrimPrefix(right, "GLIBC_")
	var a, b int
	if _, err := fmt.Sscanf(l, "%d.%d", &a, new(int)); err != nil {
		return 1
	}
	var aMinor, bMinor int
	_, _ = fmt.Sscanf(l, "%d.%d", &a, &aMinor)
	_, _ = fmt.Sscanf(r, "%d.%d", &b, &bMinor)
	if a != b {
		return a - b
	}
	return aMinor - bMinor
}
