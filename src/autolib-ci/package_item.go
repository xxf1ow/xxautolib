package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func packageCIItem(ctx context.Context, o ciRunOptions, run ciRunIdentity, platform *ciPlatformPlan, publishers map[string]string, record ciCacheRecord) (ciLocalAsset, error) {
	asset := ciLocalAsset{Record: record}
	source, work := o.SourceRoot, o.WorkRoot
	if record.Origin != run || record.Verification.Platform != o.Platform {
		return asset, errors.New("packaging requires this run's item verification")
	}
	if err := record.Key.validate(); err != nil {
		return asset, err
	}
	install := filepath.Join(work, "install")
	if record.Key.Kind == "library" {
		var selected *describedModule
		for i := range platform.Description.Modules {
			m := &platform.Description.Modules[i]
			if ciModuleCacheKey(*m) == record.Key {
				selected = m
				break
			}
		}
		if selected == nil {
			return asset, errors.New("package item is outside the selected plan")
		}
		if selected.PackagePlatform == "any-any" && publishers[moduleIdentity(*selected)] != o.Platform {
			return asset, errors.New("universal package can only be packaged by its selected publisher")
		}
		if len(record.DependencyFingerprints) != len(selected.Depends) {
			return asset, errors.New("package item dependency fingerprints are incomplete")
		}
		for _, ref := range selected.Depends {
			if !digestValue.MatchString(record.DependencyFingerprints[ref]) {
				return asset, fmt.Errorf("missing exact dependency fingerprint %s", ref)
			}
		}
		data, err := os.ReadFile(filepath.Join(source, "src/dependencies", record.Key.Name, "package.toml"))
		if err != nil {
			return asset, err
		}
		var pointer map[string]any
		if err := toml.Unmarshal(data, &pointer); err != nil {
			return asset, err
		}
		latest, ok := pointer["latest"].(string)
		if !ok || len(pointer) != 1 || !ciModuleName.MatchString(latest) {
			return asset, errors.New("invalid source latest candidate")
		}
		if info, err := os.Stat(filepath.Join(source, "src/dependencies", record.Key.Name, latest, "module.toml")); err != nil || !info.Mode().IsRegular() {
			return asset, errors.New("latest candidate has no source recipe")
		}
		if record.LatestCandidate != "" && record.LatestCandidate != latest {
			return asset, errors.New("item latest candidate differs from current source")
		}
		record.LatestCandidate = latest
	} else {
		if record.Key.OS+"-"+record.Key.Arch != o.Platform {
			return asset, errors.New("tool item belongs to another platform")
		}
		if _, err := verifyCIEntry(source, install); err != nil {
			return asset, err
		}
		data, err := os.ReadFile(filepath.Join(source, "src/autolib.cmake"))
		if err != nil {
			return asset, err
		}
		if record.Entry != nil && (!bytes.Equal(record.Entry.Bytes, data) || record.Entry.SHA256 != ciSHA256(data)) {
			return asset, errors.New("tool item entry differs from current source")
		}
		record.Entry = &ciCacheEntry{Bytes: data, SHA256: ciSHA256(data)}
	}
	probe := record
	probe.Archive = ciCacheArchive{ID: 1, Name: record.Key.archiveName(), Size: 1, SHA256: strings.Repeat("0", 64)}
	if err := validateCICacheRecord(probe, o.Repository, record.Key, record.Fingerprint, record.DependencyFingerprints, []ciCacheAsset{{ID: 1, Name: probe.Archive.Name, State: "uploaded", Size: 1}}); err != nil {
		return asset, err
	}
	output := filepath.Join(work, "packaged", record.Key.archiveName())
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return asset, errors.New("item packaging requires a fresh output directory")
	}
	builder := filepath.Join(work, "build", "tools", "autolib-builder")
	if record.Key.Kind == "importer" || len(platform.Description.Modules) == 0 {
		builder = filepath.Join(work, "tools", "autolib-builder")
	}
	if strings.HasPrefix(o.Platform, "windows-") {
		builder += ".exe"
	}
	osName, arch, _ := ciTarget(o.Platform)
	args := []string{"package", "--asset-kind", record.Key.Kind, "--source-root", source, "--install-root", install, "--output-root", output, "--os", strings.ToLower(osName), "--arch", arch, "--host-os", strings.ToLower(osName), "--host-arch", arch, "--cmake-command", o.CMakeCommand}
	if record.Key.Kind == "library" {
		args = append(args, "--module", record.Key.Name+"@"+record.Key.Version)
	} else {
		args = append(args, "--importer-file", o.ImporterFile)
		if o.ImporterFile == "" {
			args[len(args)-1] = filepath.Join(install, ciImporterExecutable(o.Platform))
		}
	}
	if err := ciCommand(ctx, builder, args, source, nil, filepath.Join(work, "logs", "package", record.Key.archiveName()+".log")); err != nil {
		return asset, err
	}
	asset.Path = filepath.Join(output, record.Key.archiveName())
	file, err := os.Open(asset.Path)
	if err != nil {
		return asset, err
	}
	h := sha256.New()
	size, copyErr := io.Copy(h, file)
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return asset, err
	}
	record.Archive = ciCacheArchive{Name: record.Key.archiveName(), Size: size, SHA256: hex.EncodeToString(h.Sum(nil))}
	asset.Record = record
	if err := validateCILocalCacheAsset(asset, o.Repository); err != nil {
		return asset, err
	}
	return asset, nil
}
