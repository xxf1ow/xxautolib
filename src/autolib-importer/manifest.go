package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func readManifest(path string) (result manifest, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("manifest %s: %w", path, err)
		}
	}()
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	var top map[string]json.RawMessage
	if err = json.Unmarshal(data, &top); err != nil {
		return result, err
	}
	if len(top) != 2 || top["importers"] == nil || top["packages"] == nil {
		return result, fmt.Errorf("expected only importers and packages arrays")
	}
	seen := map[string]bool{}
	for _, kind := range []string{"importers", "packages"} {
		var rows []json.RawMessage
		if err = json.Unmarshal(top[kind], &rows); err != nil {
			return result, err
		}
		if rows == nil {
			return result, fmt.Errorf("%s must be an array, not null", kind)
		}
		for _, row := range rows {
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(row, &fields); err != nil {
				return result, err
			}
			keys := []string{"os", "arch", "asset", "sha256"}
			if kind == "packages" {
				keys = append(keys, "name")
			}
			if len(fields) != len(keys) {
				return result, fmt.Errorf("%s record has missing or unknown fields", kind)
			}
			values := map[string]string{}
			for _, key := range keys {
				var value string
				if err = json.Unmarshal(fields[key], &value); err != nil {
					return result, fmt.Errorf("%s.%s: expected string: %w", kind, key, err)
				}
				if value == "" {
					return result, fmt.Errorf("%s.%s must be a nonempty string", kind, key)
				}
				values[key] = value
			}
			if err = validatePlatform(values["os"], values["arch"]); err != nil {
				return result, err
			}
			if kind == "packages" && !packageName.MatchString(values["name"]) {
				return result, fmt.Errorf("invalid package name %q", values["name"])
			}
			asset := values["asset"]
			if !filepath.IsLocal(asset) || asset == "." || strings.ContainsAny(asset, "/\\:\x00") {
				return result, fmt.Errorf("asset must be a single local filename: %q", asset)
			}
			digest, decodeErr := hex.DecodeString(values["sha256"])
			if decodeErr != nil || len(digest) != 32 {
				return result, fmt.Errorf("%s: sha256 must contain 64 hexadecimal digits", asset)
			}
			slot := kind + "/" + values["name"] + "/" + values["os"] + "/" + values["arch"]
			if seen[slot] {
				return result, fmt.Errorf("duplicate manifest slot %s", slot)
			}
			seen[slot] = true
			if kind == "packages" {
				result.Packages = append(result.Packages, packageRecord{Name: values["name"], OS: values["os"], Arch: values["arch"], Asset: asset, SHA256: values["sha256"]})
			} else {
				result.Importers = append(result.Importers, importerRecord{OS: values["os"], Arch: values["arch"], Asset: asset, SHA256: values["sha256"]})
			}
		}
	}
	return result, nil
}

func selectPackage(m manifest, id packageIdentity) (packageRecord, error) {
	for _, row := range m.Packages {
		if row.Name == id.Name && row.OS == id.OS && row.Arch == id.Arch {
			return row, nil
		}
	}
	return packageRecord{}, fmt.Errorf("manifest has no package %s/%s/%s", id.Name, id.OS, id.Arch)
}
