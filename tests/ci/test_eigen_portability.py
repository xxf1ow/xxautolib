import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
DRIVER = ROOT / "tests/autolib/run-eigen-portability.cmake"


class EigenPortabilityTests(unittest.TestCase):
    def test_pcl_requires_native_package_before_creating_output(self):
        self.check_preflight("pcl", "Missing Eigen portability input")

    def test_producer_source_identity_must_match_consumer(self):
        self.check_preflight("identity", "Eigen producer must match the expected repository and source SHA")

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
