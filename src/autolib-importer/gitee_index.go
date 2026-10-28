package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

type giteeIndexEntry struct {
	SHA256 string
	ID     int64
}

func uniqueJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		if delimiter == '{' {
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate or invalid JSON key %v", key)
				}
				seen[name] = true
				if err := value(); err != nil {
					return err
				}
			}
		} else if delimiter == '[' {
			for decoder.More() {
				if err := value(); err != nil {
					return err
				}
			}
		} else {
			return fmt.Errorf("invalid JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON content")
	}
	return json.Unmarshal(raw, destination)
}

func parseGiteeIndex(body string) (map[string]giteeIndexEntry, error) {
	const start = "<!-- autolib-gitee-sync:start -->"
	const end = "<!-- autolib-gitee-sync:end -->"
	bad := func() (map[string]giteeIndexEntry, error) {
		return nil, fmt.Errorf("invalid or missing Gitee body SHA256 index")
	}
	if strings.Count(body, start) != 1 || strings.Count(body, end) != 1 {
		return bad()
	}
	a, b := strings.Index(body, start), strings.Index(body, end)
	if a >= b {
		return bad()
	}
	for _, marker := range []struct {
		at   int
		text string
	}{{a, start}, {b, end}} {
		tail := body[marker.at+len(marker.text):]
		if (marker.at > 0 && body[marker.at-1] != '\n') || (tail != "" && !strings.HasPrefix(tail, "\n") && !strings.HasPrefix(tail, "\r\n")) {
			return bad()
		}
	}
	block := regexp.MustCompile("(?s)^\\s*```json\\r?\\n(.*?)\\r?\\n```\\s*$").FindStringSubmatch(body[a+len(start) : b])
	if block == nil {
		return bad()
	}
	var document map[string]json.RawMessage
	if err := uniqueJSON([]byte(block[1]), &document); err != nil || len(document) != 1 || document["files"] == nil {
		return bad()
	}
	var files map[string]json.RawMessage
	if err := json.Unmarshal(document["files"], &files); err != nil || files == nil {
		return bad()
	}
	result := map[string]giteeIndexEntry{}
	for name, raw := range files {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || name == "" || len(fields) != 2 || fields["sha256"] == nil || fields["gitee_asset_id"] == nil {
			return bad()
		}
		var digest string
		var id int64
		if json.Unmarshal(fields["sha256"], &digest) != nil || len(digest) != 64 || json.Unmarshal(fields["gitee_asset_id"], &id) != nil || id <= 0 {
			return bad()
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return bad()
		}
		result[name] = giteeIndexEntry{strings.ToLower(digest), id}
	}
	return result, nil
}
