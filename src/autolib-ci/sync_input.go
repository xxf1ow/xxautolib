package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type sourceCatalog struct {
	Packages map[string]sourcePackage `json:"packages"`
}
type sourcePackage struct {
	Latest   string                   `json:"latest"`
	Error    string                   `json:"error"`
	Versions map[string]sourceVersion `json:"versions"`
}
type sourceVersion struct {
	Platforms []string `json:"platforms"`
	Error     string   `json:"error"`
}
type ciSyncSource struct {
	Schema     int                 `json:"schema"`
	Repository string              `json:"repository"`
	Ref        string              `json:"ref"`
	SHA        string              `json:"sha"`
	Catalog    sourceCatalog       `json:"catalog"`
	Plan       ciPlan              `json:"plan"`
	Inputs     ciFingerprintReport `json:"inputs"`
}

func prepareCISyncSource(ctx context.Context, sourceRoot, builderFile, repository, ref string) (ciSyncSource, error) {
	result := ciSyncSource{Schema: 1, Repository: repository, Ref: ref}
	if !ciRepositoryPattern.MatchString(repository) || !strings.HasPrefix(ref, "refs/heads/") || strings.TrimPrefix(ref, "refs/heads/") == "" {
		return result, fmt.Errorf("sync requires a trusted repository and ordinary branch ref")
	}
	if err := ciExternalCommand(ctx, "git", "check-ref-format", ref).Run(); err != nil {
		return result, fmt.Errorf("sync branch ref is invalid")
	}
	head, err := ciExternalCommand(ctx, "git", "-C", sourceRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		return result, fmt.Errorf("read sync checkout HEAD: %w", err)
	}
	result.SHA = strings.TrimSpace(string(head))
	if !commitPattern.MatchString(result.SHA) {
		return result, fmt.Errorf("sync checkout HEAD is invalid")
	}
	cmd := ciExternalCommand(ctx, builderFile, "catalog", "--source-root", sourceRoot)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return result, fmt.Errorf("builder catalog: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result.Catalog); err != nil {
		return result, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return result, fmt.Errorf("builder catalog has trailing data")
	}
	if result.Catalog.Packages == nil {
		return result, fmt.Errorf("builder catalog is missing packages")
	}
	all := "all"
	result.Plan, err = planCI(ctx, ciPlanOptions{SourceRoot: sourceRoot, BuilderFile: builderFile, Modules: &all})
	if err != nil {
		return result, err
	}
	result.Inputs, err = computeCIFingerprints(sourceRoot, result.Plan)
	return result, err
}

func runCISyncSource(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sync-source", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("source-root", "", "repository source root")
	builder := flags.String("builder-file", "", "autolib-builder executable")
	repo := flags.String("repository", "", "trusted source repository")
	ref := flags.String("ref", "", "ordinary branch ref")
	output := flags.String("output", "", "source JSON output file")
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 || *output == "" || *builder == "" || *root == "" {
		return fail(fmt.Errorf("sync-source requires source-root, builder-file, repository, ref and output"))
	}
	source, err := prepareCISyncSource(context.Background(), *root, *builder, *repo, *ref)
	if err != nil {
		return fail(err)
	}
	data, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0755); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(*output, append(data, '\n'), 0644); err != nil {
		return fail(err)
	}
	fmt.Fprintln(stdout, *output)
	return 0
}
