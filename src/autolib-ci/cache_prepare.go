package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ciCachePrepareOptions struct {
	Repository, SourceRoot, PlanFile, RunIdentityFile, Output string
	RequireCachedDependencies                                 bool
}

func requireCICachedDependencies(plan ciPlan, snapshot ciCacheSnapshot) error {
	for _, platform := range plan.Platforms {
		if len(plan.RequestedModules) != 1 || len(platform.Roots) != 1 || platform.Roots[0] != plan.RequestedModules[0] {
			return fmt.Errorf("%s: cached dependencies require one exact root package", platform.ID)
		}
		root := platform.Roots[0]
		modules := make(map[string]describedModule, len(platform.Description.Modules))
		for _, module := range platform.Description.Modules {
			modules[moduleIdentity(module)] = module
		}
		closure, err := ciModuleClosure(root, modules)
		if err != nil {
			return fmt.Errorf("%s: %w", platform.ID, err)
		}
		if len(closure) != len(modules) {
			return fmt.Errorf("%s: plan contains packages outside %s", platform.ID, root)
		}
		for _, ref := range closure[:len(closure)-1] {
			module := modules[ref]
			key := ciModuleCacheKey(module)
			name := key.archiveName()
			record, ok := snapshot.Records[name]
			if !ok {
				return fmt.Errorf("%s: %s requires a cached %s", platform.ID, root, ref)
			}
			deps := make(map[string]string, len(module.Depends))
			for _, dep := range module.Depends {
				deps[dep] = snapshot.Inputs.Fingerprints[ciModuleCacheKey(modules[dep]).archiveName()]
			}
			if err := validateCICacheRecord(record, snapshot.Repository, key, snapshot.Inputs.Fingerprints[name], deps, snapshot.Assets); err != nil {
				return fmt.Errorf("%s: %s requires a valid cache for %s: %w", platform.ID, root, ref, err)
			}
		}
	}
	return nil
}

func preflightCICacheCapacity(assets []ciCacheAsset, plannedNames []string) error {
	existing := map[string]bool{}
	for _, a := range assets {
		existing[a.Name] = true
	}
	planned := map[string]bool{}
	added := 0
	for _, name := range plannedNames {
		if name == "" || planned[name] {
			return fmt.Errorf("duplicate or empty planned cache name %q", name)
		}
		planned[name] = true
		if !existing[name] {
			added++
		}
	}
	if len(assets)+added > ciCacheAssetLimit {
		return fmt.Errorf("cache capacity exceeded: %d existing + %d new assets > %d", len(assets), added, ciCacheAssetLimit)
	}
	return nil
}

func plannedCICacheNames(plan ciPlan) ([]string, error) {
	if err := validateCIPublishers(plan); err != nil {
		return nil, err
	}
	names := []string{}
	writers := map[string]string{}
	platforms := map[string]bool{}
	add := func(key ciCacheKey, writer string) error {
		if err := key.validate(); err != nil {
			return err
		}
		name := key.archiveName()
		if previous, ok := writers[name]; ok {
			return fmt.Errorf("cache identity %s has duplicate writers %s and %s", name, previous, writer)
		}
		writers[name] = writer
		names = append(names, name)
		return nil
	}
	for _, platform := range plan.Platforms {
		if platforms[platform.ID] {
			return nil, errors.New("duplicate planned platform")
		}
		platforms[platform.ID] = true
		osName, arch, err := ciTarget(platform.ID)
		if err != nil {
			return nil, err
		}
		if err := add(ciCacheKey{Kind: "importer", Name: "autolib-importer", OS: strings.ToLower(osName), Arch: arch}, platform.ID); err != nil {
			return nil, err
		}
		for _, module := range platform.Description.Modules {
			if plan.Publishers[moduleIdentity(module)] != platform.ID && module.PackagePlatform == "any-any" {
				continue
			}
			if err := add(ciModuleCacheKey(module), platform.ID); err != nil {
				return nil, err
			}
		}
	}
	return names, nil
}

func prepareCICache(ctx context.Context, o ciCachePrepareOptions) error {
	c, err := newCICacheClient(o.Repository, os.Getenv("GITHUB_TOKEN"))
	if err != nil {
		return err
	}
	c.Records, err = newCICacheGitStore(o.Repository, os.Getenv("GITEE_REPOSITORY"), os.Getenv("GITEE_TOKEN"), filepath.Dir(o.Output))
	if err != nil {
		return err
	}
	return prepareCICacheWithClient(ctx, o, c)
}

func prepareCICacheWithClient(ctx context.Context, o ciCachePrepareOptions, c *ciCacheClient) error {
	if o.SourceRoot == "" || o.PlanFile == "" || o.RunIdentityFile == "" || o.Output == "" || o.Repository != c.Repository {
		return errors.New("cache-prepare requires matching repository, source, plan, run identity and output")
	}
	var plan ciPlan
	var run ciRunIdentity
	if err := decodeStrictJSONFile(o.PlanFile, &plan); err != nil {
		return err
	}
	if err := decodeStrictJSONFile(o.RunIdentityFile, &run); err != nil {
		return err
	}
	if err := validateCIRunIdentity(ctx, o.SourceRoot, o.Repository, run); err != nil {
		return err
	}
	names, err := plannedCICacheNames(plan)
	if err != nil {
		return err
	}
	inputs, err := computeCIFingerprints(o.SourceRoot, plan)
	if err != nil {
		return err
	}
	c.TargetSHA = run.SHA
	s, err := c.Snapshot(ctx, true)
	if err != nil {
		return err
	}
	if err := preflightCICacheCapacity(s.Assets, names); err != nil {
		return err
	}
	s.Inputs = inputs
	if o.RequireCachedDependencies {
		if err := requireCICachedDependencies(plan, s); err != nil {
			return err
		}
	}
	return writeCIJSON(o.Output, s)
}

func runCICachePrepare(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cache-prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var o ciCachePrepareOptions
	flags.BoolVar(&o.RequireCachedDependencies, "require-cached-dependencies", false, "require one exact root and a valid cache for every dependency")
	for _, field := range []struct {
		key   string
		value *string
	}{{"repository", &o.Repository}, {"source-root", &o.SourceRoot}, {"plan", &o.PlanFile}, {"run-identity", &o.RunIdentityFile}, {"output", &o.Output}} {
		flags.StringVar(field.value, field.key, "", field.key)
	}
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "cache-prepare does not accept positional arguments")
		return 1
	}
	if err := prepareCICache(context.Background(), o); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, o.Output)
	return 0
}
