package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

type metadataSource struct{ RepositoryURL, DownloadRoot string }

func parseMetadataSource(raw string) (metadataSource, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || (strings.ToLower(u.Host) != "github.com" && strings.ToLower(u.Host) != "gitee.com") || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return metadataSource{}, fmt.Errorf("expected public GitHub or Gitee Release download root, got %q", raw)
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	component := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	if len(parts) != 5 || !component.MatchString(parts[0]) || !component.MatchString(parts[1]) || parts[2] != "releases" || parts[3] != "download" || parts[4] == "" {
		return metadataSource{}, fmt.Errorf("expected https://<github.com|gitee.com>/<owner>/<repo>/releases/download/<encoded-tag>, got %q", raw)
	}
	tag, err := url.PathUnescape(parts[4])
	if err != nil || strings.ContainsFunc(tag, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return metadataSource{}, fmt.Errorf("invalid Release tag in %q", raw)
	}
	base := "https://" + strings.ToLower(u.Host) + "/" + parts[0] + "/" + parts[1]
	return metadataSource{RepositoryURL: base + ".git", DownloadRoot: base + "/releases/download/" + url.PathEscape(tag)}, nil
}

func metadataPackageAsset(root string, source metadataSource, id packageIdentity) (releaseAsset, packageIdentity, error) {
	if err := validateIdentity(id); err != nil {
		return releaseAsset{}, packageIdentity{}, err
	}
	if err := exactPackageEntry(root, id.Name, true); err != nil {
		return releaseAsset{}, packageIdentity{}, err
	}
	parent := filepath.Join(root, id.Name)
	if err := validatePackageVersionEntries(parent, id.Version); err != nil {
		return releaseAsset{}, packageIdentity{}, err
	}
	if err := exactPackageEntry(parent, id.Version, true); err != nil {
		return releaseAsset{}, packageIdentity{}, err
	}
	directory := packageDirectory(root, id)
	identities := []packageIdentity{id}
	if id.OS != "any" {
		identities = append(identities, packageIdentity{Name: id.Name, Version: id.Version, OS: "any", Arch: "any"})
	}
	var selected releaseAsset
	var actual packageIdentity
	for _, candidate := range identities {
		name := candidate.Name + "-" + candidate.Version + "-" + candidate.OS + "-" + candidate.Arch + ".tar.zst"
		if err := exactPackageEntry(directory, name+".sha256", false); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return releaseAsset{}, packageIdentity{}, err
		}
		path := filepath.Join(directory, name+".sha256")
		content, err := os.ReadFile(path)
		if err != nil {
			return releaseAsset{}, packageIdentity{}, err
		}
		digest, err := parsePackageDigest(path, content)
		if err != nil {
			return releaseAsset{}, packageIdentity{}, err
		}
		if selected.Name != "" {
			return releaseAsset{}, packageIdentity{}, fmt.Errorf("%s: ambiguous package digests for %s and %s", directory, selected.Name, name)
		}
		selected = releaseAsset{Name: name, URL: source.DownloadRoot + "/" + url.PathEscape(name), SHA256: digest}
		actual = candidate
	}
	if selected.Name == "" {
		return releaseAsset{}, packageIdentity{}, &os.PathError{Op: "find package digest", Path: directory, Err: os.ErrNotExist}
	}
	return selected, actual, nil
}
