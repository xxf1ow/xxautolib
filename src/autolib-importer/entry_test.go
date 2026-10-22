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
	manifest                                                       manifest
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
	o, m := localReleaseFixture(t, "demo")
	root := filepath.Dir(o.URI)
	if suffix != "" {
		destination := filepath.Join(root, suffix)
		if err := os.Rename(o.URI, destination); err != nil {
			t.Fatal(err)
		}
		o.URI = destination
		o.WorkRoot = filepath.Join(root, "work "+strings.ReplaceAll(suffix, ";", ""))
	}
	tool := buildEntryTool(t, fake)
	data, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	hostOS := runtime.GOOS
	hostArch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	putTestFile(t, filepath.Join(o.URI, "chosen-tool.bin"), string(data))
	if err := os.Chmod(filepath.Join(o.URI, "chosen-tool.bin"), 0755); err != nil {
		t.Fatal(err)
	}
	m.Importers = []importerRecord{{OS: hostOS, Arch: hostArch, Asset: "chosen-tool.bin", SHA256: testDigest(data)}}
	// 目标刻意不同于常用 Windows 宿主，验证工具与库槽独立。
	id := packageIdentity{Name: "demo", OS: o.OS, Arch: o.Arch}
	adapter := identityHeader(id) + "if(TARGET AutoLib::demo)\nreturn()\nendif()\nadd_library(AutoLib::demo INTERFACE IMPORTED GLOBAL)\n"
	a := testArchive(t, []archiveMember{{Name: "demo/autolib-import.cmake", Body: adapter}})
	archive, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(o.URI, m.Packages[0].Asset), string(archive))
	m.Packages[0].SHA256 = testDigest(archive)
	writeTestManifest(t, filepath.Join(o.URI, "manifest.json"), m)
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
	f := &entryFixture{t: t, root: root, source: source, release: o.URI, work: o.WorkRoot, cmake: cmake, generator: generator, buildTool: buildTool, tool: tool, options: o, manifest: m}
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
	var shuffled map[string]any
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(f.release, "manifest.json"))), &shuffled); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(shuffled)
	if err != nil {
		t.Fatal(err)
	}
	putTestFile(t, filepath.Join(f.release, "manifest.json"), string(data))
	f.mustConfigure()
	source, err := normalizeLocalSource(f.release)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.ToSlash(packageDirectory(f.work, source, packageIdentity{Name: "demo", OS: f.options.OS, Arch: f.options.Arch}))
	if got := readTestFile(t, filepath.Join(f.root, "consumer-build/observed.txt")); got != want {
		t.Fatalf("%q != %q", got, want)
	}
	toolDir := filepath.Join(f.work, sourceKey(source), "tools", f.manifest.Importers[0].OS+"-"+f.manifest.Importers[0].Arch)
	toolName := "autolib-importer"
	if runtime.GOOS == "windows" {
		toolName += ".exe"
	}
	cached := filepath.Join(toolDir, toolName)
	before := readTestFile(t, cached)
	updated := before + "updated bytes"
	putTestFile(t, filepath.Join(f.release, "chosen-tool.bin"), updated)
	f.manifest.Importers[0].SHA256 = testDigest([]byte(updated))
	writeTestManifest(t, filepath.Join(f.release, "manifest.json"), f.manifest)
	f.mustConfigure()
	if readTestFile(t, cached) != before {
		t.Fatal("automatically refreshed tool")
	}
	if err := os.RemoveAll(toolDir); err != nil {
		t.Fatal(err)
	}
	f.mustConfigure()
	if readTestFile(t, cached) != updated {
		t.Fatal("explicit tool removal did not refresh")
	}
	if err := os.Rename(f.release, f.release+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	f.mustConfigure()
}

func TestCMakeBootstrapFailure(t *testing.T) {
	t.Run("cached tool corruption", func(t *testing.T) {
		f := newEntryFixture(t, true, "")
		f.mustConfigure()
		if err := os.Remove(filepath.Join(f.root, "calls.jsonl")); err != nil {
			t.Fatal(err)
		}
		source, err := normalizeLocalSource(f.release)
		if err != nil {
			t.Fatal(err)
		}
		name := "autolib-importer"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		cached := filepath.Join(f.work, sourceKey(source), "tools", f.manifest.Importers[0].OS+"-"+f.manifest.Importers[0].Arch, name)
		putTestFile(t, cached, "corrupt cached tool")
		if out, err := f.configure(); err == nil || !strings.Contains(out, "SHA256 mismatch") {
			t.Fatalf("%v\n%s", err, out)
		}
		if got := readTestFile(t, cached); got != "corrupt cached tool" {
			t.Fatal("corrupted cache overwritten")
		}
		if _, err := os.Stat(filepath.Join(f.root, "calls.jsonl")); !os.IsNotExist(err) {
			t.Fatal("corrupted cached tool executed")
		}
	})
	for _, kind := range []string{"checksum", "missing manifest", "missing host", "missing tool", "cannot start", "prepare fails", "no result", "unknown field"} {
		t.Run(kind, func(t *testing.T) {
			f := newEntryFixture(t, true, "")
			switch kind {
			case "checksum":
				f.manifest.Importers[0].SHA256 = strings.Repeat("0", 64)
			case "missing manifest":
				if err := os.Remove(filepath.Join(f.release, "manifest.json")); err != nil {
					t.Fatal(err)
				}
			case "missing host":
				f.manifest.Importers[0].Arch = "unsupported"
			case "missing tool":
				if err := os.Remove(filepath.Join(f.release, "chosen-tool.bin")); err != nil {
					t.Fatal(err)
				}
			case "cannot start":
				putTestFile(t, filepath.Join(f.release, "chosen-tool.bin"), "not executable")
				f.manifest.Importers[0].SHA256 = testDigest([]byte("not executable"))
			case "prepare fails":
				t.Setenv("AUTOLIB_TEST_MODE", "fail")
			case "no result":
				t.Setenv("AUTOLIB_TEST_MODE", "no-result")
			}
			if kind != "missing manifest" {
				writeTestManifest(t, filepath.Join(f.release, "manifest.json"), f.manifest)
			}
			if kind == "unknown field" {
				data := readTestFile(t, filepath.Join(f.release, "manifest.json"))
				putTestFile(t, filepath.Join(f.release, "manifest.json"), strings.Replace(data, `"asset":`, `"protocol":"unused","asset":`, 1))
			}
			out, err := f.configure()
			if err == nil || strings.Contains(out, "STALE_RESULT_LOADED") {
				t.Fatalf("wrong failure: %v\n%s", err, out)
			}
			if kind != "prepare fails" && kind != "no result" {
				if _, err := os.Stat(filepath.Join(f.root, "calls.jsonl")); !os.IsNotExist(err) {
					t.Fatal("invalid importer executed")
				}
			}
		})
	}
}

func TestCMakeEntryPaths(t *testing.T) {
	t.Run("real importer special paths", func(t *testing.T) {
		f := newEntryFixture(t, false, "中文 空格;${literal}")
		f.mustConfigure()
		source, err := normalizeLocalSource(f.release)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.ToSlash(packageDirectory(f.work, source, packageIdentity{Name: "demo", OS: f.options.OS, Arch: f.options.Arch}))
		if got := readTestFile(t, filepath.Join(f.root, "consumer-build/observed.txt")); got != want {
			t.Fatalf("%q != %q", got, want)
		}
	})
	for _, suffix := range []string{"", "中文 空格;${literal}"} {
		t.Run(suffix, func(t *testing.T) {
			f := newEntryFixture(t, true, suffix)
			t.Setenv("AUTOLIB_TEST_REMOVE_MANIFEST", "1")
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
				for _, want := range []string{"--uri=" + wantSource, "--work-root=" + filepath.ToSlash(f.work), "--os=" + f.options.OS, "--arch=" + f.options.Arch, "--package=demo"} {
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
				manifestArg, resultArg := "", ""
				for _, arg := range args {
					if strings.HasPrefix(arg, "--manifest=") {
						manifestArg = arg
					}
					if strings.HasPrefix(arg, "--result=") {
						resultArg = arg
					}
				}
				if manifestArg == "" || resultArg == "" || resultArg == previousResult {
					t.Fatalf("missing current manifest or reused result: %#v", args)
				}
				previousResult = resultArg
			}
		})
	}
}

func TestCMakeWorkRootSemicolon(t *testing.T) {
	for _, kind := range []string{"explicit", "default"} {
		t.Run(kind, func(t *testing.T) {
			f := newEntryFixture(t, true, "")
			work := filepath.Join(f.root, "work;literal")
			var extra []string
			if kind == "explicit" {
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
			if err == nil || !strings.Contains(out, "WORK_ROOT must not contain semicolons") {
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
	source, err := normalizeLocalSource(f.release)
	if err != nil {
		t.Fatal(err)
	}
	dir := packageDirectory(f.work, source, packageIdentity{Name: "demo", OS: f.options.OS, Arch: f.options.Arch})
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.release, f.manifest.Packages[0].Asset)); err != nil {
		t.Fatal(err)
	}
}

func representativeFixture(t *testing.T, packages string) *entryFixture {
	t.Helper()
	f := newEntryFixture(t, false, "")
	f.manifest.Packages = nil
	for _, name := range []string{"queue", "lz4", "flann"} {
		adapter, err := os.ReadFile(filepath.Join("../dependencies", name, "autolib-import.cmake"))
		if err != nil {
			t.Fatal(err)
		}
		id := packageIdentity{Name: name, OS: f.options.OS, Arch: f.options.Arch}
		members := []archiveMember{{Name: name + "/autolib-import.cmake", Body: identityHeader(id) + string(adapter)}, {Name: name + "/include/", Kind: '5'}}
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
		asset := name + ".tar.zst"
		putTestFile(t, filepath.Join(f.release, asset), string(data))
		f.manifest.Packages = append(f.manifest.Packages, packageRecord{Name: name, OS: id.OS, Arch: id.Arch, Asset: asset, SHA256: testDigest(data)})
	}
	writeTestManifest(t, filepath.Join(f.release, "manifest.json"), f.manifest)
	putTestFile(t, filepath.Join(f.source, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\nproject(representatives NONE)\ninclude(autolib.cmake)\nautolib_import(URI \"${FIXTURE_RELEASE}\" WORK_ROOT \"${FIXTURE_WORK}\" TARGET_OS \"${FIXTURE_OS}\" TARGET_ARCH \"${FIXTURE_ARCH}\" PACKAGES "+packages+")\nadd_subdirectory(child)\n")
	putTestFile(t, filepath.Join(f.source, "child/CMakeLists.txt"), "foreach(name IN ITEMS queue lz4 flann)\nif(NOT TARGET AutoLib::${name})\nmessage(FATAL_ERROR \"target not visible: ${name}\")\nendif()\nendforeach()\nadd_library(consumer INTERFACE)\ntarget_link_libraries(consumer INTERFACE AutoLib::queue AutoLib::lz4 AutoLib::flann)\nautolib_import(URI \"${FIXTURE_RELEASE}\" WORK_ROOT \"${FIXTURE_WORK}\" TARGET_OS \"${FIXTURE_OS}\" TARGET_ARCH \"${FIXTURE_ARCH}\" PACKAGES flann queue lz4)\n")
	return f
}

func TestRepresentativeAdapters(t *testing.T) {
	f := representativeFixture(t, "flann queue lz4 flann")
	destination := f.release + ";中文 空格"
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
	f := representativeFixture(t, "flann")
	system := filepath.Join(f.root, "system")
	marker := filepath.Join(f.root, "system-was-used")
	putTestFile(t, filepath.Join(system, "lz4Config.cmake"), "file(WRITE \""+filepath.ToSlash(marker)+"\" \"unexpected system fallback\")\nadd_library(LZ4::lz4 INTERFACE IMPORTED)\n")
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
 result:="";uri:="";for _,arg:=range os.Args[2:] {if strings.HasPrefix(arg,"--result="){result=strings.TrimPrefix(arg,"--result=")};if strings.HasPrefix(arg,"--uri="){uri=strings.TrimPrefix(arg,"--uri=")}}
 if os.Getenv("AUTOLIB_TEST_REMOVE_MANIFEST")=="1" {os.Remove(uri+"/manifest.json")}
 fs:=flag.NewFlagSet("prepare",flag.ExitOnError);for _,n:=range []string{"uri","work-root","os","arch","result","manifest","package"}{fs.String(n,"",n)};fs.Parse(os.Args[2:])
 mode:=os.Getenv("AUTOLIB_TEST_MODE");if mode=="no-result" {return}
 if mode=="fail" {os.WriteFile(result,[]byte("message(FATAL_ERROR STALE_RESULT_LOADED)\n"),0644);os.Exit(7)}
 body:="set(AUTOLIB_PREPARED_PACKAGES demo)\nset(AUTOLIB_PACKAGE_DIR_demo [==["+os.Getenv("AUTOLIB_TEST_PACKAGE_DIR")+"]==])\n"
 if e:=os.WriteFile(result,[]byte(body),0644);e!=nil {panic(e)}
}`
