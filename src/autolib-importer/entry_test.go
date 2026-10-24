package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

type entryFixture struct {
	t                                                              *testing.T
	root, source, release, work, cmake, generator, buildTool, tool string
	options                                                        prepareOptions
	toolAsset                                                      string
}

func buildEntryTool(t *testing.T, fake bool) string {
	t.Helper()
	name := "autolib-importer"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output := filepath.Join(t.TempDir(), name)
	goCommand := filepath.Join(os.Getenv("GOROOT"), "bin", "go")
	if runtime.GOOS == "windows" {
		goCommand += ".exe"
	}
	input := "."
	if fake {
		input = putTestFile(t, filepath.Join(t.TempDir(), "main.go"), fakeImporterSource)
	}
	if out, err := exec.Command(goCommand, "build", "-mod=readonly", "-buildvcs=false", "-trimpath", "-o", output, input).CombinedOutput(); err != nil {
		t.Fatalf("build fixture tool: %v\n%s", err, out)
	}
	return output
}

func newEntryFixture(t *testing.T, fake bool, suffix string) *entryFixture {
	t.Helper()
	o := localReleaseFixture(t, "demo")
	root := filepath.Dir(o.LocalRoot)
	if suffix != "" {
		destination := filepath.Join(root, suffix)
		if err := os.Rename(o.LocalRoot, destination); err != nil {
			t.Fatal(err)
		}
		o.LocalRoot = destination
		o.WorkRoot = filepath.Join(root, "work "+strings.ReplaceAll(suffix, ";", ""))
	}
	tool := buildEntryTool(t, fake)
	data, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	toolAsset := "autolib-importer-" + runtime.GOOS + "-" + map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if runtime.GOOS == "windows" {
		toolAsset += ".exe"
	}
	putTestFile(t, filepath.Join(o.LocalRoot, toolAsset), string(data))
	if err := os.Chmod(filepath.Join(o.LocalRoot, toolAsset), 0755); err != nil {
		t.Fatal(err)
	}
	// 目标刻意不同于常用 Windows 宿主，验证工具与库槽独立。
	id := packageIdentity{Name: "demo", OS: o.OS, Arch: o.Arch}
	adapter := identityHeader(id) + "if(TARGET AutoLib::demo)\nreturn()\nendif()\nadd_library(AutoLib::demo INTERFACE IMPORTED GLOBAL)\n"
	a := testArchive(t, []archiveMember{{Name: "demo/autolib-import.cmake", Body: adapter}})
	archive, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(o.LocalRoot, "demo-"+o.OS+"-"+o.Arch+".tar.zst"), string(archive))
	source := filepath.Join(root, "consumer")
	entry, err := os.ReadFile("../autolib.cmake")
	if err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(source, "autolib.cmake"), string(entry))
	consumer, err := os.ReadFile("testdata/consumer/CMakeLists.txt")
	if err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(source, "CMakeLists.txt"), string(consumer))
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	generator, toolName := "Ninja", "ninja"
	if runtime.GOOS == "linux" {
		generator, toolName = "Unix Makefiles", "make"
	}
	buildTool, err := exec.LookPath(toolName)
	if err != nil {
		t.Fatal(err)
	}
	f := &entryFixture{t: t, root: root, source: source, release: o.LocalRoot, work: o.WorkRoot, cmake: cmake, generator: generator, buildTool: buildTool, tool: tool, options: o, toolAsset: toolAsset}
	if fake {
		dir := filepath.Join(root, "fake prepared")
		putTestFile(t, filepath.Join(dir, "autolib-import.cmake"), adapter)
		t.Setenv("AUTOLIB_TEST_PACKAGE_DIR", filepath.ToSlash(dir))
		t.Setenv("AUTOLIB_TEST_TOOL_LOG", filepath.Join(root, "calls.jsonl"))
	}
	return f
}

func (f *entryFixture) configure(extra ...string) (string, error) {
	f.t.Helper()
	args := []string{"-S", f.source, "-B", filepath.Join(f.root, "consumer-build"), "-G", f.generator, "-DCMAKE_MAKE_PROGRAM=" + f.buildTool, "-DFIXTURE_RELEASE=" + f.release, "-DFIXTURE_WORK=" + f.work, "-DFIXTURE_OS=" + f.options.OS, "-DFIXTURE_ARCH=" + f.options.Arch}
	args = append(args, extra...)
	out, err := exec.Command(f.cmake, args...).CombinedOutput()
	return string(out), err
}

func (f *entryFixture) mustConfigure() {
	f.t.Helper()
	if out, err := f.configure(); err != nil {
		f.t.Fatalf("configure: %v\n%s", err, out)
	}
}

func TestCMakeBootstrap(t *testing.T) {
	f := newEntryFixture(t, false, "")
	f.mustConfigure()
	want := filepath.ToSlash(packageDirectory(f.release, packageIdentity{"demo", f.options.OS, f.options.Arch}))
	if got := readTestFile(t, filepath.Join(f.root, "consumer-build/observed.txt")); got != want {
		t.Fatalf("%q != %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(f.release, "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("unexpected manifest", err)
	}
	if err := os.Rename(filepath.Join(f.release, "demo-"+f.options.OS+"-"+f.options.Arch+".tar.zst"), filepath.Join(f.release, "archive-away")); err != nil {
		t.Fatal(err)
	}
	f.mustConfigure()
}

func TestUniversalPackageThroughCMakeEntry(t *testing.T) {
	f := newEntryFixture(t, false, "")
	id := packageIdentity{"demo", "any", "any"}
	archive := testArchive(t, []archiveMember{{Name: "demo/autolib-import.cmake", Body: identityHeader(id) + "if(NOT TARGET AutoLib::demo)\nadd_library(AutoLib::demo INTERFACE IMPORTED GLOBAL)\nendif()\n"}})
	if err := os.Rename(filepath.Join(f.release, "demo-"+f.options.OS+"-"+f.options.Arch+".tar.zst"), filepath.Join(f.release, "old-away")); err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(f.release, "demo-any-any.tar.zst"), readTestFile(t, archive))
	f.mustConfigure()
	m, err := readPackageMetadata(packageDirectory(f.release, packageIdentity{"demo", f.options.OS, f.options.Arch}))
	if err != nil || m.Identity != id {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestCMakeBootstrapFailure(t *testing.T) {
	for _, kind := range []string{"missing tool", "cannot start", "prepare fails", "no result", "incomplete result"} {
		t.Run(kind, func(t *testing.T) {
			f := newEntryFixture(t, true, "")
			putTestFile(t, filepath.Join(f.work, "previous-result.cmake"), "message(FATAL_ERROR STALE_RESULT_LOADED)\n")
			marker := filepath.Join(f.root, "adapter-loaded")
			putTestFile(t, filepath.Join(os.Getenv("AUTOLIB_TEST_PACKAGE_DIR"), "autolib-import.cmake"), "file(WRITE [==["+filepath.ToSlash(marker)+"]==] loaded)\n")
			switch kind {
			case "missing tool":
				if err := os.Rename(filepath.Join(f.release, f.toolAsset), filepath.Join(f.release, "tool-away")); err != nil {
					t.Fatal(err)
				}
			case "cannot start":
				putTestFile(t, filepath.Join(f.release, f.toolAsset), "not executable")
			case "prepare fails":
				t.Setenv("AUTOLIB_TEST_MODE", "fail")
			case "no result":
				t.Setenv("AUTOLIB_TEST_MODE", "no-result")
			case "incomplete result":
				t.Setenv("AUTOLIB_TEST_MODE", "incomplete")
			}
			out, err := f.configure()
			if err == nil || strings.Contains(out, "STALE_RESULT_LOADED") {
				t.Fatalf("%v\n%s", err, out)
			}
			if kind == "incomplete result" && !strings.Contains(out, "invalid package directory") {
				t.Fatalf("incomplete result did not fail directory validation:\n%s", out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("adapter loaded on failure", err)
			}
		})
	}
}

func TestCMakeEntryPaths(t *testing.T) {
	t.Run("real importer special paths", func(t *testing.T) {
		f := newEntryFixture(t, false, "中文 空格${literal}")
		f.mustConfigure()
		want := filepath.ToSlash(packageDirectory(f.release, packageIdentity{Name: "demo", OS: f.options.OS, Arch: f.options.Arch}))
		if got := readTestFile(t, filepath.Join(f.root, "consumer-build/observed.txt")); got != want {
			t.Fatalf("%q != %q", got, want)
		}
	})
	for _, suffix := range []string{"", "中文 空格${literal}"} {
		t.Run(suffix, func(t *testing.T) {
			f := newEntryFixture(t, true, suffix)
			absolute := f.release
			rel, err := filepath.Rel(f.source, absolute)
			if err != nil {
				t.Fatal(err)
			}
			f.release = rel
			f.mustConfigure()
			data := readTestFile(t, filepath.Join(f.root, "calls.jsonl"))
			lines := strings.Split(strings.TrimSpace(data), "\n")
			if len(lines) != 2 {
				t.Fatal(data)
			}
			previousResult := ""
			for _, line := range lines {
				var args []string
				if err := json.Unmarshal([]byte(line), &args); err != nil {
					t.Fatal(err)
				}
				if args[0] != "prepare" {
					t.Fatal(args)
				}
				wantSource, err := normalizeLocalSource(absolute)
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{"--local-root=" + wantSource, "--work-root=" + filepath.ToSlash(f.work), "--os=" + f.options.OS, "--arch=" + f.options.Arch, "--package=demo"} {
					found := false
					for _, a := range args {
						if a == want {
							found = true
						}
					}
					if !found {
						t.Fatalf("missing %q: %#v", want, args)
					}
				}
				resultArg := ""
				for _, arg := range args {

					if strings.HasPrefix(arg, "--result=") {
						resultArg = arg
					}
				}
				if resultArg == "" || resultArg == previousResult {
					t.Fatalf("missing current or reused result: %#v", args)
				}
				previousResult = resultArg
			}
		})
	}
}

func TestCMakeWorkRootSemicolon(t *testing.T) {
	for _, kind := range []string{"explicit", "default", "local"} {
		t.Run(kind, func(t *testing.T) {
			f := newEntryFixture(t, true, "")
			work := filepath.Join(f.root, "work;literal")
			var extra []string
			if kind == "local" {
				f.release = filepath.Join(f.root, "local;literal")
			} else if kind == "explicit" {
				f.work = work
			} else {
				consumer := filepath.Join(f.source, "CMakeLists.txt")
				putTestFile(t, consumer, strings.ReplaceAll(readTestFile(t, consumer), ` WORK_ROOT "${FIXTURE_WORK}"`, ""))
				build := filepath.Join(f.root, "build;literal")
				work = filepath.Join(build, "autolib")
				extra = []string{"-B", build}
			}
			putTestFile(t, filepath.Join(work, "sentinel"), "preserve")
			out, err := f.configure(extra...)
			field := "WORK_ROOT"
			if kind == "local" {
				field = "LOCAL_ROOT"
			}
			if err == nil || !strings.Contains(out, field+" must not contain semicolons") {
				t.Fatalf("unsafe work root was not rejected clearly: %v\n%s", err, out)
			}
			entries, err := os.ReadDir(work)
			if err != nil || len(entries) != 1 || readTestFile(t, filepath.Join(work, "sentinel")) != "preserve" {
				t.Fatalf("rejected work root changed: %v, %v", entries, err)
			}
			if _, err := os.Stat(filepath.Join(f.root, "calls.jsonl")); !os.IsNotExist(err) {
				t.Fatal("importer executed for rejected work root")
			}
		})
	}
}

func TestCMakeConfigureIdentity(t *testing.T) {
	for _, kind := range []string{"same", "local", "work", "target", "old URI", "remote", "stale directory"} {
		t.Run(kind, func(t *testing.T) {
			f := newEntryFixture(t, true, "")
			consumer := filepath.Join(f.source, "CMakeLists.txt")
			body := readTestFile(t, consumer)
			body = strings.Replace(body, "include(\"${CMAKE_CURRENT_LIST_DIR}/autolib.cmake\")", "include(\"${CMAKE_CURRENT_LIST_DIR}/autolib.cmake\")\ninclude(\"${CMAKE_CURRENT_LIST_DIR}/autolib.cmake\")", 1)
			index := strings.LastIndex(body, "autolib_import(")
			second := body[index:]
			switch kind {
			case "local":
				second = strings.Replace(second, "${FIXTURE_RELEASE}", "${FIXTURE_RELEASE}/other", 1)
			case "work":
				second = strings.Replace(second, "${FIXTURE_WORK}", "${FIXTURE_WORK}/other", 1)
			case "target":
				second = strings.Replace(second, "${FIXTURE_OS}", "windows", 1)
			case "old URI":
				second = strings.Replace(second, "LOCAL_ROOT", "URI", 1)
			case "remote":
				second = strings.Replace(second, "LOCAL_ROOT", `REMOTE_ROOT "https://example.invalid/release" LOCAL_ROOT`, 1)
			case "stale directory":
				second = "set(ENV{AUTOLIB_TEST_MODE} incomplete)\n" + second
			}
			putTestFile(t, consumer, body[:index]+second)
			out, err := f.configure()
			if (err == nil) != (kind == "same") {
				t.Fatalf("%v\n%s", err, out)
			}
			if kind == "stale directory" && !strings.Contains(out, "invalid package directory") {
				t.Fatalf("incomplete result inherited an earlier directory:\n%s", out)
			}
			lines := strings.Split(strings.TrimSpace(readTestFile(t, filepath.Join(f.root, "calls.jsonl"))), "\n")
			want := 1
			if kind == "same" || kind == "stale directory" {
				want = 2
			}
			if len(lines) != want {
				t.Fatalf("expected %d prepares, got %d: %s", want, len(lines), out)
			}
		})
	}
}

func TestLocalConsumerNoToolchainRuntime(t *testing.T) {
	f := newEntryFixture(t, false, "offline 中文")
	trap := buildEntryTool(t, true)
	traps := filepath.Join(f.root, "controlled-path")
	for _, name := range []string{"go", "python", "python3", "uv", "curl", "wget"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		putTestFile(t, filepath.Join(traps, name), readTestFile(t, trap))
		if err := os.Chmod(filepath.Join(traps, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(f.root, "forbidden-calls")
	t.Setenv("AUTOLIB_TEST_TOOL_LOG", log)
	t.Setenv("PATH", traps)
	t.Setenv("GOROOT", "")
	t.Setenv("GOPATH", "")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(404) }))
	defer server.Close()
	t.Setenv("HTTP_PROXY", server.URL)
	t.Setenv("HTTPS_PROXY", server.URL)
	f.mustConfigure()
	newEntryMissingPackage(t, f)
	if out, err := f.configure(); err == nil {
		t.Fatalf("missing package accepted: %s", out)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("consumer invoked forbidden runtime")
	}
	if requests.Load() != 0 {
		t.Fatal("consumer issued network request")
	}
}

func newEntryMissingPackage(t *testing.T, f *entryFixture) {
	t.Helper()
	dir := packageDirectory(f.release, packageIdentity{Name: "demo", OS: f.options.OS, Arch: f.options.Arch})
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.release, "demo-"+f.options.OS+"-"+f.options.Arch+".tar.zst")); err != nil {
		t.Fatal(err)
	}
}

func representativeFixture(t *testing.T, packages string) *entryFixture {
	t.Helper()
	f := newEntryFixture(t, false, "")
	for _, name := range []string{"queue", "lz4", "flann"} {
		adapter, err := os.ReadFile(filepath.Join("../dependencies", name, "autolib-import.cmake"))
		if err != nil {
			t.Fatal(err)
		}
		id := packageIdentity{Name: name, OS: f.options.OS, Arch: f.options.Arch}
		members := []archiveMember{{Name: name + "/autolib-import.cmake", Body: strings.Replace(identityHeader(id), `set(AUTOLIB_DEPENDENCIES "")`, `set(AUTOLIB_DEPENDENCIES "`+map[string]string{"flann": "lz4"}[name]+`")`, 1) + string(adapter)}, {Name: name + "/include/", Kind: '5'}}
		if name == "queue" {
			members = append(members, archiveMember{Name: "queue/include/queue/readerwriterqueue.h", Body: "// fixture header\n"})
		}
		if name == "lz4" {
			// 上游 Targets 在完整目标集已存在时直接返回，不能靠重新定义报错来发现来源冲突。
			members = append(members, archiveMember{Name: "lz4/lib/cmake/lz4/lz4Config.cmake", Body: "if(TARGET LZ4::lz4 AND TARGET LZ4::lz4_static)\nreturn()\nendif()\nadd_library(LZ4::lz4 INTERFACE IMPORTED)\nadd_library(LZ4::lz4_static INTERFACE IMPORTED)\n"})
		}
		if name == "flann" {
			members = append(members, archiveMember{Name: "flann/lib/cmake/flann/flann-config.cmake", Body: "if(NOT TARGET AutoLib::lz4)\nmessage(FATAL_ERROR \"lz4 not loaded first\")\nendif()\nif(TARGET flann::flann_cpp AND TARGET flann::flann)\nreturn()\nendif()\nadd_library(flann::flann_cpp INTERFACE IMPORTED)\nadd_library(flann::flann INTERFACE IMPORTED)\n"})
		}
		a := testArchive(t, members)
		data, err := os.ReadFile(a)
		if err != nil {
			t.Fatal(err)
		}
		asset := name + "-" + id.OS + "-" + id.Arch + ".tar.zst"
		putTestFile(t, filepath.Join(f.release, asset), string(data))
	}
	putTestFile(t, filepath.Join(f.source, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(representatives NONE)\ninclude(autolib.cmake)\nautolib_import(LOCAL_ROOT \"${FIXTURE_RELEASE}\" WORK_ROOT \"${FIXTURE_WORK}\" TARGET_OS \"${FIXTURE_OS}\" TARGET_ARCH \"${FIXTURE_ARCH}\" PACKAGES "+packages+")\nadd_subdirectory(child)\n")
	putTestFile(t, filepath.Join(f.source, "child/CMakeLists.txt"), "foreach(name IN ITEMS queue lz4 flann)\nif(NOT TARGET AutoLib::${name})\nmessage(FATAL_ERROR \"target not visible: ${name}\")\nendif()\nendforeach()\nadd_library(consumer INTERFACE)\ntarget_link_libraries(consumer INTERFACE AutoLib::queue AutoLib::lz4 AutoLib::flann)\nautolib_import(LOCAL_ROOT \"${FIXTURE_RELEASE}\" WORK_ROOT \"${FIXTURE_WORK}\" TARGET_OS \"${FIXTURE_OS}\" TARGET_ARCH \"${FIXTURE_ARCH}\" PACKAGES flann queue lz4)\n")
	return f
}

func TestRepresentativeAdapters(t *testing.T) {
	f := representativeFixture(t, "flann queue")
	consumer := filepath.Join(f.source, "CMakeLists.txt")
	putTestFile(t, consumer, strings.Replace(readTestFile(t, consumer), "add_subdirectory(child)", "if(NOT AUTOLIB_PREPARED_PACKAGES STREQUAL \"lz4;flann;queue\")\nmessage(FATAL_ERROR \"wrong dependency order: ${AUTOLIB_PREPARED_PACKAGES}\")\nendif()\nadd_subdirectory(child)", 1))
	destination := f.release + "中文 空格"
	if err := os.Rename(f.release, destination); err != nil {
		t.Fatal(err)
	}
	f.release = destination
	f.mustConfigure()
}

func TestAdapterExistingTargets(t *testing.T) {
	for _, name := range []string{"lz4", "flann"} {
		t.Run(name, func(t *testing.T) {
			f := representativeFixture(t, "flann queue lz4")
			targets := []string{"LZ4::lz4", "LZ4::lz4_static"}
			if name == "flann" {
				targets = []string{"flann::flann_cpp", "flann::flann"}
			}
			definitions := ""
			for _, target := range targets {
				definitions += "add_library(" + target + " INTERFACE IMPORTED)\n"
			}
			consumer := filepath.Join(f.source, "CMakeLists.txt")
			putTestFile(t, consumer, strings.Replace(readTestFile(t, consumer), "autolib_import(", definitions+"autolib_import(", 1))
			out, err := f.configure()
			if err == nil || !strings.Contains(out, "AutoLib "+name+" cannot import existing target") {
				t.Fatalf("existing package-external targets were not rejected clearly: %v\n%s", err, out)
			}
		})
	}
}

func TestAdapterDependencyOrder(t *testing.T) {
	t.Run("root only", func(t *testing.T) {
		f := representativeFixture(t, "flann")
		putTestFile(t, filepath.Join(f.source, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(root_only NONE)\ninclude(autolib.cmake)\nautolib_import(LOCAL_ROOT \"${FIXTURE_RELEASE}\" WORK_ROOT \"${FIXTURE_WORK}\" TARGET_OS \"${FIXTURE_OS}\" TARGET_ARCH \"${FIXTURE_ARCH}\" PACKAGES flann)\nif(NOT TARGET AutoLib::lz4 OR NOT TARGET AutoLib::flann OR NOT AUTOLIB_PREPARED_PACKAGES STREQUAL \"lz4;flann\")\nmessage(FATAL_ERROR \"missing dependency closure\")\nendif()\n")
		f.mustConfigure()
	})
	f := representativeFixture(t, "flann")
	system := filepath.Join(f.root, "system")
	marker := filepath.Join(f.root, "system-was-used")
	putTestFile(t, filepath.Join(system, "lz4Config.cmake"), "file(WRITE \""+filepath.ToSlash(marker)+"\" \"unexpected system fallback\")\nadd_library(LZ4::lz4 INTERFACE IMPORTED)\n")
	if err := os.Rename(filepath.Join(f.release, "lz4-"+f.options.OS+"-"+f.options.Arch+".tar.zst"), filepath.Join(f.release, "lz4-away")); err != nil {
		t.Fatal(err)
	}
	if out, err := f.configure("-DCMAKE_PREFIX_PATH=" + system); err == nil {
		t.Fatalf("unlisted sibling was imported: %s", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("adapter fell back to system lz4")
	}
}

const fakeImporterSource = `package main
import("encoding/json";"flag";"fmt";"os";"strings")
func main(){
 log:=os.Getenv("AUTOLIB_TEST_TOOL_LOG");if log!="" {f,e:=os.OpenFile(log,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0644);if e!=nil {panic(e)};json.NewEncoder(f).Encode(os.Args[1:]);f.Close()}
 if len(os.Args)<2 || os.Args[1]!="prepare" {fmt.Fprintln(os.Stderr,"only prepare is supported");os.Exit(2)}
 result:="";for _,arg:=range os.Args[2:] {if strings.HasPrefix(arg,"--result="){result=strings.TrimPrefix(arg,"--result=")}}
 fs:=flag.NewFlagSet("prepare",flag.ExitOnError);for _,n:=range []string{"local-root","remote-root","work-root","os","arch","result","package"}{fs.String(n,"",n)};fs.Parse(os.Args[2:])
 mode:=os.Getenv("AUTOLIB_TEST_MODE");if mode=="no-result" {return}
 if mode=="fail" {os.WriteFile(result,[]byte("message(FATAL_ERROR STALE_RESULT_LOADED)\n"),0644);os.Exit(7)}
 body:="set(AUTOLIB_PREPARED_PACKAGES demo)\nset(AUTOLIB_PACKAGE_DIR_demo [==["+os.Getenv("AUTOLIB_TEST_PACKAGE_DIR")+"]==])\n"
 if mode=="incomplete" {body="set(AUTOLIB_PREPARED_PACKAGES demo)\n"}
 if e:=os.WriteFile(result,[]byte(body),0644);e!=nil {panic(e)}
}`
