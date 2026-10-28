"""有状态的请求边界替身；保存字节和 ID，禁止真实远端访问。"""

import hashlib
import io
import json
import threading
from urllib.parse import unquote, urlsplit

import requests
from requests_toolbelt.multipart.decoder import MultipartDecoder


GH = "/repos/xxf1ow/xxautolib"
GT = "/api/v5/repos/fixture/mirror"
TAG = "fixed/release"
TOKEN = "fixture-gitee-private-token"


def response(status, data, headers=None):
    result = requests.Response()
    result.status_code = status
    result._content = data if isinstance(data, bytes) else json.dumps(data).encode()
    result._content_consumed = True
    result.headers.update(headers or {})
    return result


class Remote:
    def __init__(self):
        self.lock = threading.RLock()
        self.release = {"id": 42, "tag_name": TAG, "name": "Keep title", "body": "说明\n"}
        self.sources, self.source_bytes, self.targets, self.target_bytes = [], {}, [], {}
        self.calls, self.writes, self.asset_downloads = [], [], []
        self.overrides = {}
        self.before_request = None
        self.next_id = 100
        self.add_source("lz4-linux-x86_64.tar.zst", b"new\x00asset")

    def add_source(self, name, data=b"new", identity=None):
        identity = identity or len(self.sources) + 1
        asset = {"id": identity, "name": name, "size": len(data), "state": "uploaded",
                 "digest": "sha256:" + hashlib.sha256(data).hexdigest(),
                 "url": f"https://api.github.com{GH}/releases/assets/{identity}"}
        self.sources.append(asset)
        self.source_bytes[identity] = data
        return asset

    def add_target(self, name, data=b"old", identity=None):
        identity = identity or self.next_id
        self.next_id = max(self.next_id + 1, identity + 1)
        target = {"id": identity, "name": name, "size": len(data),
                  "browser_download_url": f"https://gitee.com/fixture/download/{identity}"}
        self.targets.append(target)
        self.target_bytes[identity] = data
        return target

    def asset(self, name):
        return next(a for a in self.targets if a["name"] == name)

    def inject(self, method, path, *outcomes):
        self.overrides[(method.upper(), path)] = list(outcomes)

    def request(self, session, method, url, **kwargs):
        method = method.upper()
        parsed = urlsplit(url)
        call = {"method": method, "path": parsed.path, "host": parsed.hostname,
                "kwargs": kwargs, "session": session, "thread": threading.get_ident()}
        with self.lock:
            self.calls.append(call)
            if method in ("POST", "DELETE", "PATCH"):
                self.writes.append(call)
            scripted = self.overrides.get((method, parsed.path), [])
            outcome = scripted.pop(0) if scripted else None
        if self.before_request:
            self.before_request(call)
        if isinstance(outcome, Exception):
            raise outcome
        if callable(outcome):
            return outcome(call)
        if outcome is not None:
            return response(*outcome)
        with self.lock:
            return self.dispatch(call)

    def dispatch(self, call):
        method, path, kwargs = call["method"], call["path"], call["kwargs"]
        if call["host"] == "api.github.com":
            if method == "GET" and path.startswith(GH + "/releases/tags/"):
                assert unquote(path.split("/tags/", 1)[1]) == TAG
                return response(200, {"id": 9, "tag_name": TAG, "assets_url": f"https://api.github.com{GH}/releases/9/assets"})
            if method == "GET" and path == GH + "/releases/9/assets":
                page = kwargs["params"]["page"]
                return response(200, self.sources[(page - 1) * 100:page * 100])
            if method == "GET" and path.startswith(GH + "/releases/assets/"):
                identity = int(path.rsplit("/", 1)[1])
                asset = next((a for a in self.sources if a["id"] == identity), None)
                if asset is None:
                    return response(404, {"message": "not found"})
                self.asset_downloads.append(asset["name"])
                return response(200, self.source_bytes[identity])
        if call["host"] == "gitee.com":
            if method == "GET" and path == "/api/v5/user":
                return response(200, {"id": 5, "login": "publisher"})
            if method == "GET" and path == GT:
                return response(200, {"id": 8, "full_name": "fixture/mirror", "default_branch": "gitee-default"})
            if method == "GET" and path.startswith(GT + "/releases/tags/"):
                assert unquote(path.split("/tags/", 1)[1]) == TAG
                return response(200, self.release)
            if method == "POST" and path == GT + "/releases":
                data = kwargs["data"]
                assert data["target_commitish"] == "gitee-default"
                self.release = {"id": 42, "tag_name": data["tag_name"], "name": data["name"], "body": data["body"]}
                return response(201, self.release)
            if method == "GET" and path == GT + "/releases/42/attach_files":
                page = kwargs["params"]["page"]
                return response(200, self.targets[(page - 1) * 100:page * 100])
            if method == "DELETE" and path.startswith(GT + "/releases/42/attach_files/"):
                identity = int(path.rsplit("/", 1)[1])
                if identity not in self.target_bytes:
                    return response(404, {"message": "not found"})
                self.targets[:] = [a for a in self.targets if a["id"] != identity]
                del self.target_bytes[identity]
                return response(204, b"")
            if method == "POST" and path == GT + "/releases/42/attach_files":
                encoder = kwargs["data"]
                parts = MultipartDecoder(encoder.to_string(), encoder.content_type).parts
                files = [part for part in parts if b"filename=" in part.headers.get(b"Content-Disposition", b"")]
                assert len(files) == 1
                part = files[0]
                name = part.headers[b"Content-Disposition"].decode().split('filename="', 1)[1].split('"', 1)[0]
                return response(201, self.add_target(name, part.content))
            if method == "PATCH" and path == GT + "/releases/42":
                data = kwargs["data"]
                assert set(data) == {"access_token", "body"}
                self.release = dict(self.release, body=data["body"])
                return response(200, self.release)
        raise AssertionError(f"Unexpected fixture request: {method} {call['host']} {path}")
