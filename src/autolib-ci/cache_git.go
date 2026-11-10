package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

const ciCacheGitMarker = ".autolib-cache.json"

type ciCacheGitSnapshot struct {
	Head    string
	Records map[string][]byte
}

type ciCacheGitStore struct {
	mu                                                       sync.Mutex
	SourceRepository, Repository, Token, URL, APIBase, Login string
	WorkRoot, directory                                      string
	HTTP                                                     *http.Client
}

func newCICacheGitStore(sourceRepository, giteeRepository, token, workRoot string) (*ciCacheGitStore, error) {
	valid := regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	if !ciRepositoryPattern.MatchString(sourceRepository) || !valid.MatchString(giteeRepository) || workRoot == "" || strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("cache records require source repository, GITEE_REPOSITORY, GITEE_TOKEN and work root")
	}
	for _, part := range strings.Split(giteeRepository, "/") {
		if part == "." || part == ".." {
			return nil, errors.New("invalid Gitee cache repository")
		}
	}
	return &ciCacheGitStore{SourceRepository: sourceRepository, Repository: giteeRepository, Token: token, URL: "https://gitee.com/" + giteeRepository + ".git", APIBase: "https://gitee.com/api/v5", WorkRoot: workRoot, HTTP: &http.Client{Timeout: 30 * time.Second}}, nil
}

func runCICacheGitAskpass(args []string, stdout io.Writer) int {
	if os.Getenv("AUTOLIB_CACHE_ASKPASS") != "1" || len(args) != 1 {
		return 1
	}
	var value, address string
	switch {
	case strings.HasPrefix(args[0], "Username for '"):
		address = strings.TrimPrefix(args[0], "Username for '")
		value = os.Getenv("AUTOLIB_CACHE_GIT_LOGIN")
	case strings.HasPrefix(args[0], "Password for '"):
		address = strings.TrimPrefix(args[0], "Password for '")
		value = os.Getenv("AUTOLIB_CACHE_GIT_TOKEN")
	default:
		return 1
	}
	address, ok := strings.CutSuffix(strings.TrimSpace(address), "':")
	parsed, err := url.Parse(address)
	if !ok || err != nil || parsed.Scheme != "https" || parsed.Hostname() != "gitee.com" || (parsed.Port() != "" && parsed.Port() != "443") {
		return 1
	}
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return 1
	}
	if _, err := fmt.Fprintln(stdout, value); err != nil {
		return 1
	}
	return 0
}

func (s *ciCacheGitStore) api(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", s.APIBase+path, nil)
	if err != nil {
		return errors.New("invalid Gitee cache request")
	}
	req.Header.Set("Authorization", "token "+s.Token)
	req.Header.Set("Accept", "application/json")
	client := *s.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Gitee cache request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("Gitee cache HTTP %d", response.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out); err != nil {
		return errors.New("invalid Gitee cache API response")
	}
	return nil
}

type ciCacheGitError struct {
	Operation      string
	Code           int
	Authentication bool
}

func (e *ciCacheGitError) Error() string { return "cache git " + e.Operation + " failed" }

func (s *ciCacheGitStore) git(ctx context.Context, input []byte, extra []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "credential.helper=", "-c", "core.autocrlf=false", "-c", "core.hooksPath="}, args...)...)
	if s.directory != "" {
		cmd.Dir = s.directory
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, errors.New("cache Git askpass executable unavailable")
	}
	env := withoutEnvironment(ciSafeEnvironment(nil), "GIT_ASKPASS", "SSH_ASKPASS", "GIT_TERMINAL_PROMPT")
	cmd.Env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS="+executable, "AUTOLIB_CACHE_ASKPASS=1", "AUTOLIB_CACHE_GIT_LOGIN="+s.Login, "AUTOLIB_CACHE_GIT_TOKEN="+s.Token, "GIT_AUTHOR_NAME=xxautolib", "GIT_AUTHOR_EMAIL=ci@xxautolib.invalid", "GIT_COMMITTER_NAME=xxautolib", "GIT_COMMITTER_EMAIL=ci@xxautolib.invalid")
	cmd.Env = append(cmd.Env, extra...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		code := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		message := strings.ToLower(stderr.String())
		return nil, &ciCacheGitError{Operation: args[0], Code: code, Authentication: strings.Contains(message, "authentication failed") || strings.Contains(message, "returned error: 403") || strings.Contains(message, "returned error: 401")}
	}
	return out, nil
}

func (s *ciCacheGitStore) prepare(ctx context.Context) (string, error) {
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := s.api(ctx, "/user", &user); err != nil {
		return "", err
	}
	if user.ID <= 0 || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(user.Login) {
		return "", errors.New("invalid Gitee cache authenticated identity")
	}
	s.Login = user.Login
	var repo struct {
		ID            int64  `json:"id"`
		FullName      string `json:"full_name"`
		Private       *bool  `json:"private"`
		DefaultBranch string `json:"default_branch"`
		Permission    struct {
			Push bool `json:"push"`
		} `json:"permission"`
	}
	if err := s.api(ctx, "/repos/"+s.Repository, &repo); err != nil {
		return "", err
	}
	if repo.ID <= 0 || repo.FullName != s.Repository || repo.Private == nil || !repo.Permission.Push || repo.DefaultBranch == "" || strings.HasPrefix(repo.DefaultBranch, "-") {
		return "", errors.New("Gitee cache repository identity or push permission mismatch")
	}
	if s.directory == "" {
		if err := os.MkdirAll(s.WorkRoot, 0700); err != nil {
			return "", err
		}
		directory, err := os.MkdirTemp(s.WorkRoot, "cache-git-")
		if err != nil {
			return "", err
		}
		s.directory = directory
		if _, err = s.git(ctx, nil, nil, "init", "--bare", "."); err != nil {
			return "", err
		}
	}
	if _, err := s.git(ctx, nil, nil, "check-ref-format", "refs/heads/"+repo.DefaultBranch); err != nil {
		return "", errors.New("invalid Gitee default branch")
	}
	return repo.DefaultBranch, nil
}

func (s *ciCacheGitStore) readBranch(ctx context.Context) (ciCacheGitSnapshot, error) {
	out, err := s.git(ctx, nil, nil, "ls-remote", "--exit-code", s.URL, "refs/heads/"+ciCacheTag)
	var remote *ciCacheGitError
	if errors.As(err, &remote) && remote.Code == 2 {
		return ciCacheGitSnapshot{}, nil
	}
	if err != nil {
		return ciCacheGitSnapshot{}, err
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || !commitPattern.MatchString(fields[0]) || fields[1] != "refs/heads/"+ciCacheTag {
		return ciCacheGitSnapshot{}, errors.New("invalid cache branch reference")
	}
	if _, err = s.git(ctx, nil, nil, "fetch", "--no-tags", s.URL, "refs/heads/"+ciCacheTag); err != nil {
		return ciCacheGitSnapshot{}, err
	}
	head, err := s.git(ctx, nil, nil, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return ciCacheGitSnapshot{}, err
	}
	return s.readTree(ctx, strings.TrimSpace(string(head)))
}

func ciCacheRecordFilename(name string) bool {
	archive, ok := strings.CutSuffix(name, ".publication.json")
	if !ok || strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	for _, platform := range []string{"windows-x86_64", "linux-x86_64", "linux-aarch64", "darwin-aarch64"} {
		if archive == ciImporterExecutable(platform)+".tar.xz" {
			return true
		}
	}
	for _, platform := range []string{"windows-x86_64", "linux-x86_64", "linux-aarch64", "darwin-aarch64", "any-any"} {
		prefix, ok := strings.CutSuffix(archive, "-"+platform+".tar.zst")
		if ok && strings.Contains(prefix, "-") && ciModuleName.MatchString(prefix) {
			return true
		}
	}
	return false
}

func (s *ciCacheGitStore) readTree(ctx context.Context, head string) (ciCacheGitSnapshot, error) {
	result := ciCacheGitSnapshot{Head: head, Records: map[string][]byte{}}
	tree, err := s.git(ctx, nil, nil, "ls-tree", "-r", "-t", "-z", head)
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	marker := false
	for _, line := range bytes.Split(tree, []byte{0}) {
		if len(line) == 0 {
			continue
		}
		meta, name, ok := strings.Cut(string(line), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || (fields[0] != "100644" && fields[0] != "100755") || fields[1] != "blob" || seen[strings.ToLower(name)] || (name != ciCacheGitMarker && !ciCacheRecordFilename(name)) {
			return result, errors.New("invalid cache branch tree")
		}
		seen[strings.ToLower(name)] = true
		data, err := s.git(ctx, nil, nil, "cat-file", "blob", fields[2])
		if err != nil {
			return result, err
		}
		if name == ciCacheGitMarker {
			var identity struct {
				Type     string `json:"type"`
				Schema   int    `json:"schema"`
				Upstream string `json:"upstream"`
			}
			if err := decodeCICacheJSON(bytes.NewReader(data), &identity, true); err != nil || identity.Type != "xxautolib-build-cache" || identity.Schema != 1 || identity.Upstream != "https://github.com/"+s.SourceRepository {
				return result, errors.New("cache branch identity mismatch")
			}
			marker = true
		} else {
			result.Records[name] = data
		}
	}
	if !marker {
		return result, errors.New("cache branch identity missing")
	}
	return result, nil
}

func (s *ciCacheGitStore) Snapshot(ctx context.Context, allowCreate bool) (ciCacheGitSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	branch, err := s.prepare(ctx)
	if err != nil {
		return ciCacheGitSnapshot{}, err
	}
	snapshot, err := s.readBranch(ctx)
	if err != nil || snapshot.Head != "" {
		return snapshot, err
	}
	if !allowCreate {
		return snapshot, errors.New("Gitee cache branch is missing")
	}
	if _, err = s.git(ctx, nil, nil, "fetch", "--no-tags", s.URL, "refs/heads/"+branch); err != nil {
		return snapshot, err
	}
	data, err := s.git(ctx, nil, nil, "show", "FETCH_HEAD:.autolib-repository.json")
	if err != nil {
		return snapshot, errors.New("Gitee default branch identity missing")
	}
	var identity struct {
		Type     string `json:"type"`
		Schema   int    `json:"schema"`
		Upstream string `json:"upstream"`
	}
	if err = json.Unmarshal(data, &identity); err != nil || identity.Type != "xxautolib-artifacts" || identity.Schema != 2 || identity.Upstream != "https://github.com/"+s.SourceRepository {
		return snapshot, errors.New("Gitee default branch identity mismatch")
	}
	marker, _ := json.Marshal(map[string]any{"type": "xxautolib-build-cache", "schema": 1, "upstream": "https://github.com/" + s.SourceRepository})
	head, err := s.commitFile(ctx, "", ciCacheGitMarker, append(marker, '\n'))
	if err != nil {
		return snapshot, err
	}
	if _, err = s.git(ctx, nil, nil, "push", s.URL, head+":refs/heads/"+ciCacheTag); err != nil {
		remote, readErr := s.readBranch(ctx)
		if readErr == nil && remote.Head != "" {
			return remote, nil
		}
		return snapshot, err
	}
	return s.readBranch(ctx)
}

func (s *ciCacheGitStore) commitFile(ctx context.Context, parent, name string, data []byte) (string, error) {
	index := filepath.Join(s.directory, "cache-index")
	if err := os.Remove(index); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	env := []string{"GIT_INDEX_FILE=" + index}
	args := []string{"read-tree", "--empty"}
	if parent != "" {
		args = []string{"read-tree", parent}
	}
	if _, err := s.git(ctx, nil, env, args...); err != nil {
		return "", err
	}
	if data == nil {
		if _, err := s.git(ctx, []byte("0 "+strings.Repeat("0", 40)+"\t"+name+"\x00"), env, "update-index", "-z", "--index-info"); err != nil {
			return "", err
		}
	} else {
		blob, err := s.git(ctx, data, nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		if _, err = s.git(ctx, nil, env, "update-index", "--add", "--cacheinfo", "100644,"+strings.TrimSpace(string(blob))+","+name); err != nil {
			return "", err
		}
	}
	tree, err := s.git(ctx, nil, env, "write-tree")
	if err != nil {
		return "", err
	}
	args = []string{"commit-tree", strings.TrimSpace(string(tree)), "-m", "Update build cache record"}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	head, err := s.git(ctx, nil, nil, args...)
	return strings.TrimSpace(string(head)), err
}

func sameCICacheGitRecord(left, right []byte) bool {
	if bytes.Equal(left, right) {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	var a, b ciCacheRecord
	return decodeCICacheJSON(bytes.NewReader(left), &a, true) == nil && decodeCICacheJSON(bytes.NewReader(right), &b, true) == nil && reflect.DeepEqual(a, b)
}

func (s *ciCacheGitStore) Update(ctx context.Context, baselineHead, name string, desired []byte) (ciCacheGitSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !commitPattern.MatchString(baselineHead) || !ciCacheRecordFilename(name) {
		return ciCacheGitSnapshot{}, errors.New("cache update requires a fixed baseline and record filename")
	}
	if _, err := s.prepare(ctx); err != nil {
		return ciCacheGitSnapshot{}, err
	}
	var baseline ciCacheGitSnapshot
	for round := 0; round < 3; round++ {
		current, err := s.readBranch(ctx)
		if err != nil {
			return current, err
		}
		if current.Head == "" {
			return current, errors.New("cache branch disappeared")
		}
		if _, err = s.git(ctx, nil, nil, "merge-base", "--is-ancestor", baselineHead, current.Head); err != nil {
			return current, errors.New("cache baseline is outside current branch history")
		}
		if baseline.Head == "" {
			baseline, err = s.readTree(ctx, baselineHead)
			if err != nil {
				return current, err
			}
		}
		if sameCICacheGitRecord(current.Records[name], desired) {
			return current, nil
		}
		if !sameCICacheGitRecord(current.Records[name], baseline.Records[name]) {
			return current, errors.New("selected cache record changed since baseline")
		}
		head, err := s.commitFile(ctx, current.Head, name, desired)
		if err != nil {
			return current, err
		}
		_, pushErr := s.git(ctx, nil, nil, "push", s.URL, head+":refs/heads/"+ciCacheTag)
		if pushErr != nil {
			var remote *ciCacheGitError
			if !errors.As(pushErr, &remote) || remote.Authentication {
				return current, pushErr
			}
		}
		confirmed, err := s.readBranch(ctx)
		if err != nil {
			return confirmed, err
		}
		if confirmed.Head != "" && sameCICacheGitRecord(confirmed.Records[name], desired) {
			return confirmed, nil
		}
	}
	return ciCacheGitSnapshot{}, errors.New("cache record push was not confirmed after three rounds")
}
