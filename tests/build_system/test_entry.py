import os
import platform
import shutil
import time
import unittest

from test_cmake import PipelineCase, ROOT


class EntryTests(PipelineCase):
    def test_dependency_toml_reconfigures_leaf_build(self):
        repo = self.repository("dep", {"CMakeLists.txt": '''cmake_minimum_required(VERSION 3.20)
project(child NONE)
file(WRITE "${CMAKE_BINARY_DIR}/value.txt" "${VALUE}")
install(FILES "${CMAKE_BINARY_DIR}/value.txt" DESTINATION include)
'''})
        dependency = self.definition('[source]\n' + repo + '[cmake.cache]\nVALUE="one"\n', name="dep")
        leaf = self.copy_module("leaf")
        leaf.path.write_text(leaf.path.read_text().replace('type="copy"', 'type="copy"\ndepends=["dep"]'))
        self.copy_module("unused")
        self.configure("-DBUILD_LIST=leaf")
        self.build()
        installed = self.source / "3rdparty/dep/include/value.txt"
        self.assertEqual(installed.read_text(), "one")
        time.sleep(1.1)
        dependency.path.write_text(dependency.path.read_text().replace('VALUE="one"', 'VALUE="two"'))
        result = self.build()
        self.assertIn("Building Summary", result.stdout)
        self.assertEqual(installed.read_text(), "two")
        self.assertFalse((self.source / "git/unused").exists())

    def test_dependency_preserves_root_project_include(self):
        self.copy_module("dep")
        repo = self.repository("leaf", {"CMakeLists.txt": '''cmake_minimum_required(VERSION 3.20)
project(child NONE)
if(NOT CALLER_MARKER)
message(FATAL_ERROR "caller project include lost")
endif()
file(WRITE "${CMAKE_BINARY_DIR}/marker" "${CALLER_MARKER}")
install(FILES "${CMAKE_BINARY_DIR}/marker" DESTINATION .)
'''})
        self.definition('depends=["dep"]\n[source]\n' + repo, name="leaf")
        caller = self.source / "caller.cmake"
        caller.write_text('set(CALLER_MARKER "caller preserved")\n')
        self.configure("-DBUILD_LIST=leaf", f"-DCMAKE_PROJECT_INCLUDE={caller.as_posix()}")
        self.build()
        self.assertEqual((self.source / "3rdparty/leaf/marker").read_text(), "caller preserved")

    def setUp(self):
        super().setUp()
        for name in ("CMakeLists.txt", "pyproject.toml", "uv.lock", ".python-version"):
            shutil.copy2(ROOT / name, self.source / name)
        shutil.copytree(ROOT / "src/build_system", self.source / "src/build_system")
        self.environment = {**os.environ, "UV_OFFLINE": "true"}
        self.environment.pop("CMAKE_POLICY_VERSION_MINIMUM", None)

    def configure(self, *args, success=True):
        return self.run_command("cmake", "-S", self.source, "-B", self.context.build_root,
                                "-G", self.context.generator, *args,
                                env=self.environment, success=success)

    def copy_module(self, name):
        repo = self.repository(name, {"x.h": name})
        return self.definition('[source]\n' + repo + '\n[[copy]]\nfiles=["x.h"]\n'
                               'destination="include"\n', kind="copy", name=name)

    def test_root_entry_selects_target_architecture(self):
        x64 = self.repository("x64", {"x.h": "x86_64"})
        arm = self.repository("arm", {"x.h": "aarch64"})
        name = "windows" if os.name == "nt" else "linux"
        path = self.source / "src/dependencies/demo/module.toml"
        path.parent.mkdir(parents=True)
        path.write_text('name="demo"\ntype="copy"\n[[copy]]\nfiles=["x.h"]\ndestination="include"\n'
                        f'[platforms.{name}.architectures.x86_64.source]\n' + x64 +
                        f'[platforms.{name}.architectures.aarch64.source]\n' + arm)
        arguments = ["-DBUILD_LIST=demo"]
        if os.name == "nt":
            arguments += ["-A", "x64", "-DCMAKE_SYSTEM_PROCESSOR=ARM64"]
        self.configure(*arguments)
        self.build()
        expected = "aarch64" if os.name != "nt" and platform.machine().lower() in ("aarch64", "arm64") else "x86_64"
        self.assertEqual((self.source / "3rdparty/demo/include/x.h").read_text(), expected)

    def test_plain_entry_propagates_cmake_defaults(self):
        for name, minimum, policy in (("legacy", "3.0", "CMP0054"),
                                       ("modern", "3.20", "CMP0077")):
            repo = self.repository(name, {"CMakeLists.txt": f'''cmake_minimum_required(VERSION {minimum})
project(child CXX)
add_library(child STATIC child.cpp)
cmake_policy(GET {policy} behavior)
get_target_property(pic child POSITION_INDEPENDENT_CODE)
file(WRITE "${{CMAKE_BINARY_DIR}}/settings.txt"
    "${{CMAKE_POLICY_VERSION_MINIMUM}}|${{CMAKE_CXX_STANDARD}}|${{pic}}|${{behavior}}")
install(TARGETS child ARCHIVE DESTINATION lib)
install(FILES "${{CMAKE_BINARY_DIR}}/settings.txt" DESTINATION include)
''', "child.cpp": '''#include <optional>
int value() { return std::optional<int>(17).value(); }
'''})
            self.definition('[source]\n' + repo, name=name)
        self.configure()
        parent_cache = (self.context.build_root / "CMakeCache.txt").read_text(encoding="utf-8")
        for key in ("CMAKE_POLICY_VERSION_MINIMUM", "CMAKE_POSITION_INDEPENDENT_CODE", "CMAKE_CXX_STANDARD"):
            self.assertFalse(any(line.startswith(key + ":") for line in parent_cache.splitlines()), key)
        self.build()
        cmake_major = int(self.run_command("cmake", "--version").stdout.split()[2].split(".")[0])
        for name in ("legacy", "modern"):
            installed = self.source / f"3rdparty/{name}"
            # policy 下限变量自 CMake 4.0 生效；3.x 保留旧声明的 policy 状态。
            behavior = "" if name == "legacy" and cmake_major < 4 else "NEW"
            self.assertEqual((installed / "include/settings.txt").read_text(), f"3.5|17|ON|{behavior}")
            self.assertTrue(any((installed / "lib").iterdir()))

    def test_common_cache_and_module_overrides_reach_children(self):
        repo = self.repository("cache", {"CMakeLists.txt": '''cmake_minimum_required(VERSION 3.20)
project(child NONE)
file(WRITE "${CMAKE_BINARY_DIR}/settings.txt"
    "${CMAKE_POLICY_VERSION_MINIMUM}|${CMAKE_CXX_STANDARD}|${CMAKE_POSITION_INDEPENDENT_CODE}")
install(FILES "${CMAKE_BINARY_DIR}/settings.txt" DESTINATION include)
'''})
        self.definition('[source]\n' + repo, name="common")
        self.definition('[source]\n' + repo + '''[cmake.cache]
CMAKE_POLICY_VERSION_MINIMUM="3.10"
CMAKE_CXX_STANDARD=17
CMAKE_POSITION_INDEPENDENT_CODE=true
''', name="specific")
        self.configure("-DCMAKE_POLICY_VERSION_MINIMUM=3.6",
                       "-DCMAKE_CXX_STANDARD=20", "-DCMAKE_POSITION_INDEPENDENT_CODE=OFF")
        self.build()
        common = self.source / "3rdparty/common/include/settings.txt"
        specific = self.source / "3rdparty/specific/include/settings.txt"
        self.assertEqual(common.read_text(), "3.6|20|OFF")
        self.assertEqual(specific.read_text(), "3.10|17|ON")
        self.configure("-DCMAKE_CXX_STANDARD=23")
        self.build()
        self.assertEqual(common.read_text(), "3.6|23|OFF")
        self.assertEqual(specific.read_text(), "3.10|17|ON")
        # CMake 安装按秒级时间戳和大小判断文件是否更新。
        time.sleep(1.1)
        self.configure("-UCMAKE_POLICY_VERSION_MINIMUM", "-UCMAKE_CXX_STANDARD",
                       "-UCMAKE_POSITION_INDEPENDENT_CODE")
        self.build()
        self.assertEqual(common.read_text(), "3.5|17|ON")
        self.assertEqual(specific.read_text(), "3.10|17|ON")

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
        self.assertFalse((self.source / "git/second/.git").exists())
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
        # 快速 fixture 修改跨过构建工具的时间戳分辨率，不把输入设到未来。
        time.sleep(1.1)
        module.path.write_text(module.path.read_text().replace('VALUE="one"', 'VALUE="two"'))
        self.build()
        self.assertEqual(installed.read_text(), "two")
        generated = self.context.build_root / "generated/demo.cmake"
        before = generated.stat().st_mtime_ns
        helper = self.source / "src/build_system/cmake.py"
        time.sleep(1.1)
        helper.write_text(helper.read_text(encoding="utf-8").replace(
            'args = [_cache_argument(key, value) for key, value in cache.items()]',
            'cache["VALUE"] = "helper"\n        args = [_cache_argument(key, value) for key, value in cache.items()]'),
            encoding="utf-8")
        result = self.build()
        self.assertEqual(installed.read_text(), "helper", result.stdout + result.stderr)
        self.assertNotEqual(generated.stat().st_mtime_ns, before)
        before = generated.stat().st_mtime_ns
        time.sleep(1.1)
        helper.write_text(helper.read_text(encoding="utf-8") + "\n# no output change\n", encoding="utf-8")
        result = self.build()
        self.assertIn("Building Summary", result.stdout)
        self.assertEqual(generated.stat().st_mtime_ns, before)
        result = self.build()
        self.assertNotIn("Building Summary", result.stdout)


if __name__ == "__main__":
    unittest.main()
