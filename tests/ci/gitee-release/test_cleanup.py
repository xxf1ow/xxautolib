"""Source deletions prune exact identities through real Git commits."""

import importlib
import json
import sys
from pathlib import Path
import threading
import unittest
from unittest.mock import patch

import test_metadata_repository as repositories

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / '.github/actions/gitee-release'))


def source_facts(packages):
    return {'schema': 1, 'repository': 'owner/repo', 'ref': 'refs/heads/main', 'sha': 'a' * 40,
            'catalog': {'packages': packages}, 'plan': {'platforms': []},
            'inputs': {'fingerprints': {}, 'failures': {}}}


def package(versions, latest='1', error=''):
    return {'latest': latest, 'error': error, 'versions': None if versions is None else {
        version: {'platforms': platforms, 'error': '' if platforms is not None else 'bad declaration'}
        for version, platforms in versions.items()}}


def cache_facts(records=None, assets=None):
    return {'repository': 'owner/repo', 'release_id': 9, 'records': records or {},
            'record_errors': {}, 'assets': assets or [], 'inputs': {'fingerprints': {}, 'failures': {}}}


class CleanupAPI:
    def __init__(self, assets, fail=None):
        self.assets = {a['id']: dict(a) for a in assets}
        self.fail = fail

    def delete_asset(self, release, identity):
        if identity == self.fail:
            raise RuntimeError('controlled deletion failure')
        del self.assets[identity]


class CleanupTests(unittest.TestCase):
    def setUp(self):
        self.cleanup = importlib.import_module('cleanup')
        self.fixture = repositories.MetadataRepositoryTests()
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)

    def open(self, extra=None):
        self.fixture.seed_files(self.fixture.legitimate(**(extra or {})))
        self.fixture.make_remote()
        return self.fixture.open()

    def test_github_only_source_removal_cleans_json_and_latest(self):
        repo = self.open()
        present = source_facts({'queue': package({'1': ['any-any']})})
        path = 'queue/1/queue-1-any-any.tar.zst.json'
        self.assertFalse(self.cleanup.plan_cleanup(present, cache_facts(), repo, [])['groups'])
        plan = self.cleanup.plan_cleanup(source_facts({}), cache_facts(), repo, [])
        self.cleanup.execute_cleanup(plan, delete_cache=lambda key: self.fail('no cache exists'),
            api=CleanupAPI([]), release_id=None, repository=repo, git_lock=threading.Lock())
        self.assertNotIn(path, repo['existing_records'])
        self.assertNotIn('queue', repo['existing_latest'])

    def test_deleted_version_and_orphan_asset_are_removed_with_latest_repair(self):
        repo = self.open({'b/package.toml': b'latest="2"\n',
                          'b/2/b-2-any-any.tar.zst.json': repositories.record_bytes('b' * 64)})
        assets = [{'id': 1, 'name': 'b-2-any-any.tar.zst'}, {'id': 2, 'name': 'gone-1-any-any.tar.zst'},
                  {'id': 3, 'name': 'unknown-1-any-any.tar.zst', 'content': b'preserve'},
                  {'id': 4, 'name': 'autolib.cmake'}]
        key = {'kind': 'library', 'name': 'gone', 'version': '1', 'os': 'any', 'arch': 'any'}
        cache = cache_facts({'gone-1-any-any.tar.zst': {'key': key}},
                            [{'id': 21, 'name': 'gone-1-any-any.tar.zst'}])
        source = source_facts({'queue': package({'1': ['any-any']}), 'b': package({'1': ['any-any']})})
        plan = self.cleanup.plan_cleanup(source, cache, repo, assets)
        api, deleted = CleanupAPI(assets), []
        before = repo['head']
        result = self.cleanup.execute_cleanup(plan, delete_cache=deleted.append, api=api, release_id=7,
                                               repository=repo, git_lock=threading.Lock())
        self.assertFalse(result['errors'])
        self.assertEqual({a['name'] for a in api.assets.values()}, {'unknown-1-any-any.tar.zst', 'autolib.cmake'})
        self.assertEqual(api.assets[3]['content'], b'preserve')
        tree = repositories.git(self.fixture.bare, 'ls-tree', '-r', '--name-only', 'HEAD').decode()
        self.assertNotIn('b/2/b-2-any-any.tar.zst.json', tree)
        self.assertEqual(repositories.git(self.fixture.bare, 'show', 'HEAD:b/package.toml'), b'latest = "1"\n')
        self.assertEqual(repositories.git(self.fixture.bare, 'rev-list', '--count', before + '..HEAD').strip(), b'1')
        self.assertIn(key, deleted)

    def test_absent_cache_and_invalid_source_never_delete_present_items(self):
        repo = self.open()
        assets = [{'id': 1, 'name': 'queue-1-any-any.tar.zst'}]
        for versions, error in [({'1': ['any-any']}, ''), ({'1': None}, 'bad'), (None, 'unreadable')]:
            with self.subTest(versions=versions):
                source = source_facts({'queue': package(versions, error=error)})
                plan = self.cleanup.plan_cleanup(source, cache_facts(), repo, assets)
                api = CleanupAPI(assets)
                self.cleanup.execute_cleanup(plan, delete_cache=lambda key: self.fail('cache deletion'), api=api,
                                             release_id=7, repository=repo, git_lock=threading.Lock())
                self.assertIn(1, api.assets)
                self.assertIn('queue/1/queue-1-any-any.tar.zst.json', repo['existing_records'])

    def test_platform_removal_to_any_is_exact(self):
        repo = self.open({'b/1/b-1-linux-x86_64.tar.zst.json': repositories.record_bytes('b' * 64)})
        assets = [{'id': 1, 'name': 'b-1-linux-x86_64.tar.zst'}, {'id': 2, 'name': 'b-1-any-any.tar.zst'}]
        source = source_facts({'queue': package({'1': ['any-any']}), 'b': package({'1': ['any-any']})})
        plan = self.cleanup.plan_cleanup(source, cache_facts(), repo, assets)
        api = CleanupAPI(assets)
        self.cleanup.execute_cleanup(plan, delete_cache=lambda key: None, api=api, release_id=7,
                                     repository=repo, git_lock=threading.Lock())
        self.assertEqual(set(api.assets), {2})

    def test_each_endpoint_can_recover_without_rolling_back_confirmed_work(self):
        repo = self.open()
        source = source_facts({})
        assets = [{'id': 1, 'name': 'queue-1-any-any.tar.zst'}]
        plan = self.cleanup.plan_cleanup(source, cache_facts(), repo, assets)
        api = CleanupAPI(assets, fail=1)
        result = self.cleanup.execute_cleanup(plan, delete_cache=lambda key: None, api=api, release_id=7,
                                             repository=repo, git_lock=threading.Lock())
        self.assertTrue(result['errors'])
        self.assertIn('queue/1/queue-1-any-any.tar.zst.json', repo['existing_records'])
        api.fail = None
        plan = self.cleanup.plan_cleanup(source, cache_facts(), repo, list(api.assets.values()))
        result = self.cleanup.execute_cleanup(plan, delete_cache=lambda key: None, api=api, release_id=7,
                                             repository=repo, git_lock=threading.Lock())
        self.assertFalse(result['errors'])
        self.assertFalse(repo['existing_records'])
        self.assertNotIn('queue', repo['existing_latest'])

    def test_failed_orphan_deletion_preserves_identity_for_next_run(self):
        repo = self.open()
        name = 'gone-1-any-any.tar.zst'
        key = dict(kind='library', name='gone', version='1', os='any', arch='any')
        cache = cache_facts({name: {'key': key}},
                            [])
        source = source_facts({'queue': package({'1': ['any-any']})})
        api = CleanupAPI([{'id': 1, 'name': name}, {'id': 2, 'name': 'unknown.bin'}], fail=1)
        def delete_cache(identity):
            self.assertEqual(identity, key)
            cache['records'].pop(name)
            cache['assets'].clear()
        plan = self.cleanup.plan_cleanup(source, cache, repo, list(api.assets.values()))
        first = self.cleanup.execute_cleanup(plan, delete_cache=delete_cache, api=api, release_id=7,
                                            repository=repo, git_lock=threading.Lock())
        self.assertIn(name + ':gitee', first['errors'])
        self.assertIn(name, cache['records'])
        api.fail = None
        plan = self.cleanup.plan_cleanup(source, cache, repo, list(api.assets.values()))
        second = self.cleanup.execute_cleanup(plan, delete_cache=delete_cache, api=api, release_id=7,
                                             repository=repo, git_lock=threading.Lock())
        self.assertFalse(second['errors'])
        self.assertEqual(second['deleted'], [name])
        self.assertEqual(set(api.assets), {2})
        self.assertFalse(cache['records'])
        self.assertFalse(cache['assets'])

    def test_incomplete_or_wrong_source_is_not_a_deletion_input(self):
        repo = self.open()
        for source, cache, assets in [(source_facts({}), dict(cache_facts(), repository='fake/repo'), []),
                                      (source_facts(None), cache_facts(), []),
                                      (source_facts({}), cache_facts(), None)]:
            with self.subTest(source=source), self.assertRaises(ValueError):
                self.cleanup.plan_cleanup(source, cache, repo, assets)

    def test_ambiguous_asset_names_are_preserved(self):
        repo = self.open()
        assets = [{'id': 1, 'name': 'queue-1-any-any.tar.zst'}, {'id': 2, 'name': 'queue-1-any-any.tar.zst'}]
        plan = self.cleanup.plan_cleanup(source_facts({}), cache_facts(), repo, assets)
        api = CleanupAPI(assets)
        result = self.cleanup.execute_cleanup(plan, delete_cache=lambda key: None, api=api, release_id=7,
                                             repository=repo, git_lock=threading.Lock())
        self.assertTrue(result['errors'])
        self.assertEqual(set(api.assets), {1, 2})
        self.assertIn('queue/1/queue-1-any-any.tar.zst.json', repo['existing_records'])


if __name__ == '__main__':
    unittest.main()
