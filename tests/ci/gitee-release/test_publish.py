"""HTTP substitutes and temporary Git repositories exercise local publication."""

import contextlib
import hashlib
import importlib
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch

import requests
from requests_toolbelt.multipart.decoder import MultipartDecoder

from test_metadata_repository import IDENTITY, git

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / '.github/actions/gitee-release'))


class Server:
    def __init__(self):
        self.assets = []
        self.events = []
        self.bodies = {}
        self.delete_status = 204
        self.fail = set()
        self.missing_release = False
        self.lock = threading.Lock()

    def add_asset(self, name, body, asset_id=7):
        asset = {'id': asset_id, 'name': name, 'size': len(body),
                 'browser_download_url': 'https://gitee.com/fixture/mirror/releases/download/fixed/' + name}
        self.assets.append(asset)
        self.bodies[name] = body
        return asset

    def request(self, method, url, **options):
        with self.lock:
            self.events.append((method, url))
            status, data = 200, None
            if method == 'GET' and url.endswith('/releases/tags/fixed'):
                data = None if self.missing_release else {'id': 3, 'tag_name': 'fixed', 'name': 'fixed', 'body': 'fixed'}
            elif method == 'POST' and url.endswith('/releases'):
                assert options['json']['tag_name'] == 'fixed'
                assert len(options['json']['target_commitish']) == 40
                self.missing_release = False
                status, data = 201, {'id': 3, 'tag_name': 'fixed', 'name': 'fixed', 'body': 'fixed'}
            elif method == 'GET' and url.endswith('/attach_files'):
                data = list(self.assets)
            elif method == 'GET' and '/releases/download/fixed/' in url:
                response = requests.Response()
                response.status_code = 200
                response._content = self.bodies[url.rsplit('/', 1)[1]]
                response._content_consumed = True
                return response
            elif method == 'DELETE':
                status = self.delete_status
                if status == 204:
                    self.assets = [a for a in self.assets if str(a['id']) != url.rsplit('/', 1)[1]]
            elif method == 'POST' and url.endswith('/attach_files'):
                encoder = options['data']
                parts = MultipartDecoder(encoder.to_string(), encoder.content_type).parts
                assert len(parts) == 2
                assert parts[0].content == b'fake-secret'
                assert parts[0].headers[b'Content-Disposition'] == b'form-data; name="access_token"'
                disposition = parts[1].headers[b'Content-Disposition'].decode()
                assert disposition.startswith('form-data; name="file"; filename="')
                name = disposition.split('filename="', 1)[1][:-1]
                assert parts[1].headers[b'Content-Type'] == b'application/octet-stream'
                status = 503 if name in self.fail else 201
                data = {'id': 20 + len(self.bodies), 'name': name, 'size': len(parts[1].content),
                        'browser_download_url': 'https://gitee.com/fixture/mirror/releases/download/fixed/' + name}
                if status == 201:
                    self.bodies[name] = parts[1].content
                    self.assets.append(data)
            else:
                raise AssertionError((method, url))
            response = requests.Response()
            response.status_code = status
            response._content = json.dumps(data).encode()
            response._content_consumed = True
            return response


class PublishTests(unittest.TestCase):
    def setUp(self):
        self.assertIsNotNone(importlib.util.find_spec('publish'), 'local publication is not implemented')
        self.module = importlib.import_module('publish')
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.server = Server()
        self.api = self.module.GiteeAPI('fake-secret', 'fixture/mirror')
        self.addCleanup(self.api.close)
        self.transport = patch('requests.sessions.Session.request', side_effect=self.server.request)
        self.transport.start()
        self.addCleanup(self.transport.stop)
        clock = patch('time.sleep')
        self.clock = clock.start()
        self.addCleanup(clock.stop)

    def test_main_reports_input_rule_on_stderr_without_credentials(self):
        environment = {'PUBLISH_HOST': 'gitee', 'PUBLISH_MODE': 'latest', 'PUBLISH_LATEST': '[]',
                       'GITEE_REPOSITORY': 'fixture/mirror', 'GITEE_RELEASE_TAG': 'fixed',
                       'GITEE_TOKEN': 'fake-secret'}
        for latest, rule in [('[]', 'latest must be an object'),
                             ('fake-secret', 'valid UTF-8 JSON')]:
            with self.subTest(latest=latest):
                stdout, stderr = io.StringIO(), io.StringIO()
                with patch.dict(os.environ, dict(environment, PUBLISH_LATEST=latest), clear=True), \
                        contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                    self.assertEqual(self.module.main(), 1)
                self.assertEqual(stdout.getvalue(), '')
                self.assertIn(rule, stderr.getvalue())
                self.assertNotIn('fake-secret', stderr.getvalue())
        self.assertEqual(self.server.events, [])

    def asset(self, name='queue', version='1', body=b'archive\0bytes'):
        filename = f'{name}-{version}-any-any.tar.zst'
        path = self.root / filename
        path.write_bytes(body)
        return {'kind': 'library', 'name': filename, 'path': str(path), 'size': len(body),
                'sha256': hashlib.sha256(body).hexdigest(),
                'package': {'name': name, 'version': version, 'os': 'any', 'arch': 'any'}}

    def remote(self, files=None):
        seed = self.root / 'seed'
        seed.mkdir()
        git(seed, 'init', '--initial-branch=server-default')
        git(seed, 'config', 'user.name', 'fixture')
        git(seed, 'config', 'user.email', 'fixture@example.invalid')
        git(seed, 'config', 'core.autocrlf', 'false')
        for name, content in (files or {'.autolib-repository.json': json.dumps(IDENTITY).encode()}).items():
            target = seed / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(content)
        git(seed, 'add', '--all')
        git(seed, 'commit', '-m', 'Seed')
        self.bare = self.root / 'remote.git'
        git(self.root, 'clone', '--bare', str(seed), str(self.bare))

    def config(self, assets, initial, explicit=None):
        return {'host': 'gitee', 'mode': 'artifacts', 'repository': 'fixture/mirror', 'tag': 'fixed',
                'gitee_token': 'fake-secret', 'assets': assets, 'initial_latest': initial,
                'explicit_latest': explicit or {}, 'work_root': self.root / ('work-' + str(len(list(self.root.glob('work-*')))))}

    def run_release(self, config, *, created=False, break_push=False, entry=False):
        import metadata_repository
        from test_metadata_repository import RepositoryAPI
        info = RepositoryAPI(created)
        original = metadata_repository.open_repository
        def open_repo(config, api, work_root):
            repo = original(config, info, work_root)
            if break_push:
                git(repo['path'], 'remote', 'set-url', 'origin', str(self.root / 'absent.git'))
            return repo
        with patch('metadata_repository._repository_url', return_value=str(self.bare)), \
                patch('publish.open_repository', side_effect=open_repo):
            if entry:
                with patch('publish.resolve_config', return_value=config):
                    return self.module.main()
            return self.module.publish_release(config)

    def test_main_requeries_after_received_write_with_lost_response(self):
        self.remote()
        asset = self.asset()
        for method, operation in [('GET', 'target_assets'), ('GET', 'asset_sha256'),
                                  ('DELETE', 'delete_asset'), ('POST', 'upload_asset')]:
            with self.subTest(operation=operation):
                self.server.assets.clear()
                self.server.add_asset(asset['name'], b'old')
                self.server.events.clear()
                self.clock.reset_mock()
                output, errors = io.StringIO(), io.StringIO()
                lost = False
                def received_then_lost(request_method, url, **options):
                    nonlocal lost
                    response = self.server.request(request_method, url, **options)
                    selected = (operation == 'target_assets' and url.endswith('/attach_files') or
                                operation == 'asset_sha256' and '/releases/download/' in url or
                                method != 'GET')
                    if request_method == method and selected and not lost:
                        lost = True
                        response.close()
                        raise requests.Timeout('fake-secret: response lost after acceptance')
                    return response
                with patch('requests.sessions.Session.request', side_effect=received_then_lost), \
                        contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
                    self.assertEqual(self.run_release(self.config([asset], {'queue': '1'}), entry=True), 0)
                result = json.loads(output.getvalue())
                self.assertEqual((result['uploaded'], result['skipped'], result['failed']),
                                 (0, 1, 0) if method == 'POST' else (1, 0, 0))
                self.assertTrue(result['metadata_committed'])
                self.assertEqual(len(self.server.assets), 1)
                self.assertEqual(self.server.bodies[asset['name']], b'archive\0bytes')
                self.assertEqual([m for m, _ in self.server.events if m in {'DELETE', 'POST'}],
                                 ['DELETE', 'POST'])
                self.assertEqual(sum(url.endswith('/attach_files') and m == 'GET'
                                     for m, url in self.server.events), 2)
                self.assertEqual([c.args[0] for c in self.clock.call_args_list], [2])
                self.assertEqual(git(self.bare, 'show', 'HEAD:queue/1/' + asset['name'] + '.sha256'),
                                 (asset['sha256'] + '\n').encode())
                self.assertNotIn('fake-secret', output.getvalue() + errors.getvalue())

    def test_identical_remote_bytes_skip_writes_and_repair_metadata(self):
        self.remote()
        asset = self.asset()
        self.server.add_asset(asset['name'], b'archive\0bytes')
        result = self.run_release(self.config([asset], {'queue': '1'}))
        self.assertEqual((result['uploaded'], result.get('skipped', -1), result['failed']), (0, 1, 0))
        self.assertTrue(result['metadata_committed'])
        self.assertFalse(any(m in {'DELETE', 'POST'} for m, _ in self.server.events))
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/1/' + asset['name'] + '.sha256'),
                         (asset['sha256'] + '\n').encode())
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/package.toml'), b'latest = "1"\n')

    def test_attachment_retry_budget_restarts_at_query_and_caps_delay(self):
        self.remote()
        asset = self.asset()
        self.server.fail.add(asset['name'])
        result = self.run_release(self.config([asset], {'queue': '1'}))
        self.assertEqual((result['uploaded'], result['failed']), (0, 1))
        self.assertEqual([m for m, url in self.server.events if url.endswith('/attach_files')],
                         ['GET', 'POST'] * 11)
        self.assertEqual([c.args[0] for c in self.clock.call_args_list],
                         [2, 4, 8, 16, 32, 32, 32, 32, 32, 32])
        self.assertNotIn('queue/', git(self.bare, 'ls-tree', '-r', '--name-only', 'HEAD').decode())

    def test_main_reports_local_asset_failure(self):
        self.remote()
        asset = self.asset()
        Path(asset['path']).write_bytes(b'changed')
        output, errors = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            self.assertEqual(self.run_release(self.config([asset], {'queue': '1'}), entry=True), 1)
        self.assertEqual(json.loads(output.getvalue())['uploaded'], 0)
        self.assertFalse(any(method in {'DELETE', 'POST'} for method, _ in self.server.events))
        self.assertTrue(errors.getvalue(), 'main lost the local asset failure')
        self.assertEqual(json.loads(errors.getvalue()),
                         {'asset': asset['name'], 'operation': 'local_asset', 'outcome': 'failed'})

    def test_main_preserves_http_and_validation_diagnostics_without_response_body(self):
        self.remote()
        asset = self.asset()
        for status, reason in [(503, 'unexpected_http_status'), (201, 'invalid_asset_size')]:
            with self.subTest(status=status):
                self.server.assets.clear()
                self.server.fail = {asset['name']} if status == 503 else set()
                def reply(method, url, **options):
                    response = self.server.request(method, url, **options)
                    if method == 'POST' and url.endswith('/attach_files'):
                        response.status_code = status
                        response._content = json.dumps({'message': 'fake-secret'} if status == 503 else
                            dict(response.json(), size=999, message='fake-secret')).encode()
                    return response
                stdout, stderr = io.StringIO(), io.StringIO()
                with patch('requests.sessions.Session.request', side_effect=reply), \
                        contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                    self.assertEqual(self.run_release(self.config([asset], {'queue': '1'}), entry=True), 1)
                self.assertEqual(json.loads(stdout.getvalue())['uploaded'], 0)
                diagnostics = [json.loads(line) for line in stderr.getvalue().splitlines()]
                diagnostic = diagnostics[-1]
                self.assertEqual(diagnostic['asset'], asset['name'])
                self.assertEqual(diagnostic['operation'], 'upload_asset')
                self.assertEqual(diagnostic['outcome'], 'unknown')
                self.assertEqual(diagnostic.get('http_status'), status)
                self.assertEqual(diagnostic.get('reason'), reason)
                self.assertNotIn('fake-secret', stdout.getvalue() + stderr.getvalue())
                self.assertEqual(len(diagnostics), 11 if status == 503 else 1)

    def test_main_reports_unknown_repository_creation(self):
        config = self.config([], {}, {'queue': '1'})
        config['work_root'].mkdir()
        output, errors = io.StringIO(), io.StringIO()
        with patch('publish.resolve_config', return_value=config), \
                patch('publish.open_repository', side_effect=self.module.GiteeAPIError('create_repository', outcome='unknown')), \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            self.assertEqual(self.module.main(), 1)
        self.assertFalse(json.loads(output.getvalue())['metadata_committed'])
        self.assertTrue(errors.getvalue(), 'main lost the uncertain repository creation')
        self.assertEqual(json.loads(errors.getvalue()), {'operation': 'create_repository', 'outcome': 'unknown',
                         'http_status': None, 'reason': 'unspecified', 'exception_type': None})
        self.assertEqual(self.server.events, [])

    def test_main_reports_received_release_creation_with_lost_response(self):
        self.remote()
        self.server.missing_release = True
        asset = self.asset()
        def received_then_lost(method, url, **options):
            response = self.server.request(method, url, **options)
            if method == 'POST' and url.endswith('/releases'):
                response.close()
                raise requests.Timeout('fake-secret: release response lost')
            return response
        output, errors = io.StringIO(), io.StringIO()
        with patch('requests.sessions.Session.request', side_effect=received_then_lost), \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            self.assertEqual(self.run_release(self.config([asset], {'queue': '1'}), entry=True), 1)
        self.assertFalse(self.server.missing_release)
        self.assertFalse(json.loads(output.getvalue())['metadata_committed'])
        self.assertNotIn('fake-secret', output.getvalue() + errors.getvalue())
        self.assertTrue(errors.getvalue(), 'main lost the uncertain release creation')
        self.assertEqual(json.loads(errors.getvalue()), {'operation': 'create_release', 'outcome': 'unknown',
                         'http_status': None, 'reason': 'timeout', 'exception_type': 'Timeout'})

    def test_main_reports_unconfirmed_metadata_push(self):
        self.remote()
        before = git(self.bare, 'rev-parse', 'HEAD')
        asset = self.asset()
        output, errors = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            self.assertEqual(self.run_release(self.config([asset], {'queue': '1'}), break_push=True, entry=True), 1)
        result = json.loads(output.getvalue())
        self.assertEqual(result['uploaded'], 1)
        self.assertFalse(result['metadata_committed'])
        self.assertEqual(git(self.bare, 'rev-parse', 'HEAD'), before)
        self.assertEqual(self.server.bodies[asset['name']], b'archive\0bytes')
        self.assertNotIn('fake-secret', output.getvalue() + errors.getvalue())
        self.assertTrue(errors.getvalue(), 'main lost the unconfirmed metadata push')
        self.assertEqual(json.loads(errors.getvalue()), {'operation': 'metadata_commit', 'outcome': 'unconfirmed'})

    def test_replace_same_name_and_failures(self):
        asset = self.asset()
        for target, status, fail, expected in [(None, 204, False, ['POST']), (7, 204, False, ['DELETE', 'POST']),
                                              (7, 403, False, ['DELETE']), (7, 503, False, ['DELETE']),
                                              (7, 204, True, ['DELETE', 'POST'])]:
            with self.subTest(target=target, status=status, fail=fail):
                self.server.events.clear()
                self.server.assets.clear()
                if target is not None:
                    self.server.add_asset(asset['name'], b'x' * asset['size'], target)
                self.server.delete_status = status
                self.server.fail = {asset['name']} if fail else set()
                with contextlib.redirect_stderr(io.StringIO()):
                    result = self.module.publish_one(self.api, 3, asset)
                if status == 503:
                    expected = ['DELETE'] * 11
                elif fail:
                    expected = ['DELETE'] + ['POST'] * 11
                self.assertEqual([method for method, _ in self.server.events if method in {'DELETE', 'POST'}], expected)
                self.assertEqual(result['status'], 'uploaded' if status == 204 and not fail else 'failed')
                if result['status'] == 'failed':
                    self.assertNotIn('sha256', result)
        self.remote()
        self.server.assets = [{'id': 7, 'name': asset['name']}, {'id': 8, 'name': asset['name']}]
        self.server.events.clear()
        result = self.run_release(self.config([asset], {'queue': '1'}))
        self.assertEqual((result['uploaded'], result['failed']), (0, 1))
        self.assertFalse(any(method in {'DELETE', 'POST'} for method, _ in self.server.events))

    def test_success_metadata_and_latest(self):
        self.remote({'README.md': b'template', 'README.en.md': b'template'})
        self.server.missing_release = True
        assets = [self.asset('eigen', '3.4.0'), self.asset('eigen', '5.0.1'), self.asset()]
        result = self.run_release(self.config(assets, {'eigen': '3.4.0', 'queue': '1'}), created=True)
        self.assertEqual((result['selected'], result['uploaded'], result['failed']), (3, 3, 0))
        self.assertTrue(result['metadata_committed'])
        self.assertEqual(git(self.bare, 'show', 'HEAD:eigen/package.toml'), b'latest = "3.4.0"\n')
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/package.toml'), b'latest = "1"\n')
        result = self.run_release(self.config([self.asset('lz4')], {'lz4': '1'}, {'eigen': '5.0.1'}))
        self.assertTrue(result['metadata_committed'])
        self.assertEqual(git(self.bare, 'show', 'HEAD:lz4/package.toml'), b'latest = "1"\n')
        self.assertEqual(git(self.bare, 'show', 'HEAD:eigen/package.toml'), b'latest = "5.0.1"\n')
        paths = git(self.bare, 'ls-tree', '-r', '--name-only', 'HEAD').decode().splitlines()
        self.assertEqual(sum(p == 'eigen/package.toml' for p in paths), 1)
        self.assertEqual(sum(p == 'queue/package.toml' for p in paths), 1)

    def test_importer_digest_has_no_latest(self):
        self.remote()
        name = 'autolib-importer-linux-x86_64.tar.xz'
        path = self.root / name
        path.write_bytes(b'tool')
        asset = {'kind': 'importer', 'name': name, 'path': str(path), 'size': 4,
                 'sha256': hashlib.sha256(b'tool').hexdigest(), 'platform': 'linux-x86_64'}
        result = self.run_release(self.config([asset], {}))
        self.assertTrue(result['metadata_committed'])
        self.assertEqual(result['uploaded'], 1)
        self.assertEqual(git(self.bare, 'show', 'HEAD:autolib-importer/' + name + '.sha256'),
                         (hashlib.sha256(b'tool').hexdigest() + '\n').encode())
        self.assertNotIn('package.toml', git(self.bare, 'ls-tree', '-r', '--name-only', 'HEAD').decode())

    def test_entry_success_or_failure_never_registers_git_metadata(self):
        self.remote()
        path = self.root / 'autolib.cmake'
        path.write_bytes(b'entry')
        entry_asset = dict(kind='entry', name='autolib.cmake', path=str(path), size=5,
                           sha256=hashlib.sha256(b'entry').hexdigest())
        tool_name = 'autolib-importer-linux-x86_64.tar.xz'
        tool_path = self.root / tool_name
        tool_path.write_bytes(b'tool')
        importer = dict(kind='importer', name=tool_name, platform='linux-x86_64', path=str(tool_path),
                        size=4, sha256=hashlib.sha256(b'tool').hexdigest())
        for fail in [False, True]:
            with self.subTest(fail=fail):
                self.server.fail = {'autolib.cmake'} if fail else set()
                if fail:
                    self.server.assets = [a for a in self.server.assets if a['name'] != 'autolib.cmake']
                library = self.asset('queue', '2' if fail else '1')
                errors, output = io.StringIO(), io.StringIO()
                with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
                    code = self.run_release(self.config([entry_asset, library, importer], {'queue': '1'}), entry=True)
                result = json.loads(output.getvalue())
                self.assertEqual(code, int(fail))
                self.assertEqual((result['uploaded'], result['skipped'], result['failed']),
                                 (1, 1, 1) if fail else (3, 0, 0))
                self.assertTrue(result['metadata_committed'])
                paths = git(self.bare, 'ls-tree', '-r', '--name-only', 'HEAD').decode().splitlines()
                self.assertFalse(any('autolib.cmake' in name for name in paths))
                self.assertIn('queue/' + library['package']['version'] + '/' + library['name'] + '.sha256', paths)
                self.assertIn('autolib-importer/' + tool_name + '.sha256', paths)
                if not fail:
                    self.assertEqual(self.server.bodies['autolib.cmake'], b'entry')

    def test_attachment_pool_respects_cpu_affinity_and_continues_after_failure(self):
        self.remote()
        assets = [self.asset('p' + str(i)) for i in range(5)]
        self.server.fail.add(assets[0]['name'])
        ready, release = threading.Event(), threading.Event()
        lock = threading.Lock()
        state = {'started': 0, 'active': 0, 'maximum': 0}
        sessions = set()
        def send(session, method, url, **options):
            if method == 'POST' and url.endswith('/attach_files'):
                with lock:
                    sessions.add(id(session))
                    state['started'] += 1
                    state['active'] += 1
                    state['maximum'] = max(state['maximum'], state['active'])
                    if state['started'] == 4:
                        ready.set()
                try:
                    if not release.wait(10):
                        raise AssertionError('upload gate not released')
                finally:
                    with lock:
                        state['active'] -= 1
            return self.server.request(method, url, **options)
        results, errors = [], []
        def run():
            try:
                results.append(self.run_release(self.config(assets, {'p' + str(i): '1' for i in range(5)})))
            except BaseException as error:
                errors.append(error)
        self.transport.stop()
        with patch('publish.os.sched_getaffinity', return_value={0, 1}, create=True), \
                patch('publish.os.cpu_count', return_value=1), \
                patch('requests.sessions.Session.request', autospec=True, side_effect=send):
            thread = threading.Thread(target=run)
            thread.start()
            try:
                self.assertTrue(ready.wait(5))
                self.assertEqual(state['active'], 4)
            finally:
                release.set()
                thread.join(15)
        self.assertFalse(thread.is_alive())
        self.assertEqual(errors, [])
        self.assertEqual(state['maximum'], 4)
        self.assertEqual(state['started'], 15)
        self.assertEqual(len(sessions), 4)
        self.assertEqual((results[0]['uploaded'], results[0]['failed']), (4, 1))
        self.assertTrue(results[0]['metadata_committed'])

    def test_partial_failure_preserves_unselected_latest_bytes(self):
        original = b'# keep format\r\nlatest="old"\r\n'
        self.remote({'.autolib-repository.json': json.dumps(IDENTITY).encode(), 'queue/package.toml': original})
        failed = self.asset('eigen')
        self.server.fail.add(failed['name'])
        result = self.run_release(self.config([failed, self.asset()], {'eigen': '1', 'queue': '1'}))
        self.assertEqual((result['uploaded'], result['failed']), (1, 1))
        self.assertTrue(result['metadata_committed'])
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/package.toml'), original)
        self.assertNotIn('eigen/package.toml', git(self.bare, 'ls-tree', '-r', '--name-only', 'HEAD').decode())

    def test_republish_bytes_and_push_failure(self):
        self.remote()
        asset = self.asset()
        first = self.run_release(self.config([asset], {'queue': '1'}))
        before = git(self.bare, 'rev-parse', 'HEAD')
        result = self.run_release(self.config([asset], {'queue': '1'}, {'queue': '2'}), break_push=True)
        self.assertFalse(result['metadata_committed'])
        self.assertEqual((result['uploaded'], result['skipped']), (0, 1))
        self.assertEqual(git(self.bare, 'rev-parse', 'HEAD'), before)
        self.assertEqual(len(self.server.bodies), 1)
        repaired = self.run_release(self.config([asset], {'queue': '1'}, {'queue': '2'}))
        self.assertTrue(repaired['metadata_committed'])
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/package.toml'), b'latest = "2"\n')
        self.assertEqual(sum(method == 'POST' for method, _ in self.server.events), 1)
        self.assertEqual(sum(method == 'DELETE' for method, _ in self.server.events), 0)
        body = self.server.bodies[asset['name']]
        self.assertEqual(body, b'archive\0bytes')
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/1/queue-1-any-any.tar.zst.sha256'),
                         (hashlib.sha256(body).hexdigest() + '\n').encode())
        self.assertTrue(first['metadata_committed'])

    def test_changed_input_and_failed_query_never_mutate_assets(self):
        asset = self.asset()
        Path(asset['path']).write_bytes(b'changed bytes')
        result = self.module.publish_one(self.api, 3, asset)
        self.assertEqual(result['status'], 'failed')
        self.assertEqual(self.server.events, [])
        asset = self.asset()
        with patch('requests.sessions.Session.request', side_effect=requests.Timeout('fake-secret')) as send:
            output = io.StringIO()
            with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
                result = self.module.publish_one(self.api, 3, asset)
        self.assertEqual(result['status'], 'failed')
        self.assertEqual(result['operation'], 'target_assets')
        self.assertEqual(send.call_count, 11)
        self.assertTrue(all(call.args[0] == 'GET' for call in send.call_args_list))
        self.assertNotIn('sha256', result)
        self.assertNotIn('fake-secret', output.getvalue() + json.dumps(result))

    def test_latest_only_has_no_publication_or_release_queries_and_main_fails(self):
        environment = {'PUBLISH_HOST': 'gitee', 'PUBLISH_MODE': 'latest', 'PUBLISH_LATEST': '{"queue":"2"}',
                       'GITEE_REPOSITORY': 'fixture/mirror', 'GITEE_RELEASE_TAG': 'fixed', 'GITEE_TOKEN': 'fake-secret'}
        with patch('publish.load_publication', side_effect=AssertionError('must not read build')):
            config = self.module.resolve_config(environment)
        self.addCleanup(Path(config['work_root']).rmdir)
        self.assertEqual(config['assets'], [])
        self.remote()
        config['work_root'] = self.root / 'latest-work'
        result = self.run_release(config)
        self.assertTrue(result['metadata_committed'])
        self.assertEqual(self.server.events, [])
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/package.toml'), b'latest = "2"\n')
        for result in [dict(result, failed=1), dict(result, metadata_committed=False)]:
            output = io.StringIO()
            config = dict(config, work_root=Path(tempfile.mkdtemp(dir=self.root)))
            readonly = config['work_root'] / 'git-object'
            readonly.write_bytes(b'object')
            readonly.chmod(0o444)
            with patch('publish.resolve_config', return_value=config), patch('publish.publish_release', return_value=result), \
                    contextlib.redirect_stdout(output):
                self.assertEqual(self.module.main(), 1)

    def test_resolve_artifacts_uses_only_manifest_and_rejects_conflicting_inputs(self):
        asset = self.asset()
        document = {'source_run': {'repository': 'xxf1ow/xxautolib', 'ref': 'refs/heads/test', 'sha': 'a' * 40,
                                   'checkout_sha': 'a' * 40, 'run_id': '1', 'attempt': '1'},
                    'assets': [dict(asset, path=asset['name'])], 'initial_latest': {'queue': '1'}, 'explicit_latest': {}}
        manifest = self.root / 'publication.json'
        manifest.write_text(json.dumps(document))
        environment = {'PUBLISH_HOST': 'gitee', 'PUBLISH_MODE': 'artifacts', 'PUBLISH_INPUT': str(manifest),
                       'GITHUB_REPOSITORY': 'xxf1ow/xxautolib',
                       'GITEE_REPOSITORY': 'fixture/mirror', 'GITEE_RELEASE_TAG': 'fixed', 'GITEE_TOKEN': 'fake-secret'}
        config = self.module.resolve_config(environment)
        self.addCleanup(lambda: Path(config['work_root']).rmdir())
        self.assertEqual(config['assets'][0]['path'], asset['path'])
        self.assertEqual(config['initial_latest'], {'queue': '1'})
        for changes in [{'PUBLISH_LATEST': '{"queue":"2"}'}, {'PUBLISH_HOST': 'github'},
                        {'PUBLISH_MODE': 'latest'}, {'GITEE_TOKEN': ''}, {'GITEE_RELEASE_TAG': ''},
                        {'GITHUB_REPOSITORY': 'another/source'}, {'GITHUB_REPOSITORY': ''},
                        {'GITEE_RELEASE_TAG': 'has space'}, {'GITEE_RELEASE_TAG': 'wide\u3000space'}]:
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                self.module.resolve_config(dict(environment, **changes))


if __name__ == '__main__':
    unittest.main()
