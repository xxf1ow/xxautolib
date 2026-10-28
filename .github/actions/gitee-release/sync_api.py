"""同步专用 API 与有界等待；复用固定 Gitee 请求构造和 multipart。"""

from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
import threading
import time
from urllib.parse import quote, urlsplit, urljoin

import requests

from gitee_release import Gitee
from sync_rules import valid_id, valid_https, validate_target


GH = "https://api.github.com/repos/xxf1ow/xxautolib"
TRANSIENT = {429, 502, 503, 504}
REJECTED = {400, 401, 403, 404, 405, 409, 422, 429}
RETRY_DELAYS = (2, 4, 8, 16, 32)


class SyncAPIError(Exception):
    """只暴露受控操作名、状态码和确认状态，不携带服务端文本或令牌。"""

    def __init__(self, operation, status=None, outcome="failed"):
        self.operation, self.status, self.outcome = operation, status, outcome
        super().__init__(operation + ":" + outcome)


def valid_ref(prefix, value):
    if not isinstance(value, str) or not value or any(ord(c) < 32 or ord(c) == 127 for c in value):
        return False
    environment = {k: v for k, v in os.environ.items() if k.upper() not in {"GITEE_TOKEN", "GITHUB_TOKEN"}}
    return subprocess.run(["git", "check-ref-format", prefix + value], env=environment,
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0


class SyncAPI:
    """每线程拥有连接；所有附件任务结束后由协调器调用 close。"""

    def __init__(self, github_token: str, gitee_token: str, repository: str):
        self.github_token, self.gitee_token, self.repository = github_token, gitee_token, repository
        owner, self.repo = repository.split("/")
        self.client = Gitee(owner, gitee_token, request=self._gitee_request)
        self._local = threading.local()
        self._sessions = []
        self._lock = threading.Lock()
        self._repository_info = None
        self.rate_limited = 0

    def _session(self):
        if not hasattr(self._local, "session"):
            session = requests.Session()
            self._local.session = session
            with self._lock:
                self._sessions.append(session)
        return self._local.session

    def close(self) -> None:
        """关闭所有拥有的 Session；不与仍在运行的任务并发调用。"""
        with self._lock:
            sessions, self._sessions = self._sessions, []
        for session in sessions:
            session.close()

    def _gitee_request(self, method, url, **kwargs):
        transfer = method == "POST" and url.endswith("/attach_files")
        return self._session().request(method, url, timeout=(10, 300 if transfer else 30),
                                       allow_redirects=False, verify=True, **kwargs)

    def _github_request(self, url, **kwargs):
        headers = {"Accept": "application/vnd.github+json", "Authorization": "Bearer " + self.github_token}
        return self._session().request("GET", url, headers=headers, timeout=(10, 30),
                                       allow_redirects=False, verify=True, **kwargs)

    def _emit(self, operation, event, started, attempt, **fields):
        record = dict(operation=operation, event=event, attempt=attempt,
                      elapsed_seconds=round(time.monotonic() - started, 3), **fields)
        with self._lock:
            print(json.dumps(record), flush=True)

    def _retry_allowed(self, status, headers, attempt):
        if status != 429 or "Retry-After" not in headers:
            return True
        try:
            value = headers["Retry-After"]
            seconds = int(value) if value.isdigit() else (
                parsedate_to_datetime(value) - datetime.now(timezone.utc)).total_seconds()
            return seconds <= RETRY_DELAYS[attempt - 1]
        except (ValueError, TypeError, OverflowError):
            return False

    def _run(self, operation, send, expected, read, *, write=False, repeatable=False):
        for attempt in range(1, len(RETRY_DELAYS) + 2):
            started, status, headers, retry = time.monotonic(), None, {}, False
            self._emit(operation, "start", started, attempt)
            try:
                with send() as result:
                    status, headers = result.status_code, result.headers
                    if status == 429:
                        with self._lock:
                            self.rate_limited += 1
                    if status != expected:
                        error = SyncAPIError(operation, status, "unknown" if write and status not in REJECTED else "failed")
                        retry = status == 429 or repeatable and status in TRANSIENT
                    else:
                        value = read(result)
                        self._emit(operation, "end", started, attempt, status=status, outcome="confirmed")
                        return value
            except requests.exceptions.SSLError:
                error = SyncAPIError(operation, status, "unknown" if write else "failed")
            except (requests.ConnectionError, requests.Timeout, requests.exceptions.ChunkedEncodingError):
                error = SyncAPIError(operation, status, "unknown" if write else "failed")
                retry = repeatable
            except requests.RequestException:
                error = SyncAPIError(operation, status, "unknown" if write else "failed")
            except (ValueError, TypeError, KeyError):
                error = SyncAPIError(operation, status, "unknown" if write else "failed")
            except OSError:
                error = SyncAPIError(operation, status)
            self._emit(operation, "end", started, attempt, status=status, outcome=error.outcome)
            if not retry or attempt > len(RETRY_DELAYS) or not self._retry_allowed(status, headers, attempt):
                raise error from None
            time.sleep(RETRY_DELAYS[attempt - 1])

    def _json(self, operation, send, expected, validate, *, write=False, repeatable=False):
        return self._run(operation, send, expected, lambda response: validate(response.json()),
                         write=write, repeatable=repeatable)

    @staticmethod
    def _release(data, tag):
        if (not isinstance(data, dict) or not valid_id(data.get("id")) or data.get("tag_name") != tag
                or not isinstance(data.get("name"), str) or "body" not in data
                or data["body"] is not None and not isinstance(data["body"], str)):
            raise ValueError("invalid_release")
        return dict(data, body=data["body"] or "")

    @staticmethod
    def _assets(data):
        if not isinstance(data, list) or any(not isinstance(a, dict) or not isinstance(a.get("name"), str)
                                             or not a["name"] for a in data):
            raise ValueError("invalid_asset_list")
        return data

    def source_assets(self, tag: str) -> list[dict]:
        def release(data):
            if not isinstance(data, dict) or not valid_id(data.get("id")) or data.get("tag_name") != tag:
                raise ValueError("invalid_source_release")
            return data

        source = self._json("source_release", lambda: self._github_request(GH + "/releases/tags/" + quote(tag, safe="")),
                            200, release, repeatable=True)
        result, page = [], 1
        while True:
            current = page
            items = self._json("source_assets", lambda: self._github_request(
                f'{GH}/releases/{source["id"]}/assets', params={"page": current, "per_page": 100}),
                200, self._assets, repeatable=True)
            result.extend(items)
            if len(items) < 100:
                return result
            page += 1

    def _ensure_target(self):
        if self._repository_info is not None:
            return

        def user(data):
            if (not isinstance(data, dict) or not valid_id(data.get("id"))
                    or not isinstance(data.get("login"), str) or not data["login"]):
                raise ValueError("invalid_user")
            return data

        def repository(data):
            if (not isinstance(data, dict) or not valid_id(data.get("id"))
                    or data.get("full_name") != self.repository
                    or not valid_ref("refs/heads/", data.get("default_branch"))):
                raise ValueError("invalid_repository")
            return data

        self._json("authentication", self.client.get_user, 200, user, repeatable=True)
        self._repository_info = self._json("repository", lambda: self.client.get_repository(self.repo),
                                           200, repository, repeatable=True)

    def target_release(self, tag: str) -> dict | None:
        self._ensure_target()
        return self._json("target_release", lambda: self.client.get_release_details(self.repo, tag), 200,
                          lambda data: None if data is None else self._release(data, tag), repeatable=True)

    def create_release(self, tag: str) -> dict:
        self._ensure_target()
        return self._json("create_release", lambda: self.client.create_release_details(
            self.repo, tag, tag, body=tag, target_commitish=self._repository_info["default_branch"]),
            201, lambda data: self._release(data, tag), write=True)

    def target_assets(self, release_id: int) -> list[dict]:
        result, page = [], 1
        while True:
            current = page
            items = self._json("target_assets", lambda: self.client.list_assets(self.repo, release_id, current),
                               200, self._assets, repeatable=True)
            result.extend(items)
            if len(items) < 100:
                return result
            page += 1

    def _download_request(self, source):
        url = source["url"]
        for _ in range(31):
            headers = {"Accept": "application/octet-stream"}
            if urlsplit(url).hostname == "api.github.com":
                headers["Authorization"] = "Bearer " + self.github_token
            result = self._session().request("GET", url, headers=headers, stream=True, timeout=(10, 300),
                                              allow_redirects=False, verify=True)
            if result.status_code not in {301, 302, 303, 307, 308}:
                return result
            location = urljoin(url, result.headers.get("Location", ""))
            result.close()
            if not valid_https(location):
                raise ValueError("invalid_download_redirect")
            url = location
        raise requests.TooManyRedirects()

    def download(self, source: dict, destination: Path) -> None:
        """流式下载并核对来源摘要；失败只移除本任务的临时输出。"""
        def consume(result):
            digest, size = hashlib.sha256(), 0
            with destination.open("wb") as output:
                for chunk in result.iter_content(1024 * 1024):
                    output.write(chunk)
                    digest.update(chunk)
                    size += len(chunk)
            if size != source["size"] or digest.hexdigest() != source["digest"][7:].lower():
                raise ValueError("download_digest_mismatch")

        try:
            self._run("download", lambda: self._download_request(source), 200, consume, repeatable=True)
        except SyncAPIError:
            destination.unlink(missing_ok=True)
            raise

    def delete_asset(self, release_id: int, asset_id: int) -> None:
        self._run("delete_asset", lambda: self.client.delete_asset_details(self.repo, release_id, asset_id),
                  204, lambda response: None, write=True)

    def upload_asset(self, release_id: int, source: dict, file: Path) -> dict:
        def uploaded(data):
            validate_target(data)
            if data["name"] != source["name"] or data["size"] != source["size"]:
                raise ValueError("invalid_uploaded_asset")
            return data

        return self._json("upload_asset", lambda: self.client.upload_asset_details(
            self.repo, release_id, file_name=source["name"], file_path=str(file)), 201, uploaded, write=True)

    def update_body(self, release: dict, body: str) -> None:
        def updated(data):
            data = self._release(data, release["tag_name"])
            if data["id"] != release["id"] or data["name"] != release["name"] or data["body"] != body:
                raise ValueError("invalid_body_response")
            return data

        self._json("update_body", lambda: self.client.update_release_body(
            self.repo, release["id"], release["tag_name"], release["name"], body),
                   200, updated, write=True, repeatable=True)
