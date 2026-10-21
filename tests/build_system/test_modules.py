import unittest

from test_cmake import ROOT
from definition import load_definition


class ModuleTests(unittest.TestCase):
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
