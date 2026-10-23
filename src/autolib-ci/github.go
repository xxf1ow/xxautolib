package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	ciGitHubRepository = "xxf1ow/xxautolib"
	ciGitHubReleaseTag = "autolib-release"
	ciGitHubAPIURL     = "https://api.github.com"
	ciGitHubUploadURL  = "https://uploads.github.com"
)

type githubClient struct {
	BaseURL, UploadURL, Token string
	HTTP                      *http.Client
}

func productionGitHubClient(token string) *githubClient {
	return &githubClient{BaseURL: ciGitHubAPIURL, UploadURL: ciGitHubUploadURL, Token: token, HTTP: http.DefaultClient}
}

type githubRelease struct {
	ID        int64  `json:"id"`
	TagName   string `json:"tag_name"`
	AssetsURL string `json:"assets_url"`
}

type githubAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Digest             string `json:"digest"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (c *githubClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *githubClient) apiURL(path string) (string, error) {
	base, err := url.Parse(strings.TrimRight(c.BaseURL, "/") + "/")
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("invalid GitHub API base URL")
	}
	relative, err := url.Parse(strings.TrimLeft(path, "/"))
	if err != nil {
		return "", err
	}
	return base.ResolveReference(relative).String(), nil
}

func (c *githubClient) request(ctx context.Context, method, address, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "xxautolib-ci")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	response, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub request failed: %w", err)
	}
	return response, nil
}

func (c *githubClient) getJSON(ctx context.Context, address string, output any) error {
	response, err := c.request(ctx, http.MethodGet, address, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return githubStatusError(response)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode GitHub response: trailing JSON data")
	}
	return nil
}

func (c *githubClient) release(ctx context.Context) (githubRelease, bool, error) {
	address, err := c.apiURL("repos/" + ciGitHubRepository + "/releases/tags/" + url.PathEscape(ciGitHubReleaseTag))
	if err != nil {
		return githubRelease{}, false, err
	}
	var release githubRelease
	err = c.getJSON(ctx, address, &release)
	if err != nil {
		var status *githubHTTPError
		if errors.As(err, &status) && status.Status == http.StatusNotFound {
			return githubRelease{}, false, nil
		}
		return githubRelease{}, false, err
	}
	if release.ID <= 0 || release.TagName != ciGitHubReleaseTag || release.AssetsURL == "" {
		return githubRelease{}, false, fmt.Errorf("GitHub release metadata is incomplete")
	}
	if !sameOrigin(c.BaseURL, release.AssetsURL) {
		return githubRelease{}, false, fmt.Errorf("GitHub release assets URL leaves the API host")
	}
	return release, true, nil
}

func sameOrigin(left, right string) bool {
	a, errA := url.Parse(left)
	b, errB := url.Parse(right)
	return errA == nil && errB == nil && a.Scheme != "" && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

type githubHTTPError struct{ Status int }

func (e *githubHTTPError) Error() string { return fmt.Sprintf("GitHub API returned HTTP %d", e.Status) }

func githubStatusError(response *http.Response) error {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return &githubHTTPError{Status: response.StatusCode}
}

func (c *githubClient) releaseAssets(ctx context.Context, release githubRelease) ([]githubAsset, error) {
	address := release.AssetsURL
	if address == "" {
		var err error
		address, err = c.apiURL("repos/" + ciGitHubRepository + "/releases/" + strconv.FormatInt(release.ID, 10) + "/assets")
		if err != nil {
			return nil, err
		}
	}
	assets := []githubAsset{}
	seenPages := map[string]bool{}
	for {
		if seenPages[address] {
			return nil, fmt.Errorf("GitHub pagination repeated an assets page")
		}
		seenPages[address] = true
		var page []githubAsset
		response, err := c.request(ctx, http.MethodGet, address, "application/vnd.github+json")
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			err = githubStatusError(response)
			response.Body.Close()
			return nil, err
		}
		decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
		err = decoder.Decode(&page)
		if err == nil {
			var extra any
			if !errors.Is(decoder.Decode(&extra), io.EOF) {
				err = fmt.Errorf("decode GitHub assets: trailing JSON data")
			}
		}
		next := nextGitHubPage(response.Header.Get("Link"))
		closeErr := response.Body.Close()
		if err != nil {
			return nil, errors.Join(fmt.Errorf("decode GitHub release assets: %w", err), closeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close GitHub release assets response: %w", closeErr)
		}
		assets = append(assets, page...)
		if next == "" {
			return assets, nil
		}
		current, err := url.Parse(address)
		if err != nil {
			return nil, err
		}
		following, err := url.Parse(next)
		if err != nil || following.Scheme != current.Scheme || !strings.EqualFold(following.Host, current.Host) {
			return nil, fmt.Errorf("GitHub pagination link leaves the API host")
		}
		address = following.String()
	}
}

func nextGitHubPage(header string) string {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		if len(fields) < 2 || !strings.Contains(fields[1], `rel="next"`) {
			continue
		}
		return strings.Trim(fields[0], " <>\t")
	}
	return ""
}

func (c *githubClient) downloadAsset(ctx context.Context, asset githubAsset, destination string) (string, error) {
	address, err := c.apiURL("repos/" + ciGitHubRepository + "/releases/assets/" + strconv.FormatInt(asset.ID, 10))
	if err != nil {
		return "", err
	}
	response, err := c.request(ctx, http.MethodGet, address, "application/octet-stream")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", githubStatusError(response)
	}
	return writeDigestFile(destination, response.Body, asset.Digest)
}
