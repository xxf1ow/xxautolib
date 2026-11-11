package main

import (
	"bytes"
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
	"strconv"
	"strings"
	"time"
)

type ciCacheSnapshot struct {
	RecordHead   string                   `json:"record_head"`
	Repository   string                   `json:"repository"`
	ReleaseID    int64                    `json:"release_id"`
	Assets       []ciCacheAsset           `json:"assets"`
	Records      map[string]ciCacheRecord `json:"records"`
	RecordErrors map[string]string        `json:"record_errors"`
	Inputs       ciFingerprintReport      `json:"inputs"`
}

type ciCacheClient struct {
	Records                                           *ciCacheGitStore
	Repository, Token, APIBase, UploadBase, TargetSHA string
	HTTP                                              *http.Client
	Wait                                              func(context.Context, time.Duration) error
}

func newCICacheClient(repository, token string) (*ciCacheClient, error) {
	if !ciRepositoryPattern.MatchString(repository) || strings.TrimSpace(token) == "" {
		return nil, errors.New("release cache requires repository and GITHUB_TOKEN")
	}
	return &ciCacheClient{Repository: repository, Token: token, APIBase: "https://api.github.com", UploadBase: "https://uploads.github.com", HTTP: &http.Client{Timeout: 5 * time.Minute}, Wait: func(ctx context.Context, d time.Duration) error {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}}, nil
}

type ciCacheHTTPError struct {
	Status    int
	Temporary bool
}

func (e *ciCacheHTTPError) Error() string {
	if e.Status == 0 {
		return "GitHub cache transport failed"
	}
	return fmt.Sprintf("GitHub cache HTTP %d", e.Status)
}

func (c *ciCacheClient) retry(ctx context.Context, operation func() error) error {
	delays := []time.Duration{2, 4, 8, 16, 32, 32, 32, 32, 32, 32}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		if err == nil {
			return nil
		}
		var remote *ciCacheHTTPError
		if !errors.As(err, &remote) || !remote.Temporary || attempt >= len(delays) {
			return err
		}
		if err := c.Wait(ctx, delays[attempt]*time.Second); err != nil {
			return err
		}
	}
}

func (c *ciCacheClient) request(ctx context.Context, method, address, accept, contentType string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, errors.New("invalid GitHub cache request")
	}
	api, apiErr := url.Parse(c.APIBase)
	upload, uploadErr := url.Parse(c.UploadBase)
	if apiErr != nil || uploadErr != nil || !((request.URL.Scheme == api.Scheme && request.URL.Host == api.Host) || (request.URL.Scheme == upload.Scheme && request.URL.Host == upload.Host)) {
		return nil, errors.New("untrusted GitHub cache request origin")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Accept", accept)
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if file, ok := body.(*os.File); ok {
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		request.ContentLength = info.Size()
		request.Body = io.NopCloser(file)
	}
	client := *c.HTTP
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many cache download redirects")
		}
		first := via[0]
		if next.URL.Scheme != first.URL.Scheme || next.URL.Host != first.URL.Host {
			next.Header.Del("Authorization")
		}
		if first.Method != "GET" {
			return errors.New("cache writes must not redirect")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &ciCacheHTTPError{Temporary: true}
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	status := response.StatusCode
	temporary := status == 429 || status >= 500
	if method == "POST" && status == 422 && contentType != "application/json" {
		var failure struct {
			Errors []struct{ Resource, Field, Code string }
		}
		if json.Unmarshal(data, &failure) == nil {
			for _, item := range failure.Errors {
				if item.Resource == "ReleaseAsset" && item.Field == "name" && item.Code == "already_exists" {
					temporary = true
				}
			}
		}
	}
	return nil, &ciCacheHTTPError{Status: status, Temporary: temporary}
}

func (c *ciCacheClient) endpoint(base, suffix string) string {
	return strings.TrimRight(base, "/") + "/repos/" + c.Repository + "/releases" + suffix
}

func decodeCICacheJSON(reader io.Reader, value any, strict bool) error {
	d := json.NewDecoder(reader)
	if strict {
		d.DisallowUnknownFields()
	}
	if err := d.Decode(value); err != nil {
		return errors.New("invalid GitHub cache JSON")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing GitHub cache JSON")
	}
	return nil
}

func (c *ciCacheClient) jsonRequest(ctx context.Context, method, address string, body []byte, value any) error {
	r, err := c.request(ctx, method, address, "application/vnd.github+json", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer r.Body.Close()
	return decodeCICacheJSON(r.Body, value, false)
}

func (c *ciCacheClient) Snapshot(ctx context.Context, allowCreate bool) (ciCacheSnapshot, error) {
	var result ciCacheSnapshot
	err := c.retry(ctx, func() error { var err error; result, err = c.snapshot(ctx, allowCreate, ""); return err })
	return result, err
}

// snapshot never retries a write in place: callers restart from remote facts after an uncertain response.
func (c *ciCacheClient) snapshot(ctx context.Context, allowCreate bool, target string) (ciCacheSnapshot, error) {
	s := ciCacheSnapshot{Repository: c.Repository, Assets: []ciCacheAsset{}, Records: map[string]ciCacheRecord{}, RecordErrors: map[string]string{}}
	var release struct {
		ID               int64
		TagName          string `json:"tag_name"`
		Immutable, Draft bool
	}
	err := c.jsonRequest(ctx, "GET", c.endpoint(c.APIBase, "/tags/"+ciCacheTag), nil, &release)
	var remote *ciCacheHTTPError
	if allowCreate && errors.As(err, &remote) && remote.Status == 404 {
		payload, _ := json.Marshal(map[string]any{"tag_name": ciCacheTag, "name": ciCacheTag, "target_commitish": c.TargetSHA, "prerelease": true, "make_latest": "false"})
		err = c.jsonRequest(ctx, "POST", c.endpoint(c.APIBase, ""), payload, &release)
	}
	if err != nil {
		return s, err
	}
	if release.ID <= 0 || release.TagName != ciCacheTag || release.Immutable || release.Draft {
		return s, errors.New("release cache must be the published mutable autolib-cache release")
	}
	s.ReleaseID = release.ID
	ids := map[int64]bool{}
	for page := 1; ; page++ {
		var assets []ciCacheAsset
		if err := c.jsonRequest(ctx, "GET", c.endpoint(c.APIBase, fmt.Sprintf("/%d/assets?per_page=100&page=%d", s.ReleaseID, page)), nil, &assets); err != nil {
			return s, err
		}
		if assets == nil {
			return s, errors.New("cache asset list is not an array")
		}
		for _, a := range assets {
			if a.ID <= 0 || a.Name == "" || ids[a.ID] || a.Size < 0 {
				return s, errors.New("invalid or repeated cache asset")
			}
			ids[a.ID] = true
		}
		s.Assets = append(s.Assets, assets...)
		if len(assets) < 100 {
			break
		}
	}
	return c.recordSnapshot(ctx, s, allowCreate, target)
}

func (c *ciCacheClient) recordSnapshot(ctx context.Context, s ciCacheSnapshot, allowCreate bool, target string) (ciCacheSnapshot, error) {
	counts := map[string]int{}
	for _, a := range s.Assets {
		counts[a.Name]++
	}
	if c.Records == nil {
		return s, errors.New("Gitee cache record store is required")
	}
	records, err := c.Records.Snapshot(ctx, allowCreate)
	if err != nil {
		return s, err
	}
	s.RecordHead = records.Head
	for filename, data := range records.Records {
		name := strings.TrimSuffix(filename, ".publication.json")
		if target != "" && name != target {
			continue
		}
		var value ciCacheRecord
		if err := decodeCICacheJSON(bytes.NewReader(data), &value, true); err != nil {
			s.RecordErrors[name] = err.Error()
			continue
		}
		if value.Key.archiveName() != name {
			s.RecordErrors[name] = "cache record name mismatch"
			continue
		}
		assets := s.Assets
		if counts[name] == 0 {
			assets = []ciCacheAsset{{ID: value.Archive.ID, Name: name, State: "uploaded", Size: value.Archive.Size, Digest: "sha256:" + value.Archive.SHA256}}
		}
		if err := validateCICacheRecord(value, c.Repository, value.Key, value.Fingerprint, value.DependencyFingerprints, assets); err != nil {
			s.RecordErrors[name] = err.Error()
			continue
		}
		s.Records[name] = value
		if counts[name] == 0 {
			s.RecordErrors[name] = "archive missing; record retained for cleanup identity only"
		}
	}

	return s, nil
}

func validateCILocalCacheAsset(asset ciLocalAsset, repository string) error {
	r := asset.Record
	info, err := os.Lstat(asset.Path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != r.Archive.Size || r.Archive.Size <= 0 || r.Archive.Size >= ciCacheSizeLimit {
		return errors.New("cache archive must be a regular file below 2 GiB with the recorded size")
	}
	f, err := os.Open(asset.Path)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != r.Archive.SHA256 {
		return errors.New("cache archive SHA256 mismatch")
	}
	r.Archive.ID = 1
	return validateCICacheRecord(r, repository, r.Key, r.Fingerprint, r.DependencyFingerprints, []ciCacheAsset{{ID: 1, Name: r.Archive.Name, State: "uploaded", Size: r.Archive.Size, Digest: "sha256:" + r.Archive.SHA256}})
}

func (c *ciCacheClient) upload(ctx context.Context, release int64, name, contentType string, body io.Reader) (ciCacheAsset, error) {
	var asset ciCacheAsset
	r, err := c.request(ctx, "POST", c.endpoint(c.UploadBase, fmt.Sprintf("/%d/assets?name=%s", release, url.QueryEscape(name))), "application/vnd.github+json", contentType, body)
	if err != nil {
		return asset, err
	}
	defer r.Body.Close()
	if err := decodeCICacheJSON(r.Body, &asset, false); err != nil {
		return asset, err
	}
	if asset.ID <= 0 || asset.Name != name || asset.State != "uploaded" {
		return asset, errors.New("cache upload returned an invalid asset")
	}
	return asset, nil
}

func (c *ciCacheClient) Put(ctx context.Context, asset ciLocalAsset) (ciCacheRecord, error) {
	var result ciCacheRecord
	if err := validateCILocalCacheAsset(asset, c.Repository); err != nil {
		return result, err
	}
	err := c.retry(ctx, func() error {
		s, err := c.snapshot(ctx, false, asset.Record.Archive.Name)
		if err != nil {
			return err
		}
		r := asset.Record
		name := r.Archive.Name
		if existing, ok := s.Records[name]; ok {
			if validateCICacheRecord(existing, c.Repository, r.Key, r.Fingerprint, r.DependencyFingerprints, s.Assets) == nil {
				result = existing
				return nil
			}
		}
		count := 0
		for _, a := range s.Assets {
			if a.Name == name {
				count++
			}
		}
		if count > 1 {
			return fmt.Errorf("ambiguous managed cache name %s", name)
		}
		if err := preflightCICacheCapacity(s.Assets, []string{name}); err != nil {
			return err
		}
		for _, a := range s.Assets {
			if a.Name != name {
				continue
			}
			response, err := c.request(ctx, "DELETE", c.endpoint(c.APIBase, "/assets/"+strconv.FormatInt(a.ID, 10)), "application/vnd.github+json", "", nil)
			if err != nil {
				return err
			}
			response.Body.Close()
		}
		file, err := os.Open(asset.Path)
		if err != nil {
			return err
		}
		uploaded, uploadErr := c.upload(ctx, s.ReleaseID, name, "application/octet-stream", file)
		closeErr := file.Close()
		if err := errors.Join(uploadErr, closeErr); err != nil {
			return err
		}
		r.Archive.ID = uploaded.ID
		if err := validateCICacheRecord(r, c.Repository, r.Key, r.Fingerprint, r.DependencyFingerprints, []ciCacheAsset{uploaded}); err != nil {
			return err
		}
		body, err := json.Marshal(r)
		if err != nil {
			return err
		}
		confirmed, err := c.Records.Update(ctx, s.RecordHead, name+".publication.json", body)
		if err != nil {
			return err
		}
		var stored ciCacheRecord
		if decodeCICacheJSON(bytes.NewReader(confirmed.Records[name+".publication.json"]), &stored, true) != nil {
			return errors.New("cache record readback failed")
		}
		if err := validateCICacheRecord(stored, c.Repository, r.Key, r.Fingerprint, r.DependencyFingerprints, []ciCacheAsset{uploaded}); err != nil {
			return err
		}
		r = stored

		result = r
		return nil
	})
	return result, err
}

func (c *ciCacheClient) Download(ctx context.Context, record ciCacheRecord, directory string) (ciLocalAsset, error) {
	asset := ciLocalAsset{Record: record}
	if err := record.Key.validate(); err != nil {
		return asset, err
	}
	if record.Archive.Name != record.Key.archiveName() || record.Archive.ID <= 0 || record.Archive.Size <= 0 || record.Archive.Size >= ciCacheSizeLimit || !digestValue.MatchString(record.Archive.SHA256) {
		return asset, errors.New("invalid cache download identity")
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return asset, err
	}
	asset.Path = filepath.Join(directory, record.Archive.Name)
	if _, err := os.Lstat(asset.Path); !os.IsNotExist(err) {
		return asset, errors.New("cache download target already exists")
	}
	err := c.retry(ctx, func() error {
		response, err := c.request(ctx, "GET", c.endpoint(c.APIBase, "/assets/"+strconv.FormatInt(record.Archive.ID, 10)), "application/octet-stream", "", nil)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		file, err := os.CreateTemp(directory, ".cache-download-")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		h := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(file, h), io.LimitReader(response.Body, record.Archive.Size+1))
		closeErr := file.Close()
		if copyErr != nil {
			return &ciCacheHTTPError{Temporary: true}
		}
		if closeErr != nil {
			return closeErr
		}
		if size != record.Archive.Size || hex.EncodeToString(h.Sum(nil)) != record.Archive.SHA256 {
			return errors.New("downloaded cache archive size or SHA256 mismatch")
		}
		return os.Rename(file.Name(), asset.Path)
	})
	return asset, err
}
