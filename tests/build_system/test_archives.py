from contextlib import contextmanager
from dataclasses import replace
import hashlib
from http.server import BaseHTTPRequestHandler, HTTPServer
import io
import json
import tarfile
import threading
from typing import Iterator
from urllib.error import URLError
from urllib.request import urlopen
import unittest

from test_cmake import PipelineCase


@contextmanager
def _http(routes: dict[str, bytes]) -> Iterator[tuple[str, list[str], threading.Thread]]:
    requests = []

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            requests.append(self.path)
            if self.path not in routes:
                self.send_error(404)
                return
            payload = routes[self.path]
            self.send_response(200)
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, format, *args):
            pass

    server = HTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_address[1]}", requests, thread
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


def archive_source(urls: list[str], payload: bytes) -> str:
    digest = hashlib.sha256(payload).hexdigest()
    return f'URL={json.dumps(urls)}\nURL_HASH="SHA256={digest}"\n'


class ArchiveTests(PipelineCase):
    def test_cached_archives_reuse_across_builds_and_copy_complete_directories(self):
        payload = self.archive({"include/x.h": "header", "lib/runtime.bin": "runtime", "LICENSE": "license"})
        with _http({"/package.tar.xz": payload}) as (base, requests, thread):
            module = self.definition('[source]\n' + archive_source([base + "/package.tar.xz"], payload) +
                'DOWNLOAD_NAME="package.tar.xz"\n[[copy]]\ndirectories=["."]\ndestination="."\n', kind="copy")
            self.project(module)
            self.build()
            self.assertEqual(requests, ["/package.tar.xz"])
            cached = self.source / "git/package.tar.xz"
            self.assertEqual(cached.read_bytes(), payload)
            requests.clear()
            self.context = replace(self.context, build_root=self.source / "fresh build")
            self.project(module)
            self.build()
            self.assertEqual(requests, [])
            for path, expected in (("include/x.h", "header"), ("lib/runtime.bin", "runtime"), ("LICENSE", "license")):
                self.assertEqual((self.source / "3rdparty/demo" / path).read_text(), expected)
            cached.write_bytes(b"wrong digest")
            self.context = replace(self.context, build_root=self.source / "third build")
            self.project(module)
            self.build()
            self.assertEqual(requests, ["/package.tar.xz"])
            self.assertEqual(cached.read_bytes(), payload)

    def archive(self, files: dict[str, str]) -> bytes:
        output = io.BytesIO()
        with tarfile.open(fileobj=output, mode="w:xz") as archive:
            for name, content in files.items():
                data = content.encode("utf-8")
                member = tarfile.TarInfo(f"fixture/{name}")
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
        return output.getvalue()

    def child_archive(self, header: str = "archive header") -> bytes:
        return self.archive({"header.h": header, "CMakeLists.txt": '''
cmake_minimum_required(VERSION 3.20)
project(child NONE)
install(FILES header.h DESTINATION include)
'''})

    def test_http_fixture_cleans_up_after_exception(self):
        with self.assertRaisesRegex(RuntimeError, "fixture failure"):
            with _http({}) as (base, requests, thread):
                raise RuntimeError("fixture failure")
        self.assertFalse(thread.is_alive())
        with self.assertRaises(URLError):
            urlopen(base, timeout=1)

    def test_primary_archive_extracts_single_root_and_installs(self):
        payload = self.child_archive()
        with _http({"/first.tar.xz": payload, "/backup.tar.xz": payload}) as (base, requests, thread):
            module = self.definition('[source]\n' + archive_source(
                [base + "/first.tar.xz", base + "/backup.tar.xz"], payload))
            self.project(module)
            self.assertEqual(requests, [])
            self.build()
            self.assertIn("/first.tar.xz", requests)
            self.assertNotIn("/backup.tar.xz", requests)
            self.assertTrue((self.context.build_root / "ep/demo/src/CMakeLists.txt").is_file())
            self.assertFalse((self.context.build_root / "ep/demo/src/fixture").exists())
            self.assertEqual((self.source / "3rdparty/demo/include/header.h").read_text(), "archive header")

    def test_archive_falls_back_after_404(self):
        payload = self.child_archive()
        with _http({"/backup.tar.xz": payload}) as (base, requests, thread):
            module = self.definition('[source]\n' + archive_source(
                [base + "/missing.tar.xz", base + "/backup.tar.xz"], payload))
            self.project(module)
            self.build()
            self.assertLess(requests.index("/missing.tar.xz"), requests.index("/backup.tar.xz"))
            self.assertEqual((self.source / "3rdparty/demo/include/header.h").read_text(), "archive header")

    def test_archive_falls_back_after_hash_mismatch(self):
        payload = self.child_archive()
        wrong = self.child_archive("untrusted header")
        with _http({"/wrong.tar.xz": wrong, "/backup.tar.xz": payload}) as (base, requests, thread):
            module = self.definition('[source]\n' + archive_source(
                [base + "/wrong.tar.xz", base + "/backup.tar.xz"], payload))
            self.project(module)
            self.build()
            self.assertLess(requests.index("/wrong.tar.xz"), requests.index("/backup.tar.xz"))
            self.assertEqual((self.source / "3rdparty/demo/include/header.h").read_text(), "archive header")

    def test_all_archive_urls_fail_without_install(self):
        payload = self.child_archive()
        with _http({}) as (base, requests, thread):
            module = self.definition('[source]\n' + archive_source(
                [base + "/missing.tar.xz", base + "/backup.tar.xz"], payload))
            self.project(module)
            self.assertNotEqual(self.build(success=False).returncode, 0)
            self.assertIn("/missing.tar.xz", requests)
            self.assertIn("/backup.tar.xz", requests)
            self.assertFalse((self.source / "3rdparty/demo/include/header.h").exists())
            self.assertFalse((self.context.build_root / "ep/demo/src/CMakeLists.txt").exists())

    def test_all_archive_hashes_fail_without_install(self):
        payload = self.child_archive()
        wrong = self.child_archive("untrusted header")
        with _http({"/wrong.tar.xz": wrong, "/backup.tar.xz": wrong}) as (base, requests, thread):
            module = self.definition('[source]\n' + archive_source(
                [base + "/wrong.tar.xz", base + "/backup.tar.xz"], payload))
            self.project(module)
            self.assertNotEqual(self.build(success=False).returncode, 0)
            self.assertIn("/wrong.tar.xz", requests)
            self.assertIn("/backup.tar.xz", requests)
            self.assertFalse((self.source / "3rdparty/demo/include/header.h").exists())
            self.assertFalse((self.context.build_root / "ep/demo/src/CMakeLists.txt").exists())

    def test_archive_resource_installs_from_its_own_directory(self):
        payload = self.archive({"header.h": "resource header"})
        with _http({"/extra.tar.xz": payload}) as (base, requests, thread):
            module = self.definition('[resources.extra]\n' + archive_source(
                [base + "/extra.tar.xz"], payload) + '''
[[copy]]
resource="extra"
files=["header.h"]
destination="include"
''', kind="copy")
            self.project(module)
            self.assertEqual(requests, [])
            self.build()
            self.assertEqual((self.context.build_root / "ep/demo/res/extra/src/header.h").read_text(), "resource header")
            self.assertFalse((self.context.build_root / "ep/demo/src/header.h").exists())
            self.assertEqual((self.source / "3rdparty/demo/include/header.h").read_text(), "resource header")


if __name__ == "__main__":
    unittest.main()
