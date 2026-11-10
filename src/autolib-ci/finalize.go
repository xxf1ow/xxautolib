package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func ciModuleSlot(module describedModule, platform string) ciSlot {
	osName, arch, _ := ciTarget(platform)
	if module.PackagePlatform == "any-any" {
		return ciSlot{Name: module.Name, Version: module.Version, OS: "any", Arch: "any"}
	}
	return ciSlot{Name: module.Name, Version: module.Version, OS: strings.ToLower(osName), Arch: arch}
}

func ciImporterExecutable(platform string) string {
	name := "autolib-importer-" + platform
	if platform == "windows-x86_64" {
		name += ".exe"
	}
	return name
}

func finalizeCI(plan ciPlan, repository string, run ciRunIdentity, results []ciPlatformResult) error {
	if err := validateCIPublishers(plan); err != nil {
		return err
	}
	return validateCIResults(plan, repository, run, results)
}

func validateCIResults(plan ciPlan, repository string, run ciRunIdentity, results []ciPlatformResult) error {
	if !ciRepositoryPattern.MatchString(repository) || run.Repository != repository || !commitPattern.MatchString(run.SHA) || run.CheckoutSHA != run.SHA || !ciRunNumber(run.RunID) || !ciRunNumber(run.Attempt) {
		return errors.New("invalid run identity")
	}
	if len(results) != len(plan.Platforms) {
		return errors.New("platform results are missing or duplicated")
	}
	selected := map[string]ciPlatformPlan{}
	for _, platform := range plan.Platforms {
		if _, ok := selected[platform.ID]; ok {
			return errors.New("duplicate planned platform")
		}
		if _, _, err := ciTarget(platform.ID); err != nil {
			return err
		}
		selected[platform.ID] = platform
	}
	seen := map[string]bool{}
	entryDigest := ""
	failures := []error{}
	for _, result := range results {
		platform, ok := selected[result.Platform]
		if !ok || seen[result.Platform] || result.Run != run || !result.Processed || result.Error != "" {
			return fmt.Errorf("platform %s has unexpected, duplicate, mismatched or incomplete results", result.Platform)
		}
		seen[result.Platform] = true
		if !ciItemReady(result.Importer.Status) || result.Importer.Status == "local_ready" || !result.Importer.Verified || result.Importer.File != ciImporterExecutable(result.Platform) {
			return fmt.Errorf("platform %s importer is not verified", result.Platform)
		}
		if !result.Entry.Verified || result.Entry.File != "autolib.cmake" || !digestValue.MatchString(result.Entry.SHA256) || (entryDigest != "" && result.Entry.SHA256 != entryDigest) {
			return fmt.Errorf("platform %s entry is unverified or has inconsistent SHA256", result.Platform)
		}
		entryDigest = result.Entry.SHA256
		if len(result.Packages) != len(platform.Description.Modules) {
			return fmt.Errorf("platform %s package results are incomplete", result.Platform)
		}
		modules := map[string]describedModule{}
		for _, module := range platform.Description.Modules {
			key := moduleIdentity(module)
			if _, exists := modules[key]; exists {
				return fmt.Errorf("duplicate planned package %s", key)
			}
			modules[key] = module
		}
		packages := map[string]bool{}
		for _, item := range result.Packages {
			key := item.Slot.Name + "@" + item.Slot.Version
			module, ok := modules[key]
			if !ok || packages[key] || item.Slot != ciModuleSlot(module, result.Platform) {
				return fmt.Errorf("invalid exact package result %s on %s", key, result.Platform)
			}
			packages[key] = true
			switch item.Status {
			case "stored":
				if !item.Built || !item.Verified || item.Error != "" {
					return fmt.Errorf("invalid stored package %s", key)
				}
			case "reused":
				if !item.Verified || item.Error != "" {
					return fmt.Errorf("invalid reused package %s", key)
				}
			case "local_ready":
				if !item.Built || !item.Verified || item.Error != "" || module.PackagePlatform != "any-any" || plan.Publishers[key] == result.Platform {
					return fmt.Errorf("invalid local package %s", key)
				}
			case "failed", "dependency_failed":
				if item.Error == "" {
					return fmt.Errorf("missing failure for %s", key)
				}
				failures = append(failures, fmt.Errorf("%s %s: %s: %s", result.Platform, key, item.Status, item.Error))
			default:
				return fmt.Errorf("invalid terminal status for %s", key)
			}
		}
		if len(result.FailedRoots) != len(platform.FailedRoots) {
			return fmt.Errorf("platform %s planning failures differ", result.Platform)
		}
		for i, failed := range result.FailedRoots {
			expected := platform.FailedRoots[i]
			if failed.Root != expected.Root || failed.Code != expected.Code || failed.Message != expected.Message || !slices.Equal(failed.Chain, expected.Chain) {
				return errors.New("planning failure differs from plan")
			}
			failures = append(failures, fmt.Errorf("%s %s: %s", result.Platform, failed.Root, failed.Message))
		}
		if !result.Complete {
			failures = append(failures, fmt.Errorf("platform %s completed with failures", result.Platform))
		}
		if len(result.Roots) != len(platform.Roots) {
			return fmt.Errorf("platform %s root results are incomplete", result.Platform)
		}
		roots := map[string]bool{}
		for _, item := range result.Roots {
			if roots[item.Root] || !slices.Contains(platform.Roots, item.Root) {
				return fmt.Errorf("invalid root result %s", item.Root)
			}
			roots[item.Root] = true
			for _, pkg := range result.Packages {
				if pkg.Slot.Name+"@"+pkg.Slot.Version == item.Root && item.Verified != (ciItemReady(pkg.Status) && pkg.Verified) {
					return fmt.Errorf("root %s differs from package result", item.Root)
				}
			}
		}
	}
	return errors.Join(failures...)
}

func runCIFinalize(args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	flags := flag.NewFlagSet("finalize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	planFile := flags.String("plan", "", "plan JSON file")
	runFile := flags.String("run-identity", "", "run identity JSON file")
	resultRoot := flags.String("results", "", "directory of platform result JSON files")
	repository := flags.String("repository", "", "trusted GitHub source repository")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if *planFile == "" || *runFile == "" || *resultRoot == "" || *repository == "" || flags.NArg() != 0 {
		return fail(errors.New("finalize requires --plan, --run-identity, --results and --repository"))
	}
	var plan ciPlan
	var identity ciRunIdentity
	if err := decodeStrictJSONFile(*planFile, &plan); err != nil {
		return fail(err)
	}
	if err := decodeStrictJSONFile(*runFile, &identity); err != nil {
		return fail(err)
	}
	for _, platform := range plan.Platforms {
		fmt.Fprintf(stdout, "%s: requested_roots=%v roots=%v filtered_roots=%v\n", platform.ID, platform.RequestedRoots, platform.Roots, platform.FilteredRoots)
		for _, reason := range platform.Description.FilteredRoots {
			fmt.Fprintf(stdout, "%s: filtered %s: %s (%s)\n", platform.ID, reason.Root, reason.Code, strings.Join(reason.Chain, " -> "))
		}
	}
	files, err := filepath.Glob(filepath.Join(*resultRoot, "*.json"))
	if err != nil {
		return fail(err)
	}
	var results []ciPlatformResult
	for _, file := range files {
		var result ciPlatformResult
		if err := decodeStrictJSONFile(file, &result); err != nil {
			return fail(err)
		}
		results = append(results, result)
		fmt.Fprintf(stdout, "%s: processed=%t complete=%t packages=%d error=%s\n", result.Platform, result.Processed, result.Complete, len(result.Packages), result.Error)
		for _, item := range result.Packages {
			fmt.Fprintf(stdout, "  %s@%s: %s built=%t verified=%t error=%s\n", item.Slot.Name, item.Slot.Version, item.Status, item.Built, item.Verified, item.Error)
		}
	}
	if err := finalizeCI(plan, *repository, identity, results); err != nil {
		return fail(err)
	}
	fmt.Fprintln(stdout, "CI selected work verified")
	return 0
}

func writeCIJSON(file string, value any) error {
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return err
	}
	// Each platform has one sequential writer; results are local to this run and attempt.
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, data, 0644)
}
