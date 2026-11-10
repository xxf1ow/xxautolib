"""只服务固定 GitHub/Gitee 域名的本地 TLS 代理；未匹配请求直接失败。"""

import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import ssl
import subprocess
import threading
from urllib.parse import parse_qs, unquote, urlsplit

from requests_toolbelt.multipart.decoder import MultipartDecoder

GH = '/repos/xxf1ow/xxautolib/releases'
GT = '/api/v5/repos/fixture/mirror'
TOKEN = 'fixture-gitee-private-token'


class Remote:
    def __init__(self, directory):
        self.lock = threading.RLock()
        self.sources, self.source_bytes, self.targets, self.target_bytes = [], {}, [], {}
        self.calls, self.downloads, self.uploads = [], [], []
        self.next_id = 100
        self.fail_entry = False
        self.fail_deletes, self.fail_uploads = set(), set()
        self.lose_upload = None
        self.pause_uploads = {}
        self.errors = []
        directory = Path(directory)
        self.cert = directory / 'fixture.pem'
        key = directory / 'fixture.key'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                        '-keyout', str(key), '-out', str(self.cert), '-subj', '/CN=api.github.com',
                        '-addext', 'subjectAltName=DNS:api.github.com,DNS:uploads.github.com,DNS:github.com,DNS:gitee.com'],
                       check=True, capture_output=True)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(self.cert, key)
        remote = self
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args): pass
            def do_CONNECT(self):
                if self.path not in {'api.github.com:443', 'uploads.github.com:443', 'github.com:443', 'gitee.com:443'}:
                    self.send_error(403); return
                self.send_response(200); self.end_headers()
                self.wfile.flush()
                connection = context.wrap_socket(self.connection, server_side=True)
                try: Handler(connection, self.client_address, self.server)
                finally: connection.close()
            def serve(self):
                body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
                try:
                    with remote.lock:
                        status, data = remote.dispatch(self.command, self.headers['Host'], self.path, body, self.headers)
                except Exception as error:
                    remote.errors.append(repr(error))
                    status, data = 500, {'error': 'fixture request rejected'}
                if status is None:
                    self.close_connection = True
                    return
                if self.command == 'POST' and isinstance(data, dict):
                    pause = remote.pause_uploads.get(data.get('name'))
                    if pause is not None:
                        entered, release = pause
                        entered.set()
                        release.wait(10)
                data = data if isinstance(data, bytes) else json.dumps(data).encode()
                self.send_response(status)
                self.send_header('Content-Length', str(len(data)))
                self.send_header('Content-Type', 'application/json')
                try:
                    self.end_headers(); self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError, ssl.SSLEOFError):
                    pass
            do_GET = do_POST = do_DELETE = serve
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def close(self):
        self.server.shutdown(); self.server.server_close(); self.thread.join()

    def environment(self):
        address = 'http://127.0.0.1:' + str(self.server.server_port)
        return {'HTTPS_PROXY': address, 'https_proxy': address, 'HTTP_PROXY': address, 'http_proxy': address,
                'ALL_PROXY': address, 'all_proxy': address, 'NO_PROXY': '', 'no_proxy': '',
                'SSL_CERT_FILE': str(self.cert), 'REQUESTS_CA_BUNDLE': str(self.cert)}

    def add_source(self, name, data):
        self.sources[:] = [a for a in self.sources if a['name'] != name]
        self.next_id += 1
        asset = dict(id=self.next_id, name=name, size=len(data), state='uploaded',
                     digest='sha256:' + hashlib.sha256(data).hexdigest())
        self.sources.append(asset); self.source_bytes[asset['id']] = data
        return asset

    def add_target(self, name, data):
        self.next_id += 1
        asset = dict(id=self.next_id, name=name, size=len(data),
            browser_download_url='https://gitee.com/fixture/mirror/releases/download/fixed/' + name)
        self.targets.append(asset); self.target_bytes[asset['id']] = data
        return asset

    def target(self, name):
        return next(a for a in self.targets if a['name'] == name)

    def dispatch(self, method, host, address, body, headers):
        parsed = urlsplit(address); path, query = unquote(parsed.path), parse_qs(parsed.query)
        self.calls.append((method, host, path))
        if host == 'github.com' and method == 'GET' and path.startswith('/xxf1ow/xxautolib/releases/download/autolib-cache/'):
            assert headers.get('Authorization') is None
            name = path.rsplit('/', 1)[1]
            asset = next((a for a in self.sources if a['name'] == name), None)
            if asset is None: return 404, b''
            return 200, self.source_bytes[asset['id']]
        if host == 'api.github.com':
            assert headers['Authorization'] == 'Bearer fixture-github-token'
            assert TOKEN not in str(headers)
            if method == 'GET' and path == GH + '/tags/autolib-cache':
                return 200, dict(id=9, tag_name='autolib-cache', immutable=False, draft=False)
            if method == 'GET' and path == GH + '/9/assets':
                page = int(query['page'][0]); return 200, self.sources[(page-1)*100:page*100]
            if path.startswith(GH + '/assets/'):
                identity = int(path.rsplit('/', 1)[1])
                if method == 'DELETE':
                    self.sources[:] = [a for a in self.sources if a['id'] != identity]
                    return 204, b''
                assert method == 'GET'
                asset = next(a for a in self.sources if a['id'] == identity)
                self.downloads.append(asset['name']); return 200, self.source_bytes[identity]
        if host == 'gitee.com':
            if method == 'GET' and path == '/api/v5/user': return 200, dict(id=5, login='publisher')
            if method == 'GET' and path == GT:
                return 200, dict(id=8, full_name='fixture/mirror', private=True, default_branch='server-default', permission={'push': True})
            if method == 'GET' and path == GT + '/releases/tags/fixed':
                return 200, dict(id=42, tag_name='fixed', name='keep title', body='keep body')
            if method == 'GET' and path == GT + '/releases/42/attach_files':
                page = int(query['page'][0]); return 200, self.targets[(page-1)*100:page*100]
            if method == 'DELETE' and path.startswith(GT + '/releases/42/attach_files/'):
                identity = int(path.rsplit('/', 1)[1])
                if next(a for a in self.targets if a['id'] == identity)['name'] in self.fail_deletes: return 403, {}
                self.targets[:] = [a for a in self.targets if a['id'] != identity]
                return 204, b''
            if method == 'POST' and path == GT + '/releases/42/attach_files':
                parts = MultipartDecoder(body, headers['Content-Type']).parts
                assert parts[0].content == TOKEN.encode()
                part = parts[1]; name = part.headers[b'Content-Disposition'].decode().split('filename="')[1][:-1]
                self.uploads.append(name)
                if name in self.fail_uploads: return 403, {}
                if self.fail_entry and name == 'autolib.cmake': return 403, {}
                asset = self.add_target(name, part.content)
                if self.lose_upload == name:
                    self.lose_upload = None; return None, None
                return 201, asset
        raise AssertionError((method, host, path))
