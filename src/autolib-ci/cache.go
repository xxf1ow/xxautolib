package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type ciImporterRecord struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Asset  string `json:"asset"`
	SHA256 string `json:"sha256"`
}

type ciPackageRecord struct {
	Name   string `json:"name"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Asset  string `json:"asset"`
	SHA256 string `json:"sha256"`
}

type ciManifest struct {
	Importers []ciImporterRecord `json:"importers"`
	Packages  []ciPackageRecord  `json:"packages"`
}

type ciSlot struct {
	Name string `json:"name"`
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type ciCacheRecord struct {
	Slot    ciSlot `json:"slot"`
	BuildID string `json:"build_id"`
	SHA256  string `json:"sha256"`
	Commit  string `json:"commit"`
}

type ciCacheIndex struct {
	Packages []ciCacheRecord `json:"packages"`
}

type ciCacheOptions struct {
	SourceRoot, WorkRoot, InstallRoot, ReleaseRoot, ImporterFile, CMakeCommand, BuildID string
	Slot                                                                                ciSlot
}

type ciCacheResult struct {
	Hit         bool
	Reason      string
	ArchiveFile string
	Package     ciPackageRecord
}

var cacheDigestPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
var cacheCommitPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var cachePackagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.+\-]*$`)

type ciAssetLabel struct {
	BuildID string `json:"build_id"`
	Commit  string `json:"commit"`
}

func ciPackageAssetName(slot ciSlot) string {
	return slot.Name + "-" + slot.OS + "-" + slot.Arch + ".tar.zst"
}

func selectCICacheAsset(assets []githubAsset, slot ciSlot, buildID string) (githubAsset, string, error) {
	name := ciPackageAssetName(slot)
	var selected githubAsset
	conflict := false
	for _, asset := range assets {
		if asset.Name == name {
			if selected.Name != "" {
				return githubAsset{}, "", fmt.Errorf("duplicate GitHub asset %s", name)
			}
			selected = asset
			continue
		}
		if slot.OS != "any" {
			conflict = conflict || asset.Name == ciPackageAssetName(ciSlot{Name: slot.Name, OS: "any", Arch: "any"})
		} else {
			for _, platform := range ciPlatforms {
				conflict = conflict || asset.Name == slot.Name+"-"+platform+".tar.zst"
			}
		}
	}
	if selected.Name == "" {
		return githubAsset{}, "fixed package asset is missing", nil
	}
	if conflict {
		return githubAsset{}, "", fmt.Errorf("conflicting universal and platform assets for %s", slot.Name)
	}
	if selected.ID <= 0 || selected.State != "uploaded" {
		return githubAsset{}, "package asset is not completely uploaded", nil
	}
	var label ciAssetLabel
	decoder := json.NewDecoder(strings.NewReader(selected.Label))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&label); err != nil {
		return githubAsset{}, "package production label is invalid", nil
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) || strings.TrimSpace(label.BuildID) == "" || !cacheCommitPattern.MatchString(label.Commit) {
		return githubAsset{}, "package production label is invalid", nil
	}
	if label.BuildID != buildID {
		return githubAsset{}, "cached package build ID does not match", nil
	}
	if !strings.HasPrefix(selected.Digest, "sha256:") || !cacheDigestPattern.MatchString(strings.TrimPrefix(selected.Digest, "sha256:")) {
		return githubAsset{}, "package asset has no valid GitHub SHA256 digest", nil
	}
	return selected, "", nil
}

func lookupCICache(ctx context.Context, client *githubClient, o ciCacheOptions) (ciCacheResult, error) {
	if client == nil {
		return ciCacheResult{}, fmt.Errorf("GitHub client is required")
	}
	if err := validateCICacheOptions(o); err != nil {
		return ciCacheResult{}, err
	}
	release, found, err := client.release(ctx)
	if err != nil {
		return ciCacheResult{}, err
	}
	if !found {
		return ciCacheMiss("fixed GitHub release is missing"), nil
	}
	assets, err := client.releaseAssets(ctx, release)
	if err != nil {
		return ciCacheResult{}, err
	}
	byName := make(map[string]githubAsset, len(assets))
	for _, asset := range assets {
		if asset.ID <= 0 || asset.Name == "" || byName[asset.Name].ID != 0 {
			return ciCacheResult{}, fmt.Errorf("GitHub release contains invalid or duplicate asset metadata")
		}
		byName[asset.Name] = asset
	}
	manifestAsset, ok := byName["manifest.json"]
	if !ok {
		return ciCacheMiss("release has no public manifest.json asset"), nil
	}
	manifestFile, err := downloadMetadata(ctx, client, manifestAsset, o.WorkRoot)
	if err != nil {
		return ciCacheResult{}, err
	}
	defer os.Remove(manifestFile)
	manifest, err := readCICacheManifest(manifestFile)
	if err != nil {
		return ciCacheResult{}, err
	}
	indexAsset, ok := byName["build-cache.json"]
	if !ok {
		return ciCacheMiss("release has no private build-cache.json asset"), nil
	}
	indexFile, err := downloadMetadata(ctx, client, indexAsset, o.WorkRoot)
	if err != nil {
		return ciCacheResult{}, err
	}
	defer os.Remove(indexFile)
	index, err := readCICacheIndex(indexFile)
	if err != nil {
		return ciCacheResult{}, err
	}
	var public *ciPackageRecord
	for i := range manifest.Packages {
		record := &manifest.Packages[i]
		if record.Name == o.Slot.Name && matchesCISlot(record.OS, record.Arch, o.Slot.OS, o.Slot.Arch) {
			public = record
			break
		}
	}
	if public == nil {
		return ciCacheMiss("public manifest has no matching package slot"), nil
	}
	var private *ciCacheRecord
	for i := range index.Packages {
		record := &index.Packages[i]
		if record.Slot.Name == o.Slot.Name && matchesCISlot(record.Slot.OS, record.Slot.Arch, o.Slot.OS, o.Slot.Arch) {
			private = record
			break
		}
	}
	if private == nil {
		return ciCacheMiss("private cache index has no matching package slot"), nil
	}
	if private.BuildID != o.BuildID {
		return ciCacheMiss("cached package build ID does not match"), nil
	}
	if !strings.EqualFold(private.SHA256, public.SHA256) {
		return ciCacheMiss("public and private package digests differ"), nil
	}
	archiveAsset, ok := byName[public.Asset]
	if !ok {
		return ciCacheMiss("release package asset is missing"), nil
	}
	if !digestMatches(archiveAsset.Digest, public.SHA256) {
		return ciCacheMiss("GitHub asset digest does not match public manifest"), nil
	}
	archivePath := filepath.Join(o.ReleaseRoot, public.Asset)
	if err := os.MkdirAll(o.ReleaseRoot, 0755); err != nil {
		return ciCacheResult{}, fmt.Errorf("create release cache: %w", err)
	}
	if existing, err := os.Open(archivePath); err == nil {
		actual, hashErr := hashReader(existing)
		closeErr := existing.Close()
		if err := errors.Join(hashErr, closeErr); err != nil {
			return ciCacheResult{}, err
		}
		if !strings.EqualFold(actual, public.SHA256) {
			return ciCacheMiss("existing release archive does not match the verified digest"), nil
		}
	} else if !os.IsNotExist(err) {
		return ciCacheResult{}, err
	} else {
		staged, err := os.CreateTemp(o.ReleaseRoot, ".cache-download-")
		if err != nil {
			return ciCacheResult{}, err
		}
		stagedPath := staged.Name()
		staged.Close()
		defer os.Remove(stagedPath)
		digest, err := client.downloadAsset(ctx, archiveAsset, stagedPath)
		if err != nil {
			var mismatch *cacheDigestMismatch
			if errors.As(err, &mismatch) {
				return ciCacheMiss("downloaded archive bytes do not match GitHub asset digest"), nil
			}
			return ciCacheResult{}, err
		}
		if !strings.EqualFold(digest, public.SHA256) {
			return ciCacheMiss("downloaded archive bytes do not match public manifest"), nil
		}
		if err := os.Rename(stagedPath, archivePath); err != nil {
			return ciCacheResult{}, fmt.Errorf("retain verified release archive: %w", err)
		}
	}

	if err := restoreCICache(ctx, o, *public, archivePath); err != nil {
		return ciCacheResult{}, err
	}
	return ciCacheResult{Hit: true, ArchiveFile: archivePath, Package: *public}, nil
}

func validateCICacheOptions(o ciCacheOptions) error {
	for name, value := range map[string]string{"source root": o.SourceRoot, "work root": o.WorkRoot, "install root": o.InstallRoot, "release root": o.ReleaseRoot, "importer file": o.ImporterFile, "CMake command": o.CMakeCommand, "build ID": o.BuildID} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("CI cache %s cannot be empty", name)
		}
	}
	if !cachePackagePattern.MatchString(o.Slot.Name) || !validCachePlatform(o.Slot.OS, o.Slot.Arch) {
		return fmt.Errorf("invalid CI cache slot %q/%q/%q", o.Slot.Name, o.Slot.OS, o.Slot.Arch)
	}
	return nil
}

func validCachePlatform(system, arch string) bool {
	if system == "any" || arch == "any" {
		return system == "any" && arch == "any"
	}
	return (system == "linux" || system == "windows") && (arch == "x86_64" || arch == "aarch64")
}

func matchesCISlot(actualOS, actualArch, requestedOS, requestedArch string) bool {
	return (actualOS == requestedOS && actualArch == requestedArch) || (actualOS == "any" && actualArch == "any")
}

func ciCacheMiss(reason string) ciCacheResult { return ciCacheResult{Reason: reason} }

func downloadMetadata(ctx context.Context, client *githubClient, asset githubAsset, workRoot string) (string, error) {
	assetDigest := strings.TrimPrefix(asset.Digest, "sha256:")
	if !cacheDigestPattern.MatchString(assetDigest) {
		return "", fmt.Errorf("GitHub asset %s has no valid SHA-256 digest", asset.Name)
	}
	if err := os.MkdirAll(workRoot, 0755); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(workRoot, ".github-metadata-")
	if err != nil {
		return "", err
	}
	name := file.Name()
	file.Close()
	digest, err := client.downloadAsset(ctx, asset, name)
	if err != nil {
		os.Remove(name)
		return "", err
	}
	if !strings.EqualFold(digest, assetDigest) {
		os.Remove(name)
		return "", fmt.Errorf("GitHub metadata asset %s bytes do not match its digest", asset.Name)
	}
	return name, nil
}

func readCICacheManifest(filename string) (ciManifest, error) {
	var manifest ciManifest
	if err := decodeStrictJSONFile(filename, &manifest); err != nil {
		return manifest, fmt.Errorf("decode production manifest: %w", err)
	}
	if manifest.Importers == nil || manifest.Packages == nil {
		return manifest, fmt.Errorf("production manifest arrays must not be null")
	}
	seen := map[ciSlot]bool{}
	for _, record := range manifest.Packages {
		if !cachePackagePattern.MatchString(record.Name) || !validCachePlatform(record.OS, record.Arch) || !validCacheAsset(record.Asset) || !cacheDigestPattern.MatchString(record.SHA256) {
			return manifest, fmt.Errorf("production manifest contains an invalid package record")
		}
		key := ciSlot{Name: record.Name, OS: record.OS, Arch: record.Arch}
		if seen[key] {
			return manifest, fmt.Errorf("production manifest repeats package slot %s/%s/%s", key.Name, key.OS, key.Arch)
		}
		seen[key] = true
	}
	for i, record := range manifest.Packages {
		for _, other := range manifest.Packages[i+1:] {
			if record.Name == other.Name && ((record.OS == "any" && record.Arch == "any") || (other.OS == "any" && other.Arch == "any")) {
				return manifest, fmt.Errorf("production manifest has conflicting universal and platform package slots for %s", record.Name)
			}
		}
	}
	seenImporters := map[ciSlot]bool{}
	for _, record := range manifest.Importers {
		if !validCachePlatform(record.OS, record.Arch) || !validCacheAsset(record.Asset) || !cacheDigestPattern.MatchString(record.SHA256) {
			return manifest, fmt.Errorf("production manifest contains an invalid importer record")
		}
		key := ciSlot{OS: record.OS, Arch: record.Arch}
		if seenImporters[key] {
			return manifest, fmt.Errorf("production manifest repeats importer slot %s/%s", key.OS, key.Arch)
		}
		seenImporters[key] = true
	}
	return manifest, nil
}

func readCICacheIndex(filename string) (ciCacheIndex, error) {
	var index ciCacheIndex
	if err := decodeStrictJSONFile(filename, &index); err != nil {
		return index, fmt.Errorf("decode private cache index: %w", err)
	}
	if index.Packages == nil {
		return index, fmt.Errorf("private cache index packages must not be null")
	}
	seen := map[ciSlot]bool{}
	for _, record := range index.Packages {
		if !cachePackagePattern.MatchString(record.Slot.Name) || !validCachePlatform(record.Slot.OS, record.Slot.Arch) || record.BuildID == "" || !cacheDigestPattern.MatchString(record.SHA256) || !cacheCommitPattern.MatchString(record.Commit) {
			return index, fmt.Errorf("private cache index contains an invalid package record")
		}
		if seen[record.Slot] {
			return index, fmt.Errorf("private cache index repeats package slot %s/%s/%s", record.Slot.Name, record.Slot.OS, record.Slot.Arch)
		}
		seen[record.Slot] = true
	}
	for i, record := range index.Packages {
		for _, other := range index.Packages[i+1:] {
			if record.Slot.Name == other.Slot.Name && ((record.Slot.OS == "any" && record.Slot.Arch == "any") || (other.Slot.OS == "any" && other.Slot.Arch == "any")) {
				return index, fmt.Errorf("private cache index has conflicting universal and platform slots for %s", record.Slot.Name)
			}
		}
	}
	return index, nil
}

func decodeStrictJSONFile(filename string, result any) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func validCacheAsset(name string) bool {
	return name != "" && filepath.Base(name) == name && name != "." && name != ".." && !strings.ContainsAny(name, `/\:`)
}

func digestMatches(apiDigest, digest string) bool {
	apiDigest = strings.TrimPrefix(apiDigest, "sha256:")
	digest = strings.TrimPrefix(digest, "sha256:")
	return cacheDigestPattern.MatchString(apiDigest) && cacheDigestPattern.MatchString(digest) && strings.EqualFold(apiDigest, digest)
}

func hashReader(reader io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeDigestFile(filename string, reader io.Reader, expected string) (string, error) {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(file, hash), reader)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		os.Remove(filename)
		return "", err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if !digestMatches(expected, digest) {
		os.Remove(filename)
		return digest, &cacheDigestMismatch{}
	}
	return digest, nil
}

type cacheDigestMismatch struct{}

func (*cacheDigestMismatch) Error() string { return "GitHub asset bytes do not match API digest" }

func restoreCICache(ctx context.Context, o ciCacheOptions, record ciPackageRecord, archive string) error {
	if err := os.MkdirAll(o.WorkRoot, 0755); err != nil {
		return err
	}
	candidate, err := os.MkdirTemp(o.WorkRoot, "ci-cache-candidate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(candidate)
	archiveName := filepath.Base(record.Asset)
	copyPath := filepath.Join(candidate, archiveName)
	if err := copyCacheFile(archive, copyPath); err != nil {
		return err
	}
	manifest := ciManifest{Importers: []ciImporterRecord{}, Packages: []ciPackageRecord{record}}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(candidate, "manifest.json")
	if err := os.WriteFile(manifestPath, data, 0644); err != nil {
		return err
	}
	isolatedWork, err := os.MkdirTemp(o.WorkRoot, "ci-importer-work-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(isolatedWork)
	resultPath := filepath.Join(candidate, "prepared.cmake")
	args := []string{"prepare", "--uri", candidate, "--manifest", manifestPath, "--work-root", isolatedWork, "--os", o.Slot.OS, "--arch", o.Slot.Arch, "--result", resultPath, "--package", record.Name}
	command := exec.CommandContext(ctx, o.ImporterFile, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("importer prepare failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	if _, err := os.Stat(resultPath); err != nil {
		return fmt.Errorf("importer prepare did not create result: %w", err)
	}
	if err := os.MkdirAll(o.InstallRoot, 0755); err != nil {
		return err
	}
	cmakeCommand := exec.CommandContext(ctx, o.CMakeCommand, "-DRESULT_FILE="+resultPath, "-DSOURCE_ROOT="+o.SourceRoot, "-DINSTALL_ROOT="+o.InstallRoot, "-DPACKAGE_NAME="+record.Name, "-DOS="+o.Slot.OS, "-DARCH="+o.Slot.Arch, "-P", filepath.Join(o.SourceRoot, "src", "autolib-ci", "restore-cache.cmake"))
	cmakeOutput, err := cmakeCommand.CombinedOutput()
	if err != nil {
		return fmt.Errorf("CMake cache restore failed: %w\n%s", err, strings.TrimSpace(string(cmakeOutput)))
	}
	return nil
}

func copyCacheFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	return errors.Join(copyErr, output.Close())
}
