package main

import (
	"path/filepath"
	"testing"
)

func TestVersionedNoConsumerAPI(t *testing.T) {
	for _, host := range []string{"github.com", "gitee.com"} {
		t.Run(host, func(t *testing.T) {
			e := remoteEntry(t, host)
			tool := e.f.toolAsset + ".tar.xz"
			e.asset(t, "autolib-importer/"+tool, readTestFile(t, e.f.importerArchive(e.f.toolAsset, readTestFile(t, e.f.tool))))
			pkg := e.pkg(t, "demo", "1.0", "")
			e.consumer(t, "demo@1.0", "if(NOT TARGET autolib::demo)\nmessage(FATAL_ERROR missing_demo)\nendif()\n")
			if out, err := e.configure(t); err != nil {
				t.Fatalf("real consumption: %v\n%s", err, out)
			}
			if got := readTestFile(t, filepath.Join(entryCache(e.f), "demo/1.0/data")); got != "archive" {
				t.Fatalf("wrong downloaded bytes: %q", got)
			}
			e.counts(t, 1, map[string]int{tool: 1, pkg: 1})
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.apiRequests != 0 {
				t.Fatalf("consumer made %d API/Raw requests", e.apiRequests)
			}
		})
	}
}
