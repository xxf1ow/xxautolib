package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCMakePackageInfo(t *testing.T) {
	f := versionedEntryFixture(t, false)
	cache := entryCache(f)
	marker := filepath.ToSlash(filepath.Join(f.root, "adapter-count"))
	body := "file(APPEND [==[" + marker + "]==] loaded)\n"
	cmakeEntryPackage(t, cache, "leaf", "2", "", body)
	cmakeEntryPackage(t, cache, "demo", "0123456789ab", "leaf@2", body)
	cmakeEntryPackage(t, cache, "extra", "1", "leaf@2", body)
	versionedEntryConsumer(f, "", "demo@0123456789ab demo@0123456789ab", `
autolib_import(TARGET consumer INTERFACE demo)
add_library(other INTERFACE)
autolib_import(TARGET other INTERFACE extra@1 demo@0123456789ab)
`)
	for attempt := 0; attempt < 2; attempt++ {
		out, err := f.configure()
		if err != nil {
			t.Fatalf("configure: %v\n%s", err, out)
		}
		for line, count := range map[string]int{
			"-- [Info] [consumer] leaf : 2\n":            2,
			"-- [Info] [consumer] demo : 0123456789ab\n": 2,
			"-- [Info] [other] leaf : 2\n":               1,
			"-- [Info] [other] extra : 1\n":              1,
			"-- [Info] [other] demo : 0123456789ab\n":    1,
		} {
			if got := strings.Count(out, line); got != count {
				t.Errorf("%q occurred %d times, want %d:\n%s", line, got, count, out)
			}
		}
		if strings.Contains(out, "[AutoLib]") {
			t.Fatalf("local package use printed preparation messages:\n%s", out)
		}
	}
	if got := readTestFile(t, marker); got != strings.Repeat("loaded", 6) {
		t.Fatalf("adapters loaded more than once per configure: %q", got)
	}
}

func TestCMakePackageDownloadMessages(t *testing.T) {
	e := remoteEntry(t, "gitee.com")
	tool := e.f.toolAsset + ".tar.xz"
	e.asset(t, "autolib-importer/"+tool, readTestFile(t, e.f.importerArchive(e.f.toolAsset, readTestFile(t, e.f.tool))))
	root := e.pkg(t, "demo", "0123456789ab", "leaf@2")
	leaf := e.pkg(t, "leaf", "2", "")
	e.consumer(t, "demo@0123456789ab", "autolib_import(TARGET consumer INTERFACE demo)\n")
	for attempt := 0; attempt < 2; attempt++ {
		out, err := e.configure(t)
		if err != nil {
			t.Fatalf("configure: %v\n%s", err, out)
		}
		for _, ref := range []string{"demo@0123456789ab", "leaf@2"} {
			for _, action := range []string{"Downloading ", "Download complete: ", "Extracting ", "Extraction complete: "} {
				line := "-- [AutoLib] " + action + ref
				if found := strings.Contains(out, line); found != (attempt == 0) {
					t.Errorf("attempt %d, %q present=%v:\n%s", attempt, line, found, out)
				}
			}
		}
		for _, line := range []string{"-- [Info] [consumer] leaf : 2\n", "-- [Info] [consumer] demo : 0123456789ab\n"} {
			if strings.Count(out, line) != 2 {
				t.Errorf("missing per-import info %q:\n%s", line, out)
			}
		}
		for _, forbidden := range []string{"[AutoLib] Downloading autolib-importer", "[AutoLib] Extracting autolib-importer", "Reusing", "Cache hit", "Already loaded"} {
			if strings.Contains(out, forbidden) {
				t.Errorf("unexpected message %q:\n%s", forbidden, out)
			}
		}
	}
	e.counts(t, 1, map[string]int{tool: 1, root: 1, leaf: 1})
}

type observedProgress struct {
	mu      sync.Mutex
	output  bytes.Buffer
	partial chan struct{}
	once    sync.Once
}

func (p *observedProgress) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, err := p.output.Write(data)
	if strings.Count(p.output.String(), "-- [AutoLib] Downloading probe@0123456789ab:") >= 2 {
		p.once.Do(func() { close(p.partial) })
	}
	return n, err
}

func TestPackageDownloadProgress(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprintf("known-length=%v", known), func(t *testing.T) {
			f := versionedRemote(t)
			asset := f.add(t, "probe", "0123456789ab", "")
			// A deliberately wrong digest exercises streaming without accepting the response as a package.
			payload := f.assets[asset] + strings.Repeat("\x00", 512*1024)
			recordPath := filepath.Join(f.root, "probe", "0123456789ab", asset+".json")
			record := map[string]any{}
			if err := json.Unmarshal([]byte(readTestFile(t, recordPath)), &record); err != nil {
				t.Fatal(err)
			}
			record["size"] = len(payload)
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(recordPath, data, 0644); err != nil {
				t.Fatal(err)
			}
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			client := githubTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if known {
					w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
				}
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				_, _ = io.WriteString(w, payload[:256*1024])
				w.(http.Flusher).Flush()
				close(started)
				<-release
				_, _ = io.WriteString(w, payload[256*1024:])
			}))
			defer unblock()
			http.DefaultTransport = client.Transport
			o, _ := versionedFixture(t, "probe@0123456789ab")
			o.RemoteRoot, o.MetadataRoot = versionedDownloadRoot, f.root
			output := &observedProgress{partial: make(chan struct{})}
			var stderr bytes.Buffer
			done := make(chan int, 1)
			go func() { done <- run(prepareArguments(o), output, &stderr) }()
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("download never started")
			}
			select {
			case <-output.partial:
			case <-time.After(3 * time.Second):
				t.Error("no byte progress before the download completed")
			}
			unblock()
			if code := <-done; code != 1 || !strings.Contains(stderr.String(), "SHA256 mismatch") {
				t.Fatalf("corrupt response accepted: %d %s", code, &stderr)
			}
			out := output.output.String()
			if !strings.Contains(out, "%") {
				t.Errorf("wrong total-length display:\n%s", out)
			}
			if strings.Contains(out, "Download complete") || strings.Contains(out, "Extracting") {
				t.Errorf("failed download reported success:\n%s", out)
			}
		})
	}
}
