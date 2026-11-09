package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSourceCatalog(t *testing.T) {
	t.Run("existence survives invalid and filtered recipes", func(t *testing.T) {
		root := t.TempDir()
		definitionVersionFile(t, root, "b", "1", "cmake", testSource)
		definitionVersionFile(t, root, "a", "1", "cmake", "depends=['b@1']\n"+testSource)
		definitionVersionFile(t, root, "c", "1", "cmake", "depends=['b@1']\n"+testSource)
		definitionVersionFile(t, root, "d", "1", "cmake", "package_platforms=['any-any']\n"+testSource)
		definitionVersionFile(t, root, "broken", "1", "cmake", testSource)
		putFile(t, filepath.Join(root, "src/dependencies/broken/1/module.toml"), "broken=[")
		definitionVersionFile(t, root, "dangling", "1", "cmake", testSource)
		putFile(t, filepath.Join(root, "src/dependencies/dangling/package.toml"), "latest='deleted'\n")
		report, err := describeCatalog(root)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(report.Packages["d"].Versions["1"].Platforms, []string{"any-any"}) {
			t.Fatalf("lost declared identity: %+v", report)
		}
		if _, ok := report.Packages["a"].Versions["1"]; !ok {
			t.Fatal("adapter filtering affected existence")
		}
		if _, ok := report.Packages["b"].Versions["missing"]; ok {
			t.Fatal("invented version")
		}
		if v, ok := report.Packages["broken"].Versions["1"]; !ok || v.Error == "" || v.Platforms != nil {
			t.Fatal("bad recipe became absent or known platforms")
		}
		if report.Packages["dangling"].Error == "" {
			t.Fatal("dangling latest accepted")
		}
		var out, diag bytes.Buffer
		if run([]string{"catalog", "--source-root", root}, &out, &diag) != 0 {
			t.Fatal(diag.String())
		}
		var decoded sourceCatalog
		if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || len(decoded.Packages) != 6 {
			t.Fatalf("CLI catalog: %s %v", &out, err)
		}
	})
	t.Run("unknown package is not empty", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "src/dependencies/b")
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(path, 0755) })
		if _, err := os.ReadDir(path); err == nil {
			t.Skip("host permits mode 000 reads")
		}
		report, err := describeCatalog(root)
		if err != nil || report.Packages["b"].Versions != nil || report.Packages["b"].Error == "" {
			t.Fatalf("unknown became empty: %+v %v", report, err)
		}
	})
	t.Run("root failure", func(t *testing.T) {
		if _, err := describeCatalog(t.TempDir()); err == nil {
			t.Fatal("missing catalog accepted")
		}
	})
	t.Run("symlink version cannot become absent", func(t *testing.T) {
		root := t.TempDir()
		definitionVersionFile(t, root, "b", "1", "cmake", testSource)
		if err := os.Symlink(t.TempDir(), filepath.Join(root, "src/dependencies/b/2")); err != nil {
			t.Fatal(err)
		}
		report, err := describeCatalog(root)
		if err == nil && report.Packages["b"].Versions != nil {
			t.Fatal("symlink version became confirmed absent")
		}
	})

	t.Run("escaping directory", func(t *testing.T) {
		root := t.TempDir()
		external := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, filepath.Join(root, "src/dependencies")); err != nil {
			t.Fatal(err)
		}
		if _, err := describeCatalog(root); err == nil {
			t.Fatal("external catalog accepted")
		}
	})
}
