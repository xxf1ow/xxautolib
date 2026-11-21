package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGStreamerFirstStageNative(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	platform := os.Getenv("PLATFORM")
	config, err := loadCIEnvironment(filepath.Join(root, ".github", "ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	production, err := ciProductionForPlatform(platform, config)
	if err != nil {
		t.Fatal(err)
	}
	cmake := os.Getenv("CMAKE_EXE")
	install := filepath.Join(root, "build", "verify-install")
	ffmpeg := "ffmpeg@8.1.3"
	opencv := "opencv@4.14.0"
	visp := "visp@3.7.0"
	gstreamer := "gstreamer@1.28.9"
	opencvClosure := []string{"zlib@1.3.1", "freetype@2.13.2", "harfbuzz@8.5.0", ffmpeg, opencv}
	for _, selection := range []struct {
		root    string
		modules []string
	}{
		{gstreamer, []string{ffmpeg, gstreamer}},
		{opencv, opencvClosure},
		{visp, append(append([]string(nil), opencvClosure...), visp)},
	} {
		t.Run(selection.root, func(t *testing.T) {
			evidence := filepath.Join(root, "build", "verify-evidence", selection.root)
			o := ciValidationOptions{
				SourceRoot: root, InstallRoot: install, BuildRoot: filepath.Join(root, "build", "verify-evidence"),
				CMakeCommand: cmake, MakeProgram: os.Getenv("MAKE_EXE"), EvidenceDirectory: evidence,
				Modules: selection.modules, RootPackages: []string{selection.root}, Context: production,
			}
			path, err := validateCIRelease(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			t.Log(path)
		})
	}
	builder := filepath.Join(root, "build", "verify", "tools", "autolib-builder")
	if production.OS == "windows" {
		builder += ".exe"
	}
	output := filepath.Join(root, "build", "verify-release")
	args := []string{
		"package", "--source-root=" + root, "--install-root=" + install,
		"--output-root=" + output, "--os=" + production.OS, "--arch=" + production.Arch,
		"--host-os=" + production.OS, "--host-arch=" + production.Arch,
		"--importer-file=" + filepath.Join(install, ciImporterExecutable(platform)), "--cmake-command=" + cmake,
		"--module=" + ffmpeg, "--module=" + gstreamer,
	}
	if result, err := exec.Command(builder, args...).CombinedOutput(); err != nil {
		t.Fatalf("package: %v\n%s", err, result)
	}
	entry, err := os.ReadFile(filepath.Join(install, "autolib.cmake"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "autolib.cmake"), entry, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestGStreamerArchiveConsumption(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	platform := os.Getenv("PLATFORM")
	config, err := loadCIEnvironment(filepath.Join(root, ".github", "ci-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	production, err := ciProductionForPlatform(platform, config)
	if err != nil {
		t.Fatal(err)
	}
	archives := filepath.Join(root, "build", "verify-release")
	entry, err := os.ReadFile(filepath.Join(archives, "autolib.cmake"))
	if err != nil {
		t.Fatal(err)
	}
	install, err := os.MkdirTemp(filepath.Join(root, "build"), "archive consumer ")
	if err != nil {
		t.Fatal(err)
	}
	cmake := os.Getenv("CMAKE_EXE")
	for _, asset := range []string{"ffmpeg-8.1.3-" + platform + ".tar.zst", "gstreamer-1.28.9-" + platform + ".tar.zst",
		ciImporterExecutable(platform) + ".tar.xz"} {
		command := exec.Command(cmake, "-E", "tar", "xf", filepath.Join(archives, asset))
		command.Dir = install
		if result, err := command.CombinedOutput(); err != nil {
			t.Fatalf("extract %s: %v\n%s", asset, err, result)
		}
	}
	if err := os.WriteFile(filepath.Join(install, "autolib.cmake"), entry, 0644); err != nil {
		t.Fatal(err)
	}
	pcDir := filepath.Join(install, "gstreamer", "1.28.9", "lib", "pkgconfig")
	pcEnv := append(os.Environ(), "PKG_CONFIG_PATH="+pcDir)
	if production.OS != "linux" {
		pcEnv = append(pcEnv, "PKG_CONFIG_LIBDIR="+pcDir)
	}
	modules := []string{"gstreamer-1.0", "gstreamer-base-1.0", "gstreamer-app-1.0", "gstreamer-video-1.0",
		"gstreamer-rtp-1.0", "gstreamer-rtsp-1.0", "gstreamer-rtsp-server-1.0", "gstreamer-net-1.0"}
	if production.OS == "darwin" {
		modules = append(modules, "gstreamer-gl-1.0")
		licensePath := filepath.Join(install, "gstreamer", "1.28.9", "share", "licenses", "homebrew", "glib", "*", "LGPL-2.1-or-later.txt")
		copies, err := filepath.Glob(licensePath)
		if err != nil || len(copies) == 0 {
			t.Fatalf("archived Homebrew GLib license missing: %v", err)
		}
	}
	if production.OS == "windows" {
		for _, file := range []string{"include/gio-win32-2.0/gio/gwin32inputstream.h", "lib/libz.lib"} {
			path := filepath.Join(install, "gstreamer", "1.28.9", file)
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("archived Windows development file %s: %v", file, err)
			}
		}
	}
	for _, module := range modules {
		pc := exec.Command("pkg-config", "--print-errors", "--cflags", "--libs", module)
		pc.Env = pcEnv
		flags, err := pc.CombinedOutput()
		if err != nil || strings.Contains(string(flags), "/opt/homebrew/") || strings.Contains(string(flags), "verify-install") {
			t.Fatalf("archived pkg-config interface for %s: %v\n%s", module, err, flags)
		}
		t.Logf("%s: %s", module, strings.TrimSpace(string(flags)))
	}
	if production.OS != "windows" {
		orcc := exec.Command(filepath.Join(install, "gstreamer", "1.28.9", "bin", "orcc"), "--version")
		if result, err := orcc.CombinedOutput(); err != nil {
			t.Fatalf("archived Orc compiler: %v\n%s", err, result)
		}
	}
	producer := filepath.Join(root, "build", "verify-install")
	if _, err := os.Stat(producer); err == nil {
		hidden := producer + ".hidden"
		if err := os.Rename(producer, hidden); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Rename(hidden, producer); err != nil {
				t.Error(err)
			}
		})
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	args := []string{
		"-DSOURCE_ROOT=" + root, "-DINSTALL_ROOT=" + install,
		"-DVALIDATION_ROOT=" + filepath.Join(root, "build", "verify-evidence", "archive"),
		"-DGENERATOR=" + production.Generator, "-DGENERATOR_PLATFORM=" + production.Platform,
		"-DGENERATOR_TOOLSET=" + production.Toolset, "-DCMAKE_MSVC_RUNTIME_LIBRARY=" + production.Runtime,
		"-DMODULES=ffmpeg@8.1.3;gstreamer@1.28.9", "-DROOT_PACKAGES=gstreamer@1.28.9",
	}
	if make := os.Getenv("MAKE_EXE"); make != "" {
		args = append(args, "-DCMAKE_MAKE_PROGRAM="+make)
	}
	for name, value := range production.CMakeDefaults {
		args = append(args, "-D"+name+"="+value)
	}
	args = append(args, "-P", filepath.Join(root, "tests", "autolib", "run-installed.cmake"))
	command := exec.Command(cmake, args...)
	command.Env = append(os.Environ(), "HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1", "ALL_PROXY=http://127.0.0.1:1")
	result, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("archive consumer: %v\n%s", err, result)
	}
	run, err := validationRunFromOutput(result)
	if err != nil {
		t.Fatal(err)
	}
	testLog, err := os.ReadFile(filepath.Join(run, "test.log"))
	if err != nil || !strings.Contains(string(testLog), "Decoded 6 frame(s)") {
		t.Fatalf("missing archived video decode evidence: %v\n%s", err, testLog)
	}
	t.Log(run)
}
