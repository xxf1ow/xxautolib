import json
from itertools import product
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest

from test_darwin_workflow import job, step_script


ROOT = Path(__file__).resolve().parents[2]

# System tools are replaced so the real preparation script cannot install or download anything.
STUB = r'''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys
name = Path(sys.argv[0]).name
args = sys.argv[1:]
root = Path(os.environ["PREPARATION_FIXTURE"])
with (root / "events").open("a") as events:
    events.write(name + " " + " ".join(args) + "\n")
if name == "dpkg":
    print(os.environ["PREPARATION_ARCH"])
elif name in ("gcc-10", "g++-10"):
    print("10.5.0")
elif name == "apt-get" and "bad-recipe-package" in args:
    sys.exit(72)
elif name == "nproc":
    print("4")
elif name == "jq":
    variables = {}
    positional = []
    index = 0
    while index < len(args):
        if args[index] in ("--arg", "--argjson"):
            variables[args[index + 1]] = json.loads(args[index + 2]) if args[index] == "--argjson" else args[index + 2]
            index += 3
        elif args[index].startswith("-"):
            index += 1
        else:
            positional.append(args[index])
            index += 1
    query = positional[0]
    if len(positional) == 1:
        print(json.dumps({"cmake_command": variables["cmake"], "make_program": variables["make"], "budget": variables["budget"]}))
    else:
        document = json.loads(Path(positional[1]).read_text())
        if "platforms[]" in query:
            selected = [p for p in document["platforms"] if p["id"] == variables["platform"]]
            if "length == 1" in query:
                print("true" if len(selected) == 1 else "false")
                sys.exit(0 if len(selected) == 1 else 1)
            elif ".roots" in query:
                print("true" if selected[0]["roots"] else "false")
            elif ".system_dependencies.apt" in query:
                for package in sorted(set(selected[0]["system_dependencies"]["apt"])):
                    print(package)
            else:
                raise RuntimeError("unexpected plan query: " + query)
        elif "cmake.assets[]" in query:
            asset = next(a for a in document["cmake"]["assets"] if a["platform"] == variables["platform"])
            print(asset[query.rsplit(".", 1)[-1]])
        else:
            value = document
            for part in query.lstrip(".").split("."):
                value = value[part]
            print(value)
elif name == "curl":
    Path(args[args.index("-o") + 1]).touch()
elif name == "cmake":
    if args == ["--version"]:
        print("cmake version 3.31.12")
elif name == "tar":
    destination = Path(args[args.index("-C") + 1])
    if "make-4.4.1.tar.gz" in " ".join(args):
        source = destination / "make-4.4.1"
        source.mkdir()
        configure = source / "configure"
        configure.write_text("#!/bin/sh\nexit 0\n")
        configure.chmod(0o755)
    else:
        (destination / "bin").mkdir(exist_ok=True)
        (destination / "bin/cmake").symlink_to(root / "stub")
elif name == "make":
    if args == ["--version"]:
        print("GNU Make 4.4.1")
    elif args[-1] == "install":
        (root / "work/ci-tools/bin/make").symlink_to(root / "stub")
'''


@unittest.skipUnless(sys.platform == "linux", "requires Linux bash")
class LinuxPreparationTests(unittest.TestCase):
    def test_library_tools_are_only_prepared_for_library_work(self):
        platforms = (("linux-x86_64", "amd64"), ("linux-aarch64", "arm64"))
        for (platform, arch), libraries in product(platforms, (False, True)):
            with self.subTest(platform=platform, libraries=libraries), tempfile.TemporaryDirectory(prefix="prepare linux ") as temporary:
                root = Path(temporary)
                scripts = root / ".github/scripts"
                scripts.mkdir(parents=True)
                script = (ROOT / ".github/scripts/prepare-linux.sh").read_text()
                # Redirect only the privileged APT file; execute all production branching unchanged.
                script = script.replace("/etc/apt/sources.list", shlex.quote(str(root / "sources.list")))
                (scripts / "prepare-linux.sh").write_text(script)
                (root / "stub").write_text(STUB)
                (root / "stub").chmod(0o755)
                binary = root / "bin"
                binary.mkdir()
                for name in ("jq", "dpkg", "apt-get", "curl", "sha256sum", "tar", "make", "gcc-10", "g++-10",
                             "git", "dpkg-query", "nproc"):
                    (binary / name).symlink_to(root / "stub")
                plan = root / "plan.json"
                plan.write_text(json.dumps({"platforms": [{"id": platform, "roots": ["queue@1"] if libraries else [],
                                                         "system_dependencies": {"apt": ["bad-recipe-package"]}}]}))
                config = root / "ci-tools.json"
                configuration = json.loads((ROOT / ".github/ci-tools.json").read_text())
                configuration["production"]["linux"]["gcc_version"] = "10.5"
                config.write_text(json.dumps(configuration))
                github_env = root / "github-env"
                env = dict(os.environ, PATH=str(binary) + os.pathsep + os.environ["PATH"],
                           PREPARATION_FIXTURE=str(root), PREPARATION_ARCH=arch, GITHUB_PATH="", GITHUB_ENV=str(github_env))
                result = subprocess.run(["bash", str(scripts / "prepare-linux.sh"), "--config", str(config),
                                         "--plan", str(plan), "--platform", platform, "--work-root", str(root / "work")],
                                        env=env, text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                output = json.loads(result.stdout)
                self.assertTrue(Path(output["cmake_command"]).is_file())
                self.assertNotIn("bad-recipe-package", (root / "events").read_text())
                self.assertEqual(output["budget"], 4 if libraries else 0)
                if libraries:
                    self.assertTrue(Path(output["make_program"]).is_file())
                    self.assertEqual(github_env.read_text(), "CC=gcc-10\nCXX=g++-10\n")
                    events = (root / "events").read_text()
                    self.assertIn("build-essential gcc-10 g++-10 pkg-config libgomp1", events)
                else:
                    self.assertEqual(output["make_program"], "")
                    self.assertFalse((root / "work/ci-tools/src/make-4.4.1").exists())
                    events = (root / "events").read_text()
                    self.assertNotIn("build-essential", events)
                    self.assertNotIn("gcc-10", events)
                    self.assertNotIn("make-4.4.1.tar.gz", events)


class LinuxConsumerWorkflowTests(unittest.TestCase):
    def test_python_headers_are_prepared_only_for_binding_consumption(self):
        self.assertIsNotNone(shutil.which('jq'), 'workflow checks require jq')
        script = step_script(job((ROOT / '.github/workflows/build-platform.yml').read_text(), 'linux'),
                             'Verify selected work')
        for platform, binding in product(('linux-x86_64', 'linux-aarch64'), (False, True)):
            with self.subTest(platform=platform, binding=binding), tempfile.TemporaryDirectory(prefix='linux consumer ') as temporary:
                root = Path(temporary)
                (root / 'build/ci-input').mkdir(parents=True)
                modules = [{'name': 'pybind11' if binding else 'queue'}]
                (root / 'build/ci-input/plan.json').write_text(json.dumps({'platforms': [
                    {'id': platform, 'description': {'modules': modules}}]}))
                binary = root / 'bin'
                binary.mkdir()
                apt = binary / 'apt-get'
                apt.write_text('#!' + sys.executable + '\n' + textwrap.dedent('''
                    import os, sys
                    from pathlib import Path
                    assert sys.argv[1:] == ['install', '-y', '--no-install-recommends', 'python3-dev']
                    Path(os.environ['CONSUMER_FIXTURE'], 'python-headers').touch()
                '''))
                apt.chmod(0o755)
                cli = root / 'autolib-ci'
                cli.write_text('#!' + sys.executable + '\n' + textwrap.dedent('''
                    import os, sys
                    from pathlib import Path
                    root = Path(os.environ['CONSUMER_FIXTURE'])
                    if os.environ['REQUIRES_PYTHON'] == '1' and not (root / 'python-headers').exists():
                        raise SystemExit('binding consumer requires Python development headers')
                    assert sys.argv[sys.argv.index('--layer') + 1] == 'verify'
                    (root / 'consumer-ran').touch()
                '''))
                cli.chmod(0o755)
                env = dict(os.environ, PATH=str(binary) + os.pathsep + os.environ['PATH'],
                           CONSUMER_FIXTURE=str(root), REQUIRES_PYTHON='1' if binding else '0',
                           WORK_ROOT=str(root), PLATFORM=platform, GITHUB_REPOSITORY='fixture/source',
                           GITHUB_WORKSPACE=str(root), CMAKE_COMMAND='cmake', MAKE_PROGRAM='make', BUDGET='4')
                result = subprocess.run(['bash', '-c', script], cwd=root, env=env, text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertTrue((root / 'consumer-ran').exists())
                self.assertEqual((root / 'python-headers').exists(), binding)


if __name__ == "__main__":
    unittest.main()
