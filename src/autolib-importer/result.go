package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func cmakeLiteral(value string) string {
	for equals := ""; ; equals += "=" {
		if !strings.Contains(value, "]"+equals+"]") && !strings.HasSuffix(value, "]"+equals) {
			return "[" + equals + "[" + value + "]" + equals + "]"
		}
	}
}

func writePreparedResult(path string, packages []preparedPackage) error {
	var body strings.Builder
	names := []string{}
	for _, p := range packages {
		if !packageName.MatchString(p.Name) {
			return fmt.Errorf("invalid result package name %q", p.Name)
		}
		names = append(names, p.Name)
		fmt.Fprintf(&body, "set(AUTOLIB_PACKAGE_DIR_%s %s)\n", p.Name, cmakeLiteral(filepath.ToSlash(p.Directory)))
	}
	fmt.Fprintf(&body, "set(AUTOLIB_PREPARED_PACKAGES %s)\n", cmakeLiteral(strings.Join(names, ";")))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".result-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := io.WriteString(f, body.String())
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
