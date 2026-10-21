from pathlib import Path
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "src/build_system"))
from definition import PatchDefinition, load_definition


SOURCE = '''
[source]
GIT_REPOSITORY = "https://example.invalid/library.git"
GIT_TAG = "0123456789012345678901234567890123456789"
'''


class DefinitionTests(unittest.TestCase):
    def test_resource_patch_and_cache_reference_validation(self):
        (self.path.parent / "change.patch").write_text("patch input")
        body = SOURCE + '''[resources.extra]
GIT_REPOSITORY="unused"
GIT_TAG="fixed"
[cmake.cache]
EXTRA="<RESOURCE_SOURCE_DIR:extra>/modules"
MANY=["<RESOURCE_SOURCE_DIR:extra>", "suffix"]
[[patches]]
file="change.patch"
resource="extra"
'''
        module = self.load(body)
        self.assertEqual(module.patches[0].file, self.path.parent / "change.patch")
        self.assertEqual(module.patches[0].resource, "extra")
        for changed, field in ((body.replace('resource="extra"', 'resource="missing"'), "patches.resource"),
                               (body.replace('file="change.patch"', 'file="change.patch"\nunknown=true'), "unknown"),
                               (body.replace('<RESOURCE_SOURCE_DIR:extra>/modules', '<RESOURCE_SOURCE_DIR:missing>/modules'), "EXTRA"),
                               (body.replace('["<RESOURCE_SOURCE_DIR:extra>"', '["<RESOURCE_SOURCE_DIR:missing>"'), "MANY")):
            with self.subTest(field=field), self.assertRaises(ValueError) as error:
                self.load(changed)
            self.assertIn(field, str(error.exception))
            self.assertIn(str(self.path), str(error.exception))
            if field != "unknown":
                self.assertIn("missing", str(error.exception))

    def test_dependency_metadata_and_platform_overrides(self):
        body = '''depends=["lz4","lz4"]
config_dir="lib/cmake/demo"
pkgconfig_dir="lib/pkgconfig"
package_name="Demo"
sentinel="include/demo.h"
''' + SOURCE + '''[platforms.windows]
config_dir="lib"
package_name="WinDemo"
sentinel="lib/DemoConfig.cmake"
[platforms.linux]
config_dir="lib/cmake/demo"
'''
        linux = self.load(body)
        self.assertEqual(linux.depends, ["lz4"])
        self.assertEqual((linux.config_dir, linux.pkgconfig_dir, linux.package_name),
                         ("lib/cmake/demo", "lib/pkgconfig", "Demo"))
        windows = self.load(body, "Windows")
        self.assertEqual((windows.config_dir, windows.package_name, windows.sentinel),
                         ("lib", "WinDemo", "lib/DemoConfig.cmake"))
        cases = [('depends=[1]\n', "depends"), ('depends=[""]\n', "depends"),
                 ('depends=["../escape"]\n', "depends"),
                 ('config_dir="/absolute"\n', "config_dir"),
                 ('pkgconfig_dir="C:/absolute"\n', "pkgconfig_dir"),
                 ('package_name="Demo"\n', "package_name")]
        for prefix, field in cases:
            with self.subTest(prefix=prefix), self.assertRaises(ValueError) as error:
                self.load(prefix + SOURCE)
            self.assertIn(str(self.path), str(error.exception))
            self.assertIn(field, str(error.exception))
        with self.assertRaisesRegex(ValueError, "platforms.windows.config_dir"):
            self.load(SOURCE + '[platforms.windows]\nconfig_dir="../escape"\n')
        with self.assertRaisesRegex(ValueError, "package_name"):
            self.load(SOURCE + '[platforms.windows]\npackage_name="Demo"\n')

    def setUp(self):
        directory = ROOT / "build/tests"
        directory.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(prefix="definition ", dir=directory)
        self.addCleanup(temporary.cleanup)
        self.path = Path(temporary.name) / "demo/module.toml"
        self.path.parent.mkdir()

    def load(self, body, system="Linux", header='name = "demo"\ntype = "cmake"\n', architecture=""):
        self.path.write_text(header + body, encoding="utf-8")
        return load_definition(self.path, system, architecture)

    def test_selects_whole_platform_and_architecture_sources(self):
        body = SOURCE + '''
[resources.extra]
GIT_REPOSITORY="common"
GIT_TAG="common"
[platforms.linux.source]
URL=["https://example.invalid/linux.tgz"]
URL_HASH="SHA256=''' + "a" * 64 + '''"
[platforms.linux.resources.extra]
URL=["https://example.invalid/extra.tgz"]
URL_HASH="SHA256=''' + "b" * 64 + '''"
[platforms.linux.architectures.aarch64.source]
GIT_REPOSITORY="arm"
GIT_TAG="arm"
[platforms.linux.architectures.aarch64.resources.extra]
GIT_REPOSITORY="arm-extra"
GIT_TAG="arm-extra"
'''
        linux = self.load(body, architecture="AMD64")
        self.assertEqual(linux.source, {"URL": ["https://example.invalid/linux.tgz"],
                                      "URL_HASH": "SHA256=" + "a" * 64})
        self.assertEqual(linux.resources["extra"]["URL"], ["https://example.invalid/extra.tgz"])
        for arch in ("ARM64", "arm64", "aarch64"):
            arm = self.load(body, architecture=arch)
            self.assertEqual(arm.source, {"GIT_REPOSITORY": "arm", "GIT_TAG": "arm"})
            self.assertEqual(arm.resources["extra"], {"GIT_REPOSITORY": "arm-extra", "GIT_TAG": "arm-extra"})
        windows = self.load(body, system="Windows", architecture="x64")
        self.assertEqual(windows.source["GIT_REPOSITORY"], "https://example.invalid/library.git")

    def test_architecture_only_source_is_required_for_current_target(self):
        body = '''[platforms.windows.architectures.x86_64.source]
GIT_REPOSITORY="windows"
GIT_TAG="fixed"
'''
        for arch in ("x64", "AMD64", "x86_64"):
            self.assertEqual(self.load(body, system="Windows", architecture=arch).source["GIT_REPOSITORY"], "windows")
        for system, arch in (("Windows", "ARM64"), ("Linux", "x86_64"), ("Windows", "")):
            with self.subTest(system=system, arch=arch), self.assertRaisesRegex(ValueError, "source"):
                self.load(body, system=system, architecture=arch)
        with self.assertRaisesRegex(ValueError, "GIT_TAG"):
            self.load(SOURCE + '[platforms.windows.architectures.x86_64.source]\nGIT_REPOSITORY="bad"\n')
        for extra in ('[platforms.linux.architectures.mips]\n',
                      '[platforms.linux.architectures.aarch64]\ncache={}\n'):
            with self.assertRaises(ValueError):
                self.load(SOURCE + extra)

    def test_validates_sentinel_source_subdir_and_copy_directories(self):
        module = self.load('sentinel="include/demo.h"\n' + SOURCE + '[cmake]\nSOURCE_SUBDIR="build/cmake"\n')
        self.assertEqual(module.sentinel, "include/demo.h")
        self.assertEqual(module.source_subdir, "build/cmake")
        copy = self.load(SOURCE + '[[copy]]\ndirectories=["."]\ndestination="."\n',
                         header='name="demo"\ntype="copy"\n')
        self.assertEqual(copy.copies[0]["directories"], ["."])
        for path in ('../escape', '/absolute', 'C:drive', 'a/../../b', '\\root'):
            for field in ('sentinel', 'SOURCE_SUBDIR'):
                body = f"sentinel='{path}'\n" + SOURCE if field == 'sentinel' else SOURCE + f"[cmake]\nSOURCE_SUBDIR='{path}'\n"
                with self.subTest(field=field, path=path), self.assertRaisesRegex(ValueError, field):
                    self.load(body)
        for mapping in ('files=["x"]\ndirectories=["."]', 'directories=[]',
                        'directories=["../escape"]', 'directories=["C:drive"]'):
            with self.assertRaisesRegex(ValueError, "copy"):
                self.load(SOURCE + '[[copy]]\n' + mapping + '\ndestination="."\n',
                          header='name="demo"\ntype="copy"\n')
        with self.assertRaisesRegex(ValueError, "SOURCE_SUBDIR"):
            self.load(SOURCE + '[cmake]\nSOURCE_SUBDIR="sub"\n[[copy]]\nfiles=["x"]\ndestination="."\n',
                      header='name="demo"\ntype="copy"\n')

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
        self.assertEqual(module.patches, [PatchDefinition(patch)])
        self.assertEqual(module.steps[0]["DEPENDS"], [script.as_posix()])
        self.assertEqual(module.steps[0]["WORKING_DIRECTORY"], "<SOURCE_DIR>")
        patch.unlink()
        with self.assertRaisesRegex(ValueError, "change.patch"):
            load_definition(self.path, "Linux")


if __name__ == "__main__":
    unittest.main()
