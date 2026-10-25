package main

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func testDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func buildTestImporter(t *testing.T) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	outputName := "autolib-importer"
	if runtime.GOOS == "windows" {
		outputName += ".exe"
	}
	output := filepath.Join(t.TempDir(), outputName)
	command := exec.Command("cmake", "-P", filepath.Join(repositoryRoot, "src", "autolib-builder", "go-sdk.cmake"), "--", "build", "-mod=readonly", "-trimpath", "-o", output, "./src/autolib-importer")
	command.Dir = repositoryRoot
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build importer fixture: %v\n%s", err, data)
	}
	return output
}

type testArchiveMember struct {
	Name, Body, Link string
	Kind             byte
}

func testPackageArchive(t *testing.T, name, osName, arch string, contents []testArchiveMember, adapterExtra string, dependencies ...string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.tar.zst")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressor, err := zstd.NewWriter(file)
	if err != nil {
		t.Fatal(err)
	}
	tarWriter := tar.NewWriter(compressor)
	identity := testIdentityHeader(name, osName, arch, dependencies...) + adapterExtra
	members := append([]testArchiveMember{{Name: name + "/autolib-import.cmake", Body: identity}}, contents...)
	for _, member := range members {
		kind := member.Kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: member.Name, Mode: 0644, Typeflag: kind, Linkname: member.Link}
		if kind == tar.TypeReg {
			header.Size = int64(len(member.Body))
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			if _, err := tarWriter.Write([]byte(member.Body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := errors.Join(tarWriter.Close(), compressor.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testIdentityHeader(name, osName, arch string, dependencies ...string) string {
	return fmt.Sprintf("set(AUTOLIB_PACKAGE_NAME \"%s\")\nset(AUTOLIB_OS \"%s\")\nset(AUTOLIB_ARCH \"%s\")\nset(AUTOLIB_DEPENDENCIES \"%s\")\n", name, osName, arch, strings.Join(dependencies, ";"))
}

func testPlatform() (string, string) {
	platformOS := runtime.GOOS
	if platformOS == "darwin" {
		platformOS = "linux"
	}
	platformArch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	return platformOS, platformArch
}

func mustReadText(t *testing.T, filename string) string {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeCIFixture(t *testing.T, root, path, value string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}
