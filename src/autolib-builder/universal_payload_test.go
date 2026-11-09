package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func universalBinaryFixtures() map[string][]byte {
	elf := make([]byte, 64)
	copy(elf, "\x7fELF\x02\x01\x01")
	pe := make([]byte, 152)
	copy(pe, "MZ")
	binary.LittleEndian.PutUint32(pe[60:], 128)
	copy(pe[128:], "PE\x00\x00\x64\x86\x01\x00")
	coff := make([]byte, 60)
	binary.LittleEndian.PutUint16(coff, 0xaa64)
	binary.LittleEndian.PutUint16(coff[2:], 1)
	imp := make([]byte, 32)
	copy(imp, "\x00\x00\xff\xff\x00\x00\x64\x86")
	binary.LittleEndian.PutUint32(imp[12:], 12)
	fixtures := map[string][]byte{"ELF": elf, "PE": pe, "COFF": coff, "COFF-import": imp}
	for _, order := range []struct {
		name  string
		order binary.ByteOrder
	}{{"little", binary.LittleEndian}, {"big", binary.BigEndian}} {
		for _, magic := range []uint32{0xfeedface, 0xfeedfacf, 0xcafebabe, 0xcafebabf} {
			data := make([]byte, 64)
			order.order.PutUint32(data, magic)
			if magic == 0xcafebabe || magic == 0xcafebabf {
				order.order.PutUint32(data[4:], 1)
				order.order.PutUint32(data[8:], 0x0100000c)
			} else {
				order.order.PutUint32(data[4:], 0x0100000c)
			}
			fixtures[fmt.Sprintf("MachO-%s-%x", order.name, magic)] = data
		}
	}
	header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d`\n", "renamed/", 0, 0, 0, 0644, len(coff))
	fixtures["archive"] = append([]byte("!<arch>\n"+header), coff...)
	fixtures["thin-archive"] = []byte("!<thin>\n")
	return fixtures
}

func TestUniversalPayloadFormats(t *testing.T) {
	fixtures := universalBinaryFixtures()
	fixtures["text-a"] = []byte("// plain text with a binary-looking extension\n")
	fixtures["text-dll"] = []byte("#define PORTABLE 1\n")
	for format, payload := range fixtures {
		t.Run(format, func(t *testing.T) {
			f := newFixture(t)
			name := "renamed"
			if format == "text-a" {
				name = "payload.a"
			}
			if format == "text-dll" {
				name = "payload.dll"
			}
			m := f.definition("demo", "copy", "package_platforms=['any-any']\n"+testSource+"[[copy]]\nfiles=['"+name+"']\ndestination='include'\n")
			putFile(t, filepath.Join(f.root, "git/demo/1.0", name), string(payload))
			putFile(t, f.installed("demo", "include/"+name), string(payload))
			err := validateUniversalPackage(m, f.root, filepath.Join(f.root, "3rdparty"), []byte("# adapter"))
			if strings.HasPrefix(format, "text-") {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatalf("accepted %s payload", format)
			}
		})
	}
}

func TestInstalledMetadataDirectories(t *testing.T) {
	for _, kind := range []string{"cmake", "copy"} {
		for _, field := range []string{"config_dir", "pkgconfig_dir"} {
			for _, phase := range []string{"installed", "reuse"} {
				t.Run(kind+"/"+field+"/"+phase, func(t *testing.T) {
					f := newFixture(t)
					body := field + "='share/lookup'\n" + testSource
					if kind == "copy" {
						body += "[[copy]]\nfiles=['input.h']\ndestination='include'\n"
					}
					f.definition("demo", kind, body)
					putFile(t, filepath.Join(f.root, "src/dependencies/demo/1.0/autolib-import.cmake"), "# adapter\n")
					output := f.installed("demo", "autolib-import.cmake")
					original := "set(AUTOLIB_PACKAGE_NAME \"demo\")\nset(AUTOLIB_PACKAGE_VERSION \"1.0\")\nset(AUTOLIB_OS \"windows\")\nset(AUTOLIB_ARCH \"x86_64\")\nset(AUTOLIB_DEPENDENCIES \"\")\n# old adapter\n"
					putFile(t, output, original)
					err := consumptionCommand(f, "demo", phase)
					if err == nil || !strings.Contains(err.Error(), field) {
						t.Fatalf("accepted missing %s: %v", field, err)
					}
					requireFile(t, output, original)
					if err := os.MkdirAll(f.installed("demo", "share/lookup"), 0755); err != nil {
						t.Fatal(err)
					}
					if err := consumptionCommand(f, "demo", phase); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestUniversalCopyMetadata(t *testing.T) {
	f := newFixture(t)
	m := f.definition("demo", "copy", "package_platforms=['any-any']\n"+testSource+"[[copy]]\nfiles=['config.cmake']\ndestination='share/demo'\n")
	putFile(t, filepath.Join(f.root, "git/demo/1.0/config.cmake"), "# portable config\n")
	installed := f.installed("demo", "share/demo/config.cmake")
	putFile(t, installed, "# portable config\n")
	check := func() error {
		return validateUniversalPackage(m, f.root, filepath.Join(f.root, "3rdparty"), []byte("# adapter"))
	}
	if err := check(); err != nil {
		t.Fatal(err)
	}
	putFile(t, installed, "# altered\n")
	if err := check(); err == nil {
		t.Fatal("accepted changed source bytes")
	}
	putFile(t, installed, "# portable config\n")
	putFile(t, f.installed("demo", "extra"), "extra")
	if err := check(); err == nil {
		t.Fatal("accepted unmapped metadata")
	}
}

func TestUniversalPayloadReuseAndStage(t *testing.T) {
	for _, kind := range []string{"cmake", "copy"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			body := "package_platforms=['any-any']\n" + testSource
			if kind == "copy" {
				body += "[[copy]]\nfiles=['input.h']\ndestination='include'\n"
			}
			f.definition("demo", kind, body)
			putFile(t, filepath.Join(f.root, "src/dependencies/demo/1.0/autolib-import.cmake"), "# adapter\n")
			putFile(t, filepath.Join(f.root, "git/demo/1.0/input.h"), "// portable\n")
			payload := f.installed("demo", "include/input.h")
			putFile(t, payload, "// portable\n")
			if err := consumptionCommand(f, "demo", "installed"); err != nil {
				t.Fatal(err)
			}
			putFile(t, payload, string(universalBinaryFixtures()["ELF"]))
			putFile(t, filepath.Join(f.root, "git/demo/1.0/input.h"), string(universalBinaryFixtures()["ELF"]))
			original := readFile(t, f.installed("demo", "autolib-import.cmake"))
			if err := consumptionCommand(f, "demo", "reuse"); err == nil {
				t.Fatal("reused binary universal payload")
			}
			script := readFile(t, "../autolib-importer/fixup-metadata.cmake")
			putFile(t, filepath.Join(f.root, "src/autolib-importer/fixup-metadata.cmake"), script)
			_, err := stagePackage(stageOptions{SourceRoot: f.root, InstallRoot: filepath.Join(f.root, "3rdparty"), StageRoot: filepath.Join(f.root, "build/stage"), Name: "demo", Version: "1.0", OS: "windows", Arch: "x86_64", CMakeCommand: f.context.CMakeCommand})
			if err == nil {
				t.Fatal("staged binary universal payload")
			}
			requireFile(t, f.installed("demo", "autolib-import.cmake"), original)
			requireFile(t, payload, string(universalBinaryFixtures()["ELF"]))
		})
	}
}

func TestInstalledMetadataWithoutAdapter(t *testing.T) {
	for _, kind := range []string{"cmake", "copy"} {
		t.Run(kind, func(t *testing.T) {
			f := newEntryFixture(t)
			repository := f.repository("demo", map[string]string{
				"demo.h":         "// header\n",
				"CMakeLists.txt": "cmake_minimum_required(VERSION 3.20)\nproject(demo NONE)\ninstall(FILES demo.h DESTINATION include)\n",
			})
			body := "sentinel='include/demo.h'\nconfig_dir='share/config'\npkgconfig_dir='share/pc'\n[source]\n" + repository
			if kind == "copy" {
				body += "[[copy]]\nfiles=['demo.h']\ndestination='include'\n"
			}
			f.definition("demo", kind, body)
			f.mustEntry("-DBUILD_LIST=demo")
			requireFailure := func(field string) {
				t.Helper()
				out, stderr, err := f.command(f.context.CMakeCommand, "--build", f.context.BuildRoot, "--config", "Release")
				if err == nil || !strings.Contains(out+stderr, field+" installation directory") {
					t.Fatalf("missing %s was not rejected: %v\n%s%s", field, err, out, stderr)
				}
			}
			requireFailure("config_dir")
			putFile(t, f.installed("demo", "share/config/DemoConfig.cmake"), "# config\n")
			requireFailure("pkgconfig_dir")
			putFile(t, f.installed("demo", "share/pc/demo.pc"), "# pkgconfig\n")
			f.build()
			requireFile(t, filepath.Join(f.root, "3rdparty/demo/package.toml"), "latest='1.0'\n")
			if _, err := os.Stat(f.installed("demo", "autolib-import.cmake")); !os.IsNotExist(err) {
				t.Fatalf("local-only package got an adapter: %v", err)
			}
			if err := os.RemoveAll(f.installed("demo", "share/config")); err != nil {
				t.Fatal(err)
			}
			// ExternalProject stamp 与 sentinel 是两条独立的复用入口。
			requireFailure("config_dir")
			f.mustEntry("-DBUILD_LIST=demo")
			requireFailure("config_dir")
			putFile(t, f.installed("demo", "share/config/DemoConfig.cmake"), "# config\n")
			f.build()
			requireFile(t, f.installed("demo", "include/demo.h"), "// header\n")
		})
	}
}
