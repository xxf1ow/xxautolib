package main

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/klauspost/compress/zstd"
)

func archivePathKey(value string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(value)
	}
	return value
}

func validateArchivePath(value string) error {
	if value == "" || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00") || (len(value) >= 2 && value[1] == ':') {
		return fmt.Errorf("invalid archive path %q", value)
	}
	if runtime.GOOS == "windows" {
		for _, part := range strings.Split(value, "/") {
			if part == "" || part == "." || part == ".." {
				continue
			}
			if strings.TrimRight(part, " .") != part || !filepath.IsLocal(part) || strings.ContainsAny(part, `<>:"|?*`) {
				return fmt.Errorf("ambiguous Windows archive path %q", value)
			}
			for _, r := range part {
				if r < 32 {
					return fmt.Errorf("invalid Windows archive path %q", value)
				}
			}
		}
	}
	return nil
}

// Link components are expanded before '..'; cleaning first can hide an escape through a link.
func resolveArchiveLink(parts, stack []string, links map[string]string, active map[string]bool) ([]string, error) {
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return nil, fmt.Errorf("symbolic link leaves package root")
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, part)
			key := archivePathKey(strings.Join(stack, "/"))
			if target, ok := links[key]; ok {
				if active[key] {
					return nil, fmt.Errorf("symbolic link cycle at %s", key)
				}
				active[key] = true
				var err error
				stack, err = resolveArchiveLink(strings.Split(target, "/"), stack[:len(stack)-1], links, active)
				if err != nil {
					return nil, err
				}
				delete(active, key)
			}
		}
	}
	return stack, nil
}

func extractPackage(archivePath, stagingParent string, id packageIdentity) (directory string, err error) {
	if err := validateIdentity(id); err != nil {
		return "", err
	}
	work, err := os.MkdirTemp(stagingParent, ".extract-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(work))
		}
	}()
	directory = filepath.Join(work, id.Name)
	if err := os.Mkdir(directory, 0755); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	input, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer input.Close()
	z, err := zstd.NewReader(input)
	if err != nil {
		return "", err
	}
	defer z.Close()
	tr := tar.NewReader(z)
	entries := map[string]byte{"": tar.TypeDir}
	links := map[string]string{}
	linkNames := []string{}
	for {
		h, readErr := tr.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("archive %s: %w", archivePath, readErr)
		}
		if err := validateArchivePath(h.Name); err != nil {
			return "", err
		}
		clean := path.Clean(h.Name)
		if clean != id.Name && !strings.HasPrefix(clean, id.Name+"/") {
			return "", fmt.Errorf("archive member %q is outside top-level %s", h.Name, id.Name)
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(clean, id.Name), "/")
		kind := h.Typeflag
		if kind == tar.TypeRegA {
			kind = tar.TypeReg
		}
		if kind != tar.TypeReg && kind != tar.TypeDir && kind != tar.TypeSymlink {
			return "", fmt.Errorf("archive member %q: unsupported type %d", h.Name, kind)
		}
		key := archivePathKey(rel)
		if previous, ok := entries[key]; ok && (previous != tar.TypeDir || kind != tar.TypeDir) {
			return "", fmt.Errorf("archive member %q: duplicate or conflicting type", h.Name)
		}
		for parent := path.Dir(rel); parent != "."; parent = path.Dir(parent) {
			key := archivePathKey(parent)
			if previous, ok := entries[key]; ok && previous != tar.TypeDir {
				return "", fmt.Errorf("archive member %q: non-directory ancestor %s", h.Name, parent)
			}
			entries[key] = tar.TypeDir
		}
		entries[key] = kind
		if rel == "" {
			continue
		}
		if err := root.MkdirAll(filepath.FromSlash(path.Dir(rel)), 0755); err != nil {
			return "", err
		}
		switch kind {
		case tar.TypeDir:
			if err := root.MkdirAll(filepath.FromSlash(rel), 0755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			f, err := root.OpenFile(filepath.FromSlash(rel), os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode)&0777)
			if err != nil {
				return "", err
			}
			_, copyErr := io.Copy(f, tr)
			if err := errors.Join(copyErr, f.Close()); err != nil {
				return "", err
			}
		case tar.TypeSymlink:
			if err := validateArchivePath(h.Linkname); err != nil {
				return "", err
			}
			links[key] = h.Linkname
			linkNames = append(linkNames, rel)
		}
	}
	if _, err := io.Copy(io.Discard, z); err != nil {
		return "", err
	}
	// No links exist during member writes. Validate the complete graph before creating any.
	for _, name := range linkNames {
		target := links[archivePathKey(name)]
		if _, err := resolveArchiveLink(strings.Split(path.Dir(name)+"/"+target, "/"), nil, links, map[string]bool{}); err != nil {
			return "", fmt.Errorf("link %s: %w", name, err)
		}
	}
	for _, name := range linkNames {
		if err := root.Symlink(filepath.FromSlash(links[archivePathKey(name)]), filepath.FromSlash(name)); err != nil {
			return "", err
		}
	}
	metadata, err := readPackageMetadata(directory)
	if err != nil {
		return "", err
	}
	if metadata.Identity != id {
		return "", fmt.Errorf("package %s: expected identity %+v, got %+v", directory, id, metadata.Identity)
	}
	return directory, nil
}
