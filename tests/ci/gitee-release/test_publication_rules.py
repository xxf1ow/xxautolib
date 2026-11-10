"""Publication validation and latest selection use only the declared inputs."""

import importlib
from pathlib import Path
import sys
import unittest

ACTION = Path(__file__).resolve().parents[3] / '.github/actions/gitee-release'
sys.path.insert(0, str(ACTION))


class PublicationRulesTests(unittest.TestCase):
    def setUp(self):
        self.assertIsNotNone(importlib.util.find_spec('publication_rules'), 'publication rules are not implemented')
        self.rules = importlib.import_module('publication_rules')

    def test_darwin_metadata_identity(self):
        importer = dict(kind='importer', platform='darwin-aarch64', name='autolib-importer-darwin-aarch64.tar.xz')
        self.assertEqual(self.rules.metadata_path(importer),
                         'autolib-importer/autolib-importer-darwin-aarch64.tar.xz.json')
        library = dict(kind='library', name='lz4-1.10.0-darwin-aarch64.tar.zst',
                       package=dict(name='lz4', version='1.10.0', os='darwin', arch='aarch64'))
        self.assertEqual(self.rules.metadata_path(library), 'lz4/1.10.0/lz4-1.10.0-darwin-aarch64.tar.zst.json')
        for platform in ('darwin-x86_64', 'darwin-universal', 'unknown-aarch64'):
            with self.subTest(platform=platform), self.assertRaises(ValueError):
                self.rules.metadata_path(dict(importer, platform=platform))

    def test_source_latest_preserves_repairs_and_isolates_errors(self):
        source = {'catalog': {'packages': {
            'keep': {'latest': '2', 'error': '', 'versions': {'1': {'platforms': ['any-any'], 'error': ''}, '2': {'platforms': ['any-any'], 'error': ''}}},
            'repair': {'latest': '1', 'error': '', 'versions': {'1': {'platforms': ['any-any'], 'error': ''}}},
            'bad': {'latest': '', 'error': 'dangling latest', 'versions': {}},
            'unknown': {'latest': '', 'error': 'unreadable', 'versions': None},
        }}}
        updates, removed, errors = self.rules.choose_source_latest(
            {'keep': '1', 'repair': 'deleted', 'bad': 'deleted', 'gone': '1', 'unknown': '1'},
            {'repair': 'deleted'}, source, {'repair'}, {'keep': 'missing'})
        self.assertEqual(updates, {'repair': '1'})
        self.assertEqual(removed, {'bad', 'gone'})
        self.assertIn('keep', errors)
        self.assertIn('bad', errors)
        self.assertNotIn('unknown', removed)
        updates, removed, errors = self.rules.choose_source_latest({}, {'keep': '1'}, source, {'keep'}, {})
        self.assertEqual(updates, {'keep': '1'})
        updates, _, _ = self.rules.choose_source_latest({'keep': '1'}, {}, source, set(), {})
        self.assertNotIn('keep', updates)
        updates, _, errors = self.rules.choose_source_latest({}, {}, source, set(), {'keep': '2', 'repair': '../bad'})
        self.assertEqual(updates, {'keep': '2'})
        self.assertIn('repair', errors)

    def test_metadata_path_uses_validated_identity(self):
        library = {'kind': 'library', 'name': 'queue-1-any-any.tar.zst',
                   'package': {'name': 'queue', 'version': '1', 'os': 'any', 'arch': 'any'}}
        self.assertEqual(self.rules.metadata_path(library), 'queue/1/queue-1-any-any.tar.zst.json')
        tool = {'kind': 'importer', 'name': 'autolib-importer-windows-x86_64.exe.tar.xz', 'platform': 'windows-x86_64'}
        self.assertEqual(self.rules.metadata_path(tool), 'autolib-importer/autolib-importer-windows-x86_64.exe.tar.xz.json')
        for bad in [dict(library, name='other.tar.zst'), dict(tool, platform='windows-aarch64'),
                    dict(library, package=dict(library['package'], version='../escape'))]:
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                self.rules.metadata_path(bad)

    def test_entry_has_no_git_metadata(self):
        self.assertIsNone(self.rules.metadata_path({'kind': 'entry', 'name': 'autolib.cmake'}))
        for fields in [{'name': 'other.cmake'}, {'platform': 'linux-x86_64'}, {'package': {}}]:
            with self.subTest(fields=fields), self.assertRaises(ValueError):
                self.rules.metadata_path(dict({'kind': 'entry', 'name': 'autolib.cmake'}, **fields))

    def test_delivery_record_contract(self):
        asset = dict(kind='library', name='queue-1-any-any.tar.zst', size=100000000, sha256='a' * 64,
                     package=dict(name='queue', version='1', os='any', arch='any'))
        expected = dict(sha256='a' * 64, size=100000000, urls=[
            'https://gitee.com/fixture/mirror/releases/download/%E4%B8%AD%E6%96%87%2Frelease/queue-1-any-any.tar.zst',
            'https://github.com/fork/source/releases/download/autolib-cache/queue-1-any-any.tar.zst'])
        make = lambda value: self.rules.delivery_record(value, repository='fixture/mirror',
            tag='中文/release', github_repository='fork/source')
        for size in (99999999, 100000000, 100000001):
            value = make(dict(asset, size=size))
            self.assertEqual(value, dict(expected, size=size, urls=expected['urls'] if size <= 100000000 else expected['urls'][1:]))
        for changes in [dict(size=True), dict(size='1'), dict(size=1.0), dict(size=0), dict(size=2147483648),
                        dict(sha256='A' * 64), dict(urls=[]), dict(urls=['http://example.com/a']),
                        dict(urls=['https://user:password@example.com/a']), dict(urls=['https://example.com/a'] * 2),
                        dict(extra=1)]:
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                self.rules.validate_delivery_record(dict(expected, **changes))


if __name__ == '__main__':
    unittest.main()
