"""真实协调入口的副作用与重跑；网络只替换在 Session 请求边界。"""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import sys
import threading
import unittest
from unittest.mock import patch

import requests

from sync_fixture import GH, GT, TAG, TOKEN, Remote
from test_sync_rules import body_for


ACTION = Path(__file__).resolve().parents[3] / ".github/actions/gitee-release"


class SyncTests(unittest.TestCase):
    def setUp(self):
        self.assertTrue((ACTION / "sync.py").is_file(), "sync coordinator not implemented")
        sys.path.insert(0, str(ACTION))
        self.addCleanup(sys.path.remove, str(ACTION))
        spec = importlib.util.spec_from_file_location("release_sync", ACTION / "sync.py")
        self.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.module)
        import sync_rules
        self.rules = sync_rules
        self.remote = Remote()
        self.stack = contextlib.ExitStack()
        self.addCleanup(self.stack.close)
        self.stack.enter_context(patch.object(requests.Session, "request", autospec=True,
                                             side_effect=lambda *args, **kwargs: self.remote.request(*args, **kwargs)))
        self.stack.enter_context(patch.object(os, "sched_getaffinity", return_value={0, 1}, create=True))
        self.stack.enter_context(patch("sync_api.time.sleep"))
        self.stdout, self.stderr = io.StringIO(), io.StringIO()
        self.stack.enter_context(contextlib.redirect_stdout(self.stdout))
        self.stack.enter_context(contextlib.redirect_stderr(self.stderr))
        self.environment = {"GITEE_REPOSITORY": "fixture/mirror", "GITEE_RELEASE_TAG": TAG,
                            "GITEE_TOKEN": TOKEN, "GITHUB_TOKEN": "fixture-github-private-token",
                            "SYNC_PACKAGES": ""}

    def run_sync(self, packages=""):
        return self.module.sync_release(self.module.resolve_config(dict(self.environment, SYNC_PACKAGES=packages)))

    def writes(self, method):
        return [c for c in self.remote.writes if c["method"] == method]

    def indexed_target(self):
        source = self.remote.sources[0]
        target = self.remote.add_target(source["name"], self.remote.source_bytes[source["id"]], identity=7)
        self.remote.release["body"] = body_for({source["name"]: {
            "sha256": source["digest"][7:], "gitee_asset_id": target["id"]}})
        return source, target

    def test_unmatched_input_never_creates_missing_release(self):
        self.remote.release = None
        with self.assertRaises(ValueError):
            self.run_sync("lz4,missing")
        self.assertEqual(self.remote.writes, [])

    def test_bad_source_and_configuration_have_zero_writes(self):
        for fields in ({"digest": None}, {"state": "new"}, {"name": "../bad"}, {"id": True}):
            original = self.remote.sources[0]
            self.remote.sources[0] = dict(original, **fields)
            with self.subTest(fields=fields), self.assertRaises(ValueError):
                self.run_sync()
            self.remote.sources[0] = original
            self.assertEqual(self.remote.writes, [])
        for fields in ({"GITEE_REPOSITORY": "wrong"}, {"GITEE_RELEASE_TAG": "bad..tag"},
                       {"GITEE_TOKEN": ""}, {"GITHUB_TOKEN": ""}):
            with self.subTest(fields=fields), self.assertRaises(ValueError):
                self.module.resolve_config(dict(self.environment, **fields))
        defaults = self.module.resolve_config({"GITEE_TOKEN": TOKEN, "GITHUB_TOKEN": "gh"})
        self.assertEqual(defaults["repository"], "xxf1ow/autolib-release")
        self.assertEqual(defaults["tag"], "autolib-release")

    def test_shared_metadata_error_and_bad_index_never_modify_target(self):
        for method, path, outcome in (
                ("GET", GH + "/releases/tags/fixed%2Frelease", (404, {})),
                ("GET", GT + "/releases/42/attach_files", (503, {})),
                ("GET", "/api/v5/user", (401, {}))):
            self.remote.inject(method, path, outcome, outcome, outcome)
            with self.subTest(path=path), self.assertRaises(self.module.SyncAPIError):
                self.run_sync()
            self.remote.overrides.clear()
            self.assertEqual(self.remote.writes, [])
        self.remote.release["body"] = "<!-- autolib-gitee-sync:start -->"
        with self.assertRaises(ValueError):
            self.run_sync()
        self.assertEqual(self.remote.writes, [])

    def test_initial_creation_and_then_zero_byte_skip(self):
        self.remote.release = None
        result = self.run_sync()
        name = self.remote.sources[0]["name"]
        self.assertEqual(result["uploaded"], [name])
        self.assertEqual(result["body_status"], "saved")
        self.assertEqual(len([c for c in self.remote.writes if c["path"] == GT + "/releases"]), 1)
        self.assertEqual(self.remote.target_bytes[self.remote.asset(name)["id"]], b"new\x00asset")
        self.remote.writes.clear()
        self.remote.asset_downloads.clear()
        with patch.object(self.module, "ThreadPoolExecutor", side_effect=AssertionError("no work should not create a pool")):
            result = self.run_sync()
        self.assertEqual(result["skipped"], [name])
        self.assertEqual(result["workers"], 0)
        self.assertEqual(self.remote.writes, [])
        self.assertEqual(self.remote.asset_downloads, [])

    def test_unchanged_assets_transfer_zero_bytes(self):
        source, target = self.indexed_target()
        result = self.run_sync()
        self.assertEqual(result["skipped"], [source["name"]])
        self.assertEqual(result["uploaded"], [])
        self.assertEqual(self.remote.asset_downloads, [])
        self.assertEqual(self.remote.writes, [])

    def test_partial_existing_without_index_and_unrelated_description(self):
        source = self.remote.sources[0]
        old = self.remote.add_target(source["name"])
        unrelated = self.remote.add_target("unrelated.bin", b"keep")
        self.remote.add_source("queue-any-any.tar.zst", b"queue")
        result = self.run_sync()
        self.assertEqual(result["failed"], [])
        self.assertEqual(len(result["uploaded"]), 2)
        self.assertNotEqual(self.remote.asset(source["name"])["id"], old["id"])
        self.assertEqual(self.remote.target_bytes[unrelated["id"]], b"keep")
        self.assertTrue(self.remote.release["body"].startswith("说明\n"))
        self.assertEqual(self.remote.release["name"], "Keep title")
        writes = [c["method"] for c in self.remote.writes if "attach_files" in c["path"]]
        self.assertLess(writes.index("DELETE"), writes.index("POST"))
        self.assertEqual(len(self.writes("PATCH")), 1)

    def test_changed_digest_id_or_size_cannot_skip(self):
        for change in ("digest", "id", "size"):
            self.remote = Remote()
            source, target = self.indexed_target()
            self.remote.calls.clear()
            if change == "digest":
                self.remote.release["body"] = body_for({source["name"]: {"sha256": "b" * 64, "gitee_asset_id": target["id"]}})
            elif change == "id":
                target["id"] = 8
                self.remote.target_bytes[8] = self.remote.target_bytes.pop(7)
            else:
                target["size"] += 1
            with self.subTest(change=change):
                result = self.run_sync()
                self.assertEqual(result["uploaded"], [source["name"]])
                self.assertEqual(result["skipped"], [])

    def test_duplicate_or_invalid_target_fails_only_affected_name(self):
        source, target = self.indexed_target()
        self.remote.add_target(source["name"], b"duplicate")
        self.remote.add_source("queue-any-any.tar.zst", b"queue")
        result = self.run_sync()
        self.assertEqual(result["failed"], [source["name"]])
        self.assertEqual(result["uploaded"], ["queue-any-any.tar.zst"])
        self.assertEqual(self.writes("DELETE"), [])
        self.assertEqual(len([a for a in self.remote.targets if a["name"] == source["name"]]), 2)
        self.assertEqual(self.rules.parse_index(self.remote.release["body"])[source["name"]]["gitee_asset_id"], 7)

    def test_download_corruption_keeps_old_target(self):
        source = self.remote.sources[0]
        old = self.remote.add_target(source["name"], b"old")
        self.remote.source_bytes[source["id"]] = b"corrupt"
        result = self.run_sync()
        self.assertEqual(result["failed"], [source["name"]])
        self.assertEqual(self.remote.target_bytes[old["id"]], b"old")
        self.assertEqual(self.remote.writes, [])

    def test_delete_failure_prevents_upload(self):
        source = self.remote.sources[0]
        old = self.remote.add_target(source["name"])
        self.remote.inject("DELETE", GT + f'/releases/42/attach_files/{old["id"]}', (403, {}))
        result = self.run_sync()
        self.assertEqual(result["failed"], [source["name"]])
        self.assertEqual(self.writes("POST"), [])
        self.assertEqual(self.remote.target_bytes[old["id"]], b"old")

    def test_disappeared_source_does_not_chase_replacement(self):
        path = GH + "/releases/assets/1"
        self.remote.inject("GET", path, (404, {}))
        old = self.remote.add_target(self.remote.sources[0]["name"])
        result = self.run_sync()
        self.assertEqual(result["failed"], [self.remote.sources[0]["name"]])
        self.assertEqual(self.remote.target_bytes[old["id"]], b"old")
        self.assertEqual(self.remote.writes, [])

    def test_failed_body_commit_rerun_replaces_unrecorded_id(self):
        self.remote.inject("PATCH", GT + "/releases/42", (403, {}))
        first = self.run_sync()
        name = self.remote.sources[0]["name"]
        first_id = self.remote.asset(name)["id"]
        self.assertEqual(first["body_status"], "failed")
        self.assertEqual(self.rules.parse_index(self.remote.release["body"]), {})
        second = self.run_sync()
        self.assertEqual(second["body_status"], "saved")
        self.assertNotEqual(self.remote.asset(name)["id"], first_id)
        self.assertEqual(self.rules.parse_index(self.remote.release["body"])[name]["gitee_asset_id"], self.remote.asset(name)["id"])
        self.remote.asset_downloads.clear()
        self.remote.writes.clear()
        self.assertEqual(self.run_sync()["skipped"], [name])
        self.assertEqual(self.remote.asset_downloads, [])
        self.assertEqual(self.remote.writes, [])

    def test_saved_body_with_lost_response_is_reusable_on_rerun(self):
        def save_then_lose(call):
            self.remote.dispatch(call)
            raise requests.ReadTimeout(TOKEN)

        self.remote.inject("PATCH", GT + "/releases/42", save_then_lose, save_then_lose, save_then_lose)
        result = self.run_sync()
        self.assertEqual(result["body_status"], "unknown")
        self.remote.writes.clear()
        self.remote.asset_downloads.clear()
        name = self.remote.sources[0]["name"]
        self.assertEqual(self.run_sync()["skipped"], [name])
        self.assertEqual(self.remote.writes, [])
        self.assertEqual(self.remote.asset_downloads, [])

    def test_unconfirmed_upload_does_not_write_success_record(self):
        def upload_then_lose(call):
            self.remote.dispatch(call)
            raise requests.ReadTimeout(TOKEN)

        self.remote.inject("POST", GT + "/releases/42/attach_files", upload_then_lose)
        result = self.run_sync()
        name = self.remote.sources[0]["name"]
        first_id = self.remote.asset(name)["id"]
        self.assertEqual(result["failed"], [name])
        self.assertEqual(result["uploaded"], [])
        self.assertEqual(self.rules.parse_index(self.remote.release["body"]), {})
        self.assertEqual(len(self.writes("POST")), 1)
        self.assertEqual(self.run_sync()["uploaded"], [name])
        self.assertNotEqual(self.remote.asset(name)["id"], first_id)

    def test_missing_after_failed_upload_and_partial_success_records(self):
        source = self.remote.sources[0]
        self.remote.add_source("queue-any-any.tar.zst", b"queue")
        self.remote.add_source("flann-linux-x86_64.tar.zst", b"flann")
        self.remote.add_source("not-selected-any-any.tar.zst", b"unselected")
        self.remote.add_target(source["name"], b"old", identity=7)
        old_records = {source["name"]: {"sha256": "b" * 64, "gitee_asset_id": 7},
                       "not-selected-any-any.tar.zst": {"sha256": "c" * 64, "gitee_asset_id": 88},
                       "unknown.bin": {"sha256": "d" * 64, "gitee_asset_id": 99}}
        self.remote.release["body"] = body_for(old_records)
        self.remote.inject("POST", GT + "/releases/42/attach_files", (403, {}))
        with patch.object(os, "sched_getaffinity", return_value={0}):
            result = self.run_sync("lz4,queue,flann")
        self.assertEqual(result["failed"], [source["name"]])
        self.assertEqual(result["uploaded"], ["queue-any-any.tar.zst", "flann-linux-x86_64.tar.zst"])
        self.assertFalse(any(a["name"] == source["name"] for a in self.remote.targets))
        records = self.rules.parse_index(self.remote.release["body"])
        for name, entry in old_records.items():
            self.assertEqual(records[name], entry)
        self.assertEqual(len(self.writes("PATCH")), 1)
        second = self.run_sync("lz4,queue,flann")
        self.assertEqual(second["uploaded"], [source["name"]])
        self.assertEqual(second["skipped"], ["queue-any-any.tar.zst", "flann-linux-x86_64.tar.zst"])

    def test_two_workers_make_progress_after_failure(self):
        self.remote.add_source("queue-any-any.tar.zst", b"queue")
        self.remote.add_source("flann-linux-x86_64.tar.zst", b"flann")
        ready, release = threading.Event(), threading.Event()
        lock = threading.Lock()
        state = {"active": 0, "started": 0, "maximum": 0}

        def block_download(call):
            if not call["path"].startswith(GH + "/releases/assets/"):
                return
            with lock:
                state["active"] += 1
                state["started"] += 1
                state["maximum"] = max(state["maximum"], state["active"])
                if state["started"] == 2:
                    ready.set()
            try:
                if not release.wait(timeout=10):
                    raise AssertionError("download gate not released")
            finally:
                with lock:
                    state["active"] -= 1
            if call["path"].endswith("/1"):
                raise requests.ConnectionError(TOKEN)

        self.remote.before_request = block_download
        result, errors = [], []

        def run():
            try:
                result.append(self.run_sync())
            except BaseException as error:
                errors.append(error)

        thread = threading.Thread(target=run)
        thread.start()
        try:
            self.assertTrue(ready.wait(timeout=5))
            with lock:
                self.assertEqual(state["active"], 2)
                self.assertEqual(state["started"], 2)
        finally:
            release.set()
            thread.join(timeout=15)
        self.assertFalse(thread.is_alive())
        self.assertEqual(errors, [])
        self.assertLessEqual(state["maximum"], 2)
        self.assertEqual(result[0]["failed"], [self.remote.sources[0]["name"]])
        self.assertEqual(result[0]["workers"], 2)
        records = self.rules.parse_index(self.remote.release["body"])
        self.assertEqual(len(records), 2)
        for source in self.remote.sources[1:]:
            asset = self.remote.asset(source["name"])
            self.assertEqual(self.remote.target_bytes[asset["id"]], self.remote.source_bytes[source["id"]])
            self.assertEqual(records[source["name"]]["gitee_asset_id"], asset["id"])
        self.assertEqual(len(self.writes("PATCH")), 1)

    def test_main_exit_status_and_secret_redaction(self):
        with patch.dict(os.environ, self.environment):
            self.assertEqual(self.module.main(), 0)
        name = self.remote.sources[0]["name"]
        self.remote.targets.clear()
        self.remote.inject("POST", GT + "/releases/42/attach_files", requests.ReadTimeout("https://gitee.com/?access_token=" + TOKEN))
        with patch.dict(os.environ, self.environment):
            self.assertEqual(self.module.main(), 1)
        self.assertNotIn(TOKEN, self.stdout.getvalue() + self.stderr.getvalue())
        self.assertNotIn("access_token", self.stdout.getvalue() + self.stderr.getvalue())
        summaries = [json.loads(line) for line in self.stdout.getvalue().splitlines() if '"event": "summary"' in line]
        self.assertEqual(summaries[-1]["failed"], [name])
        self.assertEqual(summaries[-1]["counts"]["failed"], 1)
        self.assertIn("elapsed_seconds", summaries[-1])
        self.assertIn("rate_limited", summaries[-1])

    def test_git_validation_child_has_no_tokens(self):
        import sync_api
        real_run = sync_api.subprocess.run
        environments = []

        def run(*args, **kwargs):
            environments.append(kwargs["env"])
            return real_run(*args, **kwargs)

        with patch.dict(os.environ, self.environment), patch.object(sync_api.subprocess, "run", side_effect=run):
            self.run_sync()
        self.assertGreaterEqual(len(environments), 2)
        for environment in environments:
            self.assertNotIn("GITEE_TOKEN", environment)
            self.assertNotIn("GITHUB_TOKEN", environment)


if __name__ == "__main__":
    unittest.main()
