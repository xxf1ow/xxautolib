import unittest

from test_cmake import ROOT
from definition import load_definition


class ModuleTests(unittest.TestCase):
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
