package main

import (
	"archive/tar"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const versionedEigenHeader = "set(AUTOLIB_PACKAGE_NAME \"eigen\")\nset(AUTOLIB_PACKAGE_VERSION \"5.0.1\")\nset(AUTOLIB_OS \"any\")\nset(AUTOLIB_ARCH \"any\")\nset(AUTOLIB_DEPENDENCIES \"zlib@1.3.1;lz4@1.10.0;zlib@1.3.1\")\n"

func TestVersionedPackageIdentity(t *testing.T) {
	t.Run("references", func(t *testing.T) {
		for _, tc := range []struct {
			value string
			want  packageRef
		}{
			{"eigen", packageRef{Name: "eigen"}},
			{"eigen@5.0.1", packageRef{Name: "eigen", Version: "5.0.1"}},
			{"eigen@v1.0+build-2", packageRef{Name: "eigen", Version: "v1.0+build-2"}},
			{"eigen@0123456789ab", packageRef{Name: "eigen", Version: "0123456789ab"}},
		} {
			got, err := parsePackageRef(tc.value)
			if err != nil || got != tc.want || got.String() != tc.value {
				t.Fatalf("%q: %+v, %v", tc.value, got, err)
			}
		}
		for _, value := range []string{"", "../eigen", "eigen@", "@5.0.1", "eigen@@5", "eigen@../5", "eigen@~5", "eigen@5 0", "eigen@CON.txt", "eigen@PACKAGE.TOML", "eigen@1."} {
			if _, err := parsePackageRef(value); err == nil {
				t.Fatalf("accepted %q", value)
			}
		}
	})
	t.Run("metadata", func(t *testing.T) {
		for _, tc := range []struct {
			name, header string
			valid        bool
		}{
			{"five lines", versionedEigenHeader, true},
			{"crlf", strings.ReplaceAll(versionedEigenHeader, "\n", "\r\n"), true},
			{"empty dependencies", strings.ReplaceAll(versionedEigenHeader, "zlib@1.3.1;lz4@1.10.0;zlib@1.3.1", ""), true},
			{"bare dependency", strings.ReplaceAll(versionedEigenHeader, "zlib@1.3.1", "zlib"), false},
			{"empty dependency", strings.ReplaceAll(versionedEigenHeader, ";lz4", ";;lz4"), false},
			{"dependency range", strings.ReplaceAll(versionedEigenHeader, "1.3.1", "^1.3"), false},
			{"empty version", strings.ReplaceAll(versionedEigenHeader, "5.0.1", ""), false},
			{"four lines", strings.ReplaceAll(versionedEigenHeader, "set(AUTOLIB_PACKAGE_VERSION \"5.0.1\")\n", ""), false},
			{"incomplete", "# AutoLib installation incomplete\n", false},
			{"half universal", strings.Replace(versionedEigenHeader, "\"any\"", "\"linux\"", 1), false},
			{"invalid platform", strings.ReplaceAll(versionedEigenHeader, "\"any\"", "\"mips\""), false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "eigen", "5.0.1")
				putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), tc.header+"message(FATAL_ERROR \"must not execute\")\n")
				got, err := readPackageMetadata(dir)
				if (err == nil) != tc.valid {
					t.Fatalf("metadata %+v: %v", got, err)
				}
				if tc.valid && (got.Identity != (packageIdentity{Name: "eigen", Version: "5.0.1", OS: "any", Arch: "any"}) || (tc.name == "empty dependencies" && len(got.Dependencies) != 0) || (tc.name != "empty dependencies" && !slices.Equal(got.Dependencies, []string{"lz4@1.10.0", "zlib@1.3.1"}))) {
					t.Fatalf("unexpected metadata %+v", got)
				}
			})
		}
	})
	t.Run("identity matching", func(t *testing.T) {
		requested := packageIdentity{Name: "eigen", Version: "5.0.1", OS: "linux", Arch: "x86_64"}
		if !matchesPackageIdentity(requested, requested) || !matchesPackageIdentity(packageIdentity{Name: "eigen", Version: "5.0.1", OS: "any", Arch: "any"}, requested) {
			t.Fatal("rejected exact or universal identity")
		}
		for _, actual := range []packageIdentity{
			{Name: "eigen", Version: "3.4.0", OS: "any", Arch: "any"},
			{Name: "eigen", Version: "5.0.1", OS: "windows", Arch: "x86_64"},
			{Name: "eigen", Version: "5.0.1", OS: "any", Arch: "x86_64"},
			{Name: "other", Version: "5.0.1", OS: "any", Arch: "any"},
			{Name: "eigen", OS: "any", Arch: "any"},
		} {
			if matchesPackageIdentity(actual, requested) {
				t.Fatalf("accepted %+v", actual)
			}
		}
	})
}

func TestVersionedLocalCatalog(t *testing.T) {
	t.Run("version validation", func(t *testing.T) {
		for _, version := range []string{"5.0.1", "v1", "V1", "1.0+build-2", "0123456789ab"} {
			if err := validatePackageVersion(version); err != nil {
				t.Fatal(err)
			}
		}
		for _, version := range []string{"", "CON.txt", "con", "LPT9.log", "PACKAGE.TOML", "package.toml", "1.", "../1", "v/1", `v\1`, "1@2", "1 2"} {
			if err := validatePackageVersion(version); err == nil {
				t.Fatalf("accepted version %q", version)
			}
		}
	})
	t.Run("latest is explicit", func(t *testing.T) {
		root := t.TempDir()
		putTestFile(t, filepath.Join(root, "eigen", "99.0", "data"), "unselected")
		if _, err := readPackageLatest(root, "eigen"); !os.IsNotExist(err) {
			t.Fatalf("missing latest is not absence: %v", err)
		}
		for _, version := range []string{"3.4.0", "0123456789ab", "V1"} {
			putTestFile(t, filepath.Join(root, "eigen", "package.toml"), "latest = \""+version+"\"\n")
			got, err := readPackageLatest(root, "eigen")
			if err != nil || got != version {
				t.Fatalf("%q: %q %v", version, got, err)
			}
		}
		for _, body := range []string{"", "latest = 1\n", "latest = \"\"\n", "latest = \"1.\"\n", "latest = \"CON.txt\"\n", "latest = \"PACKAGE.TOML\"\n", "latest = [", "latest = \"3.4.0\"\nextra = true\n"} {
			putTestFile(t, filepath.Join(root, "eigen", "package.toml"), body)
			if _, err := readPackageLatest(root, "eigen"); err == nil || os.IsNotExist(err) {
				t.Fatalf("invalid latest treated as missing or accepted: %q %v", body, err)
			}
		}
	})
	t.Run("directory identity", func(t *testing.T) {
		root := t.TempDir()
		id := packageIdentity{Name: "eigen", Version: "5.0.1", OS: "linux", Arch: "x86_64"}
		if got := packageDirectory(root, id); got != filepath.Join(root, "eigen", "5.0.1") {
			t.Fatal(got)
		}
		for _, relative := range []string{"eigen/3.4.0", "other/5.0.1", "eigen/5.0.1-alias", "Eigen/5.0.1"} {
			dir := filepath.Join(t.TempDir(), filepath.FromSlash(relative))
			putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), versionedEigenHeader)
			if _, err := readPackageMetadata(dir); err == nil {
				t.Fatalf("accepted wrong directory %s", relative)
			}
		}
	})
	t.Run("directory case alias", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "eigen", "V1")
		putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), strings.ReplaceAll(versionedEigenHeader, "5.0.1", "V1"))
		putTestFile(t, filepath.Join(root, "eigen", "package.toml"), "latest = \"v1\"\n")
		if _, err := readPackageLatest(root, "eigen"); err == nil {
			t.Fatal("latest accepted a directory case alias")
		}
		if _, err := readPackageMetadata(filepath.Join(root, "eigen", "v1")); err == nil {
			t.Fatal("metadata accepted a directory case alias")
		}
	})
	t.Run("directory collisions", func(t *testing.T) {
		root := t.TempDir()
		for _, version := range []string{"V1", "v1"} {
			putTestFile(t, filepath.Join(root, "eigen", version, "autolib-import.cmake"), strings.ReplaceAll(versionedEigenHeader, "5.0.1", version))
		}
		entries, err := os.ReadDir(filepath.Join(root, "eigen"))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Skip("host cannot represent case-colliding directories")
		}
		putTestFile(t, filepath.Join(root, "eigen", "package.toml"), "latest = \"V1\"\n")
		if _, err := readPackageLatest(root, "eigen"); err == nil {
			t.Fatal("accepted case-colliding latest directory")
		}
		if _, err := readPackageMetadata(filepath.Join(root, "eigen", "V1")); err == nil {
			t.Fatal("accepted case-colliding installed versions")
		}
	})
}

func TestVersionedArchiveIdentity(t *testing.T) {
	id := packageIdentity{Name: "eigen", Version: "5.0.1", OS: "any", Arch: "any"}
	header := strings.ReplaceAll(versionedEigenHeader, "zlib@1.3.1;lz4@1.10.0;zlib@1.3.1", "")
	valid := []archiveMember{{Name: "eigen/", Kind: tar.TypeDir}, {Name: "eigen/5.0.1/", Kind: tar.TypeDir}, {Name: "eigen/5.0.1/autolib-import.cmake", Body: header}, {Name: "eigen/5.0.1/include/data", Body: "preserve"}}
	t.Run("correct root", func(t *testing.T) {
		dir, err := extractPackage(testArchive(t, valid), t.TempDir(), id)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(dir) != "5.0.1" || filepath.Base(filepath.Dir(dir)) != "eigen" || readTestFile(t, filepath.Join(dir, "include/data")) != "preserve" {
			t.Fatal(dir)
		}
	})
	for name, members := range map[string][]archiveMember{
		"wrong version":        {{Name: "eigen/3.4.0/autolib-import.cmake", Body: header}},
		"flat root":            {{Name: "eigen/autolib-import.cmake", Body: header}},
		"other package":        {{Name: "other/5.0.1/autolib-import.cmake", Body: header}},
		"neighbor version":     append(slices.Clone(valid), archiveMember{Name: "eigen/3.4.0/data", Body: "bad"}),
		"wrong header version": {{Name: "eigen/5.0.1/autolib-import.cmake", Body: strings.ReplaceAll(header, "5.0.1", "3.4.0")}},
		"wrong platform":       {{Name: "eigen/5.0.1/autolib-import.cmake", Body: strings.Replace(strings.Replace(header, "\"any\"", "\"linux\"", 1), "\"any\"", "\"x86_64\"", 1)}},
		"traversal":            append(slices.Clone(valid), archiveMember{Name: "eigen/5.0.1/../../../outside/sentinel", Body: "changed"}),
		"link escape":          append(slices.Clone(valid), archiveMember{Name: "eigen/5.0.1/link", Link: "../3.4.0/data", Kind: tar.TypeSymlink}),
		"duplicate":            append(slices.Clone(valid), archiveMember{Name: "eigen/5.0.1/include/data", Body: "changed"}),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			sentinel := putTestFile(t, filepath.Join(root, "outside/sentinel"), "preserve")
			staging := filepath.Join(root, "staging")
			if err := os.Mkdir(staging, 0755); err != nil {
				t.Fatal(err)
			}
			if _, err := extractPackage(testArchive(t, members), staging, id); err == nil {
				t.Fatal("accepted invalid versioned archive")
			}
			if readTestFile(t, sentinel) != "preserve" {
				t.Fatal("outside data changed")
			}
			entries, err := os.ReadDir(staging)
			if err != nil || len(entries) != 0 {
				t.Fatalf("staging leaked: %v %v", entries, err)
			}
		})
	}
	t.Run("CRC", func(t *testing.T) {
		archive := testArchive(t, valid)
		data := []byte(readTestFile(t, archive))
		data[len(data)-1] ^= 1
		putTestFile(t, archive, string(data))
		staging := t.TempDir()
		if _, err := extractPackage(archive, staging, id); err == nil {
			t.Fatal("accepted bad CRC")
		}
		entries, err := os.ReadDir(staging)
		if err != nil || len(entries) != 0 {
			t.Fatalf("CRC failure leaked staging: %v %v", entries, err)
		}
	})
}
