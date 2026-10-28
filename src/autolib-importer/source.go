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
	"strings"
	"time"
	"unicode"
)

// Raw API responses belong to one configure and are bound to its platform and source.
type releaseSnapshot struct {
	Platform string            `json:"platform"`
	Source   string            `json:"source"`
	Release  json.RawMessage   `json:"release"`
	Pages    []json.RawMessage `json:"pages"`
}

type releaseAsset struct {
	ID                        int64
	Name, URL, SHA256, Accept string
}

type releaseSource interface {
	snapshot(context.Context, string) (releaseSnapshot, error)
	selectAsset(releaseSnapshot, []string) (releaseAsset, error)
}

func releasePage(raw json.RawMessage) ([]json.RawMessage, error) {
	var page []json.RawMessage
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, fmt.Errorf("invalid Release assets page: %w", err)
	}
	if page == nil {
		return nil, fmt.Errorf("Release assets page must be an array")
	}
	return page, nil
}

func newReleaseSource(raw string) (releaseSource, error) {
	u, err := url.Parse(raw)
	if err == nil {
		switch strings.ToLower(u.Host) {
		case "github.com":
			s, err := parseGitHubSource(raw)
			return &githubSourceClient{Source: s}, err
		case "gitee.com":
			s, err := parseGiteeSource(raw)
			return &giteeSourceClient{Source: s}, err
		}
	}
	return nil, fmt.Errorf("unsupported Release source %q; expected a public GitHub tag or Gitee download root", raw)
}

func validateReleaseHTTPS(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.ContainsFunc(address, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return fmt.Errorf("Release requests require HTTPS without credentials: %q", address)
	}
	return nil
}

func releaseGET(ctx context.Context, configured *http.Client, address, accept string) (*http.Response, error) {
	if err := validateReleaseHTTPS(address); err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 5 * time.Minute}
	if configured != nil {
		client = *configured
		if client.Timeout == 0 {
			client.Timeout = 5 * time.Minute
		}
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validateReleaseHTTPS(req.URL.String()); err != nil {
			return err
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many Release redirects")
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
		return nil, fmt.Errorf("Release request %s: %w", address, err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("Release request %s: HTTP %d", address, response.StatusCode)
	}
	return response, nil
}

func readReleaseJSON(ctx context.Context, client *http.Client, address, accept string) (json.RawMessage, error) {
	response, err := releaseGET(ctx, client, address, accept)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid Release JSON from %s", address)
	}
	return body, nil
}

func readReleaseSnapshot(path string) (releaseSnapshot, bool, error) {
	var snapshot releaseSnapshot
	if path == "" {
		return snapshot, false, nil
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return snapshot, false, nil
	}
	if err != nil {
		return snapshot, true, err
	}
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return snapshot, true, fmt.Errorf("Release response %s: %w", path, err)
	}
	return snapshot, true, nil
}

func writeReleaseSnapshot(path string, snapshot releaseSnapshot) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".source-response-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	writeErr := json.NewEncoder(f).Encode(snapshot)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func downloadReleaseAsset(ctx context.Context, client *http.Client, asset releaseAsset, destination string) error {
	if len(asset.SHA256) != 64 || strings.ToLower(asset.SHA256) != asset.SHA256 {
		return fmt.Errorf("asset %s: expected lowercase SHA256", asset.Name)
	}
	if _, err := hex.DecodeString(asset.SHA256); err != nil {
		return fmt.Errorf("asset %s: invalid SHA256", asset.Name)
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("download destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	response, err := releaseGET(ctx, client, asset.URL, asset.Accept)
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
	if hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return fmt.Errorf("asset %s: SHA256 mismatch", asset.Name)
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("download destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(f.Name(), destination)
}
