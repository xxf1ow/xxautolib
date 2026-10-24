package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type ciRunIdentity struct {
	Repository  string `json:"repository"`
	Ref         string `json:"ref"`
	SHA         string `json:"sha"`
	CheckoutSHA string `json:"checkout_sha"`
	RunID       string `json:"run_id"`
	Attempt     string `json:"attempt"`
}

type ciWriteAuthorization struct {
	client *githubClient
	sha    string
}

type ciUploadOptions struct{ File, Name, Label, SHA256 string }

func authorizeCIWrite(ctx context.Context, client *githubClient, sourceRoot string, run ciRunIdentity) (*ciWriteAuthorization, error) {
	if client == nil || run.Repository != ciGitHubRepository || !cacheCommitPattern.MatchString(run.SHA) || run.CheckoutSHA != run.SHA {
		return nil, errors.New("Release write requires the target repository and matching event/checkout SHA")
	}
	command := exec.CommandContext(ctx, "git", "-C", sourceRoot, "rev-parse", "HEAD")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read actual checkout HEAD: %w", err)
	}
	if strings.TrimSpace(string(output)) != run.SHA {
		return nil, errors.New("actual checkout HEAD does not match run SHA")
	}
	address, err := client.apiURL("repos/" + ciGitHubRepository)
	if err != nil {
		return nil, err
	}
	var repo struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := client.getJSON(ctx, address, &repo); err != nil {
		return nil, err
	}
	if repo.FullName != ciGitHubRepository || repo.DefaultBranch == "" || run.Ref != "refs/heads/"+repo.DefaultBranch {
		return nil, errors.New("Release write is restricted to the repository default branch")
	}
	if client.Token == "" {
		return nil, errors.New("Release write requires GITHUB_TOKEN")
	}
	return &ciWriteAuthorization{client: client, sha: run.SHA}, nil
}

func requireCIWrite(client *githubClient, auth *ciWriteAuthorization) error {
	if client == nil || auth == nil || auth.client != client || !cacheCommitPattern.MatchString(auth.sha) || client.Token == "" {
		return errors.New("Release write authorization is required")
	}
	return nil
}

// All supported writes are coordinated by the workflow's fixed Release concurrency group.
func (c *githubClient) write(ctx context.Context, auth *ciWriteAuthorization, method, address, contentType string, body io.Reader, size int64, output any) error {
	if err := requireCIWrite(c, auth); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return err
	}
	request.ContentLength = size
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("User-Agent", "xxautolib-ci")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return fmt.Errorf("GitHub write request failed: %w", err)
	}
	defer response.Body.Close()
	if method == http.MethodDelete {
		if response.StatusCode != http.StatusNoContent {
			return githubStatusError(response)
		}
		return nil
	}
	if response.StatusCode != http.StatusCreated {
		return githubStatusError(response)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode GitHub write response: %w", err)
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return errors.New("GitHub write response has trailing JSON")
	}
	return nil
}

func initCIRelease(ctx context.Context, client *githubClient, auth *ciWriteAuthorization) (githubRelease, error) {
	if err := requireCIWrite(client, auth); err != nil {
		return githubRelease{}, err
	}
	release, found, err := client.release(ctx)
	if err != nil || found {
		return release, err
	}
	address, err := client.apiURL("repos/" + ciGitHubRepository + "/git/ref/tags/" + ciGitHubReleaseTag)
	if err != nil {
		return githubRelease{}, err
	}
	var tag struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := client.getJSON(ctx, address, &tag); err != nil {
		var status *githubHTTPError
		if !errors.As(err, &status) || status.Status != http.StatusNotFound {
			return githubRelease{}, err
		}
	} else if tag.Ref != "refs/tags/"+ciGitHubReleaseTag || !cacheCommitPattern.MatchString(tag.Object.SHA) {
		return githubRelease{}, errors.New("fixed Release tag metadata is invalid")
	}
	// Creating a Release binds a missing tag; GitHub leaves an existing tag unchanged.
	body, _ := json.Marshal(map[string]any{"tag_name": ciGitHubReleaseTag, "target_commitish": auth.sha, "name": ciGitHubReleaseTag, "draft": false, "prerelease": false})
	address, err = client.apiURL("repos/" + ciGitHubRepository + "/releases")
	if err != nil {
		return githubRelease{}, err
	}
	if err := client.write(ctx, auth, http.MethodPost, address, "application/json", bytes.NewReader(body), int64(len(body)), &release); err != nil {
		return githubRelease{}, err
	}
	if release.ID <= 0 || release.TagName != ciGitHubReleaseTag || !sameOrigin(client.BaseURL, release.AssetsURL) {
		return githubRelease{}, errors.New("created Release metadata is invalid")
	}
	return release, nil
}

func ciPublishAssetKind(name string) (isPackage, valid bool) {
	if name == "queue-any-any.tar.zst" {
		return true, true
	}
	for _, platform := range ciPlatforms {
		for _, module := range []string{"lz4", "flann"} {
			if name == module+"-"+platform+".tar.zst" {
				return true, true
			}
		}
		suffix := ""
		if strings.HasPrefix(platform, "windows-") {
			suffix = ".exe"
		}
		if name == "autolib-importer-"+platform+suffix {
			return false, true
		}
	}
	return false, false
}

func uploadCIAsset(ctx context.Context, client *githubClient, auth *ciWriteAuthorization, release githubRelease, o ciUploadOptions) (githubAsset, error) {
	if err := requireCIWrite(client, auth); err != nil {
		return githubAsset{}, err
	}
	isPackage, valid := ciPublishAssetKind(o.Name)
	if !valid || filepath.Base(o.File) != o.Name || !cacheDigestPattern.MatchString(o.SHA256) {
		return githubAsset{}, errors.New("upload requires a fixed asset name and local SHA256")
	}
	if release.ID <= 0 || release.TagName != ciGitHubReleaseTag || !sameOrigin(client.BaseURL, release.AssetsURL) {
		return githubAsset{}, errors.New("upload requires the fixed Release")
	}
	if isPackage {
		var label ciAssetLabel
		if err := decodeStrictJSON(strings.NewReader(o.Label), &label); err != nil || strings.TrimSpace(label.BuildID) == "" || label.Commit != auth.sha {
			return githubAsset{}, errors.New("package upload requires build ID and authorized commit label")
		}
	} else if o.Label != "" {
		return githubAsset{}, errors.New("importer upload must not carry package production metadata")
	}
	file, err := os.Open(o.File)
	if err != nil {
		return githubAsset{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return githubAsset{}, err
	}
	if !info.Mode().IsRegular() {
		return githubAsset{}, errors.New("upload input must be a regular file")
	}
	digest, err := hashReader(file)
	if err != nil {
		return githubAsset{}, err
	}
	if !strings.EqualFold(digest, o.SHA256) {
		return githubAsset{}, errors.New("upload input differs from verified SHA256")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return githubAsset{}, err
	}
	assets, err := client.releaseAssets(ctx, release)
	if err != nil {
		return githubAsset{}, err
	}
	var old *githubAsset
	for i := range assets {
		if assets[i].Name == o.Name {
			if old != nil {
				return githubAsset{}, errors.New("multiple assets have the selected fixed name")
			}
			old = &assets[i]
		}
	}
	if old != nil {
		if old.ID <= 0 {
			return githubAsset{}, errors.New("selected old asset has an invalid ID")
		}
		address, err := client.apiURL("repos/" + ciGitHubRepository + "/releases/assets/" + strconv.FormatInt(old.ID, 10))
		if err != nil {
			return githubAsset{}, err
		}
		if err := client.write(ctx, auth, http.MethodDelete, address, "application/json", nil, 0, nil); err != nil {
			return githubAsset{}, err
		}
	}
	address, err := url.Parse(strings.TrimRight(client.UploadURL, "/") + "/repos/" + ciGitHubRepository + "/releases/" + strconv.FormatInt(release.ID, 10) + "/assets")
	if err != nil {
		return githubAsset{}, err
	}
	query := url.Values{"name": {o.Name}}
	if o.Label != "" {
		query.Set("label", o.Label)
	}
	address.RawQuery = query.Encode()
	var asset githubAsset
	if err := client.write(ctx, auth, http.MethodPost, address.String(), "application/octet-stream", file, info.Size(), &asset); err != nil {
		return githubAsset{}, err
	}
	if asset.ID <= 0 || asset.Name != o.Name || asset.State != "uploaded" || asset.Label != o.Label || !digestMatches(asset.Digest, digest) {
		return githubAsset{}, errors.New("uploaded asset facts differ from verified input")
	}
	return asset, nil
}

func runCIInit(args []string, stdout, stderr io.Writer, client *githubClient) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	source := flags.String("source-root", "", "repository source root")
	identityFile := flags.String("run-identity", "", "run identity JSON file")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if *source == "" || *identityFile == "" || flags.NArg() != 0 {
		return fail(errors.New("init requires --source-root and --run-identity, without positional arguments"))
	}
	var run ciRunIdentity
	if err := decodeStrictJSONFile(*identityFile, &run); err != nil {
		return fail(err)
	}
	auth, err := authorizeCIWrite(context.Background(), client, *source, run)
	if err != nil {
		return fail(err)
	}
	release, err := initCIRelease(context.Background(), client, auth)
	if err != nil {
		return fail(err)
	}
	if err := json.NewEncoder(stdout).Encode(release); err != nil {
		return fail(err)
	}
	return 0
}
