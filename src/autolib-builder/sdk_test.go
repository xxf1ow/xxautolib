package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func sdkArchive(t *testing.T) []byte {
	t.Helper()
	binary, err := os.ReadFile(goExecutable())
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if runtime.GOOS == "windows" {
		archive := zip.NewWriter(&buffer)
		for name, content := range map[string][]byte{"go/bin/go.exe": binary, "go/VERSION": []byte(runtime.Version())} {
			writer, err := archive.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(content); err != nil {
				t.Fatal(err)
			}
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		compressed := gzip.NewWriter(&buffer)
		archive := tar.NewWriter(compressed)
		for name, content := range map[string][]byte{"go/bin/go": binary, "go/VERSION": []byte(runtime.Version())} {
			if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(content))}); err != nil {
				t.Fatal(err)
			}
			if _, err := archive.Write(content); err != nil {
				t.Fatal(err)
			}
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		if err := compressed.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return buffer.Bytes()
}

func newSDKFixture(t *testing.T, url, digest string) *fixture {
	t.Helper()
	f := newFixture(t)
	putFile(t, filepath.Join(f.root, "go.mod"), "module fixture\ngo "+strings.TrimPrefix(runtime.Version(), "go")+"\n")
	for _, file := range []string{"go-sdk.cmake", "go-sdk-versions.cmake"} {
		putFile(t, filepath.Join(f.root, "src/autolib-builder", file), readFile(t, file))
	}
	// 只替换隔离副本的发行元数据，真实下载、校验和解包逻辑保持不变。
	metadata := filepath.Join(f.root, "src/autolib-builder/go-sdk-versions.cmake")
	putFile(t, metadata, readFile(t, metadata)+fmt.Sprintf("\nset(xxautolib_go_base_url \"%s\")\nset(xxautolib_go_sha256_%s_%s \"%s\")\n", url, runtime.GOOS, runtime.GOARCH, digest))
	return f
}

func (f *fixture) sdkCommand(args ...string) (string, error) {
	f.t.Helper()
	command := []string{f.context.CMakeCommand, "-P", filepath.Join(f.root, "src/autolib-builder/go-sdk.cmake"), "--"}
	out, stderr, err := f.command(append(command, args...)...)
	return out + stderr, err
}

func TestGoSDKDownload(t *testing.T) {
	archive := sdkArchive(t)
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	f := newSDKFixture(t, server.URL, digest)
	for i := 0; i < 3; i++ {
		if i == 1 {
			server.Close()
		}
		if i == 2 {
			if err := os.RemoveAll(managedGoRoot(f)); err != nil {
				t.Fatal(err)
			}
		}
		out, err := f.sdkCommand("version")
		if err != nil || !strings.Contains(out, "go version "+runtime.Version()) {
			t.Fatalf("attempt %d: %v\n%s", i, err, out)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("SDK was not reused: %d requests", requests.Load())
	}
}

func TestGoSDKDownloadFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body, digest, want string
		status                   int
	}{
		{"download", "", strings.Repeat("0", 64), "download failed", http.StatusNotFound},
		{"digest", "not an SDK", strings.Repeat("0", 64), "SHA256 mismatch", http.StatusOK},
		{"archive", "not an SDK", fmt.Sprintf("%x", sha256.Sum256([]byte("not an SDK"))), "extraction failed", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			f := newSDKFixture(t, server.URL, tc.digest)
			out, err := f.sdkCommand("version")
			if err == nil || !strings.Contains(out, tc.want) || !strings.Contains(out, server.URL) && tc.name != "archive" {
				t.Fatalf("%v\n%s", err, out)
			}
			if _, err := os.Stat(managedGoRoot(f)); !os.IsNotExist(err) {
				t.Fatal("failed SDK was committed", err)
			}
		})
	}
}
