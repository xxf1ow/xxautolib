"""Real temporary Git repositories verify preservation and push confirmation."""

import importlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / '.github/actions/gitee-release'))
IDENTITY = {'type': 'xxautolib-artifacts', 'schema': 1, 'upstream': 'https://github.com/xxf1ow/xxautolib'}


def git(path, *arguments, input=None):
    result = subprocess.run(['git', '-C', str(path), *arguments], input=input,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        raise RuntimeError(result.stderr.decode(errors='replace'))
    return result.stdout


class RepositoryAPI:
    def __init__(self, created):
        self.created = created
        self.events = []

    def repository_info(self):
        self.events.append('query')
        if self.created and len(self.events) == 1:
            return None
        return {'id': 1, 'full_name': 'fixture/mirror', 'private': True,
                'default_branch': 'server-default', 'permission': {'push': True}, 'login': 'actual-writer'}

    def create_repository(self):
        self.events.append('create')
        return {'id': 1, 'full_name': 'fixture/mirror', 'private': True}


class MetadataRepositoryTests(unittest.TestCase):
    def setUp(self):
        self.assertIsNotNone(importlib.util.find_spec('metadata_repository'), 'metadata repository is not implemented')
        self.module = importlib.import_module('metadata_repository')
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.seed = self.root / 'seed'
        self.seed.mkdir()
        git(self.seed, 'init', '--initial-branch=server-default')
        git(self.seed, 'config', 'user.name', 'fixture')
        git(self.seed, 'config', 'user.email', 'fixture@example.invalid')
        git(self.seed, 'config', 'core.autocrlf', 'false')
        self.bare = self.root / 'remote.git'
        self.config = {'host': 'gitee', 'repository': 'fixture/mirror', 'tag': 'fixed', 'gitee_token': 'fake-secret'}

    def seed_files(self, files):
        for name, content in files.items():
            target = self.seed / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(content)
        git(self.seed, 'add', '--all')
        git(self.seed, 'commit', '-m', 'Seed')

    def make_remote(self):
        git(self.root, 'clone', '--bare', str(self.seed), str(self.bare))

    def open(self, created=False):
        self.api = RepositoryAPI(created)
        with patch('metadata_repository._repository_url', return_value=str(self.bare)):
            return self.module.open_repository(self.config, self.api, self.root / 'work')

    def legitimate(self, **extra):
        return {'.autolib-repository.json': json.dumps(IDENTITY).encode(), 'README.md': b'keep description\n',
                'queue/package.toml': b'latest = "1"\n',
                'queue/1/queue-1-any-any.tar.zst.sha256': b'a' * 64 + b'\n', **extra}

    def test_initialize_and_validate_repository(self):
        self.config['tag'] = '中文/release+v1:@'
        self.seed_files({'README.md': b'server template', 'README.en.md': b'default'})
        self.make_remote()
        repo = self.open(created=True)
        self.assertEqual(repo['_auth']['AUTOLIB_GIT_USERNAME'], 'actual-writer')
        self.assertEqual(self.api.events, ['query', 'create', 'query'])
        self.assertEqual(repo['branch'], 'server-default')
        self.assertEqual(repo['existing_latest'], {})
        self.assertEqual((repo['path'] / 'LICENSE').read_bytes(), (ROOT / 'LICENSE').read_bytes())
        self.assertEqual(json.loads((repo['path'] / '.autolib-repository.json').read_bytes()), IDENTITY)
        self.assertFalse((repo['path'] / 'README.en.md').exists())
        readme = (repo['path'] / 'README.md').read_text(encoding='utf-8')
        for text in ['https://github.com/xxf1ow/xxautolib', '编译', '发布', 'MIT', '第三方', '私有']:
            self.assertIn(text, readme)
        self.assertIn('set(AUTOLIB_URL "https://gitee.com/fixture/mirror/releases/download/'
                      '%E4%B8%AD%E6%96%87%2Frelease+v1:@/autolib.cmake")', readme)
        self.assertNotIn('server template', readme)
        self.assertEqual(git(self.bare, 'rev-parse', 'HEAD').decode().strip(), repo['head'])
        self.assertNotIn('fake-secret', (repo['path'] / '.git/config').read_text())
        with patch('metadata_repository._repository_url', return_value=str(self.bare)), self.assertRaises(RuntimeError):
            self.module.open_repository(self.config, RepositoryAPI(False), self.root / 'work')

    def test_existing_repository_preserves_description_license_and_latest(self):
        files = self.legitimate(LICENSE=b'original license\r\n')
        self.seed_files(files)
        self.make_remote()
        repo = self.open()
        self.assertEqual(self.api.events, ['query'])
        self.assertEqual(repo['existing_latest'], {'queue': '1'})
        for name, content in files.items():
            self.assertEqual((repo['path'] / name).read_bytes(), content)
        self.assertEqual(repo['head'], git(self.seed, 'rev-parse', 'HEAD').decode().strip())

    def test_invalid_existing_trees_are_rejected_without_changing_remote(self):
        bad_cases = [{}, {'.autolib-repository.json': b'{"schema":true}'}, {'unknown.txt': b'keep'},
                     {'queue/package.toml': b'latest = "../bad"\n'},
                     {'queue/package.toml': b'latest = "1"\nother = "x"\n'},
                     {'autolib-importer/package.toml': b'latest = "1"\n'}]
        for index, changes in enumerate(bad_cases):
            with self.subTest(changes=changes):
                files = self.legitimate(**changes)
                if not changes:
                    del files['.autolib-repository.json']
                case = self.root / str(index)
                git(self.seed, 'rm', '-r', '--ignore-unmatch', '.')
                self.seed_files(files)
                git(self.root, 'clone', '--bare', str(self.seed), str(case))
                head = git(case, 'rev-parse', 'HEAD')
                with patch('metadata_repository._repository_url', return_value=str(case)), self.assertRaises(ValueError):
                    self.module.open_repository(self.config, RepositoryAPI(False), self.root / ('work' + str(index)))
                self.assertEqual(git(case, 'rev-parse', 'HEAD'), head)

    def test_link_tree_is_rejected_even_when_checkout_emulates_symlinks(self):
        self.seed_files(self.legitimate())
        oid = git(self.seed, 'hash-object', '-w', '--stdin', input=b'outside').decode().strip()
        git(self.seed, 'update-index', '--cacheinfo', '120000,' + oid + ',README.md')
        git(self.seed, 'commit', '-m', 'Link fixture')
        self.make_remote()
        with self.assertRaises(ValueError):
            self.open()

    def test_new_repository_rejects_fixed_link_target_without_overwriting_it(self):
        self.seed_files({'README.md': b'template'})
        oid = git(self.seed, 'hash-object', '-w', '--stdin', input=b'outside').decode().strip()
        git(self.seed, 'update-index', '--cacheinfo', '120000,' + oid + ',README.md')
        git(self.seed, 'commit', '-m', 'Link fixture')
        self.make_remote()
        before = git(self.bare, 'rev-parse', 'HEAD')
        with self.assertRaises(ValueError):
            self.open(created=True)
        self.assertEqual(git(self.bare, 'rev-parse', 'HEAD'), before)

    def test_commit_push_selects_only_requested_metadata_and_preserves_unselected(self):
        self.seed_files(self.legitimate())
        self.make_remote()
        repo = self.open()
        (repo['path'] / 'README.md').write_bytes(b'local unsent edit\n')
        git(repo['path'], 'add', 'README.md')
        head = self.module.commit_metadata(repo, {'queue/2/queue-2-any-any.tar.zst.sha256': 'b' * 64}, {'queue': '2'})
        self.assertEqual(git(self.bare, 'rev-parse', 'HEAD').decode().strip(), head)
        self.assertEqual(git(self.bare, 'show', 'HEAD:README.md'), b'keep description\n')
        self.assertEqual((repo['path'] / 'README.md').read_bytes(), b'local unsent edit\n')
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/1/queue-1-any-any.tar.zst.sha256'), b'a' * 64 + b'\n')
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/2/queue-2-any-any.tar.zst.sha256'), b'b' * 64 + b'\n')
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/package.toml'), b'latest = "2"\n')
        self.assertEqual(self.module.commit_metadata(repo, {}, {}), head)

    def test_digest_index_and_atomic_package_removal(self):
        self.seed_files(self.legitimate(**{'queue/2/queue-2-any-any.tar.zst.sha256': b'b' * 64 + b'\n'}))
        self.make_remote()
        repo = self.open()
        self.assertEqual(repo['existing_digests']['queue/1/queue-1-any-any.tar.zst.sha256'], 'a' * 64)
        previous = repo['head']
        head = self.module.commit_metadata(repo, {}, {'queue': '1'},
            remove_paths=('queue/2/queue-2-any-any.tar.zst.sha256',))
        self.assertNotEqual(head, previous)
        self.assertEqual(git(self.bare, 'rev-list', '--count', previous + '..HEAD').strip(), b'1')
        self.assertNotIn('queue/2/queue-2-any-any.tar.zst.sha256', git(self.bare, 'ls-tree', '-r', '--name-only', 'HEAD').decode())
        self.assertEqual(git(self.bare, 'show', 'HEAD:queue/package.toml'), b'latest = "1"\n')
        self.assertNotIn('queue/2/queue-2-any-any.tar.zst.sha256', repo['existing_digests'])
        self.assertEqual(self.module.commit_metadata(repo, {}, {}, remove_paths=('queue/2/queue-2-any-any.tar.zst.sha256',)), head)
        final = self.module.commit_metadata(repo, {}, {}, remove_paths=('queue/package.toml',))
        self.assertNotEqual(final, head)
        self.assertNotIn('queue', repo['existing_latest'])

    def test_delete_conflicts_fail_before_any_write(self):
        self.seed_files(self.legitimate())
        self.make_remote()
        repo = self.open()
        before = repo['head']
        target = 'queue/1/queue-1-any-any.tar.zst.sha256'
        for removals in [('README.md',), ('../outside',), (target, 'Queue/package.toml'), (target,)]:
            with self.subTest(removals=removals), self.assertRaises(ValueError):
                self.module.commit_metadata(repo, {target: 'b' * 64}, {}, remove_paths=removals)
            self.assertEqual((repo['path'] / target).read_bytes(), b'a' * 64 + b'\n')
            self.assertEqual(git(self.bare, 'rev-parse', 'HEAD').decode().strip(), before)

    def test_invalid_digest_index_is_rejected(self):
        self.seed_files(self.legitimate(**{'queue/1/queue-1-any-any.tar.zst.sha256': b'A' * 64 + b'\n'}))
        self.make_remote()
        with self.assertRaises(ValueError):
            self.open()

    def test_darwin_digest_preserves_existing_metadata(self):
        files = self.legitimate(**{
            'lz4/package.toml': b'latest = "1.9.4"\n',
            'lz4/1.10.0/lz4-1.10.0-linux-x86_64.tar.zst.sha256': b'b' * 64 + b'\n',
            'lz4/1.10.0/lz4-1.10.0-linux-aarch64.tar.zst.sha256': b'c' * 64 + b'\n',
            'lz4/1.10.0/lz4-1.10.0-windows-x86_64.tar.zst.sha256': b'd' * 64 + b'\n',
        })
        self.seed_files(files)
        self.make_remote()
        repo = self.open()
        paths = ['lz4/1.10.0/lz4-1.10.0-darwin-aarch64.tar.zst.sha256',
                 'autolib-importer/autolib-importer-darwin-aarch64.tar.xz.sha256']
        self.module.commit_metadata(repo, {name: 'e' * 64 for name in paths}, {})
        for name, content in files.items():
            self.assertEqual(git(self.bare, 'show', 'HEAD:' + name), content)
        for name in paths:
            self.assertEqual(git(self.bare, 'show', 'HEAD:' + name), b'e' * 64 + b'\n')
        with patch('metadata_repository._repository_url', return_value=str(self.bare)):
            reopened = self.module.open_repository(self.config, RepositoryAPI(False), self.root / 'again')
        self.assertEqual(reopened['existing_latest']['lz4'], '1.9.4')

    def test_push_failure_never_returns_or_records_confirmed_head(self):
        self.seed_files(self.legitimate())
        self.make_remote()
        repo = self.open()
        before = repo['head']
        before_digests = dict(repo['existing_digests'])
        before_latest = dict(repo['existing_latest'])
        git(repo['path'], 'remote', 'set-url', 'origin', str(self.root / 'missing.git'))
        with self.assertRaises(RuntimeError) as failure:
            self.module.commit_metadata(repo, {}, {'queue': '2'})
        self.assertNotIn('fake-secret', str(failure.exception))
        self.assertEqual(repo['head'], before)
        self.assertEqual(repo['existing_digests'], before_digests)
        self.assertEqual(repo['existing_latest'], before_latest)
        self.assertEqual(git(self.bare, 'rev-parse', 'HEAD').decode().strip(), before)

    def test_selected_link_parent_and_invalid_digest_fail_before_write(self):
        self.seed_files(self.legitimate())
        self.make_remote()
        repo = self.open()
        before = git(self.bare, 'rev-parse', 'HEAD')
        for digests, latest in [({'../outside': 'a' * 64}, {}),
                                ({'queue/1/queue-1-any-any.tar.zst.sha256': 'A' * 64}, {}),
                                ({}, {'queue': '../escape'})]:
            with self.subTest(digests=digests, latest=latest), self.assertRaises(ValueError):
                self.module.commit_metadata(repo, digests, latest)
        self.assertEqual(git(self.bare, 'rev-parse', 'HEAD'), before)

    def test_selected_directory_link_does_not_modify_its_target(self):
        self.seed_files(self.legitimate())
        self.make_remote()
        repo = self.open()
        outside = self.root / 'outside'
        outside.mkdir()
        link = repo['path'] / 'other'
        if sys.platform == 'win32':
            subprocess.run(['cmd', '/c', 'mklink', '/J', str(link), str(outside)], check=True, capture_output=True)
        else:
            link.symlink_to(outside, target_is_directory=True)
        try:
            with self.assertRaises(ValueError):
                self.module.commit_metadata(repo, {}, {'other': '1'})
            self.assertEqual(list(outside.iterdir()), [])
        finally:
            if sys.platform == 'win32':
                link.rmdir()
            else:
                link.unlink()

    def test_askpass_uses_temporary_script_and_process_only_credentials(self):
        captured = []
        def run(command, **options):
            environment = options['env']
            script = Path(environment['GIT_ASKPASS'])
            captured.append(script)
            self.assertTrue(script.is_file())
            self.assertNotIn('fake-secret', script.read_text())
            self.assertNotIn('fake-secret', ' '.join(command))
            self.assertEqual(environment['AUTOLIB_GIT_TOKEN'], 'fake-secret')
            self.assertEqual(environment['AUTOLIB_GIT_USERNAME'], 'actual-writer')
            return subprocess.CompletedProcess(command, 0, b'confirmed', b'')
        with patch('metadata_repository.subprocess.run', side_effect=run):
            result = self.module._git(['status'], auth={'AUTOLIB_GIT_USERNAME': 'actual-writer',
                                                       'AUTOLIB_GIT_TOKEN': 'fake-secret'})
        self.assertEqual(result, b'confirmed')
        self.assertFalse(captured[0].exists())


if __name__ == '__main__':
    unittest.main()
