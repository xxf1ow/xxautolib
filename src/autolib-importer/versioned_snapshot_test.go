package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type metadataRepositoryCase struct {
	Name     string
	Valid    bool
	Files    map[string]string
	Symlinks map[string]string
	Gitlink  string
}

func snapshotCMake(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("AUTOLIB_TEST_CMAKE"); path != "" {
		return path
	}
	path, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func snapshotGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=AutoLib fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=AutoLib fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func snapshotRepository(t *testing.T, tc metadataRepositoryCase) string {
	t.Helper()
	root := t.TempDir()
	snapshotGit(t, root, "init")
	snapshotGit(t, root, "symbolic-ref", "HEAD", "refs/heads/metadata-default")
	putTestFile(t, filepath.Join(root, "README.md"), "AutoLib binary artifacts\n")
	snapshotGit(t, root, "add", ".")
	snapshotGit(t, root, "commit", "-m", "initial metadata")
	for path, content := range tc.Files {
		putTestFile(t, filepath.Join(root, filepath.FromSlash(path)), content)
	}
	snapshotGit(t, root, "add", ".")
	if tc.Name == "case-collision" {
		for path, content := range tc.Files {
			if path == ".autolib-repository.json" {
				continue
			}
			blob := putTestFile(t, filepath.Join(t.TempDir(), "blob"), content)
			hash := snapshotGit(t, root, "hash-object", "-w", blob)
			snapshotGit(t, root, "update-index", "--add", "--cacheinfo", "100644,"+hash+","+path)
		}
	}
	for path, target := range tc.Symlinks {
		blob := putTestFile(t, filepath.Join(t.TempDir(), "link-target"), target)
		hash := snapshotGit(t, root, "hash-object", "-w", blob)
		snapshotGit(t, root, "update-index", "--add", "--cacheinfo", "120000,"+hash+","+path)
	}
	if tc.Gitlink != "" {
		snapshotGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+snapshotGit(t, root, "rev-parse", "HEAD")+","+tc.Gitlink)
	}
	snapshotGit(t, root, "commit", "--allow-empty", "-m", "current metadata")
	return root
}

func snapshotConfigure(t *testing.T, source, build, repo, trace string) (string, error) {
	t.Helper()
	cmd := exec.Command(snapshotCMake(t), "-S", source, "-B", build)
	redirect := "url.file://" + filepath.ToSlash(repo) + ".insteadOf=https://gitee.com/owner/repo.git"
	cmd.Env = append(os.Environ(), "GIT_CONFIG_PARAMETERS='"+strings.ReplaceAll(redirect, "'", "'\\''")+"'", "GIT_TRACE="+trace)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func snapshotDriver(t *testing.T, root, body string) string {
	t.Helper()
	entry, err := filepath.Abs("../autolib.cmake")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "consumer")
	putTestFile(t, filepath.Join(source, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(snapshot NONE)\ninclude([==["+filepath.ToSlash(entry)+"]==])\n"+body)
	return source
}

const snapshotRequest = `_autolib_metadata_snapshot(first "https://gitee.com/owner/repo/releases/download/v1" "${CMAKE_BINARY_DIR}/work")
`

const snapshotReuse = `_autolib_metadata_snapshot(second "https://gitee.com/owner/repo/releases/download/v1" "${CMAKE_BINARY_DIR}/work")
execute_process(COMMAND "${GIT_EXECUTABLE}" -C "${first}" rev-list --count HEAD OUTPUT_VARIABLE depth OUTPUT_STRIP_TRAILING_WHITESPACE RESULT_VARIABLE status)
if(NOT first STREQUAL second OR NOT depth STREQUAL "1" OR NOT status STREQUAL "0")
  message(FATAL_ERROR "snapshot was not shallow and reused")
endif()
execute_process(COMMAND "${GIT_EXECUTABLE}" -C "${first}" rev-parse HEAD OUTPUT_VARIABLE head OUTPUT_STRIP_TRAILING_WHITESPACE)
file(WRITE "${CMAKE_BINARY_DIR}/snapshot.txt" "${first}\n${head}\n")
`

const snapshotCallerConfiguration = `set(FETCHCONTENT_FULLY_DISCONNECTED TRUE CACHE BOOL "" FORCE)
set(FETCHCONTENT_UPDATES_DISCONNECTED TRUE CACHE BOOL "" FORCE)
set(FETCHCONTENT_BASE_DIR "${CMAKE_BINARY_DIR}/caller-fetch" CACHE PATH "" FORCE)
`

const snapshotCheckCallerConfiguration = `if(NOT FETCHCONTENT_FULLY_DISCONNECTED OR NOT FETCHCONTENT_UPDATES_DISCONNECTED OR NOT FETCHCONTENT_BASE_DIR STREQUAL "${CMAKE_BINARY_DIR}/caller-fetch")
  message(FATAL_ERROR "caller FetchContent configuration changed")
endif()
get_property(disconnected CACHE FETCHCONTENT_FULLY_DISCONNECTED PROPERTY VALUE)
if(NOT disconnected)
  message(FATAL_ERROR "caller FetchContent cache configuration changed")
endif()
`

func TestVersionedMetadataRepository(t *testing.T) {
	var repositories struct{ Repositories []metadataRepositoryCase }
	if err := json.Unmarshal([]byte(readTestFile(t, "../../tests/autolib/metadata-repository-cases.json")), &repositories); err != nil {
		t.Fatal(err)
	}
	if len(repositories.Repositories) == 0 {
		t.Fatal("missing metadata repository cases")
	}
	for _, tc := range repositories.Repositories {
		t.Run(tc.Name, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, "repository-code-ran")
			for path, body := range tc.Files {
				tc.Files[path] = strings.ReplaceAll(body, "@MARKER@", filepath.ToSlash(marker))
			}
			repo := snapshotRepository(t, tc)
			source := snapshotDriver(t, root, snapshotRequest+snapshotReuse)
			out, err := snapshotConfigure(t, source, filepath.Join(root, "build"), repo, filepath.Join(root, "git.trace"))
			if (err == nil) != tc.Valid || (!tc.Valid && (!strings.Contains(out, "AutoLib") || strings.Contains(out, "Unknown CMake command"))) {
				t.Fatalf("valid=%v: %v\n%s", tc.Valid, err, out)
			}
			if tc.Gitlink != "" {
				trace := readTestFile(t, filepath.Join(root, "git.trace"))
				if !strings.Contains(out, "requires regular blobs") || snapshotTraceCount(t, filepath.Join(root, "git.trace"), " ls-tree ") != 1 || strings.Contains(trace, "submodule") {
					t.Fatalf("gitlink must reach tree validation without submodule operations:\n%s\n%s", out, trace)
				}
			}
			if _, err := os.Lstat(marker); !os.IsNotExist(err) {
				t.Fatalf("repository CMake ran: %v", err)
			}
		})
	}
}

func TestVersionedMetadataDeliveryRestrictions(t *testing.T) {
	identity := `{"type":"xxautolib-artifacts","schema":2,"upstream":"https://github.com/xxf1ow/xxautolib"}`
	for _, path := range []string{"tools/autolib-importer-linux-x86_64.tar.xz.sha256", "autolib-importer/package.toml", "autolib-importer/1/autolib-importer-1-linux-x86_64.tar.zst.sha256"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			repo := snapshotRepository(t, metadataRepositoryCase{Files: map[string]string{".autolib-repository.json": identity, path: "latest = \"1\"\n"}})
			source := snapshotDriver(t, root, snapshotRequest)
			out, err := snapshotConfigure(t, source, filepath.Join(root, "build"), repo, filepath.Join(root, "git.trace"))
			if err == nil || !strings.Contains(out, "AutoLib") {
				t.Fatalf("non-library importer metadata accepted: %v\n%s", err, out)
			}
		})
	}
}

func snapshotTraceCount(t *testing.T, trace, command string) int {
	t.Helper()
	count := 0
	for _, line := range strings.Split(readTestFile(t, trace), "\n") {
		if strings.Contains(line, "built-in: git ") && strings.Contains(line, command) {
			count++
		}
	}
	return count
}

func TestVersionedSnapshotLifecycle(t *testing.T) {
	const identity = `{"type":"xxautolib-artifacts","schema":2,"upstream":"https://github.com/xxf1ow/xxautolib"}`
	root := t.TempDir()
	repo := snapshotRepository(t, metadataRepositoryCase{Files: map[string]string{".autolib-repository.json": identity}})
	trace := filepath.Join(root, "git.trace")
	build := filepath.Join(root, "build")
	source := snapshotDriver(t, root, snapshotCallerConfiguration+snapshotRequest+snapshotReuse+snapshotCheckCallerConfiguration)
	var previous string
	for configure := 1; configure <= 2; configure++ {
		if configure == 2 {
			putTestFile(t, filepath.Join(repo, "README.md"), "New metadata HEAD\n")
			snapshotGit(t, repo, "commit", "-am", "next configure metadata")
		}
		out, err := snapshotConfigure(t, source, build, repo, trace)
		if err != nil {
			t.Fatalf("configure %d: %v\n%s", configure, err, out)
		}
		fields := strings.Split(strings.TrimSpace(readTestFile(t, filepath.Join(build, "snapshot.txt"))), "\n")
		if len(fields) != 2 || fields[0] == previous || fields[1] != snapshotGit(t, repo, "rev-parse", "HEAD") {
			t.Fatalf("configure %d snapshot: %q previous %q", configure, fields, previous)
		}
		previous = fields[0]
		if clones, trees := snapshotTraceCount(t, trace, " clone "), snapshotTraceCount(t, trace, " ls-tree "); clones != configure || trees != configure {
			t.Fatalf("configure %d: clones=%d ls-tree=%d", configure, clones, trees)
		}
	}
	t.Run("deleted snapshot fails without clone", func(t *testing.T) {
		root := t.TempDir()
		trace := filepath.Join(root, "git.trace")
		source := snapshotDriver(t, root, snapshotRequest+`file(REAL_PATH "${first}" snapshot_real)
file(REAL_PATH "${CMAKE_BINARY_DIR}/work" work_real)
string(FIND "${snapshot_real}" "${work_real}/.metadata-" prefix)
if(NOT prefix EQUAL 0)
  message(FATAL_ERROR "refusing to remove a snapshot outside the test work directory")
endif()
file(REMOVE_RECURSE "${snapshot_real}")
`+snapshotReuse)
		out, err := snapshotConfigure(t, source, filepath.Join(root, "build"), repo, trace)
		if err == nil || !strings.Contains(out, "AutoLib") {
			t.Fatalf("deleted snapshot: %v\n%s", err, out)
		}
		if clones, trees := snapshotTraceCount(t, trace, " clone "), snapshotTraceCount(t, trace, " ls-tree "); clones != 1 || trees != 1 {
			t.Fatalf("deleted snapshot: clones=%d ls-tree=%d", clones, trees)
		}
	})
}
