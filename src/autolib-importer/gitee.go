package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

type giteeSource struct{ Owner, Repository, Tag string }

func parseGiteeSource(raw string) (giteeSource, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "gitee.com") || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return giteeSource{}, fmt.Errorf("expected public Gitee Release download root, got %q", raw)
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	component := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	if len(parts) != 5 || !component.MatchString(parts[0]) || !component.MatchString(parts[1]) || parts[2] != "releases" || parts[3] != "download" || parts[4] == "" {
		return giteeSource{}, fmt.Errorf("expected https://gitee.com/<owner>/<repo>/releases/download/<encoded-tag>, got %q", raw)
	}
	tag, err := url.PathUnescape(parts[4])
	if err != nil || strings.ContainsAny(tag, "\x00\r\n") {
		return giteeSource{}, fmt.Errorf("invalid Gitee tag in %q", raw)
	}
	return giteeSource{parts[0], parts[1], tag}, nil
}

func (s giteeSource) releaseURL() string {
	return "https://gitee.com/" + s.Owner + "/" + s.Repository + "/releases/download/" + url.PathEscape(s.Tag)
}
func (s giteeSource) apiRoot() string {
	return "https://gitee.com/api/v5/repos/" + s.Owner + "/" + s.Repository + "/releases/"
}

type giteeSourceClient struct {
	Source giteeSource
	HTTP   *http.Client
}

func giteeRelease(raw json.RawMessage, source giteeSource) (int64, string, error) {
	var release struct {
		ID   int64   `json:"id"`
		Tag  string  `json:"tag_name"`
		Body *string `json:"body"`
	}
	if err := json.Unmarshal(raw, &release); err != nil || release.ID <= 0 || release.Tag != source.Tag || release.Body == nil {
		return 0, "", fmt.Errorf("Gitee Release is incomplete or does not match %s", source.releaseURL())
	}
	if _, err := parseGiteeIndex(*release.Body); err != nil {
		return 0, "", err
	}
	return release.ID, *release.Body, nil
}

func validateGiteeSnapshot(snapshot releaseSnapshot, source giteeSource) error {
	actual, err := parseGiteeSource(snapshot.Source)
	if err != nil || actual != source || snapshot.Platform != "gitee" || len(snapshot.Pages) == 0 {
		return fmt.Errorf("Gitee response has mismatched platform/source or missing attachment pages")
	}
	if _, _, err := giteeRelease(snapshot.Release, source); err != nil {
		return err
	}
	for i, raw := range snapshot.Pages {
		page, err := releasePage(raw)
		if err != nil {
			return fmt.Errorf("Gitee attachments: %w", err)
		}
		if (i < len(snapshot.Pages)-1 && len(page) != 100) || (i == len(snapshot.Pages)-1 && len(page) >= 100) {
			return fmt.Errorf("Gitee response has incomplete attachment pagination")
		}
	}
	return nil
}

func (c *giteeSourceClient) snapshot(ctx context.Context, path string) (releaseSnapshot, error) {
	snapshot, exists, err := readReleaseSnapshot(path)
	if err != nil {
		return releaseSnapshot{}, err
	}
	if exists {
		return snapshot, validateGiteeSnapshot(snapshot, c.Source)
	}
	snapshot = releaseSnapshot{Platform: "gitee", Source: c.Source.releaseURL()}
	snapshot.Release, err = readReleaseJSON(ctx, c.HTTP, c.Source.apiRoot()+"tags/"+url.PathEscape(c.Source.Tag), "application/json")
	if err != nil {
		return releaseSnapshot{}, err
	}
	id, _, err := giteeRelease(snapshot.Release, c.Source)
	if err != nil {
		return releaseSnapshot{}, err
	}
	for number := 1; ; number++ {
		page, err := readReleaseJSON(ctx, c.HTTP, fmt.Sprintf("%s%d/attach_files?page=%d&per_page=100", c.Source.apiRoot(), id, number), "application/json")
		if err != nil {
			return releaseSnapshot{}, err
		}
		entries, err := releasePage(page)
		if err != nil {
			return releaseSnapshot{}, err
		}
		if len(entries) > 100 {
			return releaseSnapshot{}, fmt.Errorf("Gitee attachments exceed requested page size")
		}
		snapshot.Pages = append(snapshot.Pages, page)
		if len(entries) < 100 {
			break
		}
	}
	if err := validateGiteeSnapshot(snapshot, c.Source); err != nil {
		return releaseSnapshot{}, err
	}
	if err := writeReleaseSnapshot(path, snapshot); err != nil {
		return releaseSnapshot{}, err
	}
	return snapshot, nil
}

func (c *giteeSourceClient) selectAsset(snapshot releaseSnapshot, names []string) (releaseAsset, error) {
	if err := validateGiteeSnapshot(snapshot, c.Source); err != nil {
		return releaseAsset{}, err
	}
	_, body, err := giteeRelease(snapshot.Release, c.Source)
	if err != nil {
		return releaseAsset{}, err
	}
	index, err := parseGiteeIndex(body)
	if err != nil {
		return releaseAsset{}, err
	}
	var matches []json.RawMessage
	for _, raw := range snapshot.Pages {
		page, _ := releasePage(raw)
		for _, entry := range page {
			var name struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(entry, &name); err != nil {
				return releaseAsset{}, fmt.Errorf("invalid Gitee attachment name: %w", err)
			}
			if slices.Contains(names, name.Name) {
				matches = append(matches, entry)
			}
		}
	}
	if len(matches) != 1 {
		return releaseAsset{}, fmt.Errorf("Gitee attachments %v: expected one candidate, found %d", names, len(matches))
	}
	var selected struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Size *int64 `json:"size"`
		URL  string `json:"browser_download_url"`
	}
	if err := json.Unmarshal(matches[0], &selected); err != nil || selected.ID <= 0 || selected.Size == nil || *selected.Size < 0 {
		return releaseAsset{}, fmt.Errorf("invalid Gitee attachment identity")
	}
	if err := validateReleaseHTTPS(selected.URL); err != nil {
		return releaseAsset{}, err
	}
	expected, ok := index[selected.Name]
	if !ok || expected.ID != selected.ID {
		return releaseAsset{}, fmt.Errorf("Gitee attachment %s: missing SHA256 index entry or mismatched attachment ID", selected.Name)
	}
	return releaseAsset{ID: selected.ID, Name: selected.Name, URL: selected.URL, SHA256: expected.SHA256, Accept: "application/octet-stream"}, nil
}
