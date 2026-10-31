package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const testReleaseURL = "https://github.com/owner/repo/releases/download/v1"

// Preserve production URL routing and TLS verification; only the socket destination changes.
func githubTestClient(t *testing.T, handler http.Handler, diagnostic ...*log.Logger) *http.Client {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	if len(diagnostic) != 0 {
		server.Config.ErrorLog = diagnostic[0]
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

func TestReleaseDownload(t *testing.T) {
	for _, kind := range []string{"direct", "redirect", "bad certificate", "downgrade", "404", "mismatch", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			var diagnostic *log.Logger
			if kind == "bad certificate" {
				diagnostic = log.New(io.Discard, "", 0)
			}
			var calls, insecureCalls atomic.Int32
			insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { insecureCalls.Add(1); fmt.Fprint(w, "payload") }))
			defer insecure.Close()
			client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/owner/repo/releases/download/v1/demo" {
					if r.Header.Get("Accept") != "application/octet-stream" {
						t.Error("missing binary Accept")
					}
					switch kind {
					case "redirect":
						http.Redirect(w, r, "https://objects.example/content", 302)
						return
					case "downgrade":
						http.Redirect(w, r, insecure.URL, 302)
						return
					case "404":
						w.WriteHeader(404)
						return
					case "mismatch":
						fmt.Fprint(w, "changed")
						return
					case "truncated":
						w.Header().Set("Content-Length", "100")
						fmt.Fprint(w, "part")
						return
					}
				} else if r.URL.Path != "/content" {
					t.Error("unexpected request", r.URL)
				}
				fmt.Fprint(w, "payload")
			}), diagnostic)
			if kind == "bad certificate" {
				transport := client.Transport.(*http.Transport).Clone()
				transport.TLSClientConfig = nil
				client.Transport = transport
				defer transport.CloseIdleConnections()
			}
			a := releaseAsset{Name: "demo", URL: testReleaseURL + "/demo", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("payload")))}
			file := filepath.Join(t.TempDir(), "download")
			err := downloadReleaseAsset(context.Background(), client, a, file)
			if kind == "bad certificate" {
				if err == nil || !strings.Contains(err.Error(), "certificate") {
					t.Fatal("missing expected TLS rejection", err)
				}
			}
			valid := kind == "direct" || kind == "redirect"
			if (err == nil) != valid {
				t.Fatalf("download: %v", err)
			}
			if valid && readTestFile(t, file) != "payload" {
				t.Fatal("wrong content")
			}
			if !valid {
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatal("failed download left file")
				}
			}
			if insecureCalls.Load() != 0 {
				t.Fatal("followed HTTP downgrade")
			}
			want := int32(1)
			if kind == "redirect" {
				want = 2
			}
			if kind == "bad certificate" {
				want = 0
			}
			if calls.Load() != want {
				t.Fatalf("unexpected retries: got %d, want %d", calls.Load(), want)
			}
		})
	}
}

func TestReleaseDownloadExisting(t *testing.T) {
	var requests atomic.Int32
	client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "payload")
	}))
	destination := putTestFile(t, filepath.Join(t.TempDir(), "download"), "user content")
	asset := releaseAsset{Name: "demo", URL: "https://gitee.com/owner/repo/releases/download/v1/demo", SHA256: "239f59ed55e737c77147cf55ad0c1b030b6d7ee748a7426952f9b852d5a935e5"}
	if err := downloadReleaseAsset(context.Background(), client, asset, destination); err == nil {
		t.Fatal("overwrote existing destination")
	}
	if requests.Load() != 0 || readTestFile(t, destination) != "user content" {
		t.Fatal("existing destination was not preserved without a request")
	}
}
