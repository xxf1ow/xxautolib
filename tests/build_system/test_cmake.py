from dataclasses import replace
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "src/build_system"))
from definition import load_definition
from cmake import BuildContext, cmake_argument, render_module


class PipelineCase(unittest.TestCase):
    def setUp(self):
        directory = ROOT / "build/tests"
        directory.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(prefix="pipeline ", dir=directory)
        self.addCleanup(temporary.cleanup)
        self.source = Path(temporary.name)
        self.context = BuildContext(
            source_root=self.source, build_root=self.source / "build",
            system_name="Windows" if os.name == "nt" else "Linux",
            generator="Visual Studio 17 2022" if os.name == "nt" else "Unix Makefiles",
            platform="x64" if os.name == "nt" else "",
            toolset="v143,host=x64" if os.name == "nt" else "",
            instance="", multi_config=os.name == "nt", msvc_runtime_library=None,
            cmake_command=Path(shutil.which("cmake")), git_executable=Path(shutil.which("git")))

    def run_command(self, *args, success=True, **kwargs):
        result = subprocess.run([str(arg) for arg in args], capture_output=True,
                                encoding="utf-8", errors="replace", **kwargs)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def repository(self, name, files):
        path = self.source / "upstream" / name
        path.mkdir(parents=True)
        for filename, content in files.items():
            file = path / filename
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_text(content, encoding="utf-8")
        self.run_command("git", "init", str(path))
        self.run_command("git", "-C", path, "add", ".")
        self.run_command("git", "-C", path, "-c", "user.name=Fixture", "-c",
                         "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false",
                         "commit", "-m", "fixture")
        sha = self.run_command("git", "-C", path, "rev-parse", "HEAD").stdout.strip()
        return f'GIT_REPOSITORY = "{path.as_posix()}"\nGIT_TAG = "{sha}"\n'

    def definition(self, body, kind="cmake", name="demo"):
        path = self.source / "src/dependencies" / name / "module.toml"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(f'name="{name}"\ntype="{kind}"\n' + body, encoding="utf-8")
        return load_definition(path, self.context.system_name)

    def project(self, module, suffix="", context=None):
        generated = self.source / "module.cmake"
        generated.write_text(render_module(module, context or self.context), encoding="utf-8")
        (self.source / "CMakeLists.txt").write_text(
            'cmake_minimum_required(VERSION 3.20)\nproject(fixture NONE)\n'
            'include(ExternalProject)\ninclude(module.cmake)\n' + suffix, encoding="utf-8")
        args = ["cmake", "-S", self.source, "-B", self.context.build_root,
                "-G", self.context.generator]
        if self.context.platform:
            args += ["-A", self.context.platform, "-T", self.context.toolset]
        return self.run_command(*args)

    def build(self, success=True):
        return self.run_command("cmake", "--build", self.context.build_root,
                                "--config", "Release", success=success)


class CMakeTests(PipelineCase):
    def test_new_build_reuses_modified_primary_and_resource_git(self):
        main = self.repository("main", {"main.h": "original"})
        resource = self.repository("extra", {"extra.h": "extra"})
        module = self.definition('[source]\n' + main + '[resources.extra]\n' + resource + '''
[[copy]]
files=["main.h"]
destination="first"
[[copy]]
resource="extra"
files=["extra.h"]
destination="second"
''', kind="copy")
        self.project(module)
        self.build()
        primary = self.source / "git/demo"
        extra = self.source / "git/_resources/demo/extra"
        self.assertTrue((primary / ".git").exists())
        self.assertTrue((extra / ".git").exists())
        (primary / "main.h").write_text("manual edit")
        (extra / "extra.h").write_text("resource edit")
        self.context = replace(self.context, build_root=self.source / "fresh build")
        module.copies[0]["destination"] = "fresh"
        module.copies[1]["destination"] = "fresh"
        # 错误提交保证本地复用没有执行 checkout 或验证版本。
        module.source["GIT_TAG"] = "does-not-exist"
        module.resources["extra"]["GIT_TAG"] = "does-not-exist"
        self.project(module)
        self.build()
        self.assertEqual((self.source / "3rdparty/demo/fresh/main.h").read_text(), "manual edit")
        self.assertEqual((self.source / "3rdparty/demo/fresh/extra.h").read_text(), "resource edit")
        self.assertEqual((primary / "main.h").read_text(), "manual edit")

    def test_git_file_is_reused_without_git_commands(self):
        module = self.definition('[source]\nGIT_REPOSITORY="unavailable"\nGIT_TAG="unavailable"\n'
                                 '[[copy]]\nfiles=["x.h"]\ndestination="include"\n', kind="copy")
        local = self.source / "git/demo"
        local.mkdir(parents=True)
        (local / ".git").write_text("gitdir: unavailable")
        (local / "x.h").write_text("local worktree")
        self.project(module)
        self.build()
        self.assertEqual((self.source / "3rdparty/demo/include/x.h").read_text(), "local worktree")

    def test_nonempty_git_directories_are_preserved_including_hidden_files(self):
        for resource in (False, True):
            with self.subTest(resource=resource):
                name = "resource" if resource else "primary"
                body = '[resources.extra]\n' if resource else '[source]\n'
                body += 'GIT_REPOSITORY="unavailable"\nGIT_TAG="fixed"\n[[copy]]\n'
                body += 'resource="extra"\n' if resource else ''
                module = self.definition(body + 'files=["x.h"]\ndestination="include"\n', kind="copy", name=name)
                local = self.source / (f"git/_resources/{name}/extra" if resource else f"git/{name}")
                local.mkdir(parents=True)
                marker = local / ".manual"
                marker.write_text("preserve me")
                self.context = replace(self.context, build_root=self.source / f"build-{name}")
                generated = self.source / "module.cmake"
                generated.write_text(render_module(module, self.context), encoding="utf-8")
                (self.source / "CMakeLists.txt").write_text('cmake_minimum_required(VERSION 3.20)\n'
                    'project(fixture NONE)\ninclude(ExternalProject)\ninclude(module.cmake)\n')
                result = self.run_command("cmake", "-S", self.source, "-B", self.context.build_root, success=False)
                self.assertNotEqual(result.returncode, 0)
                diagnostic = " ".join((result.stdout + result.stderr).replace("\\", "/").split())
                self.assertIn(local.as_posix(), diagnostic)
                self.assertEqual(marker.read_text(), "preserve me")

    def test_source_subdirectory_installs_from_an_initially_empty_git_directory(self):
        repo = self.repository("subdir", {"public.h": "subdir header", "build/cmake/CMakeLists.txt": '''
cmake_minimum_required(VERSION 3.20)
project(child NONE)
install(FILES ../../public.h DESTINATION include)
'''})
        module = self.definition('[source]\n' + repo + '[cmake]\nSOURCE_SUBDIR="build/cmake"\n')
        (self.source / "git/demo").mkdir(parents=True)
        self.project(module)
        self.build()
        self.assertEqual((self.source / "3rdparty/demo/include/public.h").read_text(), "subdir header")
        self.assertTrue((self.source / "git/demo/.git").exists())

    def test_same_generated_cmake_checks_sentinel_before_all_module_work(self):
        repo = self.repository("sentinel", {"x.h": "installed"})
        module = self.definition('sentinel="include/x.h"\n[source]\n' + repo + '''
[resources.extra]
GIT_REPOSITORY="unavailable"
GIT_TAG="unavailable"
[[copy]]
files=["x.h"]
destination="include"
''', kind="copy")
        sentinel = self.source / "3rdparty/demo/include/x.h"
        sentinel.parent.mkdir(parents=True)
        sentinel.write_text("cached header")
        module.patches.append(self.source / "must-not-apply.patch")
        module.steps.append({"name": "must_not_run", "DEPENDEES": ["build"],
                             "DEPENDERS": ["install"], "COMMAND": ["unavailable-command"]})
        self.project(module, '''
if(TARGET _al_resource_4_demo_extra OR TARGET demo-must_not_run)
    message(FATAL_ERROR "cached module contains work targets")
endif()
get_property(external TARGET demo PROPERTY _EP_IS_EXTERNAL_PROJECT)
if(external)
    message(FATAL_ERROR "cached module is an ExternalProject")
endif()
''')
        self.build()
        self.assertEqual(sentinel.read_text(), "cached header")
        self.assertFalse((self.source / "git").exists())
        # 不重新生成 Python 输出，改变文件状态后只重配原生 CMake。
        sentinel.unlink()
        result = self.run_command("cmake", "-S", self.source, "-B", self.context.build_root, success=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cached module contains work targets", result.stdout + result.stderr)

    def test_install_then_reconfigure_switches_to_empty_target(self):
        repo = self.repository("install", {"x.h": "installed"})
        module = self.definition('sentinel="include/x.h"\n[source]\n' + repo +
                                 '[[copy]]\nfiles=["x.h"]\ndestination="include"\n', kind="copy")
        self.project(module)
        self.build()
        generated = (self.source / "module.cmake").read_text()
        result = self.run_command("cmake", "-S", self.source, "-B", self.context.build_root)
        self.assertIn("cached", result.stdout)
        self.build()
        self.assertEqual((self.source / "module.cmake").read_text(), generated)
        self.assertEqual((self.source / "3rdparty/demo/include/x.h").read_text(), "installed")

    def test_backend_defaults_reach_child_without_root_entry(self):
        repo = self.repository("defaults", {"CMakeLists.txt": '''cmake_minimum_required(VERSION 3.20)
project(child NONE)
file(WRITE "${CMAKE_BINARY_DIR}/settings.txt"
    "${CMAKE_POLICY_VERSION_MINIMUM}|${CMAKE_CXX_STANDARD}|${CMAKE_POSITION_INDEPENDENT_CODE}")
install(FILES "${CMAKE_BINARY_DIR}/settings.txt" DESTINATION include)
'''})
        module = self.definition('[source]\n' + repo)
        self.project(module)
        self.build()
        installed = self.source / "3rdparty/demo/include/settings.txt"
        self.assertEqual(installed.read_text(), "3.5|17|ON")

    def test_archive_parameters_reach_native_targets(self):
        source = 'URL=["https://one.invalid/a.tar.xz", "https://two.invalid/a.tar.xz"]\n' + \
                 'URL_HASH="SHA256=' + "a" * 64 + '"\nDOWNLOAD_NAME="archive.tar.xz"\n'
        module = self.definition('[source]\n' + source + '[resources.extra]\n' + source)
        suffix = '''
foreach(target demo _al_resource_4_demo_extra)
    foreach(key URL URL_HASH DOWNLOAD_NAME)
        get_property(value TARGET ${target} PROPERTY _EP_${key})
        file(WRITE "${CMAKE_BINARY_DIR}/${target}-${key}.txt" "${value}")
    endforeach()
endforeach()
'''
        self.project(module, suffix)
        for target in ("demo", "_al_resource_4_demo_extra"):
            for key, expected in (("URL", "https://one.invalid/a.tar.xz;https://two.invalid/a.tar.xz"),
                                  ("URL_HASH", "SHA256=" + "a" * 64),
                                  ("DOWNLOAD_NAME", "archive.tar.xz")):
                self.assertEqual((self.context.build_root / f"{target}-{key}.txt").read_text(), expected)
        self.assertFalse(any((self.source / "git").iterdir()))
        module.source["URL"] = ["https://one.invalid/a.tar.xz"]
        del module.source["DOWNLOAD_NAME"]
        self.project(module, suffix)
        self.assertEqual((self.context.build_root / "demo-URL.txt").read_text(), "https://one.invalid/a.tar.xz")
        self.assertEqual((self.context.build_root / "demo-DOWNLOAD_NAME.txt").read_text(), "")

    def test_preserves_generator_and_configuration_identity(self):
        module = self.definition('[source]\nGIT_REPOSITORY="not-downloaded"\nGIT_TAG="fixed"\n')
        context = replace(self.context, system_name="Windows", generator="Visual Studio 17 2022",
                          platform="x64", toolset="v142,host=x64", instance="C:/VS instance",
                          multi_config=True, msvc_runtime_library="MultiThreadedDLL")
        suffix = '''
foreach(key CMAKE_GENERATOR CMAKE_GENERATOR_PLATFORM CMAKE_GENERATOR_TOOLSET
            CMAKE_GENERATOR_INSTANCE CMAKE_ARGS BUILD_COMMAND INSTALL_COMMAND)
    get_property(value TARGET demo PROPERTY _EP_${key})
    file(WRITE "${CMAKE_BINARY_DIR}/${key}.txt" "${value}")
endforeach()
'''
        self.project(module, suffix, context)
        captured = lambda key: (self.context.build_root / f"{key}.txt").read_text(encoding="utf-8")
        self.assertEqual(captured("CMAKE_GENERATOR"), "Visual Studio 17 2022")
        self.assertEqual(captured("CMAKE_GENERATOR_PLATFORM"), "x64")
        self.assertEqual(captured("CMAKE_GENERATOR_TOOLSET"), "v142,host=x64")
        self.assertEqual(captured("CMAKE_GENERATOR_INSTANCE"), "C:/VS instance")
        self.assertIn("CMAKE_MSVC_RUNTIME_LIBRARY:STRING=MultiThreadedDLL", captured("CMAKE_ARGS"))
        self.assertIn("--config;Release", captured("BUILD_COMMAND"))
        self.assertIn("--config;Release", captured("INSTALL_COMMAND"))
        self.project(module, suffix, replace(context, generator="Unix Makefiles", platform="",
                     toolset="", instance="", multi_config=False, msvc_runtime_library=None))
        self.assertIn("CMAKE_BUILD_TYPE:STRING=Release", captured("CMAKE_ARGS"))
        self.assertNotIn("CMAKE_MSVC_RUNTIME_LIBRARY", captured("CMAKE_ARGS"))

    def test_arguments_round_trip_through_cmake(self):
        expected = ["a b", r"C:\a b\c", 'a"b', "a;b", "${literal}", "__AL_LIST__"]
        capture = self.source / "capture.cmake"
        capture.write_text('''file(MAKE_DIRECTORY "${OUT}")
math(EXPR last "${CMAKE_ARGC}-1")
foreach(i RANGE 4 ${last})
    file(WRITE "${OUT}/${i}.txt" "${CMAKE_ARGV${i}}")
endforeach()
''', encoding="utf-8")
        repo = self.repository("args", {"x.h": "header"})
        module = self.definition('[source]\n' + repo + '''
[[copy]]
files=["x.h"]
destination="include"
''', kind="copy")
        module.steps.append({"name": "capture", "DEPENDEES": ["build"], "DEPENDERS": ["install"],
            "COMMAND": [str(self.context.cmake_command), "-DOUT=<INSTALL_DIR>/capture", "-P",
                        capture.as_posix(), *expected]})
        self.project(module)
        self.build()
        output = self.source / "3rdparty/demo/capture"
        received = [(output / f"{i}.txt").read_text(encoding="utf-8")
                    for i in range(4, 4 + len(expected))]
        self.assertEqual(received, expected)
        self.assertEqual(len(list(output.iterdir())), len(expected))

    def test_copy_waits_for_both_resources(self):
        first = self.repository("one", {"first.h": "first", "CMakeLists.txt": 'message(FATAL_ERROR "must not configure")'})
        second = self.repository("two", {"second.h": "second", "CMakeLists.txt": 'message(FATAL_ERROR "must not configure")'})
        module = self.definition('[resources.first]\n' + first + '[resources.second]\n' + second + '''
[[copy]]
resource="first"
files=["first.h"]
destination="include/demo"
[[copy]]
resource="second"
files=["second.h"]
destination="include/demo"
''', kind="copy")
        self.project(module)
        self.assertFalse((self.source / "3rdparty/demo/include/demo/first.h").exists())
        self.assertFalse((self.source / "git/_resources/demo/first/.git").exists())
        self.build()
        self.assertEqual((self.source / "3rdparty/demo/include/demo/first.h").read_text(), "first")
        self.assertEqual((self.source / "3rdparty/demo/include/demo/second.h").read_text(), "second")
        for resource in ("first", "second"):
            directory = self.source / f"git/_resources/demo/{resource}"
            self.assertTrue((directory / ".git").exists())
        module.copies[1]["files"] = ["missing.h"]
        self.project(module)
        self.assertNotEqual(self.build(success=False).returncode, 0)

    def test_native_steps_keep_commands_and_dependencies(self):
        repo = self.repository("steps", {"x.h": "header"})
        script = self.source / "record.cmake"
        script.write_text('file(APPEND "${LOG}" "${VALUE}\\n")\n', encoding="utf-8")
        module = self.definition('[source]\n' + repo + '''
[[copy]]
files=["x.h"]
destination="include"
''', kind="copy")
        log = self.source / "sequence.txt"
        module.steps.append({"name": "record", "DEPENDEES": ["build"], "DEPENDERS": ["install"],
            "DEPENDS": [script.as_posix()], "COMMAND": [
                [str(self.context.cmake_command), f"-DLOG={log.as_posix()}", "-DVALUE=one", "-P", str(script)],
                [str(self.context.cmake_command), f"-DLOG={log.as_posix()}", "-DVALUE=two", "-P", str(script)]]})
        self.project(module)
        self.build()
        self.assertEqual(log.read_text(), "one\ntwo\n")
        self.assertTrue((self.source / "3rdparty/demo/include/x.h").exists())
        self.build()
        self.assertEqual(log.read_text(), "one\ntwo\n")

    def test_cache_lists_reach_child_configuration(self):
        repo = self.repository("cache", {"CMakeLists.txt": '''cmake_minimum_required(VERSION 3.20)
project(child NONE)
file(WRITE "${CMAKE_BINARY_DIR}/values.txt" "${ONE}\\n${MANY}\\n${CMAKE_BUILD_TYPE}")
install(FILES "${CMAKE_BINARY_DIR}/values.txt" DESTINATION include)
'''})
        module = self.definition('[source]\n' + repo + '''
[cmake.cache]
ONE="a;b"
MANY=["first path", "second path"]
''')
        self.project(module)
        self.build()
        values = (self.source / "3rdparty/demo/include/values.txt").read_text()
        self.assertTrue(values.startswith("a;b\nfirst path;second path\n"), values)


if __name__ == "__main__":
    unittest.main()
