package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ciModuleSlot(module describedModule, platform string) ciSlot {
	osName, arch, _ := ciTarget(platform)
	if module.PlatformIndependent {
		return ciSlot{Name: module.Name, OS: "any", Arch: "any"}
	}
	return ciSlot{Name: module.Name, OS: strings.ToLower(osName), Arch: arch}
}

func ciImporterAsset(platform string) string {
	name := "autolib-importer-" + platform
	if platform == "windows-x86_64" {
		name += ".exe"
	}
	return name
}

func finalizeCI(plan ciPlan, run ciRunIdentity, results []ciPlatformResult) error {
	if len(plan.Platforms) == 0 || len(results) != len(plan.Platforms) {
		return errors.New("platform results are missing or duplicated")
	}
	seen := map[string]bool{}
	for _, result := range results {
		if seen[result.Platform] || result.Run != run || !result.Complete || result.Error != "" {
			return fmt.Errorf("platform %s has duplicate, mismatched or incomplete results: %s", result.Platform, result.Error)
		}
		seen[result.Platform] = true
		var platform *ciPlatformPlan
		for i := range plan.Platforms {
			if plan.Platforms[i].ID == result.Platform {
				platform = &plan.Platforms[i]
			}
		}
		if platform == nil {
			return fmt.Errorf("unexpected platform result %s", result.Platform)
		}
		if !result.Importer.Verified || !result.Importer.Uploaded || result.Importer.Asset != ciImporterAsset(result.Platform) || !sha256Pattern.MatchString(result.Importer.SHA256) {
			return fmt.Errorf("platform %s importer is not verified and published", result.Platform)
		}
		if len(result.Packages) != len(platform.Description.Modules) {
			return fmt.Errorf("platform %s package results are incomplete", result.Platform)
		}
		packages := map[string]bool{}
		for _, item := range result.Packages {
			if packages[item.Slot.Name] {
				return fmt.Errorf("duplicate package result %s", item.Slot.Name)
			}
			packages[item.Slot.Name] = true
			var module *describedModule
			for i := range platform.Description.Modules {
				if platform.Description.Modules[i].Name == item.Slot.Name {
					module = &platform.Description.Modules[i]
				}
			}
			if module == nil || item.Slot != ciModuleSlot(*module, result.Platform) || item.Asset != ciPackageAssetName(item.Slot) || !item.Verified || !item.Built || !sha256Pattern.MatchString(item.SHA256) {
				return fmt.Errorf("invalid package result %s on %s", item.Slot.Name, result.Platform)
			}
			publisher := !module.PlatformIndependent || result.Platform == plan.Publisher
			if publisher && !item.Uploaded || !publisher && item.Uploaded {
				return fmt.Errorf("package %s has incomplete or unauthorized publication", item.Slot.Name)
			}
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
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if *planFile == "" || *runFile == "" || *resultRoot == "" || flags.NArg() != 0 {
		return fail(errors.New("finalize requires --plan, --run-identity and --results"))
	}
	var plan ciPlan
	var identity ciRunIdentity
	if err := decodeStrictJSONFile(*planFile, &plan); err != nil {
		return fail(err)
	}
	if err := decodeStrictJSONFile(*runFile, &identity); err != nil {
		return fail(err)
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
			fmt.Fprintf(stdout, "  %s: built=%t verified=%t uploaded=%t\n", item.Asset, item.Built, item.Verified, item.Uploaded)
		}
	}
	if err := finalizeCI(plan, identity, results); err != nil {
		return fail(err)
	}
	fmt.Fprintln(stdout, "CI selected work verified and published")
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
