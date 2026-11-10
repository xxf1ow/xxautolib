package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionedMetadataSourceParity(t *testing.T) {
	var cases struct {
		Sources []struct {
			Name, Input   string
			Valid         bool
			RepositoryURL string `json:"repository_url"`
		} `json:"metadata_sources"`
	}
	if err := json.Unmarshal([]byte(readTestFile(t, "../../tests/autolib/release-source-cases.json")), &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases.Sources) == 0 {
		t.Fatal("missing shared source cases")
	}
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := filepath.Abs("../autolib.cmake")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases.Sources {
		t.Run(tc.Name, func(t *testing.T) {
			goErr := validateMetadataSource(tc.Input)
			root := t.TempDir()
			script, result := filepath.Join(root, "source.cmake"), filepath.Join(root, "source.result")
			putTestFile(t, script, fmt.Sprintf("cmake_minimum_required(VERSION 3.20)\ninclude([==[%s]==])\n_autolib_metadata_source([==[%s]==] repository)\nfile(WRITE [==[%s]==] \"${repository}\")\n", filepath.ToSlash(entry), tc.Input, filepath.ToSlash(result)))
			out, cmakeErr := exec.Command(cmake, "-P", script).CombinedOutput()
			goValid, cmakeValid := goErr == nil, cmakeErr == nil
			if goValid != cmakeValid || goValid != tc.Valid {
				t.Fatalf("source parity failed: %s: Go=%v CMake=%v\n%s", tc.Name, goErr, cmakeErr, out)
			}
			if !tc.Valid && (!strings.Contains(string(out), "AutoLib") || strings.Contains(string(out), "Unknown CMake command")) {
				t.Fatalf("invalid source did not reach AutoLib validation: %s: %v\n%s", tc.Name, cmakeErr, out)
			}
			if tc.Valid && readTestFile(t, result) != tc.RepositoryURL {
				t.Fatalf("repository differs: expected=%s CMake=%s", tc.RepositoryURL, readTestFile(t, result))
			}
		})
	}
}

func TestCMakeLocalSourceDefaults(t *testing.T) {
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cache := filepath.Join(root, "中文 cache")
	entry := putTestFile(t, filepath.Join(cache, "autolib.cmake"), readTestFile(t, "../autolib.cmake"))
	for _, remote := range []string{"", "https://example.invalid/reserved"} {
		script := putTestFile(t, filepath.Join(root, "check.cmake"), `cmake_minimum_required(VERSION 3.20)
set(AUTOLIB_REMOTE_ROOT [==[`+remote+`]==])
include([==[`+filepath.ToSlash(entry)+`]==])
get_property(cache GLOBAL PROPERTY _AUTOLIB_CACHE_ROOT)
get_property(remote GLOBAL PROPERTY _AUTOLIB_RELEASE_ROOT)
if(NOT cache STREQUAL [==[`+filepath.ToSlash(cache)+`]==] OR NOT remote STREQUAL "")
message(FATAL_ERROR "entry source defaults or actual cache directory differ")
endif()
`)
		if output, err := exec.Command(cmake, "-P", script).CombinedOutput(); err != nil {
			t.Fatalf("include with reserved remote %q: %v\n%s", remote, err, output)
		}
	}
}
