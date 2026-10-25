package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
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

// Raw API responses live only for the caller's configure, never as a published package index.
type githubSnapshot struct {
	Source  string            `json:"source"`
	Release json.RawMessage   `json:"release"`
	Pages   []json.RawMessage `json:"pages"`
}

type githubSourceClient struct {
	Source githubSource
	HTTP   *http.Client
}

func (c *githubSourceClient) get(ctx context.Context, address, accept string) (*http.Response, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("GitHub requests require HTTPS: %s", address)
	}
	client := http.Client{Timeout: 5 * time.Minute}
	if c.HTTP != nil {
		client = *c.HTTP
		if client.Timeout == 0 {
			client.Timeout = 5 * time.Minute
		}
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.User != nil {
			return fmt.Errorf("GitHub redirect requires HTTPS: %s", req.URL.Redacted())
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many GitHub redirects")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "xxautolib-importer")
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub request %s: %w", address, err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("GitHub request %s: HTTP %d", address, response.StatusCode)
	}
	return response, nil
}

func readGitHubJSON(ctx context.Context, c *githubSourceClient, address string) (json.RawMessage, error) {
	response, err := c.get(ctx, address, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid GitHub JSON from %s", address)
	}
	return json.RawMessage(body), nil
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

func githubPage(raw json.RawMessage) ([]json.RawMessage, error) {
	var page []json.RawMessage
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, fmt.Errorf("invalid GitHub assets page: %w", err)
	}
	if page == nil {
		return nil, fmt.Errorf("GitHub assets page must be an array")
	}
	return page, nil
}

func validateGitHubSnapshot(snapshot githubSnapshot, source githubSource) error {
	if snapshot.Source != source.releaseURL() || len(snapshot.Pages) == 0 {
		return fmt.Errorf("GitHub response has mismatched source or missing asset pages")
	}
	if _, err := githubReleaseID(snapshot.Release, source); err != nil {
		return err
	}
	for i, raw := range snapshot.Pages {
		page, err := githubPage(raw)
		if err != nil {
			return err
		}
		if (i < len(snapshot.Pages)-1 && len(page) != 100) || (i == len(snapshot.Pages)-1 && len(page) >= 100) {
			return fmt.Errorf("GitHub response has incomplete asset pagination")
		}
	}
	return nil
}

func loadGitHubSnapshot(ctx context.Context, c *githubSourceClient, responsePath string) (githubSnapshot, error) {
	snapshot := githubSnapshot{Source: c.Source.releaseURL()}
	if responsePath != "" {
		body, err := os.ReadFile(responsePath)
		if err == nil {
			snapshot = githubSnapshot{}
			if err := json.Unmarshal(body, &snapshot); err != nil {
				return githubSnapshot{}, fmt.Errorf("GitHub response %s: %w", responsePath, err)
			}
			return snapshot, validateGitHubSnapshot(snapshot, c.Source)
		}
		if !os.IsNotExist(err) {
			return githubSnapshot{}, err
		}
	}
	release, err := readGitHubJSON(ctx, c, c.Source.apiRoot()+"tags/"+url.PathEscape(c.Source.Tag))
	if err != nil {
		return githubSnapshot{}, err
	}
	snapshot.Release = release
	id, err := githubReleaseID(release, c.Source)
	if err != nil {
		return githubSnapshot{}, err
	}
	for number := 1; ; number++ {
		page, err := readGitHubJSON(ctx, c, fmt.Sprintf("%s%d/assets?per_page=100&page=%d", c.Source.apiRoot(), id, number))
		if err != nil {
			return githubSnapshot{}, err
		}
		entries, err := githubPage(page)
		if err != nil {
			return githubSnapshot{}, err
		}
		if len(entries) > 100 {
			return githubSnapshot{}, fmt.Errorf("GitHub assets page exceeds requested page size")
		}
		snapshot.Pages = append(snapshot.Pages, page)
		if len(entries) < 100 {
			break
		}
	}
	if err := validateGitHubSnapshot(snapshot, c.Source); err != nil {
		return githubSnapshot{}, err
	}
	if responsePath != "" {
		if err := os.MkdirAll(filepath.Dir(responsePath), 0700); err != nil {
			return githubSnapshot{}, err
		}
		f, err := os.CreateTemp(filepath.Dir(responsePath), ".github-response-")
		if err != nil {
			return githubSnapshot{}, err
		}
		defer os.Remove(f.Name())
		writeErr := json.NewEncoder(f).Encode(snapshot)
		if err := errors.Join(writeErr, f.Close()); err != nil {
			return githubSnapshot{}, err
		}
		if err := os.Rename(f.Name(), responsePath); err != nil {
			return githubSnapshot{}, err
		}
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

func selectGitHubAsset(snapshot githubSnapshot, names []string) (githubAsset, error) {
	var found []json.RawMessage
	for _, raw := range snapshot.Pages {
		page, err := githubPage(raw)
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

func downloadGitHubAsset(ctx context.Context, c *githubSourceClient, asset githubAsset, destination string) error {
	expected, err := githubDigest(asset.Digest)
	if err != nil {
		return err
	}
	if asset.ID <= 0 || asset.State != "uploaded" {
		return fmt.Errorf("invalid GitHub download asset %s", asset.Name)
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("download destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	response, err := c.get(ctx, fmt.Sprintf("%sassets/%d", c.Source.apiRoot(), asset.ID), "application/octet-stream")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	f, err := os.CreateTemp(filepath.Dir(destination), ".download-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, hash), response.Body)
	if err := errors.Join(copyErr, f.Close()); err != nil {
		return fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return fmt.Errorf("GitHub asset %s: SHA256 mismatch", asset.Name)
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("download destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(f.Name(), destination)
}
