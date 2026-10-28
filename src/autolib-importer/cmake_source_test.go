package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCMakeReleaseSourceProtocol(t *testing.T) {
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := filepath.Abs("../autolib.cmake")
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, body string, valid bool) {
		t.Helper()
		path := putTestFile(t, filepath.Join(t.TempDir(), "case.cmake"), "cmake_minimum_required(VERSION 3.20)\ninclude([==["+filepath.ToSlash(entry)+"]==])\n"+body)
		out, err := exec.Command(cmake, "-P", path).CombinedOutput()
		if (err == nil) != valid {
			t.Fatalf("CMake valid=%v: %v\n%s", valid, err, out)
		}
	}
	cases := readSourceCases(t)
	for _, tc := range cases.Sources {
		t.Run("source/"+tc.Name, func(t *testing.T) {
			run(t, fmt.Sprintf("_autolib_release_source([==[%s]==])\nif(NOT platform STREQUAL [==[%s]==] OR NOT tag STREQUAL [==[%s]==])\nmessage(FATAL_ERROR incorrect_source)\nendif()\n", tc.Input, tc.Platform, tc.Tag), tc.Valid)
		})
	}
	for _, tc := range cases.Indexes {
		t.Run("index/"+tc.Name, func(t *testing.T) {
			body := fmt.Sprintf("_autolib_gitee_index(index [==[%s]==])\n", tc.Body)
			if tc.Valid && tc.ID != 0 {
				body += fmt.Sprintf("string(JSON id GET \"${index}\" files demo-linux-aarch64.tar.zst gitee_asset_id)\nstring(JSON digest GET \"${index}\" files demo-linux-aarch64.tar.zst sha256)\nstring(TOLOWER \"${digest}\" digest)\nif(NOT id STREQUAL \"%d\" OR NOT digest STREQUAL \"%s\")\nmessage(FATAL_ERROR incorrect_index)\nendif()\n", tc.ID, tc.SHA256)
			}
			run(t, body, tc.Valid)
		})
	}
	for _, tc := range cases.Selections {
		t.Run("selection/"+tc.Name, func(t *testing.T) {
			snapshot, err := json.Marshal(tc.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf("_autolib_release_source([==[%s]==])\n_autolib_gitee_select([==[%s]==] [==[%s]==])\n", tc.Source, snapshot, strings.Join(tc.Names, ";"))
			if tc.Valid {
				body += fmt.Sprintf("if(NOT asset_name STREQUAL [==[%s]==] OR NOT asset_id STREQUAL \"%d\" OR NOT digest STREQUAL \"%s\" OR NOT url STREQUAL \"https://gitee.com/download/demo\")\nmessage(FATAL_ERROR incorrect_asset)\nendif()\n", tc.AssetName, tc.ID, tc.SHA256)
			}
			run(t, body, tc.Valid)
		})
	}
}
