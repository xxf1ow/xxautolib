from pathlib import Path
import unittest

from test_cmake import PipelineCase, ROOT
from generate import main, write_if_changed


class GenerateTests(PipelineCase):
    def arguments(self, *modules):
        context = self.context
        args = [f"--module={name}" for name in modules]
        for key in ("source_root", "build_root", "system_name", "generator", "platform",
                    "toolset", "instance", "cmake_command", "git_executable"):
            args.append(f"--{key.replace('_', '-')}={getattr(context, key)}")
        return args + [f"--multi-config={'ON' if context.multi_config else 'OFF'}"]

    def test_unchanged_generation_preserves_mtime(self):
        output = self.source / "out.cmake"
        self.assertTrue(write_if_changed(output, "# generated\n"))
        before = output.stat().st_mtime_ns
        self.assertFalse(write_if_changed(output, "# generated\n"))
        self.assertEqual(output.stat().st_mtime_ns, before)
        self.assertTrue(write_if_changed(output, "# changed\n"))
        module = self.definition('[source]\nGIT_REPOSITORY="unused"\nGIT_TAG="fixed"\n')
        self.assertEqual(main(self.arguments("demo")), 0)
        generated = self.context.build_root / "generated/demo.cmake"
        text, before = generated.read_text(), generated.stat().st_mtime_ns
        self.assertEqual(main(self.arguments("demo")), 0)
        self.assertEqual(generated.stat().st_mtime_ns, before)
        module.path.write_text(module.path.read_text() + '[cmake.cache]\nVALUE="changed"\n')
        self.assertEqual(main(self.arguments("demo")), 0)
        self.assertNotEqual(generated.read_text(), text)

    def test_reports_all_project_python_and_toml_inputs(self):
        module = self.definition('[source]\nGIT_REPOSITORY="unused"\nGIT_TAG="fixed"\n')
        other = self.definition('[source]\nGIT_REPOSITORY="unused"\nGIT_TAG="fixed"\n', name="other")
        self.assertEqual(main(self.arguments("demo")), 0)
        script = self.source / "inspect.cmake"
        script.write_text(f'include("{self.context.build_root.as_posix()}/generated/inputs.cmake")\n'
                          'foreach(input IN LISTS XXAUTOLIB_CONFIGURE_INPUTS)\n'
                          '  message("INPUT=${input}")\nendforeach()\n'
                          'foreach(input IN LISTS XXAUTOLIB_GENERATED_MODULES)\n'
                          '  message("MODULE=${input}")\nendforeach()\n')
        result = self.run_command("cmake", "-P", script)
        lines = (result.stdout + result.stderr).splitlines()
        inputs = {line.removeprefix("INPUT=") for line in lines if line.startswith("INPUT=")}
        self.assertEqual(inputs, {module.path.as_posix(), *[
            (ROOT / "src/build_system" / name).as_posix()
            for name in ("generate.py", "definition.py", "cmake.py")]})
        self.assertNotIn(other.path.as_posix(), inputs)
        self.assertEqual([line for line in lines if line.startswith("MODULE=")],
                         [f"MODULE={self.context.build_root.as_posix()}/generated/demo.cmake"])


if __name__ == "__main__":
    unittest.main()
