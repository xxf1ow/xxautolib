package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func nativePublication(t *testing.T, native nativeLoopFixture) (string, map[string]string, map[string]string) {
	t.Helper()
	python := os.Getenv("AUTOLIB_ENTRY_DELIVERY_PYTHON")
	if python == "" {
		python = "python3"
	}
	fixture := os.Getenv("AUTOLIB_ENTRY_DELIVERY_FIXTURE")
	if fixture == "" {
		fixture = "../../tests/ci/gitee-release/test_entry_delivery.py"
	}
	out := loopCommand(t, python, fixture, "prepare-native", "--source", native.producer,
		"--installed", native.install, "--builder", filepath.Join(native.producer, "build/tools", native.builderName),
		"--system", runtime.GOOS, "--arch", native.arch)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	manifest := strings.TrimSpace(lines[len(lines)-1])
	if !filepath.IsAbs(manifest) {
		t.Fatalf("production preparation did not return an absolute manifest: %q", manifest)
	}
	var publication struct {
		Assets []struct {
			Kind, Name, Path, SHA256 string
			Size                     int64
			Package                  struct{ Name, Version string }
		}
	}
	if err := json.Unmarshal([]byte(readTestFile(t, manifest)), &publication); err != nil {
		t.Fatal(err)
	}
	assets, digests := map[string]string{}, map[string]string{}
	entry := ""
	for _, asset := range publication.Assets {
		body := readTestFile(t, filepath.Join(filepath.Dir(manifest), filepath.FromSlash(asset.Path)))
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
		if int64(len(body)) != asset.Size || digest != asset.SHA256 {
			t.Fatalf("production asset bytes do not match manifest: %s", asset.Name)
		}
		if asset.Kind == "entry" {
			if entry != "" || asset.Name != "autolib.cmake" || asset.Path != "autolib.cmake" {
				t.Fatal("publication must contain one fixed entry")
			}
			entry = body
			continue
		}
		path := "autolib-importer/" + asset.Name
		if asset.Kind == "library" {
			path = asset.Package.Name + "/" + asset.Package.Version + "/" + asset.Name
		}
		assets[path], digests[path] = body, digest+"\n"
	}
	if entry == "" || len(assets) != 4 {
		t.Fatal("production preparation omitted entry, tool or native packages")
	}
	t.Logf("production publication manifest: %s", manifest)
	return entry, assets, digests
}

func TestVersionedRemoteLoop(t *testing.T) {
	native := newNativeLoopFixture(t, t.TempDir())
	publishedEntry, publishedAssets, publishedDigests := nativePublication(t, native)
	assets := map[string]string{}
	digests := map[string]string{}
	for _, ref := range []string{"probe/1", "dep/1", "dep/2", "autolib-importer"} {
		name := native.toolName + ".tar.xz"
		if ref != "autolib-importer" {
			name = strings.ReplaceAll(ref, "/", "-") + "-" + runtime.GOOS + "-" + native.arch + ".tar.zst"
		}
		path := ref + "/" + name
		assets[path] = readTestFile(t, filepath.Join(native.release, name))
		digests[path] = readTestFile(t, filepath.Join(native.release, name+".sha256"))
	}
	producerBefore := sourceSnapshot(t, native.producer)
	hiddenProducer := native.producer + ".hidden"
	if err := os.Rename(native.producer, hiddenProducer); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"github.com", "gitee.com"} {
		t.Run(host, func(t *testing.T) {
			var e *versionedRemoteEntry
			currentAssets, currentDigests := assets, digests
			if host == "gitee.com" {
				e = remoteEntryTarget(t, host, "fixture/mirror", "%E4%B8%AD%E6%96%87%2Frelease")
				currentAssets, currentDigests = publishedAssets, publishedDigests
			} else {
				e = remoteEntry(t, host)
			}
			for path, body := range currentAssets {
				e.asset(t, path, body)
				if got := readTestFile(t, filepath.Join(e.repo, filepath.FromSlash(path)+".sha256")); got != currentDigests[path] {
					t.Fatalf("metadata does not preserve builder digest for %s: %q", path, got)
				}
			}
			versionedLatest(t, e.repo, "probe", "1")
			versionedLatest(t, e.repo, "dep", "2")
			marker := filepath.Join(e.f.root, "wrong-adapter-executed")
			bad := testArchive(t, []archiveMember{{Name: "broken/1/autolib-import.cmake", Body: identityHeader(packageIdentity{"other", "1", runtime.GOOS, native.arch}) + "file(WRITE [==[" + filepath.ToSlash(marker) + "]==] executed)\n"}})
			badAsset := "broken-1-" + runtime.GOOS + "-" + native.arch + ".tar.zst"
			e.asset(t, "broken/1/"+badAsset, readTestFile(t, bad))
			snapshotGit(t, e.repo, "add", ".")
			snapshotGit(t, e.repo, "commit", "-m", "native builder assets")
			metadataBefore := sourceSnapshot(t, e.repo)
			consumer := e.f.source
			cache := filepath.Join(consumer, "3rdparty")
			if host == "gitee.com" {
				putTestFile(t, filepath.Join(cache, "autolib.cmake"), publishedEntry)
			} else {
				putTestFile(t, filepath.Join(cache, "autolib.cmake"), readTestFile(t, "../autolib.cmake"))
				injectEntryRemote(t, filepath.Join(cache, "autolib.cmake"), "https://"+host+"/owner/repo/releases/download/v1")
			}
			unrelated := putTestFile(t, filepath.Join(cache, "unrelated/7/marker"), "unrelated cache bytes\n")
			if runtime.GOOS != "windows" {
				if err := os.Chmod(unrelated, 0440); err != nil {
					t.Fatal(err)
				}
			}
			unrelatedBefore := sourceSnapshot(t, filepath.Join(cache, "unrelated"))
			putTestFile(t, filepath.Join(consumer, "main.cpp"), loopConsumerSource)
			cmakeBody := "set(CMAKE_TLS_CAINFO \"${TLS_CA}\")\n" + loopConsumerCMake
			putTestFile(t, filepath.Join(consumer, "CMakeLists.txt"), cmakeBody)
			consume := func(label, source string) {
				build := filepath.Join(e.f.root, label)
				loopCommand(t, "cmake", "-S", source, "-B", build, "-G", native.generator, "-DINSTALL_ROOT="+native.producer, "-DTLS_CA="+e.ca)
				loopCommand(t, "cmake", "--build", build, "--config", "Release", "--verbose")
				out := loopCommand(t, "ctest", "--test-dir", build, "-C", "Release", "-V")
				if !strings.Contains(out, "header_dep=1 linked_dep=1") {
					t.Fatalf("wrong native versions: %s", out)
				}
				currentCache := filepath.ToSlash(filepath.Join(source, "3rdparty"))
				if runtime.GOOS != "windows" && !strings.Contains(out, "runtime_probe="+currentCache+"/probe/1/") {
					t.Fatalf("native loader did not use current cache: %s", out)
				}
				for _, file := range []string{"imported.txt", "link-inputs-Release.txt"} {
					inputs := readTestFile(t, filepath.Join(build, file))
					t.Logf("%s/%s:\n%s", label, file, inputs)
					if strings.Contains(inputs, filepath.ToSlash(native.producer)) || !strings.Contains(inputs, currentCache+"/dep/1/") || !strings.Contains(inputs, currentCache+"/probe/1/") {
						t.Fatalf("wrong imported or linked inputs: %s", inputs)
					}
				}
			}
			toolAsset := native.toolName + ".tar.xz"
			depAsset := "dep-1-" + runtime.GOOS + "-" + native.arch + ".tar.zst"
			probeAsset := "probe-1-" + runtime.GOOS + "-" + native.arch + ".tar.zst"
			want := map[string]int{toolAsset: 1, depAsset: 1, probeAsset: 1}
			consume("first", consumer)
			e.counts(t, 1, want)
			depBefore := sourceSnapshot(t, filepath.Join(cache, "dep/1"))
			consume("cached", consumer)
			e.counts(t, 1, want)
			adapter := filepath.Join(cache, "probe/1/autolib-import.cmake")
			original := readTestFile(t, adapter)
			putTestFile(t, adapter, "invalid cached candidate\nfile(WRITE [==["+filepath.ToSlash(marker)+"]==] executed)\n")
			out, err := exec.Command("cmake", "-S", consumer, "-B", filepath.Join(e.f.root, "corrupt-cache"), "-G", native.generator, "-DTLS_CA="+e.ca).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "probe/1/autolib-import.cmake") {
				t.Fatalf("corrupt cached candidate was not rejected: %v\n%s", err, out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("corrupt cached candidate executed adapter: %v", err)
			}
			e.counts(t, 1, want)
			putTestFile(t, adapter, original)
			t.Logf("corrupt existing candidate rejected before adapter; HTTP/Git delta=0:\n%s", out)
			// A new configure that needs a different exact package must fetch its own snapshot.
			newSource := filepath.Join(e.f.root, "new request")
			entry := filepath.ToSlash(filepath.Join(cache, "autolib.cmake"))
			writeRequest := func(source, packageRef string) {
				putTestFile(t, filepath.Join(source, "CMakeLists.txt"), fmt.Sprintf(`cmake_minimum_required(VERSION 3.20)
project(remote_request NONE)
set(CMAKE_SYSTEM_NAME %s)
set(CMAKE_SYSTEM_PROCESSOR %s)
include([==[%s]==])
add_library(consumer INTERFACE)
autolib_import(TARGET consumer INTERFACE %s)
`, map[string]string{"linux": "Linux", "windows": "Windows"}[runtime.GOOS], native.arch, entry, packageRef))
			}
			writeRequest(newSource, "dep@2")
			loopCommand(t, "cmake", "-S", newSource, "-B", filepath.Join(e.f.root, "next-configure"), "-G", native.generator, "-DCMAKE_TLS_CAINFO="+e.ca)
			want["dep-2-"+runtime.GOOS+"-"+native.arch+".tar.zst"] = 1
			e.counts(t, 2, want)
			writeRequest(newSource, "broken@1")
			out, err = exec.Command("cmake", "-S", newSource, "-B", filepath.Join(e.f.root, "bad-identity"), "-G", native.generator, "-DCMAKE_TLS_CAINFO="+e.ca).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "directory must end") || !strings.Contains(string(out), "other/1") {
				t.Fatalf("valid digest with wrong package identity was not rejected: %v\n%s", err, out)
			}
			t.Logf("valid digest / wrong identity rejected before adapter:\n%s", out)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("wrong identity executed adapter: %v", err)
			}
			if _, err := os.Stat(filepath.Join(cache, "broken/1")); !os.IsNotExist(err) {
				t.Fatalf("wrong identity committed a package: %v", err)
			}
			requireNoPackageStaging(t, cache)
			want[badAsset] = 1
			e.counts(t, 3, want)
			if !reflect.DeepEqual(metadataBefore, sourceSnapshot(t, e.repo)) {
				t.Fatal("consumer modified the Git metadata source")
			}
			e.stopHTTP()
			if err := os.Rename(e.repo, e.repo+".unavailable"); err != nil {
				t.Fatal(err)
			}
			moved := filepath.Join(e.f.root, "offline consumer")
			putTestFile(t, filepath.Join(moved, "CMakeLists.txt"), cmakeBody)
			putTestFile(t, filepath.Join(moved, "main.cpp"), loopConsumerSource)
			if runtime.GOOS == "windows" {
				loopCommand(t, "cmake", "-E", "copy_directory", cache, filepath.Join(moved, "3rdparty"))
			} else {
				loopCommand(t, "cp", "-a", cache, filepath.Join(moved, "3rdparty"))
			}
			consume("offline", moved)
			e.counts(t, 3, want)
			if !reflect.DeepEqual(depBefore, sourceSnapshot(t, filepath.Join(cache, "dep/1"))) || !reflect.DeepEqual(depBefore, sourceSnapshot(t, filepath.Join(moved, "3rdparty/dep/1"))) || !reflect.DeepEqual(unrelatedBefore, sourceSnapshot(t, filepath.Join(cache, "unrelated"))) || !reflect.DeepEqual(unrelatedBefore, sourceSnapshot(t, filepath.Join(moved, "3rdparty/unrelated"))) {
				t.Fatal("later preparation or offline consumption changed precise dependency or unrelated cache bytes/permissions")
			}
			if !reflect.DeepEqual(metadataBefore, sourceSnapshot(t, e.repo+".unavailable")) {
				t.Fatal("offline consumption changed unavailable metadata source")
			}
			t.Logf("HTTP assets=%v; Git clone=3 ls-tree=3; API/Raw requests=%d; cached and offline configure deltas HTTP=0 Git=0", want, e.apiRequests)
			if host == "gitee.com" {
				t.Logf("production publication entry: fixture/mirror tag=中文/release; observed URLs=%v", e.requests)
			}
		})
	}
	if !reflect.DeepEqual(producerBefore, sourceSnapshot(t, hiddenProducer)) {
		t.Fatal("remote or offline consumption changed hidden producer/archive/install bytes or permissions")
	}
}
