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
                         'autolib-importer/autolib-importer-darwin-aarch64.tar.xz.sha256')
        library = dict(kind='library', name='lz4-1.10.0-darwin-aarch64.tar.zst',
                       package=dict(name='lz4', version='1.10.0', os='darwin', arch='aarch64'))
        self.assertEqual(self.rules.metadata_path(library), 'lz4/1.10.0/lz4-1.10.0-darwin-aarch64.tar.zst.sha256')
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
        self.assertEqual(self.rules.metadata_path(library), 'queue/1/queue-1-any-any.tar.zst.sha256')
        tool = {'kind': 'importer', 'name': 'autolib-importer-windows-x86_64.exe.tar.xz', 'platform': 'windows-x86_64'}
        self.assertEqual(self.rules.metadata_path(tool), 'autolib-importer/autolib-importer-windows-x86_64.exe.tar.xz.sha256')
        for bad in [dict(library, name='other.tar.zst'), dict(tool, platform='windows-aarch64'),
                    dict(library, package=dict(library['package'], version='../escape'))]:
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                self.rules.metadata_path(bad)

    def test_entry_has_no_git_metadata(self):
        self.assertIsNone(self.rules.metadata_path({'kind': 'entry', 'name': 'autolib.cmake'}))
        for fields in [{'name': 'other.cmake'}, {'platform': 'linux-x86_64'}, {'package': {}}]:
            with self.subTest(fields=fields), self.assertRaises(ValueError):
                self.rules.metadata_path(dict({'kind': 'entry', 'name': 'autolib.cmake'}, **fields))


if __name__ == '__main__':
    unittest.main()
