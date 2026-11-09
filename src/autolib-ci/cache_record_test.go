package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func cacheRecordFixture() (ciCacheRecord, []ciCacheAsset) {
	digest := strings.Repeat("a", 64)
	r := ciCacheRecord{Schema: 1, Kind: "library", Key: ciCacheKey{Kind: "library", Name: "b", Version: "1", OS: "linux", Arch: "x86_64"}, Archive: ciCacheArchive{ID: 42, Name: "b-1-linux-x86_64.tar.zst", Size: 100, SHA256: digest}, Origin: ciRunIdentity{Repository: "owner/repo", Ref: "refs/heads/topic", SHA: strings.Repeat("1", 40), CheckoutSHA: strings.Repeat("1", 40), RunID: "10", Attempt: "2"}, Fingerprint: digest, DependencyFingerprints: map[string]string{}, LatestCandidate: "1", Verification: ciCacheVerification{Platform: "linux-x86_64", Context: ciProductionContext{OS: "linux", Arch: "x86_64", Configuration: "Release"}, Evidence: []string{"verify/b"}, Passed: true}}
	return r, []ciCacheAsset{{ID: 42, Name: r.Archive.Name, State: "uploaded", Digest: "sha256:" + digest, Size: 100}}
}

func TestCIImporterCacheName(t *testing.T) {
	k := ciCacheKey{Kind: "importer", Name: "autolib-importer", OS: "windows", Arch: "x86_64"}
	if got := k.archiveName(); got != "autolib-importer-windows-x86_64.exe.tar.xz" {
		t.Fatalf("cache must keep the existing release archive name: %s", got)
	}
}

func TestCICacheRecordValidation(t *testing.T) {
	r, assets := cacheRecordFixture()
	if err := validateCICacheRecord(r, "owner/repo", r.Key, r.Fingerprint, map[string]string{}, assets); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"schema", "repository", "tag", "bad branch", "run", "SHA", "checkout", "key", "fingerprint", "unverified", "verification platform", "verification context", "evidence", "asset ID", "asset state", "asset size", "digest", "missing asset", "duplicate asset", "dependencies", "missing dependencies", "entry"} {
		t.Run(kind, func(t *testing.T) {
			r, a := cacheRecordFixture()
			key := r.Key
			fingerprint := r.Fingerprint
			switch kind {
			case "schema":
				r.Schema = 2
			case "repository":
				r.Origin.Repository = "other/repo"
			case "tag":
				r.Origin.Ref = "refs/tags/topic"
			case "bad branch":
				r.Origin.Ref = "refs/heads/../invalid"
			case "run":
				r.Origin.RunID = "0"
			case "SHA":
				r.Origin.SHA = "bad"
			case "checkout":
				r.Origin.CheckoutSHA = strings.Repeat("2", 40)
			case "key":
				r.Key.OS = "windows"
			case "fingerprint":
				r.Fingerprint = strings.Repeat("b", 64)
			case "unverified":
				r.Verification.Passed = false
			case "verification platform":
				r.Verification.Platform = "windows-x86_64"
			case "verification context":
				r.Verification.Context.Arch = "aarch64"
			case "evidence":
				r.Verification.Evidence = nil
			case "asset ID":
				r.Archive.ID = 41
			case "asset state":
				a[0].State = "starter"
			case "asset size":
				a[0].Size++
			case "digest":
				a[0].Digest = "sha256:" + strings.Repeat("b", 64)
			case "missing asset":
				a = nil
			case "duplicate asset":
				a = append(a, a[0])
			case "dependencies":
				r.DependencyFingerprints = map[string]string{"wrong": fingerprint}
			case "missing dependencies":
				r.DependencyFingerprints = nil
			case "entry":
				r.Entry = &ciCacheEntry{Bytes: []byte("unexpected"), SHA256: fingerprint}
			}
			if err := validateCICacheRecord(r, "owner/repo", key, fingerprint, map[string]string{}, a); err == nil {
				t.Fatal("invalid cache record accepted")
			}
		})
	}
	t.Run("importer entry and original identity", func(t *testing.T) {
		r, a := cacheRecordFixture()
		r.Kind = "importer"
		r.Key = ciCacheKey{Kind: "importer", Name: "autolib-importer", OS: "linux", Arch: "x86_64"}
		r.Archive.Name = "autolib-importer-linux-x86_64.tar.xz"
		a[0].Name = r.Archive.Name
		r.LatestCandidate = ""
		b := []byte("# raw entry\r\n")
		sum := sha256.Sum256(b)
		r.Entry = &ciCacheEntry{Bytes: b, SHA256: hex.EncodeToString(sum[:])}
		origin := r.Origin
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var restored ciCacheRecord
		if err = json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		if err := validateCICacheRecord(restored, "owner/repo", r.Key, r.Fingerprint, map[string]string{}, a); err != nil {
			t.Fatal(err)
		}
		if restored.Origin != origin || string(restored.Entry.Bytes) != string(b) {
			t.Fatal("producer or original bytes changed")
		}
		restored.Entry.Bytes = []byte("changed")
		if err := validateCICacheRecord(restored, "owner/repo", r.Key, r.Fingerprint, map[string]string{}, a); err == nil {
			t.Fatal("entry hash mismatch accepted")
		}
	})
}
