package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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
	"testing"
	"time"
)

// The subprocesses keep their real Release URLs and certificate checks. Only this
// test's HTTPS proxy terminates their connections with a test-owned certificate.
func entryHTTPSProxy(t *testing.T, handler http.Handler) string {
	ca, _ := entryHTTPSProxyControl(t, handler)
	return ca
}

func entryHTTPSProxyControl(t *testing.T, handler http.Handler) (string, func()) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "AutoLib test CA"}, DNSNames: []string{"github.com", "api.github.com", "gitee.com", "downloads.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
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
	return ca, proxy.Close
}

func TestCMakeRemoteBootstrapMigration(t *testing.T) {
	for _, host := range []string{"github.com", "gitee.com"} {
		for _, kind := range []string{"success", "encoded tag", "deleted tool", "missing program", "bare remote only", "redirect"} {
			t.Run(host+"/"+kind, func(t *testing.T) {
				e := remoteEntry(t, host)
				f := e.f
				member := f.toolAsset
				if kind == "missing program" {
					member = "other-program"
				}
				tool := readTestFile(t, f.importerArchive(member, readTestFile(t, f.tool)))
				toolAsset := f.toolAsset + ".tar.xz"
				if kind == "bare remote only" {
					e.assets[f.toolAsset] = readTestFile(t, f.tool)
				} else {
					e.asset(t, "autolib-importer/"+toolAsset, tool)
				}
				if kind == "deleted tool" {
					e.status[toolAsset] = 404
				}
				pkg := e.pkg(t, "demo", "1.0", "")
				e.consumer(t, "demo@1.0", "autolib_import(TARGET consumer INTERFACE demo@1.0)\n")
				if kind == "encoded tag" {
					path := filepath.Join(f.source, "CMakeLists.txt")
					putTestFile(t, path, strings.Replace(readTestFile(t, path), "/download/v1", "/download/v%31", 1))
				}
				if kind == "redirect" {
					e.redirects[toolAsset] = "https://" + host + "/owner/repo/releases/download/v1/redirect-tool"
					e.assets["redirect-tool"] = tool
				}
				out, err := e.configure(t)
				success := kind == "success" || kind == "encoded tag" || kind == "redirect"
				if (err == nil) != success {
					t.Fatalf("configure: %v\n%s", err, out)
				}
				want := map[string]int{}
				if kind != "bare remote only" {
					want[toolAsset] = 1
				}
				if success {
					want[pkg] = 1
					if kind == "redirect" {
						want["redirect-tool"] = 1
					}
					if readTestFile(t, filepath.Join(entryCache(f), "demo/1.0/data")) != "archive" {
						t.Fatal("wrong downloaded package")
					}
				} else if _, statErr := os.Stat(filepath.Join(entryCache(f), f.toolAsset)); !os.IsNotExist(statErr) {
					t.Fatal("failed bootstrap committed tool", statErr)
				}
				entries, readErr := os.ReadDir(entryCache(f))
				if readErr != nil {
					t.Fatal(readErr)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".tool-") {
						t.Fatal("failed bootstrap left staging", entry.Name())
					}
				}
				e.counts(t, 1, want)
				if kind == "success" {
					e.offline = true
					if out, err := e.configure(t); err != nil {
						t.Fatalf("cached source-free reconfigure: %v\n%s", err, out)
					}
					e.counts(t, 1, want)
					if err := filepath.WalkDir(f.work, func(path string, entry os.DirEntry, walkErr error) error {
						if walkErr == nil && !entry.IsDir() && entry.Name() == f.toolAsset {
							t.Error("retained temporary importer program", path)
						}
						return walkErr
					}); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestGitHubValidationDriver(t *testing.T) {
	for _, host := range []string{"github.com", "gitee.com"} {
		t.Run(host, func(t *testing.T) {
			root := t.TempDir()
			entry := "# fixture entry bytes\n"
			putTestFile(t, filepath.Join(root, "src/autolib.cmake"), entry)
			putTestFile(t, filepath.Join(root, "tests/autolib/smoke.cpp"), "// fixture smoke\n")
			putTestFile(t, filepath.Join(root, "tests/autolib/CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(driver_fixture NONE)
enable_testing()
if(NOT ROOT_PACKAGES STREQUAL "flann;queue" OR NOT AUTOLIB_REMOTE_ROOT MATCHES "^https://(github|gitee).com/owner/repo/releases/download/v1$")
    message(FATAL_ERROR "driver lost source or root packages: ${ROOT_PACKAGES}")
endif()
if(NOT AUTOLIB_ENTRY STREQUAL "${CMAKE_CURRENT_SOURCE_DIR}/3rdparty/autolib.cmake")
    message(FATAL_ERROR "driver must copy entry to independent consumer: ${AUTOLIB_ENTRY}")
endif()
include("${AUTOLIB_ENTRY}")
set(runtime "${CMAKE_CURRENT_SOURCE_DIR}/3rdparty/flann/1.9.2/lib")
if(WIN32)
    set(runtime "${CMAKE_CURRENT_SOURCE_DIR}/3rdparty/flann/1.9.2/bin")
endif()
file(MAKE_DIRECTORY "${runtime}")
file(WRITE "${CMAKE_BINARY_DIR}/prepared-state.cmake" "set(AUTOLIB_CACHE_ROOT [==[${CMAKE_CURRENT_SOURCE_DIR}/3rdparty]==])\nset(AUTOLIB_PACKAGE_DIR_flann [==[${CMAKE_CURRENT_SOURCE_DIR}/3rdparty/flann/1.9.2]==])\nset(AUTOLIB_FLANN_RUNTIME_DIR [==[${runtime}]==])\n")
file(WRITE "${CMAKE_BINARY_DIR}/check.cmake" "set(expected [==[${runtime}]==])\nif(WIN32)\nset(paths \"\$ENV{PATH}\")\nelse()\nset(paths \"\$ENV{LD_LIBRARY_PATH}\")\nendif()\nstring(FIND \"\${paths}\" \"\${expected}\" position)\nif(NOT position EQUAL 0)\nmessage(FATAL_ERROR \"wrong runtime path: \${paths}\")\nendif()\n")
add_test(NAME smoke COMMAND "${CMAKE_COMMAND}" -P "${CMAKE_BINARY_DIR}/check.cmake")
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
			args := []string{"-DSOURCE_ROOT=" + filepath.ToSlash(root), "-DVALIDATION_ROOT=" + filepath.ToSlash(filepath.Join(root, "build/validation")), "-DREMOTE_ROOT=https://" + host + "/owner/repo/releases/download/v1", "-DGENERATOR=" + generator, "-P", driver}
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
			if got := readTestFile(t, filepath.Join(runs[0], "consumer/3rdparty/autolib.cmake")); got != entry {
				t.Fatal("driver changed copied entry bytes")
			}
			if _, err := os.Stat(filepath.Join(root, "src/flann")); !os.IsNotExist(err) {
				t.Fatalf("driver wrote source-adjacent cache: %v", err)
			}
		})
	}
	t.Run("public entry failure", func(t *testing.T) {
		root := t.TempDir()
		putTestFile(t, filepath.Join(root, "src/autolib.cmake"), readTestFile(t, "../autolib.cmake"))
		putTestFile(t, filepath.Join(root, "tests/autolib/smoke.cpp"), "")
		putTestFile(t, filepath.Join(root, "tests/autolib/CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(driver_failure NONE)
set(AUTOLIB_LOCAL_ROOT "")
include("${AUTOLIB_ENTRY}")
add_library(consumer INTERFACE)
autolib_import(TARGET consumer INTERFACE flann)
`)
		driver, _ := filepath.Abs("../../tests/autolib/run-github.cmake")
		out, err := exec.Command("cmake", "-DSOURCE_ROOT="+root, "-DVALIDATION_ROOT="+filepath.Join(root, "build/validation"), "-DREMOTE_ROOT=https://github.com/owner/repo/releases/tag/v1", "-P", driver).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "configure failed") || !strings.Contains(string(out), "Release download root") {
			t.Fatalf("driver did not propagate public entry failure: %v\n%s", err, out)
		}
		t.Logf("public entry rejected invalid download root:\n%s", out)
	})
}
