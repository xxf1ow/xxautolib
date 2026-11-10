package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func deliveryFixture(t *testing.T, body string, urls ...string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(body))), "size": len(body), "urls": urls})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func downloadFixture(t *testing.T, body string, urls ...string) releaseAsset {
	t.Helper()
	asset, err := parseDeliveryRecord("fixture.tar.zst.json", []byte(deliveryFixture(t, body, urls...)))
	if err != nil {
		t.Fatal(err)
	}
	return asset
}

func TestDeliveryRecord(t *testing.T) {
	valid := `{"sha256":"` + strings.Repeat("a", 64) + `","size":7,"urls":["https://first.example/a","https://second.example/a"]}`
	asset, err := parseDeliveryRecord("archive.tar.zst.json", []byte(valid))
	if err != nil || asset.Name != "archive.tar.zst" || asset.Size != 7 || len(asset.URLs) != 2 || asset.URLs[1] != "https://second.example/a" {
		t.Fatalf("record: %+v %v", asset, err)
	}
	for _, bad := range []string{
		strings.Replace(valid, `"size":7`, `"size":true`, 1), strings.Replace(valid, `"size":7`, `"size":"7"`, 1),
		strings.Replace(valid, `"size":7`, `"size":7.0`, 1), strings.Replace(valid, `"size":7`, `"size":0`, 1),
		strings.Replace(valid, `"size":7`, `"size":2147483648`, 1), strings.Replace(valid, `https://first.example/a`, `http://first.example/a`, 1),
		strings.Replace(valid, `https://first.example/a`, `https://user:secret@first.example/a`, 1),
		strings.Replace(valid, `https://second.example/a`, `https://first.example/a`, 1),
		strings.Replace(valid, `["https://first.example/a","https://second.example/a"]`, `[]`, 1),
		strings.Replace(valid, `"size":7`, `"size":7,"extra":1`, 1), strings.Replace(valid, strings.Repeat("a", 64), strings.Repeat("A", 64), 1), valid + `{}`,
	} {
		if _, err := parseDeliveryRecord("archive.json", []byte(bad)); err == nil {
			t.Errorf("accepted invalid record %s", bad)
		}
	}
}

func TestDownloadSourceFallback(t *testing.T) {
	for _, kind := range []string{"404", "500", "connect", "truncated", "short", "hash", "unknown length", "all failed"} {
		t.Run(kind, func(t *testing.T) {
			var first, second atomic.Int32
			client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/first" {
					first.Add(1)
					switch kind {
					case "404", "all failed":
						w.WriteHeader(404)
						return
					case "500":
						w.WriteHeader(500)
						return
					case "truncated":
						w.Header().Set("Content-Length", "100")
						fmt.Fprint(w, "pay")
						return
					case "short":
						fmt.Fprint(w, "pay")
						return
					case "hash":
						fmt.Fprint(w, "changed")
						return
					}
				} else {
					second.Add(1)
					if kind == "all failed" {
						w.WriteHeader(500)
						return
					}
				}
				if kind == "unknown length" {
					w.(http.Flusher).Flush()
				}
				fmt.Fprint(w, "payload")
			}))
			urls := []string{"https://first.example/first", "https://second.example/second"}
			if kind == "connect" {
				transport := client.Transport.(*http.Transport)
				dial := transport.DialContext
				transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					if strings.HasPrefix(address, "first.example:") {
						return nil, &net.DNSError{Err: "controlled connection failure", Name: "first.example"}
					}
					return dial(ctx, network, address)
				}
			}
			destination := filepath.Join(t.TempDir(), "download")
			err := downloadReleaseAsset(context.Background(), client, downloadFixture(t, "payload", urls...), destination, newDownloadProgress(io.Discard, "fixture"))
			if kind == "all failed" {
				if err == nil || !strings.Contains(err.Error(), "first.example") || !strings.Contains(err.Error(), "second.example") {
					t.Fatalf("missing source diagnostics: %v", err)
				}
				if _, err := os.Stat(destination); !os.IsNotExist(err) {
					t.Fatal("failed download committed")
				}
			} else if err != nil || readTestFile(t, destination) != "payload" {
				t.Fatalf("fallback: %v", err)
			}
			wantSecond := int32(1)
			if kind == "unknown length" {
				wantSecond = 0
			}
			if second.Load() != wantSecond {
				t.Fatalf("second source called %d, want %d", second.Load(), wantSecond)
			}
		})
	}
}

func TestDownloadSizeBound(t *testing.T) {
	var received atomic.Int64
	stopped := make(chan struct{})
	client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			defer close(stopped)
			fmt.Fprint(w, strings.Repeat("x", 4096))
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
				t.Error("oversized source was not stopped")
			}
			return
		}
		fmt.Fprint(w, "payload")
	}))
	transport := client.Transport
	client.Transport = downloadTestTransport(func(r *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(r)
		if err == nil && r.URL.Path == "/first" {
			response.Body = countedDownloadBody{response.Body, &received}
		}
		return response, err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	destination := filepath.Join(t.TempDir(), "download")
	err := downloadReleaseAsset(ctx, client, downloadFixture(t, "payload", "https://first.example/first", "https://second.example/second"), destination, newDownloadProgress(io.Discard, "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if received.Load() != 8 {
		t.Fatalf("read %d bytes, want size + 1 = 8", received.Load())
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("first source remained active")
	}
	if entries, err := os.ReadDir(filepath.Dir(destination)); err != nil || len(entries) != 1 {
		t.Fatalf("temporary files: %v %v", entries, err)
	}
}

type downloadTestTransport func(*http.Request) (*http.Response, error)

func (f downloadTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type countedDownloadBody struct {
	io.ReadCloser
	count *atomic.Int64
}

func (b countedDownloadBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	b.count.Add(int64(n))
	return n, err
}

type failingDownloadFile struct {
	*os.File
	kind string
}

func (f failingDownloadFile) Write(data []byte) (int, error) {
	if f.kind == "write" {
		return 0, errors.New("local write failure")
	}
	return f.File.Write(data)
}
func (f failingDownloadFile) Close() error {
	err := f.File.Close()
	if f.kind == "close" {
		return errors.New("local close failure")
	}
	return err
}

func TestDownloadLocalFailureStopsFallback(t *testing.T) {
	for _, kind := range []string{"create", "write", "close", "remove", "rename"} {
		t.Run(kind, func(t *testing.T) {
			var second atomic.Int32
			client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/second" {
					second.Add(1)
				}
				if kind == "remove" {
					fmt.Fprint(w, "changed")
				} else {
					fmt.Fprint(w, "payload")
				}
			}))
			files := downloadFiles{
				create: func(dir string) (downloadFile, error) {
					if kind == "create" {
						return nil, errors.New("local create failure")
					}
					f, err := os.CreateTemp(dir, ".download-")
					if err != nil {
						return nil, err
					}
					return failingDownloadFile{f, kind}, nil
				},
				remove: func(path string) error {
					if kind == "remove" {
						return errors.New("local remove failure")
					}
					return os.Remove(path)
				},
				rename: func(from, to string) error {
					if kind == "rename" {
						return errors.New("local rename failure")
					}
					return os.Rename(from, to)
				},
			}
			destination := filepath.Join(t.TempDir(), "download")
			err := downloadAsset(context.Background(), client, downloadFixture(t, "payload", "https://first.example/first", "https://second.example/second"), destination, newDownloadProgress(io.Discard, "fixture"), files)
			if err == nil || !strings.Contains(err.Error(), "local "+kind+" failure") {
				t.Fatalf("local failure missing: %v", err)
			}
			if second.Load() != 0 {
				t.Fatal("local failure switched source")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("local failure committed download")
			}
		})
	}
}

func TestTrustedArchiveFailureDoesNotFallback(t *testing.T) {
	for _, kind := range []string{"archive", "identity"} {
		t.Run(kind, func(t *testing.T) {
			f := versionedRemote(t)
			o, cache := versionedFixture(t, "probe")
			o.RemoteRoot, o.MetadataRoot = versionedDownloadRoot, f.root
			asset := f.add(t, "probe", "2", "")
			body := "bad archive"
			if kind == "identity" {
				body = readTestFile(t, testArchive(t, []archiveMember{{Name: "probe/2/autolib-import.cmake", Body: identityHeader(packageIdentity{"wrong", "2", "linux", "x86_64"})}}))
			}
			f.assets[asset] = body
			putTestFile(t, filepath.Join(f.root, "probe/2", asset+".json"), deliveryFixture(t, body, versionedDownloadRoot+"/"+asset, versionedDownloadRoot+"/"+asset+"?backup=1"))
			if _, err := prepare(o); err == nil {
				t.Fatal("trusted invalid package accepted")
			}
			f.requireRequests(t, map[string]int{asset: 1})
			requireNoPackageStaging(t, cache)
		})
	}
}

func TestDownloadTimeouts(t *testing.T) {
	for _, kind := range []string{"headers", "idle", "total", "redirect budget", "active body"} {
		t.Run(kind, func(t *testing.T) {
			var second atomic.Int32
			stopped := make(chan struct{}, 1)
			client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/second" {
					second.Add(1)
					fmt.Fprint(w, "payload")
					return
				}
				if kind == "redirect budget" && r.URL.Path == "/first" {
					select {
					case <-time.After(80 * time.Millisecond):
					case <-r.Context().Done():
						return
					}
					http.Redirect(w, r, "https://first.example/redirect", 302)
					return
				}
				if kind == "headers" || kind == "redirect budget" {
					<-r.Context().Done()
					stopped <- struct{}{}
					return
				}
				w.(http.Flusher).Flush()
				if kind == "idle" {
					<-r.Context().Done()
					stopped <- struct{}{}
					return
				}
				for _, c := range "payload" {
					select {
					case <-r.Context().Done():
						stopped <- struct{}{}
						return
					case <-time.After(25 * time.Millisecond):
					}
					fmt.Fprintf(w, "%c", c)
					w.(http.Flusher).Flush()
				}
			}))
			limits := downloadTimeouts{Connect: time.Second, TLS: time.Second, Header: time.Second, Idle: 60 * time.Millisecond, Total: time.Second}
			switch kind {
			case "headers":
				limits.Header = 60 * time.Millisecond
			case "total", "redirect budget":
				limits.Total = 120 * time.Millisecond
			}
			destination := filepath.Join(t.TempDir(), "download")
			err := downloadAssetWithTimeouts(context.Background(), client, downloadFixture(t, "payload", "https://first.example/first", "https://second.example/second"), destination, newDownloadProgress(io.Discard, "fixture"), defaultDownloadFiles(), limits)
			if err != nil || readTestFile(t, destination) != "payload" {
				t.Fatalf("download: %v", err)
			}
			want := int32(1)
			if kind == "active body" {
				want = 0
			}
			if second.Load() != want {
				t.Fatalf("fallback calls=%d, want %d", second.Load(), want)
			}
			if kind != "active body" {
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Fatal("request was not canceled")
				}
			}
		})
	}
}

func TestPrepareCancellationStopsFallback(t *testing.T) {
	for _, already := range []bool{false, true} {
		t.Run(fmt.Sprint(already), func(t *testing.T) {
			var calls, second atomic.Int32
			started := make(chan struct{})
			client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/second" {
					second.Add(1)
					fmt.Fprint(w, "payload")
					return
				}
				close(started)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			f := versionedRemote(t)
			http.DefaultTransport = client.Transport
			name := f.add(t, "probe", "1", "")
			if err := os.WriteFile(filepath.Join(f.root, "probe", "1", name+".json"), []byte(deliveryFixture(t, f.assets[name], "https://first.example/first", "https://second.example/second")), 0644); err != nil {
				t.Fatal(err)
			}
			o, _ := versionedFixture(t, "probe@1")
			o.RemoteRoot, o.MetadataRoot = versionedDownloadRoot, f.root
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if already {
				if err := os.WriteFile(o.ResultPath, []byte("stale result"), 0644); err != nil {
					t.Fatal(err)
				}
				cancel()
			}
			done := make(chan int, 1)
			var stderr strings.Builder
			go func() { done <- runContext(ctx, prepareArguments(o), io.Discard, &stderr) }()
			if !already {
				select {
				case <-started:
					cancel()
				case <-time.After(time.Second):
					t.Fatal("request not started")
				}
			}
			select {
			case code := <-done:
				if code != 1 || !strings.Contains(stderr.String(), "context canceled") {
					t.Fatalf("code %d: %s", code, &stderr)
				}
			case <-time.After(time.Second):
				t.Fatal("cancel did not stop prepare")
			}
			if second.Load() != 0 || (already && calls.Load() != 0) {
				t.Fatal("canceled prepare sent a request")
			}
			if _, err := os.Stat(o.ResultPath); !os.IsNotExist(err) {
				t.Fatal("canceled result exists")
			}
		})
	}
}

func TestDownloadProgressResetsOnFallback(t *testing.T) {
	client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			fmt.Fprint(w, "pay")
			return
		}
		fmt.Fprint(w, "payload")
	}))
	var output strings.Builder
	err := downloadReleaseAsset(context.Background(), client, downloadFixture(t, "payload", "https://first.example/first?token=SECRET", "https://second.example/second"), filepath.Join(t.TempDir(), "download"), newDownloadProgress(&output, "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"source 1/2", "source 2/2", "source 1", "42%", "100%"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("missing %q: %s", want, &output)
		}
	}
	if strings.Count(output.String(), "0%") < 2 || strings.Contains(output.String(), "SECRET") {
		t.Fatalf("progress did not reset safely: %s", &output)
	}
}

func TestDownloadErrorRedaction(t *testing.T) {
	client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "payload") }))
	base := client.Transport
	client.Transport = downloadTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/first" {
			return nil, fmt.Errorf("redirect failed: https://user:SECRET@cdn.example/archive?signature=SIGNED")
		}
		return base.RoundTrip(r)
	})
	var output strings.Builder
	if err := downloadReleaseAsset(context.Background(), client, downloadFixture(t, "payload", "https://first.example/first", "https://second.example/second"), filepath.Join(t.TempDir(), "download"), newDownloadProgress(&output, "fixture")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "SECRET") || strings.Contains(output.String(), "SIGNED") {
		t.Fatalf("secret in log: %s", &output)
	}
}
