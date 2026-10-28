"""在 Session 请求边界验证实际 API 适配、multipart、重试与连接所有权。"""

import contextlib
from datetime import datetime, timedelta, timezone
from email.utils import format_datetime
import importlib
import io
from pathlib import Path
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch

import requests

from sync_fixture import GH, GT, TAG, TOKEN, Remote, response


ACTION = Path(__file__).resolve().parents[3] / ".github/actions/gitee-release"


class APITests(unittest.TestCase):
    def setUp(self):
        self.assertTrue((ACTION / "sync_api.py").is_file(), "sync API not implemented")
        sys.path.insert(0, str(ACTION))
        self.addCleanup(sys.path.remove, str(ACTION))
        self.module = importlib.import_module("sync_api")
        self.remote = Remote()
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.file = Path(self.directory.name) / "asset.bin"
        self.source = self.remote.sources[0]
        self.file.write_bytes(self.remote.source_bytes[self.source["id"]])
        self.waits = []
        self.stack = contextlib.ExitStack()
        self.addCleanup(self.stack.close)
        self.stack.enter_context(patch.object(requests.Session, "request", autospec=True, side_effect=self.remote.request))
        self.stack.enter_context(patch.object(self.module.time, "sleep", side_effect=self.waits.append))
        self.logs = io.StringIO()
        self.stack.enter_context(contextlib.redirect_stdout(self.logs))
        self.api = self.module.SyncAPI("fixture-github-token", TOKEN, "fixture/mirror")
        self.addCleanup(self.api.close)

    def calls(self, method, path):
        return [c for c in self.remote.calls if c["method"] == method and c["path"] == path]

    def test_release_missing_is_only_200_null(self):
        self.remote.release = None
        self.assertIsNone(self.api.target_release(TAG))
        release = self.api.create_release(TAG)
        self.assertEqual(release["id"], 42)
        self.assertEqual(release["tag_name"], TAG)
        self.assertEqual(self.remote.release["body"], TAG)
        self.remote.inject("GET", GT + "/releases/tags/fixed%2Frelease", (404, {}))
        with self.assertRaises(self.module.SyncAPIError):
            self.api.target_release(TAG)
        self.assertEqual(len(self.calls("POST", GT + "/releases")), 1)

    def test_response_fields_and_codes_cannot_guess_success(self):
        path = GT + "/releases/tags/fixed%2Frelease"
        for status, data in ((200, []), (200, {"id": True}), (200, dict(self.remote.release, tag_name="wrong")),
                             (201, self.remote.release), (200, b"not-json")):
            with self.subTest(status=status, data=data):
                self.remote.inject("GET", path, (status, data))
                with self.assertRaises(self.module.SyncAPIError) as raised:
                    self.api.target_release(TAG)
                self.assertEqual(raised.exception.outcome, "failed")
        self.assertEqual(self.remote.writes, [])

    def test_upload_success_code_without_id_is_unknown(self):
        path = GT + "/releases/42/attach_files"
        for field, value in (("id", None), ("id", True), ("size", -1), ("size", 900), ("name", "other"),
                             ("browser_download_url", "http://bad")):
            result = {"id": 101, "name": self.source["name"], "size": self.source["size"],
                      "browser_download_url": "https://gitee.com/download"}
            result[field] = value
            before = len(self.calls("POST", path))
            self.remote.inject("POST", path, (201, result))
            with self.subTest(field=field, value=value), self.assertRaises(self.module.SyncAPIError) as raised:
                self.api.upload_asset(42, self.source, self.file)
            self.assertEqual(raised.exception.outcome, "unknown")
            self.assertEqual(len(self.calls("POST", path)), before + 1)

    def test_metadata_pagination_and_multipart(self):
        for i in range(100):
            self.remote.add_source(f"pkg-{i}-any-any.tar.zst")
            self.remote.add_target(f"old-{i}.bin")
        self.remote.add_target("last.bin")
        self.assertEqual(len(self.api.source_assets(TAG)), 101)
        self.assertEqual(len(self.api.target_assets(42)), 101)
        for path in (GH + "/releases/9/assets", GT + "/releases/42/attach_files"):
            self.assertEqual([c["kwargs"]["params"]["page"] for c in self.calls("GET", path)], [1, 2])
        target = self.api.upload_asset(42, self.source, self.file)
        self.assertEqual(self.remote.target_bytes[target["id"]], b"new\x00asset")
        self.api.delete_asset(42, target["id"])
        self.assertNotIn(target["id"], self.remote.target_bytes)

    def test_bad_list_is_shared_failure_but_named_bad_fields_are_preserved(self):
        path = GT + "/releases/42/attach_files"
        for data in ({}, [None], [{"id": 1}], [{"name": ""}]):
            self.remote.inject("GET", path, (200, data))
            with self.subTest(data=data), self.assertRaises(self.module.SyncAPIError):
                self.api.target_assets(42)
        named_bad = {"name": self.source["name"], "id": None}
        self.remote.inject("GET", path, (200, [named_bad]))
        self.assertEqual(self.api.target_assets(42), [named_bad])

    def test_query_retry_limit_and_waits(self):
        path = GH + "/releases/tags/fixed%2Frelease"
        self.remote.inject("GET", path, requests.ReadTimeout(TOKEN), (503, {}), (502, {}), (504, {}), (503, {}))
        self.assertEqual(self.api.source_assets(TAG), self.remote.sources)
        self.assertEqual(len(self.calls("GET", path)), 6)
        self.assertEqual(self.waits, [2, 4, 8, 16, 32])
        self.remote.inject("GET", path, *[(503, {})] * 6)
        with self.assertRaises(self.module.SyncAPIError):
            self.api.source_assets(TAG)
        self.assertEqual(len(self.calls("GET", path)), 12)
        self.assertEqual(self.waits, [2, 4, 8, 16, 32] * 2)
        self.assertNotIn(TOKEN, self.logs.getvalue())

    def test_non_retryable_query_errors(self):
        path = GH + "/releases/tags/fixed%2Frelease"
        for error in (requests.exceptions.SSLError(TOKEN), (403, {}), (200, b"bad-json")):
            self.remote.inject("GET", path, error)
            before = len(self.calls("GET", path))
            with self.subTest(error=type(error).__name__), self.assertRaises(self.module.SyncAPIError):
                self.api.source_assets(TAG)
            self.assertEqual(len(self.calls("GET", path)), before + 1)
        self.assertEqual(self.waits, [])

    def test_write_unknown_is_never_replayed(self):
        for method, path, operate in (
                ("POST", GT + "/releases/42/attach_files", lambda: self.api.upload_asset(42, self.source, self.file)),
                ("DELETE", GT + "/releases/42/attach_files/7", lambda: self.api.delete_asset(42, 7)),
                ("POST", GT + "/releases", lambda: self.api.create_release(TAG))):
            for error in (requests.ReadTimeout(TOKEN), (503, {}), (302, {}, {"Location": "https://other.invalid"})):
                self.remote.inject(method, path, error)
                before = len(self.calls(method, path))
                with self.subTest(method=method, error=type(error).__name__), self.assertRaises(self.module.SyncAPIError) as raised:
                    operate()
                self.assertEqual(raised.exception.outcome, "unknown")
                self.assertEqual(len(self.calls(method, path)), before + 1)

    def test_body_retry_reuses_payload(self):
        path = GT + "/releases/42"
        self.remote.inject("PATCH", path, requests.ReadTimeout(TOKEN), (503, {}))
        self.api.update_body(self.remote.release, "new body")
        self.assertEqual(self.remote.release["body"], "new body")
        self.assertEqual([c["kwargs"]["data"] for c in self.calls("PATCH", path)],
                         [{"access_token": TOKEN, "tag_name": TAG, "name": "Keep title", "body": "new body"}] * 3)
        self.assertEqual(self.remote.release["tag_name"], TAG)
        self.assertEqual(self.remote.release["name"], "Keep title")
        self.assertEqual(self.waits, [2, 4])
        for field in ("body", "name", "tag_name", "id"):
            self.remote.inject("PATCH", path, (200, dict(self.remote.release, **{field: "wrong"})))
            before = len(self.calls("PATCH", path))
            with self.subTest(field=field), self.assertRaises(self.module.SyncAPIError) as raised:
                self.api.update_body(self.remote.release, "new body")
            self.assertEqual(raised.exception.outcome, "unknown")
            self.assertEqual(len(self.calls("PATCH", path)), before + 1)

    def test_body_explicit_rejection_is_not_retried(self):
        path = GT + "/releases/42"
        self.remote.inject("PATCH", path, (400, {}))
        with self.assertRaises(self.module.SyncAPIError) as raised:
            self.api.update_body(self.remote.release, "new body")
        self.assertEqual(raised.exception.outcome, "failed")
        self.assertEqual(len(self.calls("PATCH", path)), 1)
        self.assertEqual(self.waits, [])

    def test_upload_and_body_retry_budgets_are_independent(self):
        path = GT + "/releases/42/attach_files"
        retry_after = iter((2, 4, 8, 16, 32))

        def consume_rejected_upload(call):
            call["kwargs"]["data"].to_string()
            return response(429, {}, {"Retry-After": str(next(retry_after))})

        self.remote.inject("POST", path, *[consume_rejected_upload] * 5)
        target = self.api.upload_asset(42, self.source, self.file)
        self.assertEqual(self.remote.target_bytes[target["id"]], b"new\x00asset")
        self.assertEqual(len(self.calls("POST", path)), 6)
        self.assertEqual(self.waits, [2, 4, 8, 16, 32])
        body_path = GT + "/releases/42"
        self.remote.inject("PATCH", body_path, *[requests.ReadTimeout(TOKEN)] * 5)
        self.api.update_body(self.remote.release, "new body")
        self.assertEqual(self.remote.release["body"], "new body")
        self.assertEqual(len(self.calls("PATCH", body_path)), 6)
        self.assertEqual(self.waits, [2, 4, 8, 16, 32] * 2)
        self.remote.inject("POST", path, *[(429, {})] * 6)
        with self.assertRaises(self.module.SyncAPIError) as raised:
            self.api.upload_asset(42, self.source, self.file)
        self.assertEqual(raised.exception.outcome, "failed")
        self.assertEqual(len(self.calls("POST", path)), 12)
        self.assertEqual(len(self.remote.targets), 1)
        self.remote.inject("PATCH", body_path, *[requests.ReadTimeout(TOKEN)] * 6)
        with self.assertRaises(self.module.SyncAPIError) as raised:
            self.api.update_body(self.remote.release, "unconfirmed body")
        self.assertEqual(raised.exception.outcome, "unknown")
        self.assertEqual(len(self.calls("PATCH", body_path)), 12)
        self.assertEqual(self.waits, [2, 4, 8, 16, 32] * 4)
        for retry_after in ("30", "not-a-date", format_datetime(datetime.now(timezone.utc) + timedelta(seconds=60))):
            self.remote.inject("POST", path, (429, {}, {"Retry-After": retry_after}))
            before = len(self.calls("POST", path))
            with self.subTest(retry_after=retry_after), self.assertRaises(self.module.SyncAPIError) as raised:
                self.api.upload_asset(42, self.source, self.file)
            self.assertEqual(raised.exception.outcome, "failed")
            self.assertEqual(len(self.calls("POST", path)), before + 1)
        self.assertEqual(self.api.rate_limited, 14)

    def test_download_integrity_and_timeouts(self):
        self.api.download(self.source, self.file)
        self.assertEqual(self.file.read_bytes(), b"new\x00asset")
        call = self.remote.calls[-1]
        self.assertEqual(call["kwargs"]["timeout"], (10, 300))
        self.assertNotIn(TOKEN, str(call["kwargs"]))
        self.remote.source_bytes[self.source["id"]] = b"corrupt"
        with self.assertRaises(self.module.SyncAPIError):
            self.api.download(self.source, self.file)
        self.assertFalse(self.file.exists())
        self.api.target_release(TAG)
        self.assertEqual(self.remote.calls[-1]["kwargs"]["timeout"], (10, 30))
        for recorded in self.remote.calls:
            self.assertFalse(recorded["kwargs"]["allow_redirects"])
            self.assertTrue(recorded["kwargs"]["verify"])

    def test_download_redirect_allows_https_but_not_downgrade(self):
        path = GH + "/releases/assets/1"
        self.remote.inject("GET", path, (302, {}, {"Location": "http://insecure.invalid/asset"}))
        with self.assertRaises(self.module.SyncAPIError):
            self.api.download(self.source, self.file)
        self.assertFalse(any(c["host"] == "insecure.invalid" for c in self.remote.calls))
        self.remote.inject("GET", path, (302, {}, {"Location": "https://cdn.invalid/asset"}))
        self.remote.inject("GET", "/asset", (200, b"new\x00asset"))
        self.api.download(self.source, self.file)
        self.assertEqual(self.file.read_bytes(), b"new\x00asset")
        self.assertNotIn("Authorization", self.remote.calls[-1]["kwargs"]["headers"])

    def test_interrupted_stream_restarts_file_and_digest(self):
        def interrupted(call):
            result = response(200, b"")

            def chunks(size):
                yield b"partial bytes"
                raise requests.exceptions.ChunkedEncodingError(TOKEN)

            result.iter_content = chunks
            return result

        self.remote.inject("GET", GH + "/releases/assets/1", interrupted)
        self.api.download(self.source, self.file)
        self.assertEqual(self.file.read_bytes(), b"new\x00asset")
        self.assertEqual(len(self.calls("GET", GH + "/releases/assets/1")), 2)
        self.assertEqual(self.waits, [2])

    def test_sessions_are_worker_local(self):
        barrier = threading.Barrier(2)
        errors = []

        def work():
            try:
                barrier.wait(timeout=5)
                self.api.target_assets(42)
                self.api.target_assets(42)
            except BaseException as error:
                errors.append(error)

        with patch.object(requests.Session, "close", autospec=True) as close:
            threads = [threading.Thread(target=work) for _ in range(2)]
            for thread in threads:
                thread.start()
            for thread in threads:
                thread.join(timeout=10)
            self.assertEqual(errors, [])
            self.assertTrue(all(not t.is_alive() for t in threads))
            by_thread = {}
            for call in self.remote.calls:
                by_thread.setdefault(call["thread"], []).append(id(call["session"]))
            self.assertEqual(len(by_thread), 2)
            values = [set(sessions) for sessions in by_thread.values()]
            self.assertTrue(all(len(sessions) == 1 for sessions in values))
            self.assertNotEqual(values[0], values[1])
            self.api.close()
            self.api.close()
            self.assertEqual(close.call_count, 2)


if __name__ == "__main__":
    unittest.main()
