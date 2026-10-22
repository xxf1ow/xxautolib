package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testPatch = `diff --git a/value.c b/value.c
--- a/value.c
+++ b/value.c
@@ -1 +1 @@
-int value(void) { return 1; }
+int value(void) { return 2; }
`

func TestPatchNativeStepInputs(t *testing.T) {
	f := newFixture(t)
	log := filepath.ToSlash(filepath.Join(f.root, "phases.txt"))
	repo := f.repository("library", map[string]string{
		"value.c": "int value(void) { return 1; }\n",
		"main.c":  "#include <stdio.h>\nint value(void);\nint main(void) { printf(\"%d\\n\", value()); return 0; }\n",
		"CMakeLists.txt": `cmake_minimum_required(VERSION 3.20)
project(child C)
file(APPEND "` + log + `" "configure\n")
add_library(value STATIC value.c)
add_executable(probe main.c)
target_link_libraries(probe PRIVATE value)
install(TARGETS value probe ARCHIVE DESTINATION lib RUNTIME DESTINATION bin)
install(CODE [[file(APPEND "` + log + `" "install\n")]])
`,
	})
	m := f.definition("demo", "cmake", "[source]\n"+repo)
	patch := putFile(t, filepath.Join(f.root, "change.patch"), testPatch)
	m.Patches = append(m.Patches, patchDefinition{File: patch})
	f.project([]*moduleDefinition{m}, "")
	f.build()
	probe := f.installed("demo", "bin/probe")
	if f.context.SystemName == "Windows" {
		probe += ".exe"
	}
	if got := strings.TrimSpace(f.must(probe)); got != "2" {
		t.Fatal(got)
	}
	before := readFile(t, log)
	time.Sleep(1100 * time.Millisecond)
	if err := os.Chtimes(patch, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	f.build()
	after := readFile(t, log)
	for _, phase := range []string{"configure", "install"} {
		if strings.Count(after, phase) <= strings.Count(before, phase) {
			t.Fatalf("%s not invalidated: %s", phase, after)
		}
	}
	rewrite := putFile(t, filepath.Join(f.root, "rewrite.cmake"), "file(WRITE \"${FILE}\" \"int value(void) { return 3; }\\n\")\n")
	m.Steps = append(m.Steps, stepDefinition{Name: "rewrite", Dependees: []string{"patch"}, Dependers: []string{"configure"}, Depends: []string{filepath.ToSlash(rewrite)}, Commands: [][]string{{f.context.CMakeCommand, "-DFILE=<SOURCE_DIR>/value.c", "-P", filepath.ToSlash(rewrite)}}})
	f.project([]*moduleDefinition{m}, "")
	f.build()
	if got := strings.TrimSpace(f.must(probe)); got != "3" {
		t.Fatal(got)
	}
	time.Sleep(1100 * time.Millisecond)
	putFile(t, rewrite, strings.ReplaceAll(readFile(t, rewrite), "return 3", "return 4"))
	f.build()
	if got := strings.TrimSpace(f.must(probe)); got != "4" {
		t.Fatal(got)
	}
}

func TestPatchPipeline(t *testing.T) {
	f := newFixture(t)
	patch := putFile(t, filepath.Join(f.root, "change.patch"), testPatch)
	extra := f.repository("extra", map[string]string{"value.c": "int value(void) { return 1; }\n"})
	main := f.repository("main", map[string]string{"value.c": "int value(void) { return 1; }\n", "CMakeLists.txt": `cmake_minimum_required(VERSION 3.20)
project(child NONE)
file(READ "${EXTRA}/value.c" extra)
if(NOT extra MATCHES "return 2")
message(FATAL_ERROR "resource not patched")
endif()
file(APPEND "${CMAKE_INSTALL_PREFIX}/configures" "configured\n")
install(FILES value.c DESTINATION .)
`})
	m := f.definition("demo", "cmake", "[source]\n"+main+"[resources.extra]\n"+extra+"[cmake.cache]\nEXTRA='<RESOURCE_SOURCE_DIR:extra>'\n[[patches]]\nfile='"+filepath.ToSlash(patch)+"'\nresource='extra'\n[[patches]]\nfile='"+filepath.ToSlash(patch)+"'\n")
	f.project([]*moduleDefinition{m}, "")
	f.build()
	requireTextFile(t, f.installed("demo", "value.c"), "int value(void) { return 2; }\n")
	before := readFile(t, f.installed("demo", "configures"))
	time.Sleep(1100 * time.Millisecond)
	if err := os.Chtimes(patch, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if output := f.build(); !strings.Contains(output, "already applied") {
		t.Fatal(output)
	}
	after := readFile(t, f.installed("demo", "configures"))
	if len(after) <= len(before) {
		t.Fatal("patch did not invalidate configure")
	}
	resource := putFile(t, filepath.Join(f.root, "git/_resources/demo/extra/value.c"), "manual content\n")
	time.Sleep(1100 * time.Millisecond)
	if err := os.Chtimes(patch, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := f.command(f.context.CMakeCommand, "--build", f.context.BuildRoot, "--config", "Release")
	if err == nil || !strings.Contains(out+stderr, "source mismatch") {
		t.Fatalf("%v %s%s", err, out, stderr)
	}
	requireFile(t, resource, "manual content\n")
	t.Run("archive in outer Git", func(t *testing.T) {
		f := newFixture(t)
		f.repository("outer", map[string]string{"archive/value.c": "int value(void) { return 1; }\n", "value.c": "outer content\n"})
		patch := putFile(t, filepath.Join(f.root, "change.patch"), testPatch)
		args := []string{f.context.CMakeCommand, "-DGIT_EXECUTABLE=" + filepath.ToSlash(f.context.GitExecutable), "-DSOURCE_DIR=" + filepath.ToSlash(filepath.Join(f.root, "upstream/outer/archive")), "-DPATCH_FILE=" + filepath.ToSlash(patch), "-P", filepath.Join(f.root, "src/autolib-builder/apply_patch.cmake")}
		f.must(args...)
		if output := f.must(args...); !strings.Contains(output, "already applied") {
			t.Fatal(output)
		}
		requireTextFile(t, filepath.Join(f.root, "upstream/outer/archive/value.c"), "int value(void) { return 2; }\n")
		requireTextFile(t, filepath.Join(f.root, "upstream/outer/value.c"), "outer content\n")
	})
}
