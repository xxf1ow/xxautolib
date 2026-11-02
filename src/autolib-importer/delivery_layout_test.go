package main

import (
	"path/filepath"
	"testing"
)

func TestDeliveryMetadataLayout(t *testing.T) {
	for _, host := range []string{"github.com", "gitee.com"} {
		t.Run(host, func(t *testing.T) {
			e := remoteEntry(t, host)
			putTestFile(t, filepath.Join(e.repo, "LICENSE"), "MIT License\n")
			tool := e.f.toolAsset + ".tar.xz"
			e.asset(t, "autolib-importer/"+tool, readTestFile(t, e.f.importerArchive(e.f.toolAsset, readTestFile(t, e.f.tool))))
			demo := e.pkg(t, "demo", "1.0", "")
			eigen := e.pkg(t, "eigen", "3.4.0", "")
			e.consumer(t, "demo;eigen", "if(NOT TARGET autolib::demo OR NOT TARGET autolib::eigen)\nmessage(FATAL_ERROR missing_delivery_target)\nendif()\n")
			if out, err := e.configure(t); err != nil {
				t.Fatalf("published metadata through public consumer: %v\n%s", err, out)
			}
			for _, path := range []string{"demo/1.0/data", "eigen/3.4.0/data"} {
				if got := readTestFile(t, filepath.Join(entryCache(e.f), path)); got != "archive" {
					t.Fatalf("consumed %s = %q", path, got)
				}
			}
			e.counts(t, 1, map[string]int{tool: 1, demo: 1, eigen: 1})
		})
	}
}
