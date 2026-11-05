from pathlib import Path
import subprocess
import tempfile
import tomllib
import unittest


ROOT = Path(__file__).resolve().parents[2]
RECIPE = ROOT / "src/dependencies/portaudio/v190700_20210406/module.toml"


class PortAudioStepTests(unittest.TestCase):
    def test_pkgconfig_fix_preserves_paths_and_other_fields(self):
        recipe = tomllib.loads(RECIPE.read_text(encoding="utf-8"))
        step = recipe["platforms"]["windows"]["steps"][0]
        with tempfile.TemporaryDirectory(prefix="portaudio-") as temporary:
            for name in ("plain", "中文 spaces & data"):
                with self.subTest(directory=name):
                    build = Path(temporary) / name
                    build.mkdir()
                    pc = build / "portaudio-2.0.pc"
                    pc.write_text("prefix=/some path\nLibs: -L${libdir} -lportaudio\nLibs.private: -lwinmm\n")
                    command = [arg.replace("<BINARY_DIR>", build.as_posix()) for arg in step["COMMAND"]]
                    cwd = RECIPE.parent / step.get("WORKING_DIRECTORY", ".")
                    result = subprocess.run(command, cwd=cwd, text=True, capture_output=True)
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertEqual(pc.read_text(), "prefix=/some path\nLibs: -L${libdir} -lportaudio_x64\nLibs.private: -lwinmm\n")
                    result = subprocess.run(command, cwd=cwd, text=True, capture_output=True)
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertEqual(pc.read_text(), "prefix=/some path\nLibs: -L${libdir} -lportaudio_x64\nLibs.private: -lwinmm\n")


if __name__ == "__main__":
    unittest.main()
