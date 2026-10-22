package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifestFixture(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestManifest(t *testing.T) {
	digest := sha256.Sum256([]byte("actual archive fixture"))
	row := `{"sha256":"` + strings.ToUpper(hex.EncodeToString(digest[:])) + `","asset":"chosen-library.tar.zst","arch":"aarch64","name":"lz4","os":"linux"}`
	tool := `{"asset":"custom-tool","sha256":"` + strings.Repeat("a", 64) + `","arch":"x86_64","os":"windows"}`
	valid := `{"packages":[` + row + `],"importers":[` + tool + `]}`
	m, err := readManifest(writeManifestFixture(t, valid))
	if err != nil {
		t.Fatal(err)
	}
	p, err := selectPackage(m, packageIdentity{Name: "lz4", OS: "linux", Arch: "aarch64"})
	if err != nil || p.Asset != "chosen-library.tar.zst" {
		t.Fatalf("%#v %v", p, err)
	}
	if _, err := selectPackage(m, packageIdentity{Name: "lz4", OS: "windows", Arch: "x86_64"}); err == nil {
		t.Fatal("selected wrong platform")
	}
	bad := map[string]string{
		"missing array":       `{"packages":[]}`,
		"null array":          `{"packages":null,"importers":[]}`,
		"null importer array": `{"packages":[],"importers":null}`,
		"unknown top":         `{"packages":[],"importers":[],"protocol":1}`,
		"trailing value":      valid + ` {}`,
		"duplicate package":   `{"packages":[` + row + `,` + row + `],"importers":[]}`,
		"duplicate tool":      `{"packages":[],"importers":[` + tool + `,` + tool + `]}`,
		"missing field":       strings.Replace(valid, `"name":"lz4",`, "", 1),
		"unknown field":       strings.Replace(valid, `"name":"lz4"`, `"name":"lz4","version":"1"`, 1),
		"case field":          strings.Replace(valid, `"name":`, `"Name":`, 1),
		"wrong type":          strings.Replace(valid, `"name":"lz4"`, `"name":3`, 1),
		"null string":         strings.Replace(valid, `"name":"lz4"`, `"name":null`, 1),
		"bad digest":          strings.Replace(valid, strings.ToUpper(hex.EncodeToString(digest[:])), "invalid", 1),
		"bad name":            strings.Replace(valid, `"lz4"`, `"../lz4"`, 1),
		"bad platform":        strings.Replace(valid, `"aarch64"`, `"any"`, 1),
	}
	for _, asset := range []string{"../escape", "/absolute", `C:drive`, "https://host/file", "dir/file", `dir\\file`, "..", ""} {
		bad["asset "+asset] = strings.Replace(valid, "chosen-library.tar.zst", asset, 1)
	}
	for label, data := range bad {
		t.Run(label, func(t *testing.T) {
			if _, err := readManifest(writeManifestFixture(t, data)); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}
