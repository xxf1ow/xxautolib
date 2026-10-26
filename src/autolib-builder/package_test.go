package main

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestPackageDependencyHeader(t *testing.T) {
	for _, tc := range []struct{ name, declaration, want string }{
		{"flann", "depends=['lz4']\n", "lz4"},
		{"empty", "", ""},
		{"sorted", "depends=['zlib','lz4','zlib']\n", "lz4;zlib"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := stageFixture(t, tc.name)
			definitionFile(t, o.SourceRoot, tc.name, "cmake", tc.declaration+testSource)
			if tc.name == "sorted" {
				f := &fixture{t: t, root: o.SourceRoot}
				consumptionModule(f, "zlib", "")
				if err := consumptionCommand(f, "zlib", "reuse"); err != nil {
					t.Fatal(err)
				}
			}
			if err := prepareConsumption(consumptionOptions{SourceRoot: o.SourceRoot, InstallRoot: o.InstallRoot, Name: tc.name, OS: o.OS, Arch: o.Arch, Phase: "reuse"}); err != nil {
				t.Fatal(err)
			}
			dir, err := stagePackage(o)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(readFile(t, filepath.Join(dir, "autolib-import.cmake")), "\n")
			if len(lines) < 4 || lines[3] != `set(AUTOLIB_DEPENDENCIES "`+tc.want+`")` {
				t.Fatalf("missing direct dependency header: %q", lines)
			}
		})
	}
	for _, invalid := range []string{"CON.txt", "trailing."} {
		t.Run(invalid, func(t *testing.T) {
			o := stageFixture(t, "valid")
			o.Name = invalid
			if _, err := stagePackage(o); err == nil {
				t.Fatal("accepted unsafe package name")
			}
		})
	}
}

func TestPackageArchiveChecksum(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	putFile(t, filepath.Join(dir, "data"), strings.Repeat("archive content\n", 1000))
	archive := filepath.Join(t.TempDir(), "demo.tar.zst")
	if err := writePackageArchive(dir, archive); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	var header zstd.Header
	if err := header.Decode(data); err != nil || !header.HasCheckSum {
		t.Fatalf("missing content checksum: %+v %v", header, err)
	}
	for _, kind := range []string{"valid", "corrupt", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			input := bytes.Clone(data)
			if kind == "corrupt" {
				input[len(input)-1] ^= 1
			}
			if kind == "truncated" {
				input = input[:len(input)-1]
			}
			decoder, err := zstd.NewReader(bytes.NewReader(input))
			if err != nil {
				t.Fatal(err)
			}
			defer decoder.Close()
			_, err = io.Copy(io.Discard, decoder)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("decode %s: %v", kind, err)
			}
		})
	}
}

func stageFixture(t *testing.T, name string) stageOptions {
	t.Helper()
	f := newFixture(t)
	consumptionModule(f, "lz4", "")
	if err := consumptionCommand(f, "lz4", "reuse"); err != nil {
		t.Fatal(err)
	}
	definitionFile(t, f.root, name, "cmake", "depends=['lz4']\n"+testSource)
	putFile(t, filepath.Join(f.root, "src/dependencies", name, "autolib-import.cmake"), "# adapter 正文\n")
	putFile(t, filepath.Join(f.root, "3rdparty", name, "include/input.h"), "unchanged installation\n")
	if err := consumptionCommand(f, name, "reuse"); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("fixup-metadata.cmake")
	if err == nil {
		putFile(t, filepath.Join(f.root, "src/autolib-builder/fixup-metadata.cmake"), string(script))
	}
	return stageOptions{SourceRoot: f.root, InstallRoot: filepath.Join(f.root, "3rdparty"), StageRoot: filepath.Join(f.root, "build/stage"), Name: name, OS: "windows", Arch: "x86_64", CMakeCommand: f.context.CMakeCommand}
}

func TestStagePackage(t *testing.T) {
	o := stageFixture(t, "queue")
	input := filepath.Join(o.InstallRoot, o.Name, "include/input.h")
	before, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(o.SourceRoot, "src/dependencies/queue/autolib-import.cmake"), "# template changed after installation\n")
	staged, err := stagePackage(o)
	if err != nil {
		t.Fatal(err)
	}
	if staged != filepath.Join(o.StageRoot, "queue") {
		t.Fatal(staged)
	}
	requireFile(t, filepath.Join(staged, "include/input.h"), "unchanged installation\n")
	requireFile(t, filepath.Join(staged, "autolib-import.cmake"), "set(AUTOLIB_PACKAGE_NAME \"queue\")\nset(AUTOLIB_OS \"windows\")\nset(AUTOLIB_ARCH \"x86_64\")\nset(AUTOLIB_DEPENDENCIES \"lz4\")\n# adapter 正文\n")
	requireFile(t, input, "unchanged installation\n")
	after, _ := os.Stat(input)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("installation mtime changed")
	}
	for _, tc := range []string{"missing adapter", "missing installation", "overlap", "existing stage", "invalid platform", "invalid name", "missing descriptor", "invalid descriptor", "incomplete descriptor", "wrong identity", "wrong dependencies"} {
		t.Run(tc, func(t *testing.T) {
			o := stageFixture(t, "demo")
			switch tc {
			case "missing adapter":
				if err := os.Remove(filepath.Join(o.SourceRoot, "src/dependencies/demo/autolib-import.cmake")); err != nil {
					t.Fatal(err)
				}
			case "missing installation":
				o.InstallRoot = filepath.Join(o.SourceRoot, "missing")
			case "overlap":
				o.StageRoot = o.InstallRoot
			case "existing stage":
				putFile(t, filepath.Join(o.StageRoot, "demo/sentinel"), "preserve")
			case "invalid platform":
				o.OS = "other"
			case "invalid name":
				o.Name = "../demo"
			case "missing descriptor":
				if err := os.Remove(filepath.Join(o.InstallRoot, "demo/autolib-import.cmake")); err != nil {
					t.Fatal(err)
				}
			case "invalid descriptor":
				putFile(t, filepath.Join(o.InstallRoot, "demo/autolib-import.cmake"), "invalid")
			case "incomplete descriptor":
				putFile(t, filepath.Join(o.InstallRoot, "demo/autolib-import.cmake"), "# AutoLib installation incomplete\n")
			case "wrong identity", "wrong dependencies":
				path := filepath.Join(o.InstallRoot, "demo/autolib-import.cmake")
				old, replacement := `"demo"`, `"other"`
				if tc == "wrong dependencies" {
					old, replacement = `"lz4"`, `"other"`
				}
				putFile(t, path, strings.Replace(readFile(t, path), old, replacement, 1))
			}
			if _, err := stagePackage(o); err == nil {
				t.Fatal("accepted invalid staging input")
			}
			requireFile(t, filepath.Join(o.SourceRoot, "3rdparty/demo/include/input.h"), "unchanged installation\n")
			if tc == "existing stage" {
				requireFile(t, filepath.Join(o.StageRoot, "demo/sentinel"), "preserve")
			}
		})
	}
}

func TestStageInstallationUnchanged(t *testing.T) {
	o := stageFixture(t, "flann")
	putFile(t, filepath.Join(o.InstallRoot, "flann/lib/cmake/flann/config.cmake"), "set(PREFIX \""+filepath.ToSlash(o.InstallRoot)+"/flann\")\n")
	putFile(t, filepath.Join(o.InstallRoot, "flann/lib/pkgconfig/flann.pc"), "prefix="+filepath.ToSlash(o.InstallRoot)+"/flann\n")
	if err := os.Symlink("include/input.h", filepath.Join(o.InstallRoot, "flann/link")); err != nil {
		t.Logf("symlink unavailable: %v", err)
	}
	type entry struct {
		Mode     os.FileMode
		Modified time.Time
		Content  string
	}
	snapshot := func() map[string]entry {
		t.Helper()
		entries := map[string]entry{}
		err := filepath.WalkDir(o.InstallRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(o.InstallRoot, path)
			if err != nil {
				return err
			}
			item := entry{Mode: info.Mode(), Modified: info.ModTime()}
			if info.Mode()&os.ModeSymlink != 0 {
				item.Content, err = os.Readlink(path)
			} else if info.Mode().IsRegular() {
				var data []byte
				data, err = os.ReadFile(path)
				item.Content = string(data)
			}
			entries[rel] = item
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return entries
	}
	before := snapshot()
	if _, err := stagePackage(o); err != nil {
		t.Fatal(err)
	}
	after := snapshot()
	if !reflect.DeepEqual(before, after) {
		for path, previous := range before {
			if !reflect.DeepEqual(previous, after[path]) { t.Errorf("installation entry %s: before %+v, after %+v", path, previous, after[path]) }
		}
		t.Fatal("packaging modified installation entries, bytes or metadata")
	}
}

func TestStageFixup(t *testing.T) {
	o := stageFixture(t, "flann")
	prefix := filepath.ToSlash(o.InstallRoot)
	metadata := "set(OWN \"" + prefix + "/flann/include\")\nset(DEP \"" + prefix + "/lz4/lib/lz4.lib\")\nset(SYSTEM \"/usr/lib/system.so\")\nset(REL \"../include\")\n"
	input := putFile(t, filepath.Join(o.InstallRoot, "flann/lib/cmake/flann/config.cmake"), metadata)
	putFile(t, filepath.Join(o.InstallRoot, "flann/lib/pkgconfig/flann.pc"), "prefix="+prefix+"/flann\n")
	before, _ := os.Stat(input)
	staged, err := stagePackage(o)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(staged, "lib/cmake/flann/config.cmake"))
	if strings.Contains(got, prefix) || !strings.Contains(got, "${CMAKE_CURRENT_LIST_DIR}/../../../../lz4/lib/lz4.lib") {
		t.Fatal(got)
	}
	if !strings.Contains(got, "/usr/lib/system.so") || !strings.Contains(got, "../include") {
		t.Fatal(got)
	}
	requireTextFile(t, filepath.Join(staged, "lib/pkgconfig/flann.pc"), "prefix=${pcfiledir}/../../../flann\n")
	requireFile(t, input, metadata)
	after, _ := os.Stat(input)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("metadata input modified")
	}
	// 仅移除自有 fixture 的原生产树，证明实际 CMake 加载不依赖生产目录。
	if err := os.Rename(o.InstallRoot, o.InstallRoot+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(o.StageRoot, "lz4/lib/lz4.lib"), "sibling")
	check := putFile(t, filepath.Join(o.SourceRoot, "check.cmake"), "include("+cmakeArgument(filepath.ToSlash(filepath.Join(staged, "lib/cmake/flann/config.cmake")))+")\nif(NOT EXISTS \"${DEP}\" OR NOT EXISTS \"${OWN}/input.h\")\nmessage(FATAL_ERROR \"relocation failed\")\nendif()\n")
	if out, err := exec.Command(o.CMakeCommand, "-P", check).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Run("undeclared producer path", func(t *testing.T) {
		o := stageFixture(t, "demo")
		putFile(t, filepath.Join(o.InstallRoot, "demo/bad.cmake"), "set(BAD \""+filepath.ToSlash(o.InstallRoot)+"/unknown/lib.a\")")
		if _, err := stagePackage(o); err == nil {
			t.Fatal("accepted unhandled producer path")
		}
	})
}

func TestPackageArchive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	data := strings.Repeat("stream data 中文\n", 20000)
	putFile(t, filepath.Join(dir, "include/data.h"), data)
	putFile(t, filepath.Join(dir, "empty"), "")
	archive := filepath.Join(t.TempDir(), "package.tar.zst")
	if err := writePackageArchive(dir, archive); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := zstd.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	tr := tar.NewReader(z)
	seen := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name != "demo/" && !strings.HasPrefix(h.Name, "demo/") {
			t.Fatal(h.Name)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		seen[h.Name] = string(b)
	}
	if seen["demo/include/data.h"] != data {
		t.Fatal("archive lost bytes")
	}
	if _, ok := seen["demo/empty"]; !ok {
		t.Fatal("missing empty file")
	}
	t.Run("internal symlink", func(t *testing.T) {
		if err := os.Symlink("include/data.h", filepath.Join(dir, "link")); err != nil {
			t.Skipf("host cannot create symlink: %v", err)
		}
		if err := writePackageArchive(dir, filepath.Join(t.TempDir(), "linked.tar.zst")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../../outside", filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		if err := writePackageArchive(dir, filepath.Join(t.TempDir(), "unsafe.tar.zst")); err == nil {
			t.Fatal("accepted external symlink")
		}
	})
}
