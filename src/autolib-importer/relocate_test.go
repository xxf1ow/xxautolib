package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionedRelocation(t *testing.T) {
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "leaf/2", "leaf/10", "other/1", "leaf/1/../../leaf/2"} {
		t.Run(map[bool]string{true: "exact dependency", false: bad}[bad == ""], func(t *testing.T) {
			root := t.TempDir()
			scripts := filepath.Join(root, "script temp")
			if err := os.Mkdir(scripts, 0755); err != nil {
				t.Fatal(err)
			}
			for _, variable := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(variable, scripts)
			}
			producer := filepath.Join(root, "producer with spaces")
			staging := filepath.Join(root, "staging")
			directory := filepath.Join(staging, "demo", "3")
			prefix := filepath.ToSlash(producer)
			metadata := packageMetadata{Identity: packageIdentity{Name: "demo", Version: "3", OS: "any", Arch: "any"}, Dependencies: []string{"leaf@1"}}
			putTestFile(t, filepath.Join(directory, "autolib-import.cmake"), "set(AUTOLIB_PACKAGE_NAME \"demo\")\nset(AUTOLIB_PACKAGE_VERSION \"3\")\nset(AUTOLIB_OS \"any\")\nset(AUTOLIB_ARCH \"any\")\nset(AUTOLIB_DEPENDENCIES \"leaf@1\")\n")
			dependency := "leaf/1"
			if bad != "" {
				dependency = bad
			}
			putTestFile(t, filepath.Join(directory, "include/own.h"), "own")
			putTestFile(t, filepath.Join(directory, "lib/cmake/demo/demo-config.cmake"), "set(DEMO_PREFIX \""+prefix+"/demo/3\")\nset(DEMO_LEAF_PREFIX \""+prefix+"/"+dependency+"\")\n")
			putTestFile(t, filepath.Join(directory, "lib/pkgconfig/demo.pc"), "prefix="+prefix+"/demo/3\nleaf="+prefix+"/"+dependency+"\n")
			putTestFile(t, filepath.Join(staging, "leaf/1/marker"), "exact")
			putTestFile(t, filepath.Join(staging, "leaf/2/marker"), "must not select")
			err := relocatePackage(directory, producer, staging, cmake, metadata)
			temporary, readErr := os.ReadDir(scripts)
			if readErr != nil || len(temporary) != 0 {
				t.Fatalf("temporary script retained: %v %v", temporary, readErr)
			}
			if bad != "" {
				if err == nil {
					t.Fatal("accepted undeclared exact producer reference")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			moved := filepath.Join(root, "moved")
			if err := os.Rename(staging, moved); err != nil {
				t.Fatal(err)
			}
			check := filepath.Join(root, "check.cmake")
			putTestFile(t, check, "include(\""+filepath.ToSlash(filepath.Join(moved, "demo/3/lib/cmake/demo/demo-config.cmake"))+"\")\nget_filename_component(actual \"${DEMO_LEAF_PREFIX}\" REALPATH)\nget_filename_component(expected \""+filepath.ToSlash(filepath.Join(moved, "leaf/1"))+"\" REALPATH)\nif(NOT actual STREQUAL expected OR NOT EXISTS \"${DEMO_PREFIX}/include/own.h\")\nmessage(FATAL_ERROR \"wrong exact dependency prefix: ${actual}; expected ${expected}\")\nendif()\n")
			if output, err := exec.Command(cmake, "-P", check).CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
			pc, err := os.ReadFile(filepath.Join(moved, "demo/3/lib/pkgconfig/demo.pc"))
			if err != nil || strings.Contains(string(pc), prefix) || !strings.Contains(string(pc), "${pcfiledir}/../../../../leaf/1") {
				t.Fatalf("pc relocation: %s, %v", pc, err)
			}
		})
	}
	if runtime.GOOS != "linux" {
		return
	}
	t.Run("metadata link cannot write through", func(t *testing.T) {
		root := t.TempDir()
		producer := filepath.Join(root, "producer")
		directory := filepath.Join(root, "staging/demo/3")
		source := filepath.Join(producer, "demo/3/config.cmake")
		original := "set(PREFIX \"" + filepath.ToSlash(producer) + "/demo/3\")\n"
		putTestFile(t, source, original)
		putTestFile(t, filepath.Join(directory, "sentinel"), "copy")
		if err := os.Symlink(source, filepath.Join(directory, "config.cmake")); err != nil {
			t.Fatal(err)
		}
		err := relocatePackage(directory, producer, filepath.Join(root, "staging"), cmake, packageMetadata{Identity: packageIdentity{Name: "demo", Version: "3", OS: "any", Arch: "any"}})
		if err == nil {
			t.Fatal("accepted metadata symlink")
		}
		data, err := os.ReadFile(source)
		if err != nil || string(data) != original {
			t.Fatal("metadata link modified source", err)
		}
	})
}
