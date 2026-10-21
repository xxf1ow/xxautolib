import unittest

from test_cmake import ROOT
from definition import load_definition


class ModuleTests(unittest.TestCase):
    def test_harfbuzz_reference_archive_and_freetype(self):
        module = load_definition(ROOT / "src/dependencies/harfbuzz/module.toml", "Windows", "x64")
        self.assertEqual(module.version, "8.5.0")
        self.assertEqual(module.depends, ["freetype"])
        self.assertEqual(module.source["URL"],
                         ["https://github.com/harfbuzz/harfbuzz/archive/refs/tags/8.5.0.zip"])
        self.assertEqual(module.source["URL_HASH"],
                         "SHA256=734985456b60c198da41f4c85280963795b9baa6fb6f3b91c6b85026da837933")
        self.assertIs(module.cache["HB_HAVE_FREETYPE"], True)
        self.assertEqual(module.pkgconfig_dir, "lib/pkgconfig")
        self.assertEqual(module.sentinel, "include/harfbuzz/hb.h")
        freetype = load_definition(ROOT / "src/dependencies/freetype/module.toml", "Windows", "x64")
        self.assertEqual((freetype.config_dir, freetype.pkgconfig_dir),
                         ("lib/cmake/freetype", "lib/pkgconfig"))

    def test_opencv_414_options_and_platform_layout(self):
        disabled = """
            BUILD_DOCS BUILD_EXAMPLES BUILD_PACKAGE BUILD_TESTS BUILD_PERF_TESTS
            BUILD_opencv_apps BUILD_JAVA BUILD_opencv_java_bindings_generator
            BUILD_opencv_js BUILD_opencv_js_bindings_generator BUILD_opencv_python2
            BUILD_opencv_python3 BUILD_opencv_python_bindings_generator BUILD_opencv_python_tests
            BUILD_opencv_objc_bindings_generator BUILD_opencv_ts BUILD_opencv_ml BUILD_opencv_dnn
            BUILD_opencv_objdetect BUILD_opencv_gapi BUILD_opencv_bgsegm BUILD_opencv_optflow
            BUILD_opencv_stereo BUILD_opencv_superres BUILD_opencv_tracking BUILD_opencv_videostab
            BUILD_opencv_datasets BUILD_opencv_phase_unwrapping BUILD_opencv_bioinspired
            BUILD_opencv_hfs BUILD_opencv_saliency BUILD_opencv_rapid BUILD_opencv_rgbd
            BUILD_opencv_surface_matching BUILD_opencv_structured_light BUILD_opencv_ccalib
            BUILD_opencv_signal WITH_GSTREAMER WITH_PROTOBUF
        """.split()
        for system, architecture, config, sentinel in (
                ("Windows", "x64", ".", "OpenCVConfig.cmake"),
                ("Linux", "aarch64", "lib/cmake/opencv4", "lib/cmake/opencv4/OpenCVConfig.cmake")):
            with self.subTest(system=system):
                module = load_definition(ROOT / "src/dependencies/opencv/module.toml", system, architecture)
                self.assertEqual(module.version, "4.14.0")
                self.assertEqual(module.depends, ["freetype", "harfbuzz"])
                self.assertEqual((module.config_dir, module.package_name, module.sentinel),
                                 (config, "OpenCV", sentinel))
                for name, source in (("opencv", module.source),
                                     ("opencv_contrib", module.resources["opencv_contrib"])):
                    self.assertEqual(source["URL"],
                                     [f"https://github.com/opencv/{name}/archive/refs/tags/4.14.0.zip"])
                    self.assertEqual(source["DOWNLOAD_NAME"], f"{name}-4.14.0.zip")
                    self.assertRegex(source["URL_HASH"], r"^SHA256=[0-9a-f]{64}$")
                for key in disabled:
                    self.assertIs(module.cache[key], False, key)
                for key in ("BUILD_opencv_world", "BUILD_opencv_freetype", "OPENCV_ENABLE_NONFREE", "WITH_FFMPEG"):
                    self.assertIs(module.cache[key], True, key)
                for key in ("WITH_KLEIDICV", "WITH_FASTCV", "BUILD_LIST", "BUILD_opencv_calib3d",
                            "BUILD_opencv_features2d", "BUILD_opencv_flann", "BUILD_opencv_ximgproc", "BUILD_opencv_video"):
                    self.assertNotIn(key, module.cache)
                self.assertEqual(module.cache["OPENCV_EXTRA_MODULES_PATH"],
                                 "<RESOURCE_SOURCE_DIR:opencv_contrib>/modules")
                self.assertEqual(module.patches[0].resource, "opencv_contrib")

    def test_flann_reuses_lz4_and_reference_options(self):
        module = load_definition(ROOT / "src/dependencies/flann/module.toml", "Windows", "x64")
        commit = "f9caaf609d8b8cb2b7104a85cf59eb92c275a25d"
        self.assertEqual(module.depends, ["lz4"])
        self.assertEqual(module.version, commit)
        self.assertEqual(module.source["GIT_TAG"], commit)
        self.assertEqual((module.config_dir, module.pkgconfig_dir, module.sentinel),
                         ("lib/cmake/flann", "lib/pkgconfig", "include/flann/flann.hpp"))
        for key in ("BUILD_C_BINDINGS", "USE_OPENMP"):
            self.assertIs(module.cache[key], True)
        for key in ("BUILD_PYTHON_BINDINGS", "BUILD_MATLAB_BINDINGS", "BUILD_CUDA_LIB",
                    "BUILD_EXAMPLES", "BUILD_TESTS", "BUILD_DOC", "USE_MPI"):
            self.assertIs(module.cache[key], False)
        self.assertNotIn("BUILD_SHARED_LIBS", module.cache)
        lz4 = load_definition(ROOT / "src/dependencies/lz4/module.toml", "Windows", "x64")
        self.assertEqual((lz4.config_dir, lz4.pkgconfig_dir), ("lib/cmake/lz4", "lib/pkgconfig"))

    def test_onnxruntime_selects_the_target_archive(self):
        path = ROOT / "src/dependencies/onnxruntime/module.toml"
        for system, architecture, filename in (
            ("Windows", "x64", "onnxruntime-win-x64-1.24.4.zip"),
            ("Linux", "x86_64", "onnxruntime-linux-x64-1.24.4.tgz"),
            ("Linux", "aarch64", "onnxruntime-linux-aarch64-1.24.4.tgz"),
        ):
            with self.subTest(system=system, architecture=architecture):
                module = load_definition(path, system, architecture)
                self.assertTrue(module.source["URL"][0].endswith("/" + filename))
                self.assertEqual(module.source["DOWNLOAD_NAME"], filename)
        with self.assertRaisesRegex(ValueError, "source"):
            load_definition(path, "Windows", "ARM64")


if __name__ == "__main__":
    unittest.main()
