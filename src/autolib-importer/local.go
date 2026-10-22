package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var sourceScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

func normalizeLocalSource(uri string) (string, error) {
	if uri == "" {
		return "", fmt.Errorf("local source must not be empty")
	}
	if scheme := sourceScheme.FindString(uri); len(scheme) > 2 {
		return "", fmt.Errorf("only local source directories are supported: %q", uri)
	}
	absolute, err := filepath.Abs(uri)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Clean(absolute)), nil
}

func sourceKey(source string) string {
	digest := sha256.Sum256([]byte(source))
	return hex.EncodeToString(digest[:])
}

func packageDirectory(workRoot, source string, id packageIdentity) string {
	return filepath.Join(workRoot, sourceKey(source), "packages", id.OS+"-"+id.Arch, id.Name)
}

func readPackageIdentity(directory string) (id packageIdentity, err error) {
	path := filepath.Join(directory, "autolib-import.cmake")
	defer func() {
		if err != nil {
			err = fmt.Errorf("package identity %s: expected fixed name/os/arch header: %w", path, err)
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return id, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for _, field := range []struct {
		key   string
		value *string
	}{{"AUTOLIB_PACKAGE_NAME", &id.Name}, {"AUTOLIB_OS", &id.OS}, {"AUTOLIB_ARCH", &id.Arch}} {
		if !scanner.Scan() {
			return id, fmt.Errorf("missing %s: %v", field.key, scanner.Err())
		}
		prefix := `set(` + field.key + ` "`
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, `")`) {
			return id, fmt.Errorf("invalid %s line", field.key)
		}
		*field.value = strings.TrimSuffix(strings.TrimPrefix(line, prefix), `")`)
	}
	if err := validateIdentity(id); err != nil {
		return id, err
	}
	return id, nil
}
