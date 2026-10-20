import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


class FoundationTests(unittest.TestCase):
    def setUp(self):
        directory = ROOT / "build/tests"
        directory.mkdir(parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(prefix="foundation ", dir=directory)
        self.addCleanup(temporary.cleanup)
        self.source = Path(temporary.name)
        for name in ("CMakeLists.txt", "pyproject.toml", "uv.lock", ".python-version"):
            if (ROOT / name).is_file():
                shutil.copy2(ROOT / name, self.source / name)
        if (ROOT / "src/build_system").is_dir():
            shutil.copytree(ROOT / "src/build_system", self.source / "src/build_system")

    def configure(self, *arguments):
        generator = "Visual Studio 17 2022" if os.name == "nt" else "Unix Makefiles"
        return subprocess.run(
            ["cmake", "-S", str(self.source), "-B", str(self.source / "build"),
             "-G", generator, *arguments],
            capture_output=True, encoding="utf-8", errors="replace",
            env={**os.environ, "UV_OFFLINE": "true",
                 "UV_PROJECT_ENVIRONMENT": str(self.source / "unrelated-environment")})

    def test_configure_prepares_only_the_root_environment(self):
        result = self.configure()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        executable = "Scripts/python.exe" if os.name == "nt" else "bin/python"
        self.assertTrue((self.source / ".venv" / executable).is_file())
        self.assertFalse((self.source / "unrelated-environment").exists())
        self.assertFalse((self.source / "3rdparty").exists())

    def test_missing_uv_fails_configure(self):
        uv_directory = Path(shutil.which("uv")).parent.as_posix()
        result = self.configure(f"-DCMAKE_IGNORE_PATH={uv_directory}")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("uv", result.stdout + result.stderr)

    def test_missing_git_fails_configure(self):
        result = self.configure("-DCMAKE_DISABLE_FIND_PACKAGE_Git=ON")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Git", result.stdout + result.stderr)

    def test_stale_lock_fails_configure(self):
        project = self.source / "pyproject.toml"
        project.write_text(project.read_text(encoding="utf-8").replace(
            'version = "0.1.0"', 'version = "0.2.0"'), encoding="utf-8")
        result = self.configure()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("locked", result.stdout + result.stderr)

    def test_reference_entry_reports_build_settings(self):
        result = self.configure()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        for heading in ("Building Summary", "CMake Version", "Operation System", "CPU Architecture",
                        "Generator Toolset", "Platform Toolset", "Install Directory", "BUILD_MODULES"):
            self.assertIn(heading, result.stdout)
        self.assertIn((self.source / "3rdparty").as_posix(), result.stdout)

    def test_build_list_rejects_missing_module(self):
        result = self.configure("-DBUILD_LIST=not-present")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Module file NOT found", result.stdout + result.stderr)
        self.assertIn("not-present", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
