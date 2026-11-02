"""HTTP substitutes and temporary Git repositories exercise local publication."""

import contextlib
import hashlib
import importlib
import io
import json
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
                        'browser_download_url': 'https://gitee.com/fixture/mirror/' + name}
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

    def run_release(self, config, *, created=False, break_push=False):
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
            return self.module.publish_release(config)

    def test_replace_same_name_and_failures(self):
        asset = self.asset()
        for target, status, fail, expected in [(None, 204, False, ['POST']), (7, 204, False, ['DELETE', 'POST']),
                                              (7, 403, False, ['DELETE']), (7, 503, False, ['DELETE']),
                                              (7, 204, True, ['DELETE', 'POST'])]:
            with self.subTest(target=target, status=status, fail=fail):
                self.server.events.clear()
                self.server.delete_status = status
                self.server.fail = {asset['name']} if fail else set()
                result = self.module.publish_one(self.api, 3, asset, target)
                self.assertEqual([method for method, _ in self.server.events], expected)
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
        self.assertEqual(state['started'], 5)
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
        self.assertEqual(result['uploaded'], 1)
        self.assertEqual(git(self.bare, 'rev-parse', 'HEAD'), before)
        self.assertEqual(len(self.server.bodies), 1)
        repaired = self.run_release(self.config([asset], {'queue': '1'}, {'queue': '2'}))
        self.assertTrue(repaired['metadata_committed'])
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/package.toml'), b'latest = "2"\n')
        self.assertEqual(sum(method == 'POST' for method, _ in self.server.events), 3)
        self.assertEqual(sum(method == 'DELETE' for method, _ in self.server.events), 2)
        body = self.server.bodies[asset['name']]
        self.assertEqual(body, b'archive\0bytes')
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/1/queue-1-any-any.tar.zst.sha256'),
                         (hashlib.sha256(body).hexdigest() + '\n').encode())
        self.assertTrue(first['metadata_committed'])

    def test_changed_input_fails_before_delete_and_unknown_upload_is_not_success(self):
        asset = self.asset()
        Path(asset['path']).write_bytes(b'changed bytes')
        result = self.module.publish_one(self.api, 3, asset, 7)
        self.assertEqual(result['status'], 'failed')
        self.assertEqual(self.server.events, [])
        asset = self.asset()
        with patch('requests.sessions.Session.request', side_effect=requests.Timeout('fake-secret')):
            output = io.StringIO()
            with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
                result = self.module.publish_one(self.api, 3, asset, None)
        self.assertEqual(result['status'], 'failed')
        self.assertEqual(result['outcome'], 'unknown')
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
                       'GITEE_REPOSITORY': 'fixture/mirror', 'GITEE_RELEASE_TAG': 'fixed', 'GITEE_TOKEN': 'fake-secret'}
        config = self.module.resolve_config(environment)
        self.addCleanup(lambda: Path(config['work_root']).rmdir())
        self.assertEqual(config['assets'][0]['path'], asset['path'])
        self.assertEqual(config['initial_latest'], {'queue': '1'})
        for changes in [{'PUBLISH_LATEST': '{"queue":"2"}'}, {'PUBLISH_HOST': 'github'},
                        {'PUBLISH_MODE': 'latest'}, {'GITEE_TOKEN': ''}, {'GITEE_RELEASE_TAG': ''}]:
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                self.module.resolve_config(dict(environment, **changes))


if __name__ == '__main__':
    unittest.main()
