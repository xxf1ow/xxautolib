package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func (f *fixture) archive(files map[string]string) []byte {
	f.t.Helper()
	dir := filepath.Join(f.t.TempDir(), "archive")
	for path, body := range files {
		putFile(f.t, filepath.Join(dir, "fixture", path), body)
	}
	cmd := exec.Command(f.context.CMakeCommand, "-E", "tar", "cJf", "input.tar.xz", "fixture")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("archive: %v %s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(dir, "input.tar.xz"))
	if err != nil {
		f.t.Fatal(err)
	}
	return data
}

func archiveSource(urls []string, data []byte) string {
	quoted := []string{}
	for _, url := range urls {
		quoted = append(quoted, "'"+url+"'")
	}
	return fmt.Sprintf("URL=[%s]\nURL_HASH='SHA256=%x'\n", strings.Join(quoted, ","), sha256.Sum256(data))
}

func TestArchivePipeline(t *testing.T) {
	for _, mode := range []string{"primary", "resource", "local", "404 fallback", "hash fallback", "all missing", "all wrong", "cache"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			data := f.archive(map[string]string{"header.h": "trusted", "lib/runtime": "runtime", "LICENSE": "license", "CMakeLists.txt": "cmake_minimum_required(VERSION 3.20)\nproject(child NONE)\ninstall(FILES header.h DESTINATION include)\n"})
			var mu sync.Mutex
			requests := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.URL.Path)
				mu.Unlock()
				if strings.HasPrefix(r.URL.Path, "/missing") {
					http.NotFound(w, r)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/wrong") {
					_, _ = w.Write([]byte("wrong digest"))
					return
				}
				_, _ = w.Write(data)
			}))
			t.Cleanup(server.Close)
			urls := []string{server.URL + "/good.tar.xz", server.URL + "/backup.tar.xz"}
			switch mode {
			case "404 fallback":
				urls[0] = server.URL + "/missing.tar.xz"
			case "hash fallback":
				urls[0] = server.URL + "/wrong.tar.xz"
			case "all missing":
				urls = []string{server.URL + "/missing.tar.xz", server.URL + "/missing-backup.tar.xz"}
			case "all wrong":
				urls = []string{server.URL + "/wrong.tar.xz", server.URL + "/wrong-backup.tar.xz"}
			case "local":
				path := filepath.Join(f.root, "local.tar.xz")
				if err := os.WriteFile(path, data, 0644); err != nil {
					t.Fatal(err)
				}
				urls = []string{filepath.ToSlash(path)}
			}
			body := "[source]\n" + archiveSource(urls, data)
			kind := "cmake"
			if mode == "resource" {
				body = "[resources.extra]\n" + archiveSource(urls, data) + "[[copy]]\nresource='extra'\nfiles=['header.h']\ndestination='include'\n"
				kind = "copy"
			}
			if mode == "cache" {
				body += "DOWNLOAD_NAME='package.tar.xz'\n[[copy]]\ndirectories=['.']\ndestination='.'\n"
				kind = "copy"
			}
			m := f.definition("demo", kind, body)
			f.project([]*moduleDefinition{m}, "")
			mu.Lock()
			count := len(requests)
			mu.Unlock()
			if count != 0 {
				t.Fatal("download at configure")
			}
			if strings.HasPrefix(mode, "all ") {
				if _, _, err := f.command(f.context.CMakeCommand, "--build", f.context.BuildRoot, "--config", "Release"); err == nil {
					t.Fatal("untrusted archive installed")
				}
				if _, err := os.Stat(f.installed("demo", "include/header.h")); !os.IsNotExist(err) {
					t.Fatal("failed download installed header")
				}
				mu.Lock()
				count = len(requests)
				mu.Unlock()
				if count < 2 {
					t.Fatal("did not try both URLs")
				}
				return
			}
			f.build()
			if mode == "cache" {
				requireFile(t, f.installed("demo", "header.h"), "trusted")
				requireFile(t, f.installed("demo", "lib/runtime"), "runtime")
				requireFile(t, f.installed("demo", "LICENSE"), "license")
				mu.Lock()
				requests = nil
				mu.Unlock()
				f.context.BuildRoot = filepath.Join(f.root, "fresh build")
				f.project([]*moduleDefinition{m}, "")
				f.build()
				mu.Lock()
				count = len(requests)
				mu.Unlock()
				if count != 0 {
					t.Fatal("cache hit downloaded")
				}
				putFile(t, filepath.Join(f.root, "git/package.tar.xz"), "wrong digest")
				f.context.BuildRoot = filepath.Join(f.root, "third build")
				f.project([]*moduleDefinition{m}, "")
				f.build()
			} else {
				requireFile(t, f.installed("demo", "include/header.h"), "trusted")
			}
			mu.Lock()
			got := append([]string{}, requests...)
			mu.Unlock()
			want := []string{"/good.tar.xz"}
			if mode == "404 fallback" {
				want = []string{"/missing.tar.xz", "/backup.tar.xz"}
			}
			if mode == "hash fallback" {
				want = []string{"/wrong.tar.xz", "/backup.tar.xz"}
			}
			if mode == "local" {
				want = []string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("request order: %v want %v", got, want)
			}
		})
	}
}
