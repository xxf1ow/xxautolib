package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type releaseAsset struct {
	Name, URL, SHA256 string
}

func validateReleaseHTTPS(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.ContainsFunc(address, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return fmt.Errorf("Release requests require HTTPS without credentials: %q", address)
	}
	return nil
}

func releaseGET(ctx context.Context, configured *http.Client, address string) (*http.Response, error) {
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
	req.Header.Set("Accept", "application/octet-stream")
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

func downloadReleaseAsset(ctx context.Context, client *http.Client, asset releaseAsset, destination string, progress *downloadProgress) error {
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
	response, err := releaseGET(ctx, client, asset.URL)
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
	progress.total = response.ContentLength
	progress.report()
	_, copyErr := io.Copy(io.MultiWriter(f, hash, progress), response.Body)
	progress.finish()
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
