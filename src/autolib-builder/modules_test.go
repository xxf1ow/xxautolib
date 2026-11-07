package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVersionedLibrealsenseDependencyPrefixes(t *testing.T) {
	f := newFixture(t)
	patches, err := filepath.Glob("../dependencies/librealsense/*/local-dependencies.patch")
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 1 {
		t.Fatalf("want one librealsense patch, got %v", patches)
	}
	text := readFile(t, patches[0])
	section := ""
	usb, json := []string{}, []string{}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			section = strings.Fields(line)[2]
		}
		if section == "a/CMake/libusb_config.cmake" && strings.HasPrefix(line, " ") {
			usb = append(usb, line[1:])
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			switch section {
			case "a/CMake/libusb_config.cmake":
				usb = append(usb, line[1:])
			case "a/third-party/CMakeLists.txt":
				json = append(json, line[1:])
			}
		}
	}
	root := filepath.ToSlash(filepath.Join(f.root, "libusb/ancestor/nlohmann/ancestor/selected install"))
	usbPrefix, jsonPrefix := root+"/libusb/chosen-usb", root+"/nlohmann/chosen-json"
	library := "libusb-1.0.a"
	if f.context.SystemName == "Windows" {
		library = "usb-1.0.lib"
	}
	putFile(t, filepath.Join(usbPrefix, "lib", library), "")
	putFile(t, filepath.Join(usbPrefix, "include/libusb-1.0/libusb.h"), "")
	putFile(t, filepath.Join(jsonPrefix, "include/nlohmann/json.hpp"), "")
	udev := putFile(t, filepath.Join(f.root, "system/libudev.a"), "")
	project := "cmake_minimum_required(VERSION 3.20)\nproject(prefix_check C)\n" +
		"set(CMAKE_INSTALL_PREFIX " + cmakeArgument(root+"/librealsense/sdk") + ")\n" +
		"set(CMAKE_PREFIX_PATH " + cmakeArgument(root+"/lz4/chosen-lz4") + " " + cmakeArgument(jsonPrefix) + " " + cmakeArgument(usbPrefix) + ")\n" +
		"set(AUTOLIB_UDEV_LIB " + cmakeArgument(filepath.ToSlash(udev)) + ")\n" +
		strings.Join(usb, "\n") + "\n" +
		"add_library(rsutils STATIC stub.c)\n" + strings.Join(json, "\n") + "\n" +
		"get_target_property(usb_includes usb INTERFACE_INCLUDE_DIRECTORIES)\nget_target_property(usb_libraries usb INTERFACE_LINK_LIBRARIES)\n" +
		"get_target_property(json_includes rsutils INTERFACE_INCLUDE_DIRECTORIES)\n" +
		"file(WRITE \"${CMAKE_BINARY_DIR}/prefix-results\" \"${usb_includes}|${usb_libraries}|${json_includes}\")\n"
	putFile(t, filepath.Join(f.root, "stub.c"), "int stub;\n")
	putFile(t, filepath.Join(f.root, "CMakeLists.txt"), project)
	f.configure()
	got := readFile(t, filepath.Join(f.context.BuildRoot, "prefix-results"))
	for _, want := range []string{usbPrefix + "/include/libusb-1.0", usbPrefix + "/lib/" + library, jsonPrefix + "/include"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing selected prefix %s: %s", want, got)
		}
	}
	if err := os.Remove(filepath.Join(jsonPrefix, "include/nlohmann/json.hpp")); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := f.command(f.context.CMakeCommand, "-S", f.root, "-B", f.context.BuildRoot)
	if err == nil || !strings.Contains(stderr, "Local nlohmann headers missing") {
		t.Fatalf("missing selected dependency accepted: %v %s", err, stderr)
	}
}

func TestRealModuleDeclarations(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	refs, err := discoverModuleRefs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 32 {
		t.Fatalf("want 32 recipe versions, got %d", len(refs))
	}
	for _, slot := range [][2]string{{"Windows", "x86_64"}, {"Linux", "x86_64"}, {"Linux", "aarch64"}, {"Darwin", "aarch64"}} {
		t.Run("all-versions/"+slot[0]+"/"+slot[1], func(t *testing.T) {
			description, err := describeSelection(root, slot[0], slot[1], nil, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(description.RequestedRoots) != 32 {
				t.Fatal(description.RequestedRoots)
			}
			if len(description.Roots) != 32 || len(description.FilteredRoots) != 0 {
				t.Fatalf("roots=%v filtered=%v", description.Roots, description.FilteredRoots)
			}
			byRef := map[string]*moduleDefinition{}
			for _, ref := range refs {
				path := filepath.Join(root, "src/dependencies", ref.Name, ref.Version, "module.toml")
				module, err := readDefinition(path, slot[0], slot[1], false)
				if err != nil {
					t.Fatal(err)
				}
				byRef[ref.String()] = module
			}
			if got := byRef["pcl@pcl-1.15.1"].Depends; !reflect.DeepEqual(got, []string{"lz4@1.10.0", "flann@f9caaf609d8b", "boost@1.87.0", "eigen@3.4.0", "zlib@1.3.1"}) {
				t.Fatal(got)
			}
			platform, _ := buildPlatform(slot[0], slot[1])
			for _, ref := range refs {
				module := byRef[ref.String()]
				supported := false
				for _, value := range module.PackagePlatforms {
					supported = supported || value == "any-any" || value == platform
				}
				selection, err := selectModuleClosure(root, []string{ref.String()}, slot[0], slot[1], false)
				if !supported {
					if err != nil || len(selection.FilteredRoots) != 1 || selection.FilteredRoots[0].Code != "unsupported_platform" {
						t.Fatalf("%s accepted unsupported %s: %v", ref, platform, err)
					}
					continue
				}

				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(" asio cpp-httplib eigen expected nlohmann pybind11 queue taskflow websocketpp ", " "+ref.Name+" ") && len(selection.Modules) != 1 {
					t.Fatalf("header package %s must have an independent publication closure: %+v", ref, selection.Modules)
				}
				for _, effective := range selection.Modules {
					deps := []*moduleDefinition{}
					for _, dependency := range effective.Depends {
						deps = append(deps, byRef[dependency])
					}
					ctx := buildContext{SourceRoot: root, BuildRoot: t.TempDir(), SystemName: slot[0], Architecture: slot[1], Generator: "Unix Makefiles", CMakeCommand: "cmake", GitExecutable: "git"}
					if slot[0] == "Darwin" {
						ctx.CMakeDefaults = map[string]string{"CMAKE_OSX_ARCHITECTURES": "arm64", "CMAKE_OSX_DEPLOYMENT_TARGET": "15.0", "CMAKE_OSX_SYSROOT": "/fixture/SDK", "CMAKE_C_COMPILER": "/fixture/clang", "CMAKE_CXX_COMPILER": "/fixture/clang++"}
					}
					if _, err := renderModule(effective, ctx, deps); err != nil {
						t.Fatal(effective.Name, effective.Version, err)
					}
				}
			}
		})
	}
	names := []string{"flann", "freetype", "harfbuzz", "librealsense", "libusb", "lz4", "nlohmann", "onnxruntime", "opencv", "queue", "zlib"}
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
				byName[m.Name+"@"+m.Version] = m
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
			check(hb.Depends, []string{"freetype@2.13.2"})
			check(hb.Source.URL, []string{"https://github.com/harfbuzz/harfbuzz/archive/refs/tags/8.5.0.zip"})
			check(hb.Source.URLHash, "SHA256=734985456b60c198da41f4c85280963795b9baa6fb6f3b91c6b85026da837933")
			check(hb.Cache["HB_HAVE_FREETYPE"], true)
			check(hb.PkgconfigDir, "lib/pkgconfig")
			check(hb.Sentinel, "include/harfbuzz/hb.h")
			check(ft.ConfigDir, "lib/cmake/freetype")
			check(ft.PkgconfigDir, "lib/pkgconfig")
			check(cv.Version, "4.14.0")
			check(cv.Depends, []string{"freetype@2.13.2", "harfbuzz@8.5.0", "zlib@1.3.1"})
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
			check(flann.Depends, []string{"lz4@1.10.0"})
			check(flann.Version, "f9caaf609d8b")
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
	if _, err := loadDefinition(filepath.Join(root, "src/dependencies/onnxruntime/1.24.4/module.toml"), "Windows", "ARM64"); err == nil {
		t.Fatal("unsupported ONNX target accepted")
	}
}

func TestVersionedRealDeclarations(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]string{
		"asio": "8806a6803cde", "boost": "1.87.0", "clipper2": "1.5.4",
		"cpp-httplib": "88956ccad838", "eigen": "3.4.0", "expected": "1.3.1",
		"flann": "f9caaf609d8b", "freetype": "2.13.2", "gtest": "1.17.0", "harfbuzz": "8.5.0",
		"imgui": "065f9e02695c", "librealsense": "2.58.1", "libusb": "1.0.27", "libzmq": "4.3.5", "lz4": "1.10.0",
		"nativefiledialog-extended": "3cd252a8f7ca", "nlohmann": "3.12.0", "oneTBB": "628c90df6867", "onnxruntime": "1.24.4", "opencv": "4.14.0",
		"pcl": "pcl-1.15.1", "portaudio": "v190700_20210406", "pybind11": "2.13.6", "queue": "45db9a8b4431",
		"sdl3": "3.2.30", "spdlog": "1.17.0", "taskflow": "83f90a279aaa", "websocketpp": "4dfe1be74e68", "yaml-cpp": "2decf96e915d", "zlib": "1.3.1",
	}
	for _, slot := range []struct{ system, arch string }{{"Windows", "x86_64"}, {"Linux", "x86_64"}, {"Linux", "aarch64"}} {
		t.Run(slot.system+"/"+slot.arch, func(t *testing.T) {
			modules, err := loadModuleClosure(root, nil, slot.system, slot.arch)
			if err != nil {
				t.Fatal(err)
			}
			if len(modules) != 32 {
				t.Fatalf("want 32 versions, got %d", len(modules))
			}
			byRef := map[string]*moduleDefinition{}
			for _, m := range modules {
				byRef[m.Name+"@"+m.Version] = m
			}
			for name, version := range versions {
				ref, err := resolveModuleRef(root, moduleRef{Name: name})
				if err != nil || ref.Version != version {
					t.Fatalf("%s latest: %v %v", name, ref, err)
				}
				if byRef[name+"@"+version] == nil {
					t.Fatal("missing recipe", name, version)
				}
			}
			if byRef["asio@1.24.0"] == nil {
				t.Fatal("missing explicit Asio 1.24.0")
			}
			if byRef["eigen@5.0.1"] == nil {
				t.Fatal("missing explicit Eigen 5.0.1")
			}
			pcl := byRef["pcl@pcl-1.15.1"]
			if !reflect.DeepEqual(pcl.Depends, []string{"lz4@1.10.0", "flann@f9caaf609d8b", "boost@1.87.0", "eigen@3.4.0", "zlib@1.3.1"}) {
				t.Fatal(pcl.Depends)
			}
			for _, m := range modules {
				deps := []*moduleDefinition{}
				for _, ref := range m.Depends {
					deps = append(deps, byRef[ref])
				}
				ctx := buildContext{SourceRoot: root, BuildRoot: t.TempDir(), SystemName: slot.system, Architecture: slot.arch, Generator: "Unix Makefiles", CMakeCommand: "cmake", GitExecutable: "git"}
				if _, err := renderModule(m, ctx, deps); err != nil {
					t.Error(m.Name, m.Version, err)
				}
			}
		})
	}
}
