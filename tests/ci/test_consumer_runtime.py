import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


class ConsumerRuntimeTests(unittest.TestCase):
    def cmake(self, *args):
        result = subprocess.run(["cmake", *map(str, args)], text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result.stdout

    def test_unknown_smoke_module_is_rejected(self):
        with tempfile.TemporaryDirectory(prefix="unknown consumer ") as temporary:
            root = Path(temporary)
            entry = root / "entry.cmake"
            entry.write_text('''function(autolib_import)
  set(AUTOLIB_PREPARED_PACKAGES untested PARENT_SCOPE)
  set(AUTOLIB_PACKAGE_VERSION_untested 1 PARENT_SCOPE)
endfunction()
''')
            result = subprocess.run(
                ["cmake", "-S", str(ROOT / "tests/autolib"), "-B", str(root / "build"),
                 f"-DAUTOLIB_ENTRY={entry}", "-DMODULES=untested@1"],
                text=True, capture_output=True)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("No API smoke implementation for untested", result.stdout + result.stderr)

    def test_selected_package_dll_directories_are_recorded(self):
        with tempfile.TemporaryDirectory(prefix="consumer runtime ") as temporary:
            root = Path(temporary)
            selected = root / "selected package"
            dependency = root / "dependency package"
            unrelated = root / "unrelated package"
            for path in (selected / "bin/one.dll", selected / "bin/two.dll",
                         selected / "nested/lib/three.dll", dependency / "lib/dep.dll", unrelated / "bin/other.dll"):
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b"fixture")
            consumer = root / "consumer"
            consumer.mkdir()
            shutil.copyfile(ROOT / "tests/autolib/CMakeLists.txt", consumer / "CMakeLists.txt")
            (consumer / "smoke.cpp").write_text("int main() { return 0; }\n")
            entry = root / "entry.cmake"
            entry.write_text(f'''function(autolib_import)
  set(AUTOLIB_PREPARED_PACKAGES dep queue PARENT_SCOPE)
  set(AUTOLIB_PACKAGE_VERSION_dep 1 PARENT_SCOPE)
  set(AUTOLIB_PACKAGE_DIR_dep [==[{dependency.as_posix()}]==] PARENT_SCOPE)
  set(AUTOLIB_PACKAGE_VERSION_queue 1 PARENT_SCOPE)
  set(AUTOLIB_PACKAGE_DIR_queue [==[{selected.as_posix()}]==] PARENT_SCOPE)
endfunction()
''')
            windows = root / "windows.cmake"
            windows.write_text("set(WIN32 TRUE)\n")
            build = root / "build"
            self.cmake("-S", consumer, "-B", build, f"-DAUTOLIB_ENTRY={entry}", "-DMODULES=queue",
                       f"-DCMAKE_PROJECT_autolib_smoke_INCLUDE={windows}")
            check = root / "check.cmake"
            check.write_text(f'''include([==[{build.as_posix()}/prepared-state.cmake]==])
set(expected [==[{dependency.as_posix()}/lib;{selected.as_posix()}/bin;{selected.as_posix()}/nested/lib]==])
if(NOT AUTOLIB_RUNTIME_DIRS STREQUAL expected)
  message(FATAL_ERROR "DLL directories differ: ${{AUTOLIB_RUNTIME_DIRS}}")
endif()
''')
            self.cmake("-P", check)

    def test_both_drivers_pass_dll_directories_to_ctest(self):
        for driver in ("run-installed.cmake", "run-github.cmake"):
            with self.subTest(driver=driver), tempfile.TemporaryDirectory(prefix="driver runtime ") as temporary:
                root = Path(temporary)
                source = root / "tests/autolib"
                source.mkdir(parents=True)
                runtime = root / "selected DLL directory"
                runtime.mkdir()
                check = root / "check-path.cmake"
                check.write_text(f'''string(FIND "$ENV{{PATH}}" [==[{runtime.as_posix()};]==] position)
if(NOT position EQUAL 0)
  message(FATAL_ERROR "Selected DLL directory was not prepended to PATH")
endif()
''')
                (source / "CMakeLists.txt").write_text(f'''cmake_minimum_required(VERSION 3.20)
project(driver_runtime NONE)
enable_testing()
file(WRITE "${{CMAKE_BINARY_DIR}}/prepared-state.cmake" "set(AUTOLIB_RUNTIME_DIRS [==[{runtime.as_posix()}]==])\\n")
add_test(NAME runtime_path COMMAND "${{CMAKE_COMMAND}}" -P [==[{check.as_posix()}]==])
''')
                (source / "smoke.cpp").write_text("")
                entry = root / "entry.cmake"
                entry.write_text("# fixture entry\n")
                wrapper = root / "driver.cmake"
                wrapper.write_text(f'''set(CMAKE_HOST_WIN32 TRUE)
include([==[{(ROOT / 'tests/autolib' / driver).as_posix()}]==])
''')
                generator = "Visual Studio 17 2022" if os.name == "nt" else "Unix Makefiles"
                self.cmake(f"-DSOURCE_ROOT={root}", f"-DINSTALL_ROOT={root}",
                           f"-DVALIDATION_ROOT={root}/build/validation", f"-DGENERATOR={generator}",
                           f"-DENTRY_URL={entry.as_uri()}", "-P", wrapper)


if __name__ == "__main__":
    unittest.main()
