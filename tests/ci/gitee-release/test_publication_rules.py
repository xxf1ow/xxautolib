"""Publication validation and latest selection use only the declared inputs."""

import copy
import hashlib
import importlib
import json
from pathlib import Path
import sys
import tempfile
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

    def test_choose_latest(self):
        choose = self.rules.choose_latest
        self.assertEqual(choose({}, {'queue': '1'}, {'queue'}, {}), {'queue': '1'})
        self.assertEqual(choose({'queue': '1'}, {'queue': '2'}, {'queue'}, {}), {'queue': '1'})
        self.assertEqual(choose({}, {'queue': '2'}, set(), {}), {})
        self.assertEqual(choose({'queue': '1'}, {}, set(), {'queue': '3'}), {'queue': '3'})
        self.assertEqual(choose({}, {'queue': 'not-uploaded'}, {'queue'}, {}), {'queue': 'not-uploaded'})

    def test_latest_rejects_invalid_existing_even_when_explicit_replaces_it(self):
        for existing in [{'queue': '../bad'}, {'autolib-importer': '1'}, {'Queue': '1', 'queue': '2'}]:
            with self.subTest(existing=existing), self.assertRaises(ValueError):
                self.rules.choose_latest(existing, {}, set(), {'queue': '1'})

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

    def publication(self, root):
        (root / 'queue-1-any-any.tar.zst').write_bytes(b'archive')
        return {'source_run': {'repository': 'xxf1ow/xxautolib', 'ref': 'refs/heads/task', 'sha': 'a' * 40,
                               'checkout_sha': 'a' * 40, 'run_id': '12', 'attempt': '2'},
                'assets': [{'kind': 'library', 'name': 'queue-1-any-any.tar.zst', 'path': 'queue-1-any-any.tar.zst',
                            'size': 7, 'sha256': hashlib.sha256(b'archive').hexdigest(),
                            'package': {'name': 'queue', 'version': '1', 'os': 'any', 'arch': 'any'}}],
                'initial_latest': {'queue': '2'}, 'explicit_latest': {}}

    def test_load_publication_validates_bytes_and_resolves_local_file(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            document = self.publication(root)
            manifest = root / 'publication.json'
            manifest.write_text(json.dumps(document), encoding='utf-8')
            loaded = self.rules.load_publication(manifest, source_repository='xxf1ow/xxautolib')
            self.assertEqual(Path(loaded['assets'][0]['path']), root / 'queue-1-any-any.tar.zst')
            self.assertEqual(loaded['initial_latest'], {'queue': '2'})
            self.assertEqual(len(loaded['assets']), 1)

    def test_load_publication_rejects_unsafe_or_inconsistent_inputs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            original = self.publication(root)
            documents = []
            for field, value in [('path', '../outside'), ('path', str(root / 'queue-1-any-any.tar.zst')),
                                 ('path', 'sub/../queue-1-any-any.tar.zst'), ('sha256', '0' * 64),
                                 ('size', True), ('name', 'wrong.tar.zst')]:
                data = copy.deepcopy(original)
                data['assets'][0][field] = value
                documents.append(data)
            for change in ['missing-candidate', 'duplicate', 'bad-run']:
                data = copy.deepcopy(original)
                if change == 'missing-candidate':
                    data['initial_latest'] = {}
                elif change == 'duplicate':
                    data['assets'] *= 2
                else:
                    data['source_run']['checkout_sha'] = 'b' * 40
                documents.append(data)
            for data in documents:
                (root / 'publication.json').write_text(json.dumps(data), encoding='utf-8')
                with self.subTest(data=data), self.assertRaises(ValueError):
                    self.rules.load_publication(root / 'publication.json', source_repository='xxf1ow/xxautolib')

    def test_load_empty_publication_does_not_invent_assets(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = self.publication(root)
            data.update(assets=[], initial_latest={})
            (root / 'publication.json').write_text(json.dumps(data), encoding='utf-8')
            self.assertEqual(self.rules.load_publication(root / 'publication.json', source_repository='xxf1ow/xxautolib')['assets'], [])

    def test_load_publication_rejects_linked_asset_directories(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = self.publication(root)
            link = root / 'linked'
            if sys.platform == 'win32':
                import subprocess
                subprocess.run(['cmd', '/c', 'mklink', '/J', str(link), str(root)], check=True, capture_output=True)
            else:
                link.symlink_to(root, target_is_directory=True)
            try:
                data['assets'][0]['path'] = 'linked/queue-1-any-any.tar.zst'
                (root / 'publication.json').write_text(json.dumps(data), encoding='utf-8')
                with self.assertRaises(ValueError):
                    self.rules.load_publication(root / 'publication.json', source_repository='xxf1ow/xxautolib')
            finally:
                if sys.platform == 'win32':
                    link.rmdir()
                else:
                    link.unlink()

    def test_entry_bytes_are_checked_without_git_metadata(self):
        self.assertIsNone(self.rules.metadata_path({'kind': 'entry', 'name': 'autolib.cmake'}))
        for fields in [{'name': 'other.cmake'}, {'platform': 'linux-x86_64'}, {'package': {}}]:
            with self.subTest(fields=fields), self.assertRaises(ValueError):
                self.rules.metadata_path(dict(kind='entry', name='autolib.cmake', **fields)
                                         if 'name' not in fields else dict(kind='entry', **fields))
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = self.publication(root)
            (root / 'autolib.cmake').write_bytes(b'entry')
            entry = dict(kind='entry', name='autolib.cmake', path='autolib.cmake', size=5,
                         sha256=hashlib.sha256(b'entry').hexdigest())
            data['assets'].append(entry)
            manifest = root / 'publication.json'
            for change in [{}, {'size': 4}, {'sha256': '0' * 64}, {'platform': 'linux-x86_64'}]:
                candidate = copy.deepcopy(data)
                candidate['assets'][-1].update(change)
                manifest.write_text(json.dumps(candidate), encoding='utf-8')
                if not change:
                    loaded = self.rules.load_publication(manifest, source_repository='xxf1ow/xxautolib')
                    self.assertEqual(loaded['initial_latest'], {'queue': '2'})
                    self.assertEqual(Path(loaded['assets'][-1]['path']).read_bytes(), b'entry')
                else:
                    with self.subTest(change=change), self.assertRaises(ValueError):
                        self.rules.load_publication(manifest, source_repository='xxf1ow/xxautolib')

    def test_manifest_source_is_bound_to_trusted_fork(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = self.publication(root)
            data['source_run']['repository'] = 'fork/source'
            manifest = root / 'publication.json'
            manifest.write_text(json.dumps(data), encoding='utf-8')
            self.assertEqual(self.rules.load_publication(manifest, source_repository='fork/source')['source_run'],
                             data['source_run'])
            for trusted in ['', 'xxf1ow/xxautolib', 'other/source']:
                with self.subTest(trusted=trusted), self.assertRaises(ValueError):
                    self.rules.load_publication(manifest, source_repository=trusted)


if __name__ == '__main__':
    unittest.main()
