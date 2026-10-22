package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRealModuleDeclarations(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"flann", "freetype", "harfbuzz", "lz4", "onnxruntime", "opencv", "queue", "zlib"}
	for _, slot := range []struct{ system, arch, onnx string }{
		{"Windows", "x64", "onnxruntime-win-x64-1.24.4.zip"},
		{"Linux", "x86_64", "onnxruntime-linux-x64-1.24.4.tgz"},
		{"Linux", "aarch64", "onnxruntime-linux-aarch64-1.24.4.tgz"},
	} {
		t.Run(slot.system+"/"+slot.arch, func(t *testing.T) {
			modules, err := loadModuleClosure(root, names, slot.system, slot.arch)
			if err != nil {
				t.Fatal(err)
			}
			byName := map[string]*moduleDefinition{}
			for _, m := range modules {
				byName[m.Name] = m
			}
			for _, m := range modules {
				deps := []*moduleDefinition{}
				for _, name := range m.Depends {
					deps = append(deps, byName[name])
				}
				context := buildContext{SourceRoot: root, BuildRoot: t.TempDir(), SystemName: slot.system, Architecture: slot.arch, Generator: "Unix Makefiles", CMakeCommand: "cmake", GitExecutable: "git"}
				text, err := renderModule(m, context, deps)
				if err != nil {
					t.Error(m.Name, err)
					continue
				}
				if strings.Contains(text, "<PYTHON_EXECUTABLE>") || strings.Contains(text, "/.venv/") {
					t.Error(m.Name, "project Python injected")
				}
			}
			check := func(got, want any) {
				t.Helper()
				if !reflect.DeepEqual(got, want) {
					t.Errorf("want %#v, got %#v", want, got)
				}
			}
			ft, hb, cv, flann, lz4, onnx := byName["freetype"], byName["harfbuzz"], byName["opencv"], byName["flann"], byName["lz4"], byName["onnxruntime"]
			check(hb.Version, "8.5.0")
			check(hb.Depends, []string{"freetype"})
			check(hb.Source.URL, []string{"https://github.com/harfbuzz/harfbuzz/archive/refs/tags/8.5.0.zip"})
			check(hb.Source.URLHash, "SHA256=734985456b60c198da41f4c85280963795b9baa6fb6f3b91c6b85026da837933")
			check(hb.Cache["HB_HAVE_FREETYPE"], true)
			check(hb.PkgconfigDir, "lib/pkgconfig")
			check(hb.Sentinel, "include/harfbuzz/hb.h")
			check(ft.ConfigDir, "lib/cmake/freetype")
			check(ft.PkgconfigDir, "lib/pkgconfig")
			check(cv.Version, "4.14.0")
			check(cv.Depends, []string{"freetype", "harfbuzz"})
			check(cv.PackageName, "OpenCV")
			config, sentinel := "lib/cmake/opencv4", "lib/cmake/opencv4/OpenCVConfig.cmake"
			if slot.system == "Windows" {
				config, sentinel = ".", "OpenCVConfig.cmake"
			}
			check(cv.ConfigDir, config)
			check(cv.Sentinel, sentinel)
			for name, source := range map[string]sourceDefinition{"opencv": *cv.Source, "opencv_contrib": cv.Resources["opencv_contrib"]} {
				check(source.URL, []string{"https://github.com/opencv/" + name + "/archive/refs/tags/4.14.0.zip"})
				check(source.DownloadName, name+"-4.14.0.zip")
				if len(source.URLHash) != 71 || !strings.HasPrefix(source.URLHash, "SHA256=") {
					t.Error(source.URLHash)
				}
			}
			for _, key := range strings.Fields(`BUILD_DOCS BUILD_EXAMPLES BUILD_PACKAGE BUILD_TESTS BUILD_PERF_TESTS
BUILD_opencv_apps BUILD_JAVA BUILD_opencv_java_bindings_generator BUILD_opencv_js
BUILD_opencv_js_bindings_generator BUILD_opencv_python2 BUILD_opencv_python3
BUILD_opencv_python_bindings_generator BUILD_opencv_python_tests BUILD_opencv_objc_bindings_generator
BUILD_opencv_ts BUILD_opencv_ml BUILD_opencv_dnn BUILD_opencv_objdetect BUILD_opencv_gapi
BUILD_opencv_bgsegm BUILD_opencv_optflow BUILD_opencv_stereo BUILD_opencv_superres
BUILD_opencv_tracking BUILD_opencv_videostab BUILD_opencv_datasets BUILD_opencv_phase_unwrapping
BUILD_opencv_bioinspired BUILD_opencv_hfs BUILD_opencv_saliency BUILD_opencv_rapid
BUILD_opencv_rgbd BUILD_opencv_surface_matching BUILD_opencv_structured_light BUILD_opencv_ccalib
BUILD_opencv_signal WITH_GSTREAMER WITH_PROTOBUF`) {
				check(cv.Cache[key], false)
			}
			for _, key := range []string{"BUILD_opencv_world", "BUILD_opencv_freetype", "OPENCV_ENABLE_NONFREE", "WITH_FFMPEG"} {
				check(cv.Cache[key], true)
			}
			for _, key := range []string{"WITH_KLEIDICV", "WITH_FASTCV", "BUILD_LIST", "BUILD_opencv_calib3d", "BUILD_opencv_features2d", "BUILD_opencv_flann", "BUILD_opencv_ximgproc", "BUILD_opencv_video"} {
				if _, ok := cv.Cache[key]; ok {
					t.Error("unexpected override", key)
				}
			}
			check(cv.Cache["OPENCV_EXTRA_MODULES_PATH"], "<RESOURCE_SOURCE_DIR:opencv_contrib>/modules")
			check(cv.Patches[0].Resource, "opencv_contrib")
			check(flann.Depends, []string{"lz4"})
			check(flann.Version, "f9caaf609d8b8cb2b7104a85cf59eb92c275a25d")
			check(flann.Source.GitTag, "f9caaf609d8b8cb2b7104a85cf59eb92c275a25d")
			check(flann.ConfigDir, "lib/cmake/flann")
			check(flann.PkgconfigDir, "lib/pkgconfig")
			check(flann.Sentinel, "include/flann/flann.hpp")
			for _, key := range []string{"BUILD_C_BINDINGS", "USE_OPENMP"} {
				check(flann.Cache[key], true)
			}
			for _, key := range []string{"BUILD_PYTHON_BINDINGS", "BUILD_MATLAB_BINDINGS", "BUILD_CUDA_LIB", "BUILD_EXAMPLES", "BUILD_TESTS", "BUILD_DOC", "USE_MPI"} {
				check(flann.Cache[key], false)
			}
			if _, ok := flann.Cache["BUILD_SHARED_LIBS"]; ok {
				t.Error("unexpected FLANN shared override")
			}
			check(lz4.ConfigDir, "lib/cmake/lz4")
			check(lz4.PkgconfigDir, "lib/pkgconfig")
			check(onnx.Source.DownloadName, slot.onnx)
			if !strings.HasSuffix(onnx.Source.URL[0], "/"+slot.onnx) {
				t.Error(onnx.Source.URL)
			}
		})
	}
	if _, err := loadDefinition(filepath.Join(root, "src/dependencies/onnxruntime/module.toml"), "Windows", "ARM64"); err == nil {
		t.Fatal("unsupported ONNX target accepted")
	}
}
