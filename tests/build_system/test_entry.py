import os
import shutil
import unittest

from test_cmake import PipelineCase, ROOT


class EntryTests(PipelineCase):
    def setUp(self):
        super().setUp()
        for name in ("CMakeLists.txt", "pyproject.toml", "uv.lock", ".python-version"):
            shutil.copy2(ROOT / name, self.source / name)
        shutil.copytree(ROOT / "src/build_system", self.source / "src/build_system")
        self.environment = {**os.environ, "UV_OFFLINE": "true"}

    def configure(self, *args, success=True):
        return self.run_command("cmake", "-S", self.source, "-B", self.context.build_root,
                                "-G", self.context.generator, *args,
                                env=self.environment, success=success)

    def copy_module(self, name):
        repo = self.repository(name, {"x.h": name})
        return self.definition('[source]\n' + repo + '\n[[copy]]\nfiles=["x.h"]\n'
                               'destination="include"\n', kind="copy", name=name)

    def test_default_discovery_and_build_list_isolation(self):
        self.copy_module("first")
        self.copy_module("second")
        entry = self.source / "CMakeLists.txt"
        entry.write_text(entry.read_text(encoding="utf-8") + '\n'
                         'get_property(targets DIRECTORY PROPERTY BUILDSYSTEM_TARGETS)\n'
                         'file(WRITE "${CMAKE_BINARY_DIR}/targets.txt" "${targets}")\n', encoding="utf-8")
        self.configure()
        self.assertTrue((self.context.build_root / "generated/first.cmake").is_file())
        self.assertTrue((self.context.build_root / "generated/second.cmake").is_file())
        self.configure("-DBUILD_LIST= first,first , first")
        targets = (self.context.build_root / "targets.txt").read_text().split(";")
        self.assertEqual(targets.count("first"), 1)
        self.assertNotIn("second", targets)
        self.run_command("cmake", "--build", self.context.build_root, "--config", "Release")
        self.assertEqual((self.source / "3rdparty/first/include/x.h").read_text(), "first")
        self.assertFalse((self.context.build_root / "ep/second/src/.git").exists())
        # 初次默认 configure 会由 ExternalProject 创建空安装目录，不代表发生安装。
        self.assertFalse(any((self.source / "3rdparty/second").rglob("*")))
        result = self.configure("-DBUILD_LIST=missing", success=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("missing", result.stdout + result.stderr)

    def test_generation_failure_never_loads_stale_output(self):
        module = self.copy_module("demo")
        self.configure()
        generated = self.context.build_root / "generated/demo.cmake"
        generated.write_text('message(FATAL_ERROR "STALE_OUTPUT_LOADED")\n')
        module.path.write_text('name = [ broken TOML\n')
        result = self.configure(success=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("module.toml", result.stdout + result.stderr)
        self.assertNotIn("STALE_OUTPUT_LOADED", result.stdout + result.stderr)

    def test_build_reconfigures_after_toml_or_helper_change(self):
        repo = self.repository("child", {"CMakeLists.txt": '''cmake_minimum_required(VERSION 3.20)
project(child NONE)
file(WRITE "${CMAKE_BINARY_DIR}/value.txt" "${VALUE}")
install(FILES "${CMAKE_BINARY_DIR}/value.txt" DESTINATION include)
'''})
        module = self.definition('[source]\n' + repo + '[cmake.cache]\nVALUE="one"\n')
        self.configure()
        self.build()
        installed = self.source / "3rdparty/demo/include/value.txt"
        self.assertEqual(installed.read_text(), "one")
        module.path.write_text(module.path.read_text().replace('VALUE="one"', 'VALUE="two"'))
        self.build()
        self.assertEqual(installed.read_text(), "two")
        generated = self.context.build_root / "generated/demo.cmake"
        before = generated.stat().st_mtime_ns
        helper = self.source / "src/build_system/cmake.py"
        helper.write_text(helper.read_text(encoding="utf-8").replace(
            'args = [_cache_argument(key, value) for key, value in cache.items()]',
            'cache["VALUE"] = "helper"\n        args = [_cache_argument(key, value) for key, value in cache.items()]'),
            encoding="utf-8")
        self.build()
        self.assertEqual(installed.read_text(), "helper")
        self.assertNotEqual(generated.stat().st_mtime_ns, before)
        before = generated.stat().st_mtime_ns
        helper.write_text(helper.read_text(encoding="utf-8") + "\n# no output change\n", encoding="utf-8")
        result = self.build()
        self.assertIn("Building Summary", result.stdout)
        self.assertEqual(generated.stat().st_mtime_ns, before)
        result = self.build()
        self.assertNotIn("Building Summary", result.stdout)


if __name__ == "__main__":
    unittest.main()
