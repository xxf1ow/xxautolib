package main

import (
	"encoding/json"
	"os"
	"testing"
)

type sourceCases struct {
	Sources []struct {
		Name, Input, Platform, Tag string
		Valid                      bool
	}
	Indexes []struct {
		Name, Body, SHA256 string
		ID                 int64
		Valid              bool
	}
	Selections []struct {
		Name, Source string
		Snapshot     releaseSnapshot
		Names        []string
		Valid        bool
		AssetName    string `json:"asset_name"`
		ID           int64
		SHA256       string
	}
}

func readSourceCases(t *testing.T) sourceCases {
	t.Helper()
	data, err := os.ReadFile("../../tests/autolib/release-source-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases sourceCases
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func TestReleaseSourceProtocol(t *testing.T) {
	cases := readSourceCases(t)
	for _, tc := range cases.Sources {
		t.Run(tc.Name, func(t *testing.T) {
			source, err := newReleaseSource(tc.Input)
			if (err == nil) != tc.Valid {
				t.Fatalf("source: %v", err)
			}
			if !tc.Valid {
				return
			}
			var platform, tag string
			switch c := source.(type) {
			case *githubSourceClient:
				platform, tag = "github", c.Source.Tag
			case *giteeSourceClient:
				platform, tag = "gitee", c.Source.Tag
			}
			if platform != tc.Platform || tag != tc.Tag {
				t.Fatalf("got platform %q tag %q", platform, tag)
			}
		})
	}
	for _, tc := range cases.Selections {
		t.Run("selection/"+tc.Name, func(t *testing.T) {
			source, err := newReleaseSource(tc.Source)
			if err != nil {
				t.Fatal(err)
			}
			asset, err := source.selectAsset(tc.Snapshot, tc.Names)
			if (err == nil) != tc.Valid {
				t.Fatalf("asset %+v: %v", asset, err)
			}
			if tc.Valid && (asset.Name != tc.AssetName || asset.ID != tc.ID || asset.SHA256 != tc.SHA256 || asset.URL != "https://gitee.com/download/demo") {
				t.Fatal(asset)
			}
		})
	}
}

func TestGiteeIndex(t *testing.T) {
	for _, tc := range readSourceCases(t).Indexes {
		t.Run(tc.Name, func(t *testing.T) {
			index, err := parseGiteeIndex(tc.Body)
			if (err == nil) != tc.Valid {
				t.Fatalf("index %+v: %v", index, err)
			}
			if tc.Valid && tc.ID != 0 {
				entry := index["demo-linux-aarch64.tar.zst"]
				if entry.ID != tc.ID || entry.SHA256 != tc.SHA256 {
					t.Fatal(entry)
				}
			}
		})
	}
}
