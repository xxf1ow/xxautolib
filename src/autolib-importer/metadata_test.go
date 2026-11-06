package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestVersionedMetadataAsset(t *testing.T) {
	var cases struct {
		Assets []struct {
			Name          string
			Request       packageIdentity
			Files         map[string]string
			Directories   []string
			AssetName     string `json:"asset_name"`
			SHA256, Error string
			Universal     bool
		}
	}
	if err := json.Unmarshal([]byte(readTestFile(t, "../../tests/autolib/metadata-repository-cases.json")), &cases); err != nil {
		t.Fatal(err)
	}
	source, err := parseMetadataSource("https://gitee.com/owner/repo/releases/download/v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases.Assets) == 0 {
		t.Fatal("missing metadata asset cases")
	}
	for _, tc := range cases.Assets {
		t.Run(tc.Name, func(t *testing.T) {
			root := t.TempDir()
			for _, directory := range tc.Directories {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.Name == "version-case-collision" {
				entries, err := os.ReadDir(filepath.Join(root, "eigen"))
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 2 {
					t.Skip("filesystem cannot represent case-colliding versions")
				}
			}
			for path, content := range tc.Files {
				putTestFile(t, filepath.Join(root, filepath.FromSlash(path)), content)
			}
			asset, id, err := metadataPackageAsset(root, source, tc.Request)
			switch tc.Error {
			case "missing":
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("want missing asset, got %+v: %v", asset, err)
				}
			case "invalid":
				if err == nil || errors.Is(err, os.ErrNotExist) {
					t.Fatalf("want invalid metadata, got %+v: %v", asset, err)
				}
			case "":
				wantID := tc.Request
				if tc.Universal {
					wantID.OS, wantID.Arch = "any", "any"
				}
				if err != nil || asset.Name != tc.AssetName || asset.SHA256 != tc.SHA256 || asset.URL != "https://gitee.com/owner/repo/releases/download/v1/"+tc.AssetName || id != wantID {
					t.Fatalf("asset %+v identity %+v: %v", asset, id, err)
				}
			default:
				t.Fatalf("unknown fixture error %q", tc.Error)
			}
		})
	}
	t.Run("digest matches archive bytes", func(t *testing.T) {
		id := packageIdentity{"eigen", "3.4.0", "linux", "x86_64"}
		archive := testArchive(t, []archiveMember{{Name: "eigen/3.4.0/autolib-import.cmake", Body: identityHeader(id)}})
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(readTestFile(t, archive))))
		root := t.TempDir()
		putTestFile(t, filepath.Join(root, "eigen/3.4.0/eigen-3.4.0-linux-x86_64.tar.zst.sha256"), digest+"\n")
		putTestFile(t, filepath.Join(root, "eigen/package.toml"), "latest = \"3.4.0\"\n")
		if latest, err := readPackageLatest(root, "eigen"); err != nil || latest != "3.4.0" {
			t.Fatalf("latest %q: %v", latest, err)
		}
		asset, actual, err := metadataPackageAsset(root, source, id)
		if err != nil || asset.SHA256 != digest || actual != id || asset.URL != "https://gitee.com/owner/repo/releases/download/v1/eigen-3.4.0-linux-x86_64.tar.zst" {
			t.Fatalf("asset %+v identity %+v: %v", asset, actual, err)
		}
	})
}
