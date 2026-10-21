import os
from pathlib import Path
import time
import unittest

from test_cmake import PipelineCase, ROOT
from definition import PatchDefinition


PATCH = '''diff --git a/value.c b/value.c
--- a/value.c
+++ b/value.c
@@ -1 +1 @@
-int value(void) { return 1; }
+int value(void) { return 2; }
'''


class PatchTests(PipelineCase):
    def test_archive_patch_is_not_skipped_inside_an_outer_repository(self):
        self.repository("outer", {"archive/value.c": "int value(void) { return 1; }\n",
                                  "value.c": "outer content\n"})
        outer = self.source / "upstream/outer"
        source = outer / "archive"
        patch = self.patch_file()
        self.apply(source, patch)
        self.assertEqual((source / "value.c").read_text(), "int value(void) { return 2; }\n")
        self.assertEqual((outer / "value.c").read_text(), "outer content\n")
        self.assertIn("already applied", self.apply(source, patch).stdout)
        (source / "value.c").write_text("manual content\n")
        result = self.apply(source, patch, success=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("source mismatch", result.stdout + result.stderr)
        self.assertEqual((source / "value.c").read_text(), "manual content\n")

    def resource_fixture(self):
        patch = self.patch_file()
        extra = self.repository("extra", {"value.c": "int value(void) { return 1; }\n"})
        main = self.repository("main", {"value.c": "int value(void) { return 1; }\n", "CMakeLists.txt": '''cmake_minimum_required(VERSION 3.20)
project(child NONE)
file(READ "${EXTRA}/value.c" value)
if(NOT value MATCHES "return 2")
message(FATAL_ERROR "resource not patched")
endif()
file(APPEND "${CMAKE_INSTALL_PREFIX}/configures" "configured\\n")
install(FILES value.c DESTINATION .)
'''})
        module = self.definition('[source]\n' + main + '[resources.extra]\n' + extra +
            '[cmake.cache]\nEXTRA="<RESOURCE_SOURCE_DIR:extra>"\n[[patches]]\n'
            f'file="{patch.as_posix()}"\nresource="extra"\n')
        return module, patch

    def test_resource_patch_precedes_main_configure(self):
        module, patch = self.resource_fixture()
        self.project(module)
        self.build()
        self.assertEqual((self.source / "git/demo/value.c").read_text(), "int value(void) { return 1; }\n")
        resource = self.source / "git/_resources/demo/extra/value.c"
        self.assertEqual(resource.read_text(), "int value(void) { return 2; }\n")
        self.build()
        self.project(module)
        self.build()
        self.assertEqual(resource.read_text(), "int value(void) { return 2; }\n")

    def test_resource_patch_input_invalidates_main_configure(self):
        module, patch = self.resource_fixture()
        self.project(module)
        self.build()
        log = self.source / "3rdparty/demo/configures"
        before = log.read_text().splitlines()
        time.sleep(1.1)
        patch.touch()
        result = self.build()
        self.assertIn("already applied", result.stdout)
        self.assertGreater(len(log.read_text().splitlines()), len(before))
        resource = self.source / "git/_resources/demo/extra/value.c"
        resource.write_text("manual content\n")
        time.sleep(1.1)
        patch.touch()
        result = self.build(success=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("source mismatch", result.stdout + result.stderr)
        self.assertEqual(resource.read_text(), "manual content\n")

    def patch_file(self):
        path = self.source / "change.patch"
        path.write_text(PATCH, encoding="utf-8")
        return path

    def apply(self, source, patch, success=True):
        return self.run_command("cmake", f"-DGIT_EXECUTABLE={self.context.git_executable.as_posix()}",
                                f"-DSOURCE_DIR={source.as_posix()}", f"-DPATCH_FILE={patch.as_posix()}",
                                "-P", ROOT / "src/build_system/apply_patch.cmake", success=success)

    def test_patch_applies_once_and_recognizes_applied_source(self):
        self.repository("direct", {"value.c": "int value(void) { return 1; }\n"})
        source = self.source / "upstream/direct"
        patch = self.patch_file()
        before = (source / "value.c").read_text()
        self.apply(source, patch)
        after = (source / "value.c").read_text()
        result = self.apply(source, patch)
        self.assertNotEqual(before, after)
        self.assertEqual(after, "int value(void) { return 2; }\n")
        self.assertEqual((source / "value.c").read_text(), after)
        self.assertIn("already applied", result.stdout)

    def test_mismatched_patch_fails_without_restoring_source(self):
        self.repository("mismatch", {"value.c": "manual content\n"})
        source = self.source / "upstream/mismatch"
        result = self.apply(source, self.patch_file(), success=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("source mismatch", result.stdout + result.stderr)
        self.assertIn("change.patch", result.stdout + result.stderr)
        self.assertEqual((source / "value.c").read_text(), "manual content\n")

    def test_patch_input_invalidates_following_steps(self):
        log = (self.source / "phases.txt").as_posix()
        repo = self.repository("library", {
            "value.c": "int value(void) { return 1; }\n",
            "main.c": '#include <stdio.h>\nint value(void);\nint main(void) { printf("%d\\n", value()); return 0; }\n',
            "record.cmake": f'file(APPEND "{log}" "build\\n")\n',
            "CMakeLists.txt": f'''cmake_minimum_required(VERSION 3.20)
project(child C)
file(APPEND "{log}" "configure\\n")
add_library(value STATIC value.c)
add_executable(probe main.c)
target_link_libraries(probe PRIVATE value)
add_custom_target(record ALL COMMAND "${{CMAKE_COMMAND}}" -P "${{CMAKE_CURRENT_SOURCE_DIR}}/record.cmake")
install(TARGETS value probe ARCHIVE DESTINATION lib RUNTIME DESTINATION bin)
install(CODE [[file(APPEND "{log}" "install\\n")]])
'''})
        module = self.definition("[source]\n" + repo)
        patch = self.patch_file()
        module.patches.append(PatchDefinition(patch))
        self.project(module)
        first = self.build()
        executable = self.source / "3rdparty/demo/bin" / ("probe.exe" if os.name == "nt" else "probe")
        self.assertEqual(self.run_command(executable).stdout.strip(), "2")
        phases = Path(log).read_text().splitlines()
        # 实机快速构建也须跨过构建工具的时间戳分辨率。
        time.sleep(1.1)
        patch.touch()
        second = self.build()
        after = Path(log).read_text().splitlines()
        self.assertIn("already applied", second.stdout)
        for phase in ("configure", "build", "install"):
            self.assertGreater(after.count(phase), phases.count(phase), first.stdout + second.stdout)
        self.assertEqual(self.run_command(executable).stdout.strip(), "2")

        rewrite = self.source / "rewrite.cmake"
        rewrite.write_text('file(WRITE "${FILE}" "int value(void) { return 3; }\\n")\n', encoding="utf-8")
        module.steps.append({"name": "rewrite", "DEPENDEES": ["patch"], "DEPENDERS": ["configure"],
            "DEPENDS": [rewrite.as_posix()], "COMMAND": [str(self.context.cmake_command),
            "-DFILE=<SOURCE_DIR>/value.c", "-P", rewrite.as_posix()]})
        self.project(module)
        self.build()
        self.assertEqual(self.run_command(executable).stdout.strip(), "3")
        time.sleep(1.1)
        rewrite.write_text(rewrite.read_text().replace("return 3", "return 4"), encoding="utf-8")
        self.build()
        self.assertEqual(self.run_command(executable).stdout.strip(), "4")


if __name__ == "__main__":
    unittest.main()
