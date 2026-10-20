from pathlib import Path
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "src/build_system"))
from definition import load_definition


SOURCE = '''
[source]
GIT_REPOSITORY = "https://example.invalid/library.git"
GIT_TAG = "0123456789012345678901234567890123456789"
'''


class DefinitionTests(unittest.TestCase):
    def setUp(self):
        directory = ROOT / "build/tests"
        directory.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(prefix="definition ", dir=directory)
        self.addCleanup(temporary.cleanup)
        self.path = Path(temporary.name) / "demo/module.toml"
        self.path.parent.mkdir()

    def load(self, body, system="Linux", header='name = "demo"\ntype = "cmake"\n'):
        self.path.write_text(header + body, encoding="utf-8")
        return load_definition(self.path, system)

    def test_accepts_archive_sources_and_resources(self):
        source = '''URL = ["https://one.invalid/a.tar.xz", "https://two.invalid/a.tar.xz"]
URL_HASH = "SHA256=''' + "a" * 64 + '''"
'''
        module = self.load('[source]\n' + source + '[resources.extra]\n' + source +
                           'DOWNLOAD_NAME="extra.tar.xz"\n')
        self.assertEqual(module.source, {
            "URL": ["https://one.invalid/a.tar.xz", "https://two.invalid/a.tar.xz"],
            "URL_HASH": "SHA256=" + "a" * 64})
        self.assertEqual(module.resources["extra"], {**module.source, "DOWNLOAD_NAME": "extra.tar.xz"})
        single = self.load('[source]\nURL=["https://one.invalid/a.tar.xz"]\n'
                           'URL_HASH="SHA256=' + "A" * 64 + '"\n')
        self.assertEqual(single.source["URL"], ["https://one.invalid/a.tar.xz"])
        self.assertEqual(single.source["URL_HASH"], "SHA256=" + "A" * 64)
        self.assertNotIn("DOWNLOAD_NAME", single.source)

    def test_rejects_invalid_archive_sources(self):
        valid = 'URL=["https://one.invalid/a.tar.xz"]\nURL_HASH="SHA256=' + "a" * 64 + '"\n'
        cases = [
            (valid.replace('URL=["https://one.invalid/a.tar.xz"]', 'URL="https://one.invalid/a.tar.xz"'), "URL"),
            (valid.replace('["https://one.invalid/a.tar.xz"]', '[]'), "URL"),
            (valid.replace('["https://one.invalid/a.tar.xz"]', '[""]'), "URL"),
            (valid.replace('["https://one.invalid/a.tar.xz"]', '[1]'), "URL"),
            (valid.splitlines()[0] + '\n', "URL_HASH"),
            (valid.splitlines()[1] + '\n', "URL"),
            (valid.replace('"SHA256=' + "a" * 64 + '"', '1'), "URL_HASH"),
            (valid.replace('SHA256=', 'MD5='), "URL_HASH"),
            (valid.replace("a" * 64, "a" * 63), "URL_HASH"),
            (valid.replace("a" * 64, "g" * 64), "URL_HASH"),
            (valid + 'GIT_REPOSITORY="repo"\n', "source"),
            (valid + 'GIT_TAG="fixed"\n', "source"),
            (valid + 'UNKNOWN="value"\n', "UNKNOWN"),
            ('GIT_REPOSITORY="repo"\n', "GIT_TAG"),
            ('GIT_TAG="fixed"\n', "GIT_REPOSITORY"),
        ]
        for filename in ('""', '1', '"dir/file.tar.xz"', "'dir\\file.tar.xz'", '"."', '".."', '"C:file.tar.xz"'):
            cases.append((valid + f'DOWNLOAD_NAME={filename}\n', "DOWNLOAD_NAME"))
        for table in ("source", "resources.extra"):
            for body, field in cases:
                with self.subTest(table=table, body=body):
                    declaration = f'[{table}]\n' + body
                    if table.startswith("resources"):
                        declaration += '\n[[copy]]\nresource="extra"\nfiles=["x.h"]\ndestination="include"\n'
                    with self.assertRaises(ValueError) as error:
                        self.load(declaration, header='name="demo"\ntype="copy"\n')
                    self.assertIn(str(self.path), str(error.exception))
                    self.assertIn(field, str(error.exception))

    def test_selects_platform_cache_and_steps(self):
        body = SOURCE + '''
[cmake.cache]
A = "common"
B = "common"
[[steps]]
name = "common_step"
DEPENDEES = ["patch"]
DEPENDERS = ["configure"]
COMMAND = ["cmake", "-E", "echo", "common"]
[platforms.linux.cmake.cache]
A = "linux"
C = true
[[platforms.linux.steps]]
name = "linux_step"
DEPENDEES = ["build"]
DEPENDERS = ["install"]
COMMAND = [["cmake", "-E", "echo", "one"], ["cmake", "-E", "echo", "two"]]
[[platforms.windows.steps]]
name = "windows_step"
DEPENDEES = ["patch"]
DEPENDERS = ["configure"]
COMMAND = ["cmake", "-E", "echo", "windows"]
'''
        module = self.load(body)
        self.assertEqual(module.cache, {"A": "linux", "B": "common", "C": True})
        self.assertEqual([s["name"] for s in module.steps], ["common_step", "linux_step"])
        self.assertEqual(module.steps[1]["COMMAND"][1][-1], "two")
        windows = self.load(body, "Windows")
        self.assertEqual(windows.cache, {"A": "common", "B": "common"})
        self.assertEqual([s["name"] for s in windows.steps], ["common_step", "windows_step"])

    def test_accepts_copy_without_primary_source(self):
        module = self.load('''
[resources.first]
GIT_REPOSITORY = "file:///not-downloaded/first"
GIT_TAG = "1111111111111111111111111111111111111111"
[resources.second]
GIT_REPOSITORY = "file:///not-downloaded/second"
GIT_TAG = "2222222222222222222222222222222222222222"
[[copy]]
resource = "first"
files = ["first.h"]
destination = "include/demo"
[[copy]]
resource = "second"
files = ["second.h"]
destination = "include/demo"
''', header='name = "demo"\ntype = "copy"\n')
        self.assertIsNone(module.source)
        self.assertIsNone(module.version)
        self.assertEqual(len(module.resources), 2)
        self.assertEqual(module.copies[1]["resource"], "second")

    def test_rejects_unknown_fields_and_wrong_shapes(self):
        cases = [
            ('dependencies = []\n' + SOURCE, "dependencies"),
            (SOURCE + '[platforms.macos]\n', "macos"),
            (SOURCE + '[platforms.linux]\nfilter = "x"\n', "filter"),
            (SOURCE + '[cmake]\ncach = {}\n', "cach"),
            (SOURCE + '[cmake.cache]\nA = 1.5\n', "A"),
            (SOURCE + '[[steps]]\nname="s"\nCOMMAND="echo no"\n', "COMMAND"),
            (SOURCE + '[[steps]]\nname="s"\nCOMMAND=["echo"]\nBEFORE="build"\n', "BEFORE"),
        ]
        for body, field in cases:
            with self.subTest(field=field):
                with self.assertRaises(ValueError) as error:
                    self.load(body)
                self.assertIn(str(self.path), str(error.exception))
                self.assertIn(field, str(error.exception))
        with self.assertRaisesRegex(ValueError, "name"):
            self.load(SOURCE, header='name="other"\ntype="cmake"\n')

    def test_rejects_copy_without_an_available_source(self):
        for reference in ('resource="missing"\n', ''):
            with self.subTest(reference=reference), self.assertRaisesRegex(ValueError, "copy"):
                self.load('[[copy]]\n' + reference + 'files=["x.h"]\ndestination="include"\n',
                          header='name="demo"\ntype="copy"\n')

    def test_resolves_local_execution_inputs(self):
        patch = self.path.parent / "change.patch"
        patch.write_text("patch input", encoding="utf-8")
        script = self.path.parent / "change.cmake"
        script.write_text("# script input", encoding="utf-8")
        module = self.load(SOURCE + '''
[[patches]]
file = "change.patch"
[[steps]]
name = "change"
DEPENDEES = ["build"]
DEPENDERS = ["install"]
DEPENDS = ["change.cmake"]
WORKING_DIRECTORY = "<SOURCE_DIR>"
COMMAND = ["cmake", "-P", "change.cmake"]
''')
        self.assertEqual(module.patches, [patch])
        self.assertEqual(module.steps[0]["DEPENDS"], [script.as_posix()])
        self.assertEqual(module.steps[0]["WORKING_DIRECTORY"], "<SOURCE_DIR>")
        patch.unlink()
        with self.assertRaisesRegex(ValueError, "change.patch"):
            load_definition(self.path, "Linux")


if __name__ == "__main__":
    unittest.main()
