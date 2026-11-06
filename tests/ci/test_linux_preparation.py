import json
from itertools import product
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import unittest


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
                                                         "system_dependencies": {"apt": []}}]}))
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


if __name__ == "__main__":
    unittest.main()
