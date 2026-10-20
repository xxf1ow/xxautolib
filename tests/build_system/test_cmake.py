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
        self.assertFalse(any((self.context.build_root / "ep/demo/dl").iterdir()))
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
        self.assertFalse((self.context.build_root / "ep/demo/res/first/src/.git").exists())
        self.build()
        self.assertEqual((self.source / "3rdparty/demo/include/demo/first.h").read_text(), "first")
        self.assertEqual((self.source / "3rdparty/demo/include/demo/second.h").read_text(), "second")
        for resource in ("first", "second"):
            directory = self.context.build_root / f"ep/demo/res/{resource}/src"
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
