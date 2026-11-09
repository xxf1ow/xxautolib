package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"strings"
)

const ciCacheTag = "autolib-cache"
const ciCacheAssetLimit = 1000
const ciCacheSizeLimit int64 = 2 << 30

type ciCacheKey struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func (k ciCacheKey) archiveName() string {
	if k.Kind == "importer" {
		return ciImporterExecutable(k.OS+"-"+k.Arch) + ".tar.xz"
	}
	return k.Name + "-" + k.Version + "-" + k.OS + "-" + k.Arch + ".tar.zst"
}

func (k ciCacheKey) validate() error {
	if k.Kind == "importer" {
		if k.Name != "autolib-importer" || k.Version != "" {
			return fmt.Errorf("invalid importer cache identity")
		}
	} else if k.Kind != "library" || !ciModuleName.MatchString(k.Name) || !ciModuleName.MatchString(k.Version) {
		return fmt.Errorf("invalid library cache identity")
	}
	if k.Kind == "library" && k.OS == "any" && k.Arch == "any" {
		return nil
	}
	_, _, err := ciTarget(k.OS + "-" + k.Arch)
	return err
}

type ciCacheAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type ciCacheArchive struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type ciCacheRecord struct {
	Schema                 int                 `json:"schema"`
	Kind                   string              `json:"kind"`
	Key                    ciCacheKey          `json:"key"`
	Archive                ciCacheArchive      `json:"archive"`
	Origin                 ciRunIdentity       `json:"origin"`
	Fingerprint            string              `json:"fingerprint"`
	DependencyFingerprints map[string]string   `json:"dependency_fingerprints"`
	Verification           ciCacheVerification `json:"verification"`
	LatestCandidate        string              `json:"latest_candidate"`
	Entry                  *ciCacheEntry       `json:"entry,omitempty"`
}

type ciCacheVerification struct {
	Platform string              `json:"platform"`
	Context  ciProductionContext `json:"context"`
	Evidence []string            `json:"evidence"`
	Passed   bool                `json:"passed"`
}

type ciCacheEntry struct {
	Bytes  []byte `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type ciLocalAsset struct {
	Path   string
	Record ciCacheRecord
}

func ciSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validateCICacheRecord(r ciCacheRecord, repository string, key ciCacheKey, fingerprint string, dependencies map[string]string, assets []ciCacheAsset) error {
	fail := func(message string) error { return fmt.Errorf("cache %s: %s", key.archiveName(), message) }
	if err := key.validate(); err != nil {
		return err
	}
	if r.Schema != 1 || r.Kind != key.Kind || r.Key != key || r.Archive.Name != key.archiveName() {
		return fail("record identity mismatch")
	}
	o := r.Origin
	if !ciRepositoryPattern.MatchString(repository) || o.Repository != repository || !commitPattern.MatchString(o.SHA) || o.CheckoutSHA != o.SHA || !ciRunNumber(o.RunID) || !ciRunNumber(o.Attempt) || !strings.HasPrefix(o.Ref, "refs/heads/") {
		return fail("invalid original producer identity")
	}
	if err := ciExternalCommand(context.Background(), "git", "check-ref-format", o.Ref).Run(); err != nil {
		return fail("invalid original branch")
	}
	if !digestValue.MatchString(r.Fingerprint) || r.Fingerprint != fingerprint || r.DependencyFingerprints == nil || !maps.Equal(r.DependencyFingerprints, dependencies) {
		return fail("input or dependency fingerprints do not match")
	}
	for _, digest := range r.DependencyFingerprints {
		if !digestValue.MatchString(digest) {
			return fail("invalid dependency fingerprint")
		}
	}
	v := r.Verification
	osName, arch, err := ciTarget(v.Platform)
	if err != nil || !v.Passed || len(v.Evidence) == 0 || v.Context.OS != strings.ToLower(osName) || v.Context.Arch != arch || v.Context.Configuration != "Release" {
		return fail("missing or mismatched verification")
	}
	for _, evidence := range v.Evidence {
		if strings.TrimSpace(evidence) == "" {
			return fail("empty verification evidence")
		}
	}
	if key.OS != "any" && v.Platform != key.OS+"-"+key.Arch {
		return fail("verification belongs to another platform")
	}
	if r.Archive.ID <= 0 || r.Archive.Size <= 0 || r.Archive.Size >= ciCacheSizeLimit || !digestValue.MatchString(r.Archive.SHA256) {
		return fail("invalid archive identity, size or SHA256")
	}
	var found *ciCacheAsset
	for i := range assets {
		a := &assets[i]
		if a.ID != r.Archive.ID && a.Name != r.Archive.Name {
			continue
		}
		if found != nil || a.ID != r.Archive.ID || a.Name != r.Archive.Name || a.State != "uploaded" || a.Size != r.Archive.Size || (a.Digest != "" && a.Digest != "sha256:"+r.Archive.SHA256) {
			return fail("archive asset binding does not match")
		}
		found = a
	}
	if found == nil {
		return fail("archive asset missing")
	}
	if key.Kind == "importer" {
		if r.LatestCandidate != "" || r.Entry == nil || len(r.Entry.Bytes) == 0 || !digestValue.MatchString(r.Entry.SHA256) || ciSHA256(r.Entry.Bytes) != r.Entry.SHA256 {
			return fail("original entry missing or mismatched")
		}
	} else if r.Entry != nil || !ciModuleName.MatchString(r.LatestCandidate) {
		return fail("invalid library latest candidate or entry")
	}
	return nil
}
