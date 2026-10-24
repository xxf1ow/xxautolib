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

type ciSlot struct {
	Name string `json:"name"`
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type ciAssetLabel struct {
	BuildID string `json:"build_id"`
	Commit  string `json:"commit"`
}

type ciReadyArchive struct{ Name, File string }

type ciCacheOptions struct {
	SourceRoot, WorkRoot, InstallRoot, ReleaseRoot, ImporterFile, CMakeCommand, BuildID string
	TargetOS, TargetArch                                                                string
	Slot                                                                                ciSlot
	DependencyArchives                                                                  []ciReadyArchive
}

type ciCacheResult struct {
	Hit                 bool
	Reason, ArchiveFile string
	Asset               githubAsset
}

var cacheDigestPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
var cacheCommitPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var cachePackagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.+\-]*$`)

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
	if err := decodeStrictJSON(strings.NewReader(selected.Label), &label); err != nil || strings.TrimSpace(label.BuildID) == "" || !cacheCommitPattern.MatchString(label.Commit) {
		return githubAsset{}, "package production label is invalid", nil
	}
	if label.BuildID != buildID {
		return githubAsset{}, "cached package build ID does not match", nil
	}
	if !validGitHubDigest(selected.Digest) {
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
	asset, reason, err := selectCICacheAsset(assets, o.Slot, o.BuildID)
	if err != nil {
		return ciCacheResult{}, err
	}
	if reason != "" {
		return ciCacheMiss(reason), nil
	}
	if err := os.MkdirAll(o.ReleaseRoot, 0755); err != nil {
		return ciCacheResult{}, err
	}
	file, err := os.CreateTemp(o.ReleaseRoot, ".cache-download-")
	if err != nil {
		return ciCacheResult{}, err
	}
	staged := file.Name()
	file.Close()
	defer os.Remove(staged)
	if _, err := client.downloadAsset(ctx, asset, staged); err != nil {
		var mismatch *cacheDigestMismatch
		if errors.As(err, &mismatch) {
			return ciCacheMiss("downloaded archive bytes do not match GitHub asset digest"), nil
		}
		return ciCacheResult{}, err
	}
	if err := restoreCICache(ctx, o, staged); err != nil {
		var invalid *ciInvalidCache
		if errors.As(err, &invalid) {
			return ciCacheMiss(invalid.Error()), nil
		}
		return ciCacheResult{}, err
	}
	archive := filepath.Join(o.ReleaseRoot, asset.Name)
	if err := os.Rename(staged, archive); err != nil {
		return ciCacheResult{}, fmt.Errorf("retain verified archive: %w", err)
	}
	return ciCacheResult{Hit: true, ArchiveFile: archive, Asset: asset}, nil
}

func validateCICacheOptions(o ciCacheOptions) error {
	for name, value := range map[string]string{"source root": o.SourceRoot, "work root": o.WorkRoot, "install root": o.InstallRoot, "release root": o.ReleaseRoot, "importer file": o.ImporterFile, "CMake command": o.CMakeCommand, "build ID": o.BuildID} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("CI cache %s cannot be empty", name)
		}
	}
	if !cachePackagePattern.MatchString(o.Slot.Name) || !validCachePlatform(o.Slot.OS, o.Slot.Arch) || o.TargetOS == "any" || !validCachePlatform(o.TargetOS, o.TargetArch) {
		return fmt.Errorf("invalid CI cache slot or target")
	}
	return nil
}

func validCachePlatform(system, arch string) bool {
	if system == "any" || arch == "any" {
		return system == "any" && arch == "any"
	}
	return (system == "linux" || system == "windows") && (arch == "x86_64" || arch == "aarch64")
}

func ciCacheMiss(reason string) ciCacheResult { return ciCacheResult{Reason: reason} }

func decodeStrictJSON(reader io.Reader, result any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func decodeStrictJSONFile(filename string, result any) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	return decodeStrictJSON(file, result)
}

func validCacheAsset(name string) bool {
	return name != "" && filepath.Base(name) == name && name != "." && name != ".." && !strings.ContainsAny(name, `/\:`)
}
func validGitHubDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && cacheDigestPattern.MatchString(strings.TrimPrefix(value, "sha256:"))
}
func digestMatches(apiDigest, digest string) bool {
	return validGitHubDigest(apiDigest) && cacheDigestPattern.MatchString(digest) && strings.EqualFold(strings.TrimPrefix(apiDigest, "sha256:"), digest)
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

type ciInvalidCache struct{ message string }

func (e *ciInvalidCache) Error() string { return e.message }

func restoreCICache(ctx context.Context, o ciCacheOptions, archive string) error {
	if err := os.MkdirAll(o.WorkRoot, 0755); err != nil {
		return err
	}
	candidate, err := os.MkdirTemp(o.WorkRoot, "ci-cache-candidate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(candidate)
	if err := copyCacheFile(archive, filepath.Join(candidate, ciPackageAssetName(o.Slot))); err != nil {
		return err
	}
	expected := []string{}
	for _, dependency := range o.DependencyArchives {
		if err := copyCacheFile(dependency.File, filepath.Join(candidate, filepath.Base(dependency.File))); err != nil {
			return err
		}
		expected = append(expected, dependency.Name)
	}
	expected = append(expected, o.Slot.Name)
	result := filepath.Join(candidate, "prepared.cmake")
	command := exec.CommandContext(ctx, o.ImporterFile, "prepare", "--local-root", candidate, "--work-root", filepath.Join(candidate, "work"), "--os", o.TargetOS, "--arch", o.TargetArch, "--result", result, "--package", o.Slot.Name)
	output, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return &ciInvalidCache{message: fmt.Sprintf("importer rejected cache candidate: %s", strings.TrimSpace(string(output)))}
		}
		return fmt.Errorf("start importer prepare: %w", err)
	}
	if _, err := os.Stat(result); err != nil {
		return fmt.Errorf("importer did not create current result: %w", err)
	}
	command = exec.CommandContext(ctx, o.CMakeCommand, "-DRESULT_FILE="+result, "-DSOURCE_ROOT="+o.SourceRoot, "-DINSTALL_ROOT="+o.InstallRoot, "-DPACKAGE_NAME="+o.Slot.Name, "-DEXPECTED_PACKAGES="+strings.Join(expected, ";"), "-DOS="+o.TargetOS, "-DARCH="+o.TargetArch, "-P", filepath.Join(o.SourceRoot, "src", "autolib-ci", "restore-cache.cmake"))
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("CMake cache restore failed: %w\n%s", err, strings.TrimSpace(string(output)))
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
