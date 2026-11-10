package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

func validateCISyncSource(source ciSyncSource, repository string) error {
	if source.Schema != 1 || !ciRepositoryPattern.MatchString(repository) || source.Repository != repository || !commitPattern.MatchString(source.SHA) || !strings.HasPrefix(source.Ref, "refs/heads/") || source.Catalog.Packages == nil || source.Inputs.Fingerprints == nil || source.Inputs.Failures == nil {
		return errors.New("invalid sync source identity or facts")
	}
	if err := ciExternalCommand(context.Background(), "git", "check-ref-format", source.Ref).Run(); err != nil {
		return errors.New("invalid sync source branch")
	}
	return nil
}

func ciSyncDependencies(source ciSyncSource) map[string]map[string]string {
	result := map[string]map[string]string{}
	for _, p := range source.Plan.Platforms {
		byRef := map[string]describedModule{}
		for _, m := range p.Description.Modules {
			byRef[moduleIdentity(m)] = m
		}
		for _, m := range p.Description.Modules {
			deps := map[string]string{}
			for _, dep := range m.Depends {
				key := ciModuleCacheKey(byRef[dep]).archiveName()
				deps[dep] = source.Inputs.Fingerprints[key]
			}
			result[ciModuleCacheKey(m).archiveName()] = deps
		}
		osName, arch, _ := strings.Cut(p.ID, "-")
		result[(ciCacheKey{Kind: "importer", Name: "autolib-importer", OS: osName, Arch: arch}).archiveName()] = map[string]string{}
	}
	return result
}

func prepareCISyncCache(ctx context.Context, c *ciCacheClient, source ciSyncSource) (ciCacheSnapshot, error) {
	if err := validateCISyncSource(source, c.Repository); err != nil {
		return ciCacheSnapshot{}, err
	}
	snapshot, err := c.Snapshot(ctx, false)
	var remote *ciCacheHTTPError
	if errors.As(err, &remote) && remote.Status == 404 && snapshot.ReleaseID == 0 {
		err = nil
	}
	if err != nil {
		return ciCacheSnapshot{}, err
	}

	// 缺少归档的合法 sidecar 只保留清理归属，不成为候选。
	counts := map[string]int{}
	for _, a := range snapshot.Assets {
		counts[a.Name]++
	}
	for _, a := range snapshot.Assets {
		name, sidecar := strings.CutSuffix(a.Name, ".publication.json")
		if !sidecar || counts[a.Name] != 1 || counts[name] != 0 || a.State != "uploaded" || a.Size <= 0 || a.Size >= ciCacheSizeLimit {
			continue
		}
		response, err := c.request(ctx, "GET", c.endpoint(c.APIBase, "/assets/"+strconv.FormatInt(a.ID, 10)), "application/octet-stream", "", nil)
		if err != nil {
			return ciCacheSnapshot{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, a.Size+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			return ciCacheSnapshot{}, errors.New("read orphan cache record failed")
		}
		var r ciCacheRecord
		if int64(len(data)) != a.Size || (a.Digest != "" && a.Digest != "sha256:"+ciSHA256(data)) || decodeCICacheJSON(bytes.NewReader(data), &r, true) != nil || r.Key.archiveName() != name {
			continue
		}
		binding := []ciCacheAsset{{ID: r.Archive.ID, Name: name, State: "uploaded", Size: r.Archive.Size, Digest: "sha256:" + r.Archive.SHA256}}
		if validateCICacheRecord(r, c.Repository, r.Key, r.Fingerprint, r.DependencyFingerprints, binding) == nil {
			snapshot.Records[name] = r
			snapshot.RecordErrors[name] = "archive missing; record retained for cleanup identity only"
		}
	}
	snapshot.Inputs = source.Inputs
	deps := ciSyncDependencies(source)
	for _, a := range snapshot.Assets {
		if _, known := deps[a.Name]; known {
			if _, exists := snapshot.Records[a.Name]; !exists && snapshot.RecordErrors[a.Name] == "" {
				snapshot.RecordErrors[a.Name] = "cache publication record missing"
			}
		}
	}

	for name, r := range snapshot.Records {
		expected, ok := deps[name]
		if !ok {
			snapshot.RecordErrors[name] = "no current production identity"
			continue
		}
		if err := validateCICacheRecord(r, c.Repository, r.Key, source.Inputs.Fingerprints[name], expected, snapshot.Assets); err != nil {
			snapshot.RecordErrors[name] = err.Error()
		}
	}
	return snapshot, nil
}

func downloadCISyncCache(ctx context.Context, c *ciCacheClient, snapshot ciCacheSnapshot, name, directory string) (ciLocalAsset, error) {
	r, ok := snapshot.Records[name]
	if snapshot.Repository != c.Repository || snapshot.RecordErrors[name] != "" || !ok {
		return ciLocalAsset{}, errors.New("cache download requires a current validated candidate")
	}
	deps := map[string]string{}
	for dep := range r.DependencyFingerprints {
		depName, version, _ := strings.Cut(dep, "@")
		osName, arch, _ := strings.Cut(r.Verification.Platform, "-")
		key := ciCacheKey{Kind: "library", Name: depName, Version: version, OS: osName, Arch: arch}
		fingerprint, native := snapshot.Inputs.Fingerprints[key.archiveName()]
		if !native {
			key.OS, key.Arch = "any", "any"
			fingerprint = snapshot.Inputs.Fingerprints[key.archiveName()]
		}
		deps[dep] = fingerprint
	}
	if err := validateCICacheRecord(r, c.Repository, r.Key, snapshot.Inputs.Fingerprints[name], deps, snapshot.Assets); err != nil {
		return ciLocalAsset{}, err
	}
	return c.Download(ctx, r, directory)
}

func deleteCISyncCache(ctx context.Context, c *ciCacheClient, source ciSyncSource, previous ciCacheSnapshot, key ciCacheKey) error {
	if err := validateCISyncSource(source, c.Repository); err != nil {
		return err
	}
	if err := key.validate(); err != nil {
		return err
	}
	if previous.Repository != c.Repository || key.Kind != "library" {
		return errors.New("cache deletion requires matching repository and library identity")
	}
	p, present := source.Catalog.Packages[key.Name]
	if present {
		if p.Versions == nil {
			return errors.New("source versions are unknown")
		}
		v, present := p.Versions[key.Version]
		if present && (v.Platforms == nil || slices.Contains(v.Platforms, key.OS+"-"+key.Arch)) {
			return errors.New("source identity remains present or unknown")
		}
	}
	var current ciCacheSnapshot
	err := c.retry(ctx, func() error {
		var err error
		current, err = c.snapshot(ctx, false, key.archiveName())
		return err
	})
	var remote *ciCacheHTTPError
	if errors.As(err, &remote) && remote.Status == 404 && current.ReleaseID == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	if current.ReleaseID != previous.ReleaseID {
		return errors.New("cache release changed before deletion")
	}
	selected := []ciCacheAsset{}
	for _, name := range []string{key.archiveName(), key.archiveName() + ".publication.json"} {
		old := []ciCacheAsset{}
		now := []ciCacheAsset{}
		for _, a := range previous.Assets {
			if a.Name == name {
				old = append(old, a)
			}
		}
		for _, a := range current.Assets {
			if a.Name == name {
				now = append(now, a)
			}
		}
		if len(old) > 1 || len(now) > 1 {
			return errors.New("ambiguous cache deletion identity")
		}
		if len(now) == 0 {
			continue
		}
		if len(old) != 1 || old[0] != now[0] {
			return errors.New("cache asset changed before deletion")
		}
		selected = append(selected, now[0])
	}
	for _, a := range selected {
		err := c.retry(ctx, func() error {
			response, err := c.request(ctx, "DELETE", c.endpoint(c.APIBase, "/assets/"+strconv.FormatInt(a.ID, 10)), "application/vnd.github+json", "", nil)
			var remote *ciCacheHTTPError
			if errors.As(err, &remote) && remote.Status == 404 {
				return nil
			}
			if err != nil {
				return err
			}
			return response.Body.Close()
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func runCISyncCache(command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repository", "", "trusted source repository")
	sourceFile := flags.String("source-input", "", "source facts JSON")
	snapshotFile := flags.String("snapshot", "", "cache snapshot JSON")
	output := flags.String("output", "", "cache snapshot output")
	directory := flags.String("output-root", "", "archive output directory")
	name := flags.String("name", "", "selected archive name")
	keyJSON := flags.String("key", "", "exact library identity JSON")
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		return fail(errors.New("unexpected positional arguments"))
	}
	client, err := newCICacheClient(*repo, os.Getenv("GITHUB_TOKEN"))
	if err != nil {
		return fail(err)
	}
	var source ciSyncSource
	var snapshot ciCacheSnapshot
	switch command {
	case "sync-cache":
		if *output == "" {
			return fail(errors.New("sync-cache requires output"))
		}
		if err := decodeStrictJSONFile(*sourceFile, &source); err != nil {
			return fail(err)
		}
		snapshot, err = prepareCISyncCache(context.Background(), client, source)
		if err == nil {
			err = writeCIJSON(*output, snapshot)
		}
		if err == nil {
			fmt.Fprintln(stdout, *output)
		}
	case "cache-download":
		if *directory == "" {
			return fail(errors.New("cache-download requires output-root"))
		}
		if err := decodeStrictJSONFile(*snapshotFile, &snapshot); err != nil {
			return fail(err)
		}
		var asset ciLocalAsset
		asset, err = downloadCISyncCache(context.Background(), client, snapshot, *name, *directory)
		if err == nil {
			fmt.Fprintln(stdout, asset.Path)
		}
	case "cache-delete":
		if err := decodeStrictJSONFile(*sourceFile, &source); err != nil {
			return fail(err)
		}
		if err := decodeStrictJSONFile(*snapshotFile, &snapshot); err != nil {
			return fail(err)
		}
		var key ciCacheKey
		if err := decodeCICacheJSON(strings.NewReader(*keyJSON), &key, true); err != nil {
			return fail(err)
		}
		err = deleteCISyncCache(context.Background(), client, source, snapshot, key)
	}
	if err != nil {
		return fail(err)
	}
	return 0
}
