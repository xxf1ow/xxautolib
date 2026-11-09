import os
from pathlib import Path
import shutil
import subprocess
import sys
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

    def test_exempt_packages_are_imported_without_compiling_their_api(self):
        for name, header in (("taskflow", "taskflow/taskflow.hpp"),
                             ("onnxruntime", "onnxruntime_c_api.h")):
            with self.subTest(package=name), tempfile.TemporaryDirectory(prefix="import-only check ") as temporary:
                root = Path(temporary)
                path = root / "include" / header
                path.parent.mkdir(parents=True)
                path.write_text('#error "Exempt package API smoke must be skipped"\n')
                entry = root / "entry.cmake"
                entry.write_text('''add_library(autolib::NAME INTERFACE IMPORTED GLOBAL)
set_property(TARGET autolib::NAME PROPERTY INTERFACE_INCLUDE_DIRECTORIES "${CMAKE_CURRENT_LIST_DIR}/include")
function(autolib_import)
  target_link_libraries(autolib_smoke PRIVATE autolib::NAME)
  set(AUTOLIB_PREPARED_PACKAGES NAME PARENT_SCOPE)
  set(AUTOLIB_PACKAGE_VERSION_NAME 1 PARENT_SCOPE)
endfunction()
'''.replace("NAME", name))
                build = root / "build"
                self.cmake("-S", ROOT / "tests/autolib", "-B", build,
                           f"-DAUTOLIB_ENTRY={entry}", f"-DMODULES={name}@1")
                self.assertEqual((build / "prepared-packages.txt").read_text(), name)
                self.cmake("--build", build)
                result = subprocess.run(["ctest", "--test-dir", str(build), "--output-on-failure"],
                                        capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_link_input_evidence_keeps_libraries_and_excludes_imported_programs(self):
        with tempfile.TemporaryDirectory(prefix="consumer link inputs ") as temporary:
            root = Path(temporary)
            consumer = root / "consumer"
            consumer.mkdir()
            shutil.copyfile(ROOT / "tests/autolib/CMakeLists.txt", consumer / "CMakeLists.txt")
            (consumer / "smoke.cpp").write_text("int main() { return 0; }\n")
            library = root / "libfixture.a"
            program = root / "python-interpreter"
            entry = root / "entry.cmake"
            entry.write_text(f'''add_executable(Fixture::Interpreter IMPORTED GLOBAL)
set_property(TARGET Fixture::Interpreter PROPERTY IMPORTED_LOCATION [==[{program.as_posix()}]==])
add_library(Fixture::Library STATIC IMPORTED GLOBAL)
set_property(TARGET Fixture::Library PROPERTY IMPORTED_LOCATION [==[{library.as_posix()}]==])
function(autolib_import)
  set(AUTOLIB_PREPARED_PACKAGES queue PARENT_SCOPE)
  set(AUTOLIB_PACKAGE_VERSION_queue 1 PARENT_SCOPE)
endfunction()
''')
            build = root / "build"
            self.cmake("-S", consumer, "-B", build, f"-DAUTOLIB_ENTRY={entry}",
                       "-DMODULES=queue", "-DCMAKE_BUILD_TYPE=Release")
            inputs = (build / "link-inputs-Release.txt").read_text().splitlines()
            self.assertIn(library.as_posix(), inputs)
            self.assertNotIn(program.as_posix(), inputs)

    def test_package_link_inputs_exclude_python_found_for_binding_smoke(self):
        with tempfile.TemporaryDirectory(prefix="binding environment ") as temporary:
            root = Path(temporary)
            consumer = root / "consumer"
            consumer.mkdir()
            shutil.copyfile(ROOT / "tests/autolib/CMakeLists.txt", consumer / "CMakeLists.txt")
            (consumer / "smoke.cpp").write_text("int main() { return 0; }\n")
            library = root / "package/libfixture.a"
            python_dll = root / "python/python315.dll"
            python_lib = root / "python/python315.lib"
            entry = root / "entry.cmake"
            entry.write_text(f'''add_library(Fixture::Library STATIC IMPORTED GLOBAL)
set_property(TARGET Fixture::Library PROPERTY IMPORTED_LOCATION [==[{library.as_posix()}]==])
add_library(autolib::pybind11 INTERFACE IMPORTED GLOBAL)
function(autolib_import)
  set(AUTOLIB_PREPARED_PACKAGES pybind11 PARENT_SCOPE)
  set(AUTOLIB_PACKAGE_VERSION_pybind11 1 PARENT_SCOPE)
endfunction()
''')
            # FindPython3 exposes a DLL target on Windows; Unix normally uses an interface target.
            (root / "FindPython3.cmake").write_text(f'''set(Python3_EXECUTABLE [==[{Path(sys.executable).as_posix()}]==])
add_library(Python3::Module SHARED IMPORTED GLOBAL)
set_target_properties(Python3::Module PROPERTIES
  IMPORTED_LOCATION [==[{python_dll.as_posix()}]==]
  IMPORTED_IMPLIB [==[{python_lib.as_posix()}]==])
function(Python3_add_library target)
  add_library(${{target}} MODULE smoke.cpp)
  target_link_libraries(${{target}} PRIVATE Python3::Module)
endfunction()
''')
            build = root / "build"
            self.cmake("-S", consumer, "-B", build, f"-DAUTOLIB_ENTRY={entry}",
                       "-DMODULES=pybind11", "-DCMAKE_BUILD_TYPE=Release",
                       f"-DCMAKE_MODULE_PATH={root}")
            inputs = (build / "link-inputs-Release.txt").read_text().splitlines()
            self.assertIn(library.as_posix(), inputs)
            self.assertNotIn(python_dll.as_posix(), inputs)
            self.assertNotIn(python_lib.as_posix(), inputs)

    def test_example_driver_preserves_existing_data_when_input_is_missing(self):
        with tempfile.TemporaryDirectory(prefix="original example ") as temporary:
            root = Path(temporary)
            source = root / "source"
            (source / "example/3rdparty").mkdir(parents=True)
            original = source / "example/3rdparty/user-cache"
            original.write_text("keep original cache")
            install = root / "install"
            install.mkdir()
            validation = source / "build/validation"
            validation.mkdir(parents=True)
            existing = validation / "keep"
            existing.write_text("keep validation data")
            result = subprocess.run(
                ["cmake", f"-DSOURCE_ROOT={source}", f"-DINSTALL_ROOT={install}",
                 f"-DVALIDATION_ROOT={validation}", "-DGENERATOR=Unix Makefiles",
                 "-P", str(ROOT / "tests/autolib/run-example.cmake")], text=True, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Missing example input", result.stdout + result.stderr)
            self.assertEqual(original.read_text(), "keep original cache")
            self.assertEqual(existing.read_text(), "keep validation data")
            self.assertEqual(sorted(p.name for p in validation.iterdir()), ["keep"])

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

    def test_windows_runtime_probes_use_selected_dll(self):
        with tempfile.TemporaryDirectory(prefix="consumer DLL probes ") as temporary:
            root = Path(temporary)
            consumer = root / "consumer"
            consumer.mkdir()
            shutil.copyfile(ROOT / "tests/autolib/CMakeLists.txt", consumer / "CMakeLists.txt")
            (consumer / "smoke.cpp").write_text("int main() { return 0; }\n")
            entry = root / "entry.cmake"
            entry.write_text('''add_library(opencv_core SHARED IMPORTED GLOBAL)
set_property(TARGET opencv_core PROPERTY IMPORTED_LOCATION "${CMAKE_CURRENT_LIST_DIR}/opencv-selected.dll")
add_library(realsense2::realsense2 SHARED IMPORTED GLOBAL)
set_property(TARGET realsense2::realsense2 PROPERTY IMPORTED_LOCATION "${CMAKE_CURRENT_LIST_DIR}/realsense-selected.dll")
function(autolib_import)
  foreach(name IN ITEMS opencv librealsense)
    set(AUTOLIB_PACKAGE_VERSION_${name} 1 PARENT_SCOPE)
    set(AUTOLIB_PACKAGE_DIR_${name} "${CMAKE_CURRENT_LIST_DIR}" PARENT_SCOPE)
  endforeach()
  file(GENERATE OUTPUT "${CMAKE_BINARY_DIR}/definitions.txt"
    CONTENT "$<TARGET_GENEX_EVAL:autolib_smoke,$<TARGET_PROPERTY:autolib_smoke,COMPILE_DEFINITIONS>>")
endfunction()
''')
            windows = root / "windows.cmake"
            windows.write_text("set(WIN32 TRUE)\n")
            build = root / "build"
            self.cmake("-S", consumer, "-B", build, f"-DAUTOLIB_ENTRY={entry}",
                       "-DMODULES=opencv;librealsense", "-DCMAKE_BUILD_TYPE=Release",
                       f"-DCMAKE_PROJECT_autolib_smoke_INCLUDE={windows}")
            definitions = (build / "definitions.txt").read_text().split(";")
            self.assertIn('AUTOLIB_OPENCV_DLL=L"opencv-selected.dll"', definitions)
            self.assertIn('AUTOLIB_LIBREALSENSE_DLL=L"realsense-selected.dll"', definitions)

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
