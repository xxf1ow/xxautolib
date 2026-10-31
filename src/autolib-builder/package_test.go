package main

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestPackageDependencyHeader(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"flann", "lz4@1.0"},
		{"empty", ""},
		{"multiple", "lz4@1.0;zlib@1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := stageFixture(t, tc.name)
			input := filepath.Join(o.InstallRoot, tc.name, "1.0/autolib-import.cmake")
			putFile(t, input, strings.ReplaceAll(readFile(t, input), "leaf@1", tc.want))
			dir, err := stagePackage(o)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(readFile(t, filepath.Join(dir, "autolib-import.cmake")), "\n")
			if len(lines) < 5 || lines[4] != `set(AUTOLIB_DEPENDENCIES "`+tc.want+`")` {
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
	if err := writePackageArchive(dir, archive, moduleRef{"demo", "1.0"}); err != nil {
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
	definitionFile(t, f.root, name, "cmake", "depends=['lz4']\n"+testSource)
	putFile(t, filepath.Join(f.root, "3rdparty", name, "1.0/include/input.h"), "unchanged installation\n")
	putFile(t, filepath.Join(f.root, "3rdparty", name, "1.0/autolib-import.cmake"), "set(AUTOLIB_PACKAGE_NAME \""+name+"\")\nset(AUTOLIB_PACKAGE_VERSION \"1.0\")\nset(AUTOLIB_OS \"windows\")\nset(AUTOLIB_ARCH \"x86_64\")\nset(AUTOLIB_DEPENDENCIES \"leaf@1\")\n# adapter 正文\n")
	script, err := os.ReadFile("../autolib-importer/fixup-metadata.cmake")
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(f.root, "src/autolib-importer/fixup-metadata.cmake"), string(script))
	return stageOptions{SourceRoot: f.root, InstallRoot: filepath.Join(f.root, "3rdparty"), StageRoot: filepath.Join(f.root, "build/stage"), Name: name, Version: "1.0", OS: "windows", Arch: "x86_64", CMakeCommand: f.context.CMakeCommand}
}

func TestStagePackage(t *testing.T) {
	o := stageFixture(t, "queue")
	input := filepath.Join(o.InstallRoot, o.Name, o.Version, "include/input.h")
	before, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(o.SourceRoot, "src/dependencies/queue/autolib-import.cmake"), "# template changed after installation\n")
	staged, err := stagePackage(o)
	if err != nil {
		t.Fatal(err)
	}
	if staged != filepath.Join(o.StageRoot, "queue/1.0") {
		t.Fatal(staged)
	}
	requireFile(t, filepath.Join(staged, "include/input.h"), "unchanged installation\n")
	requireFile(t, filepath.Join(staged, "autolib-import.cmake"), "set(AUTOLIB_PACKAGE_NAME \"queue\")\nset(AUTOLIB_PACKAGE_VERSION \"1.0\")\nset(AUTOLIB_OS \"windows\")\nset(AUTOLIB_ARCH \"x86_64\")\nset(AUTOLIB_DEPENDENCIES \"leaf@1\")\n# adapter 正文\n")
	requireFile(t, input, "unchanged installation\n")
	after, _ := os.Stat(input)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("installation mtime changed")
	}
	for _, tc := range []string{"missing installation", "overlap", "existing stage", "invalid platform", "invalid name", "invalid version", "missing descriptor", "invalid descriptor", "incomplete descriptor", "wrong identity", "wrong version", "wrong platform", "bare dependency"} {
		t.Run(tc, func(t *testing.T) {
			o := stageFixture(t, "demo")
			switch tc {
			case "missing installation":
				o.InstallRoot = filepath.Join(o.SourceRoot, "missing")
			case "overlap":
				o.StageRoot = o.InstallRoot
			case "existing stage":
				putFile(t, filepath.Join(o.StageRoot, "demo/1.0/sentinel"), "preserve")
			case "invalid platform":
				o.OS = "other"
			case "invalid name":
				o.Name = "../demo"
			case "invalid version":
				o.Version = "../1.0"
			case "missing descriptor":
				if err := os.Remove(filepath.Join(o.InstallRoot, "demo/1.0/autolib-import.cmake")); err != nil {
					t.Fatal(err)
				}
			case "invalid descriptor":
				putFile(t, filepath.Join(o.InstallRoot, "demo/1.0/autolib-import.cmake"), "invalid")
			case "incomplete descriptor":
				putFile(t, filepath.Join(o.InstallRoot, "demo/1.0/autolib-import.cmake"), "# AutoLib installation incomplete\n")
			case "wrong identity", "wrong version", "wrong platform", "bare dependency":
				path := filepath.Join(o.InstallRoot, "demo/1.0/autolib-import.cmake")
				old, replacement := `"demo"`, `"other"`
				if tc == "wrong version" {
					old, replacement = `"1.0"`, `"2.0"`
				}
				if tc == "wrong platform" {
					old, replacement = `"windows"`, `"linux"`
				}
				if tc == "bare dependency" {
					old, replacement = `"leaf@1"`, `"leaf"`
				}
				putFile(t, path, strings.Replace(readFile(t, path), old, replacement, 1))
			}
			if _, err := stagePackage(o); err == nil {
				t.Fatal("accepted invalid staging input")
			}
			requireFile(t, filepath.Join(o.SourceRoot, "3rdparty/demo/1.0/include/input.h"), "unchanged installation\n")
			if tc == "existing stage" {
				requireFile(t, filepath.Join(o.StageRoot, "demo/1.0/sentinel"), "preserve")
			}
		})
	}
}

func TestStageInstallationUnchanged(t *testing.T) {
	o := stageFixture(t, "flann")
	putFile(t, filepath.Join(o.InstallRoot, "flann/1.0/lib/cmake/flann/config.cmake"), "set(PREFIX \""+filepath.ToSlash(o.InstallRoot)+"/flann/1.0\")\n")
	putFile(t, filepath.Join(o.InstallRoot, "flann/1.0/lib/pkgconfig/flann.pc"), "prefix="+filepath.ToSlash(o.InstallRoot)+"/flann/1.0\n")
	if err := os.Chmod(filepath.Join(o.InstallRoot, "flann/1.0/include/input.h"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("include/input.h", filepath.Join(o.InstallRoot, "flann/1.0/link")); err != nil {
		if runtime.GOOS == "linux" {
			t.Fatal(err)
		}
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
	staged, err := stagePackage(o)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "linux" {
		info, err := os.Stat(filepath.Join(staged, "include/input.h"))
		if err != nil || info.Mode().Perm() != 0755 {
			t.Fatalf("copy lost executable bits: %v %v", info, err)
		}
	}
	after := snapshot()
	if !reflect.DeepEqual(before, after) {
		for path, previous := range before {
			if !reflect.DeepEqual(previous, after[path]) {
				t.Errorf("installation entry %s: before %+v, after %+v", path, previous, after[path])
			}
		}
		t.Fatal("packaging modified installation entries, bytes or metadata")
	}
}

func TestVersionedStageFixup(t *testing.T) {
	for _, bad := range []string{"", "leaf/2", "leaf/10", "other/1", "leaf/1/../../leaf/2"} {
		t.Run(map[bool]string{true: "exact dependency", false: bad}[bad == ""], func(t *testing.T) {
			o := stageFixture(t, "demo")
			prefix := filepath.ToSlash(o.InstallRoot)
			dep := "leaf/1"
			if bad != "" {
				dep = bad
			}
			putFile(t, filepath.Join(o.InstallRoot, "demo/1.0/lib/cmake/demo/demo-config.cmake"), "set(DEMO_PREFIX \""+prefix+"/demo/1.0\")\nset(DEMO_LEAF_PREFIX \""+prefix+"/"+dep+"\")\n")
			putFile(t, filepath.Join(o.InstallRoot, "demo/1.0/lib/pkgconfig/demo.pc"), "prefix="+prefix+"/demo/1.0\nleaf="+prefix+"/"+dep+"\n")
			// 已安装描述是依赖身份来源；暂存不读取配方。
			if err := os.RemoveAll(filepath.Join(o.SourceRoot, "src/dependencies")); err != nil {
				t.Fatal(err)
			}
			staged, err := stagePackage(o)
			if bad != "" {
				if err == nil {
					t.Fatal("accepted undeclared exact producer reference")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if staged != filepath.Join(o.StageRoot, "demo/1.0") {
				t.Fatal(staged)
			}
			putFile(t, filepath.Join(o.StageRoot, "leaf/1/marker"), "exact")
			putFile(t, filepath.Join(o.StageRoot, "leaf/2/marker"), "wrong version")
			moved := filepath.Join(o.SourceRoot, "build/moved")
			if err := os.Rename(o.StageRoot, moved); err != nil {
				t.Fatal(err)
			}
			check := putFile(t, filepath.Join(o.SourceRoot, "check.cmake"), "include(\""+filepath.ToSlash(filepath.Join(moved, "demo/1.0/lib/cmake/demo/demo-config.cmake"))+"\")\nget_filename_component(actual \"${DEMO_LEAF_PREFIX}\" REALPATH)\nget_filename_component(expected \""+filepath.ToSlash(filepath.Join(moved, "leaf/1"))+"\" REALPATH)\nif(NOT actual STREQUAL expected OR NOT EXISTS \"${DEMO_PREFIX}/include/input.h\")\nmessage(FATAL_ERROR \"wrong exact dependency prefix: ${actual}; expected ${expected}\")\nendif()\n")
			if output, err := exec.Command(o.CMakeCommand, "-P", check).CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
			requireTextFile(t, filepath.Join(moved, "demo/1.0/lib/pkgconfig/demo.pc"), "prefix=${pcfiledir}/../../../../demo/1.0\nleaf=${pcfiledir}/../../../../leaf/1\n")
		})
	}
	if runtime.GOOS != "linux" {
		return
	}
	t.Run("case-colliding installed versions", func(t *testing.T) {
		o := stageFixture(t, "demo")
		for _, version := range []string{"V1", "v1"} {
			putFile(t, filepath.Join(o.InstallRoot, "demo", version, "sentinel"), "preserve")
		}
		if _, err := stagePackage(o); err == nil {
			t.Fatal("accepted case-colliding installed versions")
		}
	})
	for _, kind := range []string{"shared library", "metadata link", "absolute link", "escaping link"} {
		t.Run(kind, func(t *testing.T) {
			o := stageFixture(t, "demo")
			input := filepath.Join(o.InstallRoot, "demo/1.0")
			if kind == "shared library" {
				source := putFile(t, filepath.Join(o.SourceRoot, "answer.c"), "int answer(void) { return 42; }\n")
				library := filepath.Join(input, "lib/libanswer.so.1")
				if err := os.MkdirAll(filepath.Dir(library), 0755); err != nil {
					t.Fatal(err)
				}
				if output, err := exec.Command("cc", "-shared", "-fPIC", "-Wl,-soname,libanswer.so.1", "-o", library, source).CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, output)
				}
				if err := os.Symlink("libanswer.so.1", filepath.Join(input, "lib/libanswer.so")); err != nil {
					t.Fatal(err)
				}
				staged, err := stagePackage(o)
				if err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(staged, "lib/libanswer.so")
				if output, err := exec.Command("readlink", link).CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "libanswer.so.1" {
					t.Fatalf("readlink: %s %v", output, err)
				}
				main := putFile(t, filepath.Join(o.SourceRoot, "main.c"), "int answer(void); int main(void) { return answer() == 42 ? 0 : 1; }\n")
				binary := filepath.Join(o.SourceRoot, "consumer")
				if output, err := exec.Command("cc", main, "-L"+filepath.Join(staged, "lib"), "-lanswer", "-Wl,-rpath,"+filepath.Join(staged, "lib"), "-o", binary).CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, output)
				}
				if err := os.Rename(o.InstallRoot, o.InstallRoot+"-unavailable"); err != nil {
					t.Fatal(err)
				}
				if output, err := exec.Command(binary).CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, output)
				}
				return
			}
			original := "set(PREFIX \"" + filepath.ToSlash(o.InstallRoot) + "/demo/1.0\")\n"
			source := putFile(t, filepath.Join(input, "metadata.txt"), original)
			link, target := filepath.Join(input, "config.cmake"), "metadata.txt"
			if kind == "absolute link" {
				target = source
			}
			if kind == "escaping link" {
				target = "../../outside"
				putFile(t, filepath.Join(o.InstallRoot, "outside"), original)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, err := stagePackage(o); err == nil {
				t.Fatal("accepted unsafe package link")
			}
			requireFile(t, source, original)
		})
	}
}

func TestStageFixup(t *testing.T) {
	o := stageFixture(t, "flann")
	prefix := filepath.ToSlash(o.InstallRoot)
	metadata := "set(OWN \"" + prefix + "/flann/1.0/include\")\nset(DEP \"" + prefix + "/leaf/1/lib/leaf.lib\")\nset(SYSTEM \"/usr/lib/system.so\")\nset(REL \"../include\")\n"
	input := putFile(t, filepath.Join(o.InstallRoot, "flann/1.0/lib/cmake/flann/config.cmake"), metadata)
	putFile(t, filepath.Join(o.InstallRoot, "flann/1.0/lib/pkgconfig/flann.pc"), "prefix="+prefix+"/flann/1.0\n")
	before, _ := os.Stat(input)
	staged, err := stagePackage(o)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(staged, "lib/cmake/flann/config.cmake"))
	if strings.Contains(got, prefix) || !strings.Contains(got, "${CMAKE_CURRENT_LIST_DIR}/../../../../../leaf/1/lib/leaf.lib") {
		t.Fatal(got)
	}
	if !strings.Contains(got, "/usr/lib/system.so") || !strings.Contains(got, "../include") {
		t.Fatal(got)
	}
	requireTextFile(t, filepath.Join(staged, "lib/pkgconfig/flann.pc"), "prefix=${pcfiledir}/../../../../flann/1.0\n")
	requireFile(t, input, metadata)
	after, _ := os.Stat(input)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("metadata input modified")
	}
	// 仅移除自有 fixture 的原生产树，证明实际 CMake 加载不依赖生产目录。
	if err := os.Rename(o.InstallRoot, o.InstallRoot+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(o.StageRoot, "leaf/1/lib/leaf.lib"), "sibling")
	check := putFile(t, filepath.Join(o.SourceRoot, "check.cmake"), "include("+cmakeArgument(filepath.ToSlash(filepath.Join(staged, "lib/cmake/flann/config.cmake")))+")\nif(NOT EXISTS \"${DEP}\" OR NOT EXISTS \"${OWN}/input.h\")\nmessage(FATAL_ERROR \"relocation failed\")\nendif()\n")
	if out, err := exec.Command(o.CMakeCommand, "-P", check).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Run("undeclared producer path", func(t *testing.T) {
		o := stageFixture(t, "demo")
		putFile(t, filepath.Join(o.InstallRoot, "demo/1.0/bad.cmake"), "set(BAD \""+filepath.ToSlash(o.InstallRoot)+"/unknown/1/lib.a\")")
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
	if err := writePackageArchive(dir, archive, moduleRef{"demo", "1.0"}); err != nil {
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
		if h.Name != "demo/1.0/" && !strings.HasPrefix(h.Name, "demo/1.0/") {
			t.Fatal(h.Name)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		seen[h.Name] = string(b)
	}
	if seen["demo/1.0/include/data.h"] != data {
		t.Fatal("archive lost bytes")
	}
	if _, ok := seen["demo/1.0/empty"]; !ok {
		t.Fatal("missing empty file")
	}
	t.Run("internal symlink", func(t *testing.T) {
		if err := os.Symlink("include/data.h", filepath.Join(dir, "link")); err != nil {
			t.Skipf("host cannot create symlink: %v", err)
		}
		if err := writePackageArchive(dir, filepath.Join(t.TempDir(), "linked.tar.zst"), moduleRef{"demo", "1.0"}); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../../outside", filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		if err := writePackageArchive(dir, filepath.Join(t.TempDir(), "unsafe.tar.zst"), moduleRef{"demo", "1.0"}); err == nil {
			t.Fatal("accepted external symlink")
		}
	})
}
