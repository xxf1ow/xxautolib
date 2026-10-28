package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

type githubSource struct{ Owner, Repository, Tag string }

func parseGitHubSource(raw string) (githubSource, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return githubSource{}, fmt.Errorf("expected public GitHub Release tag URL, got %q", raw)
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	component := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	if len(parts) != 5 || !component.MatchString(parts[0]) || !component.MatchString(parts[1]) || parts[2] != "releases" || parts[3] != "tag" || parts[4] == "" {
		return githubSource{}, fmt.Errorf("expected https://github.com/<owner>/<repo>/releases/tag/<encoded-tag>, got %q", raw)
	}
	tag, err := url.PathUnescape(parts[4])
	if err != nil || strings.ContainsAny(tag, "\x00\r\n") {
		return githubSource{}, fmt.Errorf("invalid GitHub Release tag in %q", raw)
	}
	return githubSource{parts[0], parts[1], tag}, nil
}

func (s githubSource) releaseURL() string {
	return "https://github.com/" + s.Owner + "/" + s.Repository + "/releases/tag/" + url.PathEscape(s.Tag)
}

func (s githubSource) apiRoot() string {
	return "https://api.github.com/repos/" + s.Owner + "/" + s.Repository + "/releases/"
}

type githubAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Digest string `json:"digest"`
}

type githubSourceClient struct {
	Source githubSource
	HTTP   *http.Client
}

func githubReleaseID(raw json.RawMessage, source githubSource) (int64, error) {
	var release struct {
		ID        int64  `json:"id"`
		Tag       string `json:"tag_name"`
		Draft     *bool  `json:"draft"`
		AssetsURL string `json:"assets_url"`
	}
	if err := json.Unmarshal(raw, &release); err != nil {
		return 0, fmt.Errorf("invalid GitHub release response: %w", err)
	}
	if release.ID <= 0 || release.Tag != source.Tag || release.Draft == nil || *release.Draft || release.AssetsURL != fmt.Sprintf("%s%d/assets", source.apiRoot(), release.ID) {
		return 0, fmt.Errorf("GitHub release is incomplete, draft, or does not match %s", source.releaseURL())
	}
	return release.ID, nil
}

func validateGitHubSnapshot(snapshot releaseSnapshot, source githubSource) error {
	actualSource, err := parseGitHubSource(snapshot.Source)
	if err != nil || actualSource != source || snapshot.Platform != "github" || len(snapshot.Pages) == 0 {
		return fmt.Errorf("GitHub response has mismatched source or missing asset pages")
	}
	if _, err := githubReleaseID(snapshot.Release, source); err != nil {
		return err
	}
	for i, raw := range snapshot.Pages {
		page, err := releasePage(raw)
		if err != nil {
			return err
		}
		if (i < len(snapshot.Pages)-1 && len(page) != 100) || (i == len(snapshot.Pages)-1 && len(page) >= 100) {
			return fmt.Errorf("GitHub response has incomplete asset pagination")
		}
	}
	return nil
}

func (c *githubSourceClient) snapshot(ctx context.Context, responsePath string) (releaseSnapshot, error) {
	snapshot, exists, err := readReleaseSnapshot(responsePath)
	if err != nil {
		return releaseSnapshot{}, err
	}
	if exists {
		return snapshot, validateGitHubSnapshot(snapshot, c.Source)
	}
	snapshot = releaseSnapshot{Platform: "github", Source: c.Source.releaseURL()}

	release, err := readReleaseJSON(ctx, c.HTTP, c.Source.apiRoot()+"tags/"+url.PathEscape(c.Source.Tag), "application/vnd.github+json")
	if err != nil {
		return releaseSnapshot{}, err
	}
	snapshot.Release = release
	id, err := githubReleaseID(release, c.Source)
	if err != nil {
		return releaseSnapshot{}, err
	}
	for number := 1; ; number++ {
		page, err := readReleaseJSON(ctx, c.HTTP, fmt.Sprintf("%s%d/assets?per_page=100&page=%d", c.Source.apiRoot(), id, number), "application/vnd.github+json")
		if err != nil {
			return releaseSnapshot{}, err
		}
		entries, err := releasePage(page)
		if err != nil {
			return releaseSnapshot{}, err
		}
		if len(entries) > 100 {
			return releaseSnapshot{}, fmt.Errorf("GitHub assets page exceeds requested page size")
		}
		snapshot.Pages = append(snapshot.Pages, page)
		if len(entries) < 100 {
			break
		}
	}
	if err := validateGitHubSnapshot(snapshot, c.Source); err != nil {
		return releaseSnapshot{}, err
	}
	if err := writeReleaseSnapshot(responsePath, snapshot); err != nil {
		return releaseSnapshot{}, err
	}
	return snapshot, nil
}

func githubDigest(value string) (string, error) {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return "", fmt.Errorf("GitHub asset digest must be sha256:<64 hexadecimal digits>")
	}
	if _, err := hex.DecodeString(value[7:]); err != nil {
		return "", fmt.Errorf("invalid GitHub asset SHA256: %w", err)
	}
	return strings.ToLower(value[7:]), nil
}

func selectGitHubAsset(snapshot releaseSnapshot, names []string) (githubAsset, error) {
	var found []json.RawMessage
	for _, raw := range snapshot.Pages {
		page, err := releasePage(raw)
		if err != nil {
			return githubAsset{}, err
		}
		for _, entry := range page {
			var name struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(entry, &name); err != nil {
				return githubAsset{}, fmt.Errorf("invalid GitHub asset name: %w", err)
			}
			if slices.Contains(names, name.Name) {
				found = append(found, entry)
			}
		}
	}
	if len(found) != 1 {
		return githubAsset{}, fmt.Errorf("GitHub assets %v: expected one candidate, found %d", names, len(found))
	}
	var asset githubAsset
	if err := json.Unmarshal(found[0], &asset); err != nil {
		return githubAsset{}, fmt.Errorf("invalid GitHub asset: %w", err)
	}
	if asset.ID <= 0 || asset.State != "uploaded" {
		return githubAsset{}, fmt.Errorf("GitHub asset %s: invalid ID or not uploaded", asset.Name)
	}
	if _, err := githubDigest(asset.Digest); err != nil {
		return githubAsset{}, fmt.Errorf("GitHub asset %s: %w", asset.Name, err)
	}
	return asset, nil
}

func (c *githubSourceClient) selectAsset(snapshot releaseSnapshot, names []string) (releaseAsset, error) {
	if err := validateGitHubSnapshot(snapshot, c.Source); err != nil {
		return releaseAsset{}, err
	}
	asset, err := selectGitHubAsset(snapshot, names)
	if err != nil {
		return releaseAsset{}, err
	}
	digest, err := githubDigest(asset.Digest)
	if err != nil {
		return releaseAsset{}, err
	}
	return releaseAsset{ID: asset.ID, Name: asset.Name, URL: fmt.Sprintf("%sassets/%d", c.Source.apiRoot(), asset.ID), SHA256: digest, Accept: "application/octet-stream"}, nil
}
