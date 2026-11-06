import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

from test_darwin_workflow import job, step_script


ROOT = Path(__file__).resolve().parents[2]
DRIVER = ROOT / "tests/autolib/run-eigen-portability.cmake"


class EigenPortabilityTests(unittest.TestCase):
    def test_container_jobs_use_container_workspace_and_verify_checkout(self):
        workflow = (ROOT / ".github/workflows/validate-eigen-portability.yml").read_text()
        for name in ("produce", "linux-pcl"):
            with self.subTest(job=name), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary).resolve()
                tools = root / "bin"
                tools.mkdir()
                apt = tools / "apt-get"
                apt.write_text("#!/bin/sh\nexit 0\n")
                apt.chmod(0o755)
                subprocess.run(["git", "init", "-q", str(root)], check=True)
                subprocess.run(["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                                "commit", "--allow-empty", "-qm", "fixture"], cwd=root, check=True)
                sha = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
                env = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ["PATH"],
                           WORK_ROOT="/host-only/workspace/build/ci/123-2/linux-x86_64",
                           GITHUB_WORKSPACE=str(root), GITHUB_ENV=str(root / "github-env"),
                           GITHUB_RUN_ID="123", GITHUB_RUN_ATTEMPT="2", PLATFORM="linux-x86_64",
                           GITHUB_SHA=sha, GITHUB_REPOSITORY="fixture/repo", GITHUB_REF="refs/heads/task")
                native = job(workflow, name)
                boot = subprocess.run(["/bin/bash", "-euc", step_script(native, "Bootstrap container")],
                                      env=env, cwd=root, text=True, capture_output=True)
                self.assertEqual(boot.returncode, 0, boot.stderr)
                self.assertTrue((root / "github-env").is_file(), "container work root was not exported")
                self.assertIn(f"WORK_ROOT={root}/build/ci/123-2/linux-x86_64", (root / "github-env").read_text())
                env.update(GIT_TEST_ASSUME_DIFFERENT_OWNER="1", GIT_CONFIG_GLOBAL=str(root / "empty-global"),
                           GIT_CONFIG_NOSYSTEM="1")
                identity = subprocess.run([os.sys.executable, "-c", step_script(native, "Record source identity")],
                                          env=env, cwd=root, text=True, capture_output=True)
                self.assertEqual(identity.returncode, 0, identity.stderr)
                self.assertEqual(json.loads((root / "build/ci-input/run.json").read_text())["checkout_sha"], sha)

    def test_windows_and_darwin_consumers_use_linux_archives_in_both_locations(self):
        workflow = ROOT / ".github/workflows/validate-eigen-portability.yml"
        for name, platform, kind in (("windows", "windows-x86_64", "eigen"),
                                     ("darwin", "darwin-aarch64", "eigen"),
                                     ("windows", "windows-x86_64", "pcl"),
                                     ("linux-pcl", "linux-x86_64", "pcl"),
                                     ("linux-pcl", "linux-aarch64", "pcl")):
            with self.subTest(platform=platform, kind=kind), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                raw = root / "build/linux-eigen"
                raw.mkdir(parents=True)
                manifest = {}
                for version in ("3.4.0", "5.0.1"):
                    archive = raw / f"eigen-{version}.tar.gz"
                    payload = f"Linux produced Eigen {version}".encode()
                    with tarfile.open(archive, "w:gz") as stream:
                        member = tarfile.TarInfo(f"eigen/{version}/payload")
                        member.size = len(payload)
                        stream.addfile(member, io.BytesIO(payload))
                    manifest[version] = dict(filename=archive.name, sha256=hashlib.sha256(archive.read_bytes()).hexdigest())
                (raw / "manifest.json").write_text(json.dumps(manifest))
                identity = dict(repository="fixture/repo", sha="abc", run_id="123", attempt="1")
                (raw / "producer.json").write_text(json.dumps(identity))
                (root / "build/ci-input").mkdir()
                (root / "build/ci-input/run.json").write_text(json.dumps(identity))
                native = root / "build/native"
                install = native / "install"
                (install / "eigen/3.4.0").mkdir(parents=True)
                (install / "eigen/3.4.0/payload").write_text("wrong native Eigen")
                for file in ("autolib.cmake", f"autolib-importer-{platform}" + (".exe" if name == "windows" else "")):
                    (install / file).write_text("native tools")
                if kind == "pcl":
                    pcl = install / "pcl/pcl-1.15.1"
                    pcl.mkdir(parents=True)
                    (pcl / "autolib-import.cmake").write_text("native PCL fixture")
                package = root / "src/dependencies/eigen/package.toml"
                package.parent.mkdir(parents=True)
                package.write_text('latest = "5.0.1"\n')
                driver = root / "tests/autolib"
                driver.mkdir(parents=True)
                shutil.copy2(DRIVER, driver / DRIVER.name)
                (driver / "run-installed.cmake").write_text('''list(GET MODULES 0 eigen)
string(REGEX REPLACE "eigen@" "" version "${eigen}")
if(ROOT_PACKAGES MATCHES "^pcl@")
  if(EXISTS "$ENV{WORK_ROOT}/install")
    message(FATAL_ERROR "Native install must be hidden during PCL relocation validation")
  endif()
  if(NOT MODULES STREQUAL "eigen@3.4.0;pcl@pcl-1.15.1")
    message(FATAL_ERROR "PCL must consume Eigen3.4 only")
  endif()
  file(READ "${INSTALL_ROOT}/pcl/pcl-1.15.1/autolib-import.cmake" native)
  if(NOT native STREQUAL "native PCL fixture")
    message(FATAL_ERROR "Native PCL was not copied")
  endif()
endif()
if("$ENV{FORCE_FIXTURE_FAIL}" STREQUAL "1")
  message(FATAL_ERROR "Requested fixture failure")
endif()
file(READ "${INSTALL_ROOT}/eigen/${version}/payload" payload)
if(NOT payload STREQUAL "Linux produced Eigen ${version}")
  message(FATAL_ERROR "Consumed wrong Eigen payload: ${payload}")
endif()
file(MAKE_DIRECTORY "${VALIDATION_ROOT}")
file(WRITE "${VALIDATION_ROOT}/checked.txt" "${payload}")
''')
                script = step_script(job(workflow.read_text(), name), "Consume original Linux archives")
                env = dict(os.environ, GITHUB_WORKSPACE=str(root), WORK_ROOT=str(native), PLATFORM=platform,
                           VALIDATION_KIND=kind, CMAKE_COMMAND=shutil.which("cmake"), GENERATOR="Unix Makefiles",
                           CC="unused-fixture-compiler", CXX="unused-fixture-compiler")
                result = subprocess.run([os.sys.executable, "-c", script], env=env, cwd=root, text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                runs = list((root / f"build/portability/{platform}/{kind}").glob("run-*"))
                self.assertEqual(len(runs), 1)
                for location in ("initial", "relocated"):
                    for version in (("3.4.0",) if kind == "pcl" else ("3.4.0", "5.0.1")):
                        self.assertEqual((runs[0] / f"consumers/{location}/{version}/checked.txt").read_text(),
                                         f"Linux produced Eigen {version}")
                if kind == "pcl":
                    self.assertFalse((runs[0] / "consumers/initial/5.0.1").exists())
                    self.assertFalse((runs[0] / "consumers/relocated/5.0.1").exists())
                self.assertEqual((install / "eigen/3.4.0/payload").read_text(), "wrong native Eigen")
                if kind == "pcl" and name == "windows":
                    env["FORCE_FIXTURE_FAIL"] = "1"
                    failed = subprocess.run([os.sys.executable, "-c", script], env=env, cwd=root, text=True, capture_output=True)
                    self.assertNotEqual(failed.returncode, 0)
                    self.assertIn("Requested fixture failure", failed.stderr)
                    self.assertEqual((install / "eigen/3.4.0/payload").read_text(), "wrong native Eigen")
                    self.assertTrue((install / "pcl/pcl-1.15.1/autolib-import.cmake").is_file())

    def test_pcl_requires_native_package_before_creating_output(self):
        self.check_preflight("pcl", "Missing Eigen portability input")

    def test_producer_source_identity_must_match_consumer(self):
        self.check_preflight("identity", "Eigen identities must use the same repository and source SHA")

    def check_preflight(self, scenario, expected):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source"
            install = root / "native"
            install.mkdir()
            for name in ("autolib.cmake", "autolib-importer-darwin-aarch64"):
                (install / name).write_text("native fixture")
            package = source / "src/dependencies/eigen/package.toml"
            package.parent.mkdir(parents=True)
            package.write_text('latest = "5.0.1"\n')
            validation = source / "build/validation"
            validation.mkdir(parents=True)
            (validation / "keep").write_text("existing data")
            args = [f"-DSOURCE_ROOT={source}", f"-DINSTALL_ROOT={install}",
                    f"-DVALIDATION_ROOT={validation}", "-DGENERATOR=Unix Makefiles",
                    "-DPLATFORM=darwin-aarch64"]
            for identity in ("PRODUCER", "CONSUMER"):
                path = root / f"{identity}.json"
                path.write_text(json.dumps(dict(repository="fixture/repo", run_id="123", attempt="1",
                                               sha="wrong" if scenario == "identity" and identity == "PRODUCER" else "abc")))
                args.append(f"-D{identity}_IDENTITY={path}")
            for version, label in (("3.4.0", "340"), ("5.0.1", "501")):
                archive = root / f"eigen-{version}.tar.gz"
                with tarfile.open(archive, "w:gz") as stream:
                    member = tarfile.TarInfo(f"eigen/{version}/payload")
                    member.size = 7
                    stream.addfile(member, io.BytesIO(b"payload"))
                args += [f"-DEIGEN_{label}_ARCHIVE={archive}",
                         f"-DEIGEN_{label}_SHA256={hashlib.sha256(archive.read_bytes()).hexdigest()}"]
            if scenario == "pcl":
                args.append("-DPCL_VERSION=pcl-1.15.1")
            result = subprocess.run(["cmake", *args, "-P", str(DRIVER)], text=True, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(expected, result.stdout + result.stderr)
            self.assertEqual(sorted(p.name for p in validation.iterdir()), ["keep"])
            self.assertEqual((validation / "keep").read_text(), "existing data")

    def test_all_hashes_are_checked_before_extraction(self):
        for bad_version in ("340", "501"):
            with self.subTest(version=bad_version), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                source = root / "source"
                install = root / "native-tools"
                install.mkdir()
                (install / "keep").write_text("native data")
                validation = source / "build/validation"
                validation.mkdir(parents=True)
                (validation / "keep").write_text("validation data")
                args = [f"-DSOURCE_ROOT={source}", f"-DINSTALL_ROOT={install}",
                        f"-DVALIDATION_ROOT={validation}", "-DGENERATOR=Unix Makefiles",
                        "-DPLATFORM=darwin-aarch64"]
                identity = root / "identity.json"
                identity.write_text('{"repository":"fixture/repo","sha":"abc","run_id":"123","attempt":"1"}')
                args += [f"-DPRODUCER_IDENTITY={identity}", f"-DCONSUMER_IDENTITY={identity}"]
                for version, label in (("3.4.0", "340"), ("5.0.1", "501")):
                    archive = root / f"eigen-{version}.tar.gz"
                    with tarfile.open(archive, "w:gz") as stream:
                        member = tarfile.TarInfo(f"eigen/{version}/payload")
                        member.size = 7
                        stream.addfile(member, io.BytesIO(b"payload"))
                    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
                    args += [f"-DEIGEN_{label}_ARCHIVE={archive}",
                             f"-DEIGEN_{label}_SHA256={'0' * 64 if label == bad_version else digest}"]
                result = subprocess.run(["cmake", *args, "-P", str(DRIVER)], text=True, capture_output=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(f"SHA256 mismatch for Eigen {bad_version}", result.stdout + result.stderr)
                self.assertEqual(sorted(p.name for p in validation.iterdir()), ["keep"])
                self.assertEqual((validation / "keep").read_text(), "validation data")
                self.assertEqual((install / "keep").read_text(), "native data")

    def test_missing_input_is_rejected_before_creating_output(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            result = subprocess.run(
                ["cmake", f"-DSOURCE_ROOT={root}", f"-DINSTALL_ROOT={root}/native",
                 f"-DVALIDATION_ROOT={root}/build/new", "-DGENERATOR=Unix Makefiles",
                 "-DPLATFORM=darwin-aarch64", "-P", str(DRIVER)], text=True, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("run-eigen-portability requires EIGEN_340_ARCHIVE", result.stdout + result.stderr)
            self.assertFalse((root / "build").exists())


if __name__ == "__main__":
    unittest.main()
