package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func restoreCICacheItem(ctx context.Context, o ciRunOptions, asset ciLocalAsset) error {
	r := asset.Record
	if err := r.Key.validate(); err != nil {
		return err
	}
	if r.Kind != r.Key.Kind || r.Archive.Name != r.Key.archiveName() {
		return fmt.Errorf("cache restore identity mismatch")
	}
	install := filepath.Join(o.WorkRoot, "install")
	if r.Key.Kind == "library" {
		if r.Key.OS != "any" && r.Key.OS+"-"+r.Key.Arch != o.Platform {
			return fmt.Errorf("cache restore platform mismatch")
		}
		args := []string{"restore", "--archive", asset.Path, "--cache-root", install, "--name", r.Key.Name, "--version", r.Key.Version, "--os", r.Key.OS, "--arch", r.Key.Arch, "--sha256", r.Archive.SHA256, "--size", strconv.FormatInt(r.Archive.Size, 10), "--result", filepath.Join(o.WorkRoot, "restored", r.Archive.Name+".cmake")}
		for _, dependency := range sortedKeysStringMap(r.DependencyFingerprints) {
			args = append(args, "--dependency="+dependency)
		}
		return ciCommand(ctx, o.ImporterFile, args, o.SourceRoot, nil, filepath.Join(o.WorkRoot, "logs", "restore", r.Archive.Name+".log"))
	}
	if r.Key.OS+"-"+r.Key.Arch != o.Platform {
		return fmt.Errorf("cached importer belongs to another platform")
	}
	entry, err := os.ReadFile(filepath.Join(o.SourceRoot, "src", "autolib.cmake"))
	if err != nil {
		return err
	}
	if r.Entry == nil || !bytes.Equal(r.Entry.Bytes, entry) || ciSHA256(entry) != r.Entry.SHA256 {
		return fmt.Errorf("cached importer entry differs from current source")
	}
	info, err := os.Lstat(asset.Path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != r.Archive.Size || r.Archive.Size <= 0 || r.Archive.Size >= ciCacheSizeLimit {
		return fmt.Errorf("cached importer archive size or type mismatch")
	}
	f, err := os.Open(asset.Path)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(h.Sum(nil)) != r.Archive.SHA256 {
		return fmt.Errorf("cached importer archive SHA256 mismatch")
	}
	archive, err := filepath.Abs(asset.Path)
	if err != nil {
		return err
	}
	name := ciImporterExecutable(o.Platform)
	for _, mode := range []string{"tf", "tvf"} {
		var out, diagnostic bytes.Buffer
		cmd := ciExternalCommand(ctx, o.CMakeCommand, "-E", "tar", mode, archive)
		cmd.Stdout = &out
		cmd.Stderr = &diagnostic
		if err := cmd.Run(); err != nil || diagnostic.Len() != 0 {
			return fmt.Errorf("inspect cached importer: %v: %s", err, &diagnostic)
		}
		if (mode == "tf" && out.String() != name+"\n") || (mode == "tvf" && !strings.HasPrefix(out.String(), "-")) {
			return fmt.Errorf("importer archive must contain only regular program %s", name)
		}
	}
	if err := os.MkdirAll(install, 0755); err != nil {
		return err
	}
	if info, err := os.Lstat(install); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("restore requires a regular installation directory")
	}
	target := filepath.Join(install, name)
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return fmt.Errorf("importer restore requires a new target: %s", target)
	}
	entryTarget := filepath.Join(install, "autolib.cmake")
	if info, err := os.Lstat(entryTarget); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("entry target is not a regular file")
		}
		data, err := os.ReadFile(entryTarget)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, entry) {
			return fmt.Errorf("existing entry differs from current source")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	staging, err := os.MkdirTemp(install, ".importer-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	var diagnostic bytes.Buffer
	cmd := ciExternalCommand(ctx, o.CMakeCommand, "-E", "tar", "xf", archive)
	cmd.Dir = staging
	cmd.Stderr = &diagnostic
	if err := cmd.Run(); err != nil || diagnostic.Len() != 0 {
		return fmt.Errorf("extract cached importer: %v: %s", err, &diagnostic)
	}
	program := filepath.Join(staging, name)
	if info, err := os.Lstat(program); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("cached importer is not a regular program")
	}
	if err := os.Chmod(program, 0755); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.WriteFile(entryTarget, entry, 0644); err != nil {
		return err
	}
	return os.Rename(program, target)
}
