package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The subprocesses keep their real GitHub URLs and certificate checks. Only this
// test's HTTPS proxy terminates their connections with a test-owned certificate.
func entryHTTPSProxy(t *testing.T, handler http.Handler) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"api.github.com", "downloads.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := putTestFile(t, filepath.Join(t.TempDir(), "ca.pem"), string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	config := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			w.WriteHeader(502)
			return
		}
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(buffered, "HTTP/1.1 200 Connection Established\r\n\r\n")
		buffered.Flush()
		secure := tls.Server(conn, config)
		defer secure.Close()
		if err := secure.Handshake(); err != nil {
			return
		}
		reader := bufio.NewReader(secure)
		for {
			request, err := http.ReadRequest(reader)
			if err != nil {
				return
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			request.Body.Close()
			response := recorder.Result()
			response.ContentLength = int64(recorder.Body.Len())
			err = response.Write(secure)
			response.Body.Close()
			if err != nil || request.Close {
				return
			}
		}
	}))
	t.Cleanup(proxy.Close)
	for _, name := range []string{"HTTPS_PROXY", "https_proxy"} {
		t.Setenv(name, proxy.URL)
	}
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	t.Setenv("SSL_CERT_FILE", ca)
	return ca
}

func TestCMakeGitHubBootstrap(t *testing.T) {
	for _, kind := range []string{"success", "encoded tag", "remote only", "hash mismatch", "invalid digest", "duplicate tool", "not uploaded", "deleted tool", "untrusted certificate", "existing broken tool"} {
		t.Run(kind, func(t *testing.T) {
			testCMakeGitHubEntry(t, kind)
		})
	}
}

func TestCMakeGitHubEnumerationReuse(t *testing.T) {
	testCMakeGitHubEntry(t, "paged")
}

func testCMakeGitHubEntry(t *testing.T, kind string) {
	t.Helper()
	f := newEntryFixture(t, false, "")
	tool := readTestFile(t, filepath.Join(f.release, f.toolAsset))
	tampered := ""
	marker := filepath.Join(f.root, "wrong-tool-executed")
	if kind == "hash mismatch" {
		tampered = readTestFile(t, buildEntryTool(t, true))
		t.Setenv("AUTOLIB_TEST_TOOL_LOG", marker)
	}
	archive := readTestFile(t, filepath.Join(f.release, "demo-linux-aarch64.tar.zst"))
	extraArchive := ""
	if kind == "paged" {
		extraArchive = readTestFile(t, testArchive(t, []archiveMember{{Name: "extra/autolib-import.cmake", Body: identityHeader(packageIdentity{"extra", "any", "any"}) + "if(NOT TARGET autolib::extra)\nadd_library(autolib::extra INTERFACE IMPORTED GLOBAL)\nendif()\n"}}))
		consumer := filepath.Join(f.source, "CMakeLists.txt")
		body := readTestFile(t, consumer)
		index := strings.LastIndex(body, "autolib_import(")
		putTestFile(t, consumer, body[:index]+"autolib_import(TARGET consumer INTERFACE extra)\nif(NOT TARGET autolib::extra)\nmessage(FATAL_ERROR missing_extra)\nendif()\n")
	}
	if err := os.Remove(filepath.Join(f.release, f.toolAsset)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.release, "demo-linux-aarch64.tar.zst")); err != nil {
		t.Fatal(err)
	}
	if kind == "existing broken tool" {
		putTestFile(t, filepath.Join(f.release, f.toolAsset), "not executable")
	}
	var mu sync.Mutex
	counts := map[string]int{}
	ca := entryHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/repos/owner/repo/releases/tags/v1":
			fmt.Fprint(w, `{"id":42,"tag_name":"v1","draft":false,"assets_url":"https://api.github.com/repos/owner/repo/releases/42/assets"}`)
		case "/repos/owner/repo/releases/42/assets":
			digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(tool)))
			if kind == "invalid digest" {
				digest = "sha256:bad"
			}
			state := "uploaded"
			if kind == "not uploaded" {
				state = "new"
			}
			assets := []map[string]any{{"id": 1, "name": f.toolAsset, "state": state, "digest": digest}, {"id": 2, "name": "demo-linux-aarch64.tar.zst", "state": "uploaded", "digest": fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(archive)))}}
			if kind == "duplicate tool" {
				assets = append(assets, assets[0])
			}
			if kind == "paged" {
				if r.URL.Query().Get("page") == "1" {
					for len(assets) < 100 {
						assets = append(assets, map[string]any{"name": fmt.Sprintf("unused-%d", len(assets))})
					}
				} else {
					assets = []map[string]any{{"id": 3, "name": "extra-any-any.tar.zst", "state": "uploaded", "digest": fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(extraArchive)))}}
				}
			}
			json.NewEncoder(w).Encode(assets)
		case "/repos/owner/repo/releases/assets/1":
			if kind == "deleted tool" {
				w.WriteHeader(404)
				return
			}
			http.Redirect(w, r, "https://downloads.example/tool", 302)
		case "/tool":
			if kind == "hash mismatch" {
				fmt.Fprint(w, tampered)
				return
			}
			fmt.Fprint(w, tool)
		case "/repos/owner/repo/releases/assets/2":
			fmt.Fprint(w, archive)
		case "/repos/owner/repo/releases/assets/3":
			fmt.Fprint(w, extraArchive)
		default:
			w.WriteHeader(404)
		}
	}))
	args := []string{"-DAUTOLIB_REMOTE_ROOT=" + testReleaseURL, "-DCMAKE_TLS_CAINFO=" + filepath.ToSlash(ca)}
	if kind == "encoded tag" {
		args[0] = "-DAUTOLIB_REMOTE_ROOT=https://github.com/owner/repo/releases/tag/v%31"
	}
	if kind == "success" {
		t.Setenv("PATH", t.TempDir())
		t.Setenv("GOROOT", "")
		t.Setenv("GOPATH", "")
	}
	if kind == "untrusted certificate" {
		args[1] = "-DCMAKE_TLS_CAINFO=" + filepath.ToSlash(putTestFile(t, filepath.Join(f.root, "wrong-ca.pem"), "not a CA"))
	}
	if kind == "remote only" {
		args = append(args, "-DAUTOLIB_LOCAL_ROOT=")
	}
	out, err := f.configure(args...)
	success := kind == "success" || kind == "encoded tag" || kind == "remote only" || kind == "paged"
	if (err == nil) != success {
		t.Fatalf("configure: %v\n%s", err, out)
	}
	if !success {
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("unverified tool executed", err)
		}
		if _, err := os.Stat(filepath.Join(f.root, "consumer-build/observed.txt")); !os.IsNotExist(err) {
			t.Fatal("failed bootstrap continued into adapter", err)
		}
		if kind != "existing broken tool" {
			if _, err := os.Stat(filepath.Join(f.release, f.toolAsset)); !os.IsNotExist(err) {
				t.Fatal("failed tool committed", err)
			}
		}
		if kind == "existing broken tool" {
			mu.Lock()
			n := len(counts)
			mu.Unlock()
			if n != 0 {
				t.Fatal("broken local tool triggered network")
			}
		}
		return
	}
	mu.Lock()
	wantPages, wantDownloads := 1, 1
	if kind == "paged" {
		wantPages = 2
		if counts["/repos/owner/repo/releases/assets/3"] != 1 {
			t.Error("later root was not fetched", counts)
		}
	}
	// Without a local source, a later prepare deliberately has no cache to search.
	if kind == "remote only" {
		wantDownloads = 2
	}
	if counts["/repos/owner/repo/releases/tags/v1"] != 1 || counts["/repos/owner/repo/releases/42/assets"] != wantPages || counts["/repos/owner/repo/releases/assets/2"] != wantDownloads {
		t.Error("bootstrap did not share enumeration", counts)
	}
	before := fmt.Sprint(counts)
	mu.Unlock()
	if kind == "success" {
		if out, err := f.configure(args...); err != nil {
			t.Fatalf("local reuse: %v\n%s", err, out)
		}
		mu.Lock()
		after := fmt.Sprint(counts)
		mu.Unlock()
		if before != after {
			t.Fatalf("complete local cache used network: %s -> %s", before, after)
		}
	}
}

func TestGitHubValidationDriver(t *testing.T) {
	root := t.TempDir()
	putTestFile(t, filepath.Join(root, "tests/autolib/CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(driver_fixture NONE)
enable_testing()
if(NOT ROOT_PACKAGES STREQUAL "flann;queue" OR NOT AUTOLIB_REMOTE_ROOT STREQUAL "https://github.com/owner/repo/releases/tag/v1")
    message(FATAL_ERROR "driver lost source or root packages: ${ROOT_PACKAGES}")
endif()
file(MAKE_DIRECTORY "${RELEASE_ROOT}/packages/windows-x86_64")
file(WRITE "${CMAKE_BINARY_DIR}/prepared-root.txt" "${RELEASE_ROOT}/packages/windows-x86_64")
add_test(NAME smoke COMMAND "${CMAKE_COMMAND}" -E true)
`)
	cmake, err := exec.LookPath("cmake")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := filepath.Abs("../../tests/autolib/run-github.cmake")
	if err != nil {
		t.Fatal(err)
	}
	generator := "Ninja"
	if runtime.GOOS == "linux" {
		generator = "Unix Makefiles"
	}
	args := []string{"-DSOURCE_ROOT=" + filepath.ToSlash(root), "-DVALIDATION_ROOT=" + filepath.ToSlash(filepath.Join(root, "build/validation")), "-DREMOTE_ROOT=" + testReleaseURL, "-DGENERATOR=" + generator, "-P", driver}
	if out, err := exec.Command(cmake, args...).CombinedOutput(); err != nil {
		t.Fatalf("driver: %v\n%s", err, out)
	}
	runs, err := filepath.Glob(filepath.Join(root, "build/validation/run-*"))
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %v, %v", runs, err)
	}
	if !strings.Contains(readTestFile(t, filepath.Join(runs[0], "test.log")), "100% tests passed") {
		t.Fatal("driver did not run CTest")
	}
}
