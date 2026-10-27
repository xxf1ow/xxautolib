"""固定上游真实入口的最小基线；请求替身禁止访问远端。"""

import contextlib
import io
import json
import os
from pathlib import Path
import runpy
import tempfile
import unittest
from unittest.mock import patch


ACTION = Path(__file__).resolve().parents[3] / ".github/actions/gitee-release"


class UpstreamBaseline(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.file = Path(self.directory.name) / "fixture.bin"
        self.file.write_bytes(b"new\x00asset")
        self.assets = {"fixture.bin": b"old", "unrelated.bin": b"keep"}
        self.writes = []

    def request(self, method, url, **kwargs):
        import requests
        from requests_toolbelt.multipart.decoder import MultipartDecoder

        response = requests.Response()
        response.status_code = 200
        if method == "get" and "/releases/tags/" in url:
            data = {"id": 42, "tag_name": "fixture"}
        elif method == "get" and url.endswith("/attach_files"):
            data = [{"id": 7, "name": "fixture.bin", "size": 3,
                     "uploader": {}, "browser_download_url": "https://example.invalid/fixture.bin"}]
        elif method == "delete" and url.endswith("/attach_files/7"):
            self.writes.append("DELETE attachment:7")
            del self.assets["fixture.bin"]
            response.status_code = 204
            data = None
        elif method == "post" and url.endswith("/attach_files"):
            self.writes.append("POST attachment")
            encoder = kwargs["data"]
            parts = MultipartDecoder(encoder.to_string(), encoder.content_type).parts
            files = [part for part in parts if b'filename="fixture.bin"' in part.headers.get(b"Content-Disposition", b"")]
            self.assertEqual(len(files), 1)
            self.assets["fixture.bin"] = files[0].content
            response.status_code = 201
            data = {"browser_download_url": "https://example.invalid/fixture.bin"}
        elif method == "post" and url.endswith("/releases"):
            self.writes.append("POST release")
            self.assertEqual(kwargs["data"]["target_commitish"], "mirror-default")
            response.status_code = 201
            data = {"id": 42}
        else:
            self.fail("unexpected request: " + method + " " + url)
        response._content = json.dumps(data).encode()
        return response

    def run_original(self, operation):
        self.assertTrue((ACTION / "gitee_release.py").is_file(), "fixed upstream source has not been vendored")
        environment = {
            "gitee_action": operation, "gitee_owner": "fixture", "gitee_repo": "mirror",
            "gitee_token": "fake-baseline-token", "gitee_tag_name": "fixture",
            "gitee_release_name": "fixture", "gitee_release_body": "fixture",
            "gitee_target_commitish": "mirror-default", "gitee_upload_retry_times": "0",
            "gitee_file_name": "fixture.bin", "gitee_file_path": str(self.file),
            "gitee_old_asset_name": "fixture.bin", "gitee_new_file_path": str(self.file),
        }
        if operation == "create_release":
            environment.pop("gitee_file_name")
            environment.pop("gitee_file_path")
        output = io.StringIO()
        with patch.dict(os.environ, environment, clear=True), \
                patch("requests.sessions.Session.request", side_effect=self.request), \
                contextlib.redirect_stdout(output):
            runpy.run_path(str(ACTION / "gitee_release.py"), run_name="__main__")
        return output.getvalue()

    def test_create_release_original_entry(self):
        self.assertIn("release-id=42", self.run_original("create_release"))
        self.assertEqual(self.writes, ["POST release"])

    def test_upload_asset_original_entry(self):
        self.run_original("upload_asset")
        self.assertEqual(self.writes, ["POST attachment"])
        self.assertEqual(self.assets["fixture.bin"], b"new\x00asset")

    def test_update_asset_original_entry(self):
        self.run_original("update_asset")
        self.assertEqual(self.writes, ["DELETE attachment:7", "POST attachment"])
        self.assertEqual(self.assets, {"fixture.bin": b"new\x00asset", "unrelated.bin": b"keep"})


if __name__ == "__main__":
    unittest.main()
