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
	for _, result := range results {
		platform, ok := selected[result.Platform]
		if !ok || seen[result.Platform] || result.Run != run || !result.Complete || result.Error != "" {
			return fmt.Errorf("platform %s has unexpected, duplicate, mismatched or incomplete results", result.Platform)
		}
		seen[result.Platform] = true
		if !result.Importer.Verified || result.Importer.File != ciImporterExecutable(result.Platform) {
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
			if !ok || packages[key] || item.Slot != ciModuleSlot(module, result.Platform) || !item.Built || !item.Verified {
				return fmt.Errorf("invalid exact package result %s on %s", key, result.Platform)
			}
			packages[key] = true
		}
		if len(result.Roots) != len(platform.Roots) {
			return fmt.Errorf("platform %s root results are incomplete", result.Platform)
		}
		roots := map[string]bool{}
		for _, item := range result.Roots {
			if roots[item.Root] || !slices.Contains(platform.Roots, item.Root) || !item.Verified {
				return fmt.Errorf("invalid root result %s", item.Root)
			}
			roots[item.Root] = true
		}
	}
	return nil
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
		fmt.Fprintf(stdout, "%s: complete=%t packages=%d error=%s\n", result.Platform, result.Complete, len(result.Packages), result.Error)
		for _, item := range result.Packages {
			fmt.Fprintf(stdout, "  %s@%s: built=%t verified=%t\n", item.Slot.Name, item.Slot.Version, item.Built, item.Verified)
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
