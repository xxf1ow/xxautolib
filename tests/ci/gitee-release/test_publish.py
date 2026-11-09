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


    def config(self, assets, initial=None, explicit=None):
        from test_cleanup import source_facts, package, cache_facts
        packages = {}
        records = {}
        for asset in assets:
            if asset['kind'] != 'library':
                continue
            key = dict(kind='library', **asset['package'])
            packages.setdefault(key['name'], package({}))["versions"][key['version']] = {'platforms': ['any-any'], 'error': ''}
            packages[key['name']]['latest'] = (initial or {}).get(key['name'], key['version'])
            records[asset['name']] = {'key': key, 'archive': {'name': asset['name'], 'sha256': asset['sha256'], 'size': asset['size'], 'id': 1},
                                      'fingerprint': 'a' * 64, 'dependency_fingerprints': {},
                                      'latest_candidate': packages[key['name']]['latest']}
        source = source_facts(packages)
        source['inputs']['fingerprints'] = {a['name']: 'a' * 64 for a in assets}
        cache = cache_facts(records)
        cache['inputs'] = source['inputs']
        root = self.root / ('work-' + str(len(list(self.root.glob('work-*')))))
        return {'host': 'gitee', 'mode': 'sync', 'repository': 'fixture/mirror', 'tag': 'fixed',
                'gitee_token': 'fake-secret', 'source': source, 'cache': cache, 'explicit_latest': explicit or {},
                'work_root': root, 'fetch_asset': lambda asset: next(a['path'] for a in assets if a['name'] == asset['name']),
                'delete_cache': lambda key: None}

    def test_same_git_digest_skips_even_missing_attachment_and_download(self):
        asset = self.asset()
        self.remote({'.autolib-repository.json': json.dumps(IDENTITY).encode(),
                     'queue/package.toml': b'latest="1"\n',
                     'queue/1/' + asset['name'] + '.sha256': (asset['sha256'] + '\n').encode()})
        config = self.config([asset])
        config['fetch_asset'] = lambda asset: self.fail('unchanged archive downloaded')
        previous = git(self.bare, 'rev-parse', 'HEAD')
        result = self.run_release(config)
        self.assertEqual((result['uploaded'], result['skipped'], result['failed']), (0, 1, 0))
        self.assertEqual(git(self.bare, 'rev-parse', 'HEAD'), previous)
        self.assertFalse(any(m in {'DELETE', 'POST'} or '/releases/download/' in u for m,u in self.server.events))

    def test_identical_attachment_without_digest_is_deleted_and_uploaded(self):
        asset = self.asset()
        self.remote()
        self.server.add_asset(asset['name'], Path(asset['path']).read_bytes())
        result = self.run_release(self.config([asset]))
        self.assertEqual((result['uploaded'], result['skipped'], result['failed']), (1, 0, 0))
        self.assertEqual([m for m,u in self.server.events if m in {'DELETE','POST'}], ['DELETE','POST'])
        self.assertFalse(any('/releases/download/' in u for _,u in self.server.events))
        self.assertEqual(git(self.bare,'show','HEAD:queue/1/'+asset['name']+'.sha256'),(asset['sha256']+'\n').encode())

    def test_response_lost_refreshes_then_deletes_and_reuploads(self):
        self.remote()
        asset = self.asset()
        lost = False
        def send(method,url,**options):
            nonlocal lost
            response = self.server.request(method,url,**options)
            if method=='POST' and url.endswith('/attach_files') and not lost:
                lost=True
                response.close()
                raise requests.Timeout('fake-secret')
            return response
        with patch('requests.sessions.Session.request',side_effect=send):
            result=self.run_release(self.config([asset]))
        self.assertEqual((result['uploaded'],result['failed']),(1,0))
        self.assertEqual([m for m,u in self.server.events if m in {'DELETE','POST'}],['POST','DELETE','POST'])
        self.assertEqual(sum(m=='GET' and u.endswith('/attach_files') for m,u in self.server.events),2)
        self.assertEqual([c.args[0] for c in self.clock.call_args_list],[2])

    def test_wrong_local_bytes_never_delete_remote(self):
        asset=self.asset()
        self.remote()
        self.server.add_asset(asset['name'],b'old')
        Path(asset['path']).write_bytes(b'changed')
        result=self.run_release(self.config([asset]))
        self.assertEqual(result['failed'],1)
        self.assertFalse(any(m in {'DELETE','POST'} for m,u in self.server.events))
        self.assertEqual(self.server.bodies[asset['name']],b'old')

    def test_only_changed_item_downloads_and_uploads(self):
        unchanged,changed=self.asset('a'),self.asset('b')
        self.remote({'.autolib-repository.json':json.dumps(IDENTITY).encode(),
                     'a/package.toml':b'latest="1"\n',
                     'a/1/'+unchanged['name']+'.sha256':(unchanged['sha256']+'\n').encode()})
        config=self.config([unchanged,changed])
        fetched=[]
        def fetch(asset):
            fetched.append(asset['name'])
            return changed['path']
        config['fetch_asset']=fetch
        result=self.run_release(config)
        self.assertEqual((result['uploaded'],result['skipped'],result['failed']),(1,1,0))
        self.assertEqual(fetched,[changed['name']])
        self.assertEqual(sum(m=='GET' and u.endswith('/attach_files') for m,u in self.server.events),1)

    def test_permanent_errors_do_not_retry(self):
        for status in [401,403,422]:
            asset=self.asset()
            self.server.events.clear()
            self.clock.reset_mock()
            def send(method,url,**options):
                response=self.server.request(method,url,**options)
                response.status_code=status
                return response
            with patch('requests.sessions.Session.request',side_effect=send):
                result=self.module.publish_one(self.api,3,asset,[],fetch_asset=lambda a:a['path'],confirm_item=lambda a:None)
            self.assertEqual(result['status'],'failed')
            self.assertEqual(sum(m=='POST' for m,u in self.server.events),1)
            self.clock.assert_not_called()

    def test_retry_budget_and_diagnostics_are_bounded(self):
        asset=self.asset()
        self.server.fail.add(asset['name'])
        errors=io.StringIO()
        with contextlib.redirect_stderr(errors):
            result=self.module.publish_one(self.api,3,asset,[],fetch_asset=lambda a:a['path'],confirm_item=lambda a:self.fail('failed upload confirmed'))
        self.assertEqual(result['status'],'failed')
        self.assertEqual(sum(m=='POST' for m,u in self.server.events),11)
        self.assertEqual([c.args[0] for c in self.clock.call_args_list],[2,4,8,16,32,32,32,32,32,32])
        self.assertNotIn('fake-secret',errors.getvalue())

    def test_fast_item_is_committed_while_slow_upload_is_in_flight(self):
        self.remote()
        fast,slow=self.asset('fast'),self.asset('slow')
        entered,release,confirmed=threading.Event(),threading.Event(),threading.Event()
        original=self.module.commit_metadata
        def confirm(repo,digests,latest,**kwargs):
            head=original(repo,digests,latest,**kwargs)
            if any(p.startswith('fast/') for p in digests):confirmed.set()
            return head
        def fetch(asset):
            if asset['name']==slow['name']:
                entered.set()
                if not release.wait(10):raise RuntimeError('test timeout')
            return fast['path'] if asset['name']==fast['name'] else slow['path']
        config=self.config([slow,fast]);config['fetch_asset']=fetch
        results=[]
        with patch('publish.commit_metadata',side_effect=confirm):
            thread=threading.Thread(target=lambda:results.append(self.run_release(config)))
            thread.start()
            try:
                self.assertTrue(entered.wait(10))
                self.assertTrue(confirmed.wait(10))
                self.assertEqual(git(self.bare,'show','HEAD:fast/1/'+fast['name']+'.sha256'),(fast['sha256']+'\n').encode())
            finally:
                release.set();thread.join(10)
        self.assertFalse(thread.is_alive())
        self.assertEqual(results[0]['uploaded'],2)

    def test_unconfirmed_push_stops_all_further_metadata_writes(self):
        self.remote()
        assets=[self.asset('a'),self.asset('b')]
        result=self.run_release(self.config(assets),break_push=True)
        self.assertTrue(result['errors'])
        self.assertEqual(result['uploaded'],0)
        self.assertEqual(git(self.bare,'rev-list','--count','HEAD').strip(),b'1')

    def test_configuration_binds_source_and_cache_to_trusted_snapshot(self):
        asset=self.asset()
        config=self.config([asset])
        source=self.root/'source.json';cache=self.root/'cache.json';tool=self.root/'helper'
        source.write_text(json.dumps(config['source']));cache.write_text(json.dumps(config['cache']));tool.write_text('helper')
        environment={'PUBLISH_HOST':'gitee','PUBLISH_MODE':'sync','GITEE_REPOSITORY':'fixture/mirror','GITEE_RELEASE_TAG':'fixed',
                     'GITEE_TOKEN':'fake-secret','GITHUB_REPOSITORY':'owner/repo','SYNC_REF':'refs/heads/main','SYNC_SHA':'a'*40,
                     'SYNC_SOURCE':str(source),'SYNC_CACHE':str(cache),'SYNC_CACHE_TOOL':str(tool)}
        resolved=self.module.resolve_config(environment)
        self.addCleanup(lambda root=resolved['work_root']:Path(root).rmdir())
        self.assertEqual(resolved['source']['sha'],'a'*40)
        for changes in [{'SYNC_SHA':'b'*40},{'GITHUB_REPOSITORY':'fake/repo'},{'PUBLISH_MODE':'artifacts'},{'PUBLISH_LATEST':'[]'}]:
            with self.subTest(changes=changes),self.assertRaises(ValueError):self.module.resolve_config(dict(environment,**changes))
        environment.update(PUBLISH_MODE='latest',PUBLISH_LATEST='{"queue":"1"}',SYNC_CACHE='does-not-exist',SYNC_CACHE_TOOL='')
        resolved=self.module.resolve_config(environment)
        self.addCleanup(lambda root=resolved['work_root']:Path(root).rmdir())
        self.assertIsNone(resolved['cache'])


if __name__ == '__main__':
    unittest.main()
