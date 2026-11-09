import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
STUB = r'''
import json
import os
from pathlib import Path
import shutil
import sys

root = Path(os.environ['DARWIN_FIXTURE'])
name = Path(sys.argv[0]).name
args = sys.argv[1:]
with (root / 'events').open('a') as stream:
    stream.write(json.dumps([name, args]) + '\n')
if name == 'uname':
    print(os.environ.get('FIXTURE_OS', 'Darwin') if args == ['-s'] else os.environ.get('FIXTURE_ARCH', 'arm64'))
elif name == 'sw_vers':
    print('15.7')
elif name == 'sysctl':
    assert args == ['-n', 'hw.logicalcpu'], args
    print(os.environ.get('FIXTURE_CPU', '4'))
elif name == 'xcrun':
    if args == ['--show-sdk-version']:
        print('15.5')
    elif args == ['--sdk', 'macosx', '--show-sdk-path']:
        print(root / 'selected SDK')
    else:
        assert args[:1] == ['--find'], args
        print(root / 'bin' / ('bootstrap-make' if args[1] == 'make' else args[1]))
elif name in ('clang', 'clang++'):
    print('Apple clang version 17.0.0')
elif name == 'brew':
    if os.environ.get('FIXTURE_BREW_FAILURE'):
        sys.exit(72)
    if args[:1] == ['install']:
        assert args[1:] == ['pkg-config', 'libomp'], args
    elif args[:2] == ['list', '--versions']:
        print('pkg-config 0.29.2\nlibomp 21.1.0')
    elif args[:1] == ['--prefix']:
        assert args[1] in ('pkg-config', 'libomp'), args
        print(root / 'brew' / args[1])
    else:
        raise AssertionError(args)
elif name == 'curl':
    output = Path(args[args.index('-o') + 1])
    shutil.copyfile(root / output.name, output)
elif name == 'cmake':
    if args == ['--version']:
        print('cmake version ' + os.environ.get('FIXTURE_CMAKE_VERSION', '3.31.12'))
    else:
        assert args[0] == '-P' and args[1].endswith('/src/autolib-builder/go-sdk.cmake'), args
        assert args[2:6] == ['--', 'build', '-mod=readonly', '-trimpath'], args
        assert args[-1] == './src/autolib-ci', args
        Path(args[args.index('-o') + 1]).write_text('built autolib-ci')
elif name == 'configure':
    if os.environ.get('FIXTURE_REQUIRE_SDK') and os.environ.get('SDKROOT') != str(root / 'selected SDK'):
        Path('config.log').write_text("ld: library 'System' not found\n")
        sys.exit(77)
    if os.environ.get('FIXTURE_CONFIGURE_FAILURE'):
        Path('config.log').write_text('fixture linker diagnostic: library not found\n')
        sys.exit(77)
    assert os.environ['CC'] == str(root / 'bin/clang')
    assert os.environ['CXX'] == str(root / 'bin/clang++')
    prefix = next(arg.removeprefix('--prefix=') for arg in args if arg.startswith('--prefix='))
    Path('prefix').write_text(prefix)
elif name == 'bootstrap-make':
    if os.environ.get('FIXTURE_REQUIRE_SDK'):
        assert os.environ['SDKROOT'] == str(root / 'selected SDK')
    assert args[:1] == ['-C'], args
    if args[-1] == 'install':
        prefix = Path(Path(args[1], 'prefix').read_text())
        shutil.copyfile(root / 'stub', prefix / 'bin/make')
        (prefix / 'bin/make').chmod(0o755)
elif name == 'make':
    assert args == ['--version'], args
    print('GNU Make ' + os.environ.get('FIXTURE_MAKE_VERSION', '4.4.1'))
else:
    raise AssertionError(name)
'''


class DarwinPreparationTests(unittest.TestCase):
    def fixture(self, root):
        binary = ('#!' + sys.executable + '\n' + STUB).encode()
        (root / 'bin').mkdir()
        (root / 'stub').write_bytes(binary)
        (root / 'stub').chmod(0o755)
        for name in ('uname', 'sw_vers', 'sysctl', 'xcrun', 'clang', 'clang++', 'curl', 'bootstrap-make', 'brew'):
            (root / 'bin' / name).symlink_to(root / 'stub')
        config = json.loads((ROOT / '.github/ci-tools.json').read_text())
        asset = next(a for a in config['cmake']['assets'] if a['platform'] == 'darwin-aarch64')
        for filename, member in (
            (asset['filename'], 'cmake-3.31.12-macos-universal/CMake.app/Contents/bin/cmake'),
            ('make-4.4.1.tar.gz', 'make-4.4.1/configure'),
        ):
            path = root / filename
            with tarfile.open(path, 'w:gz') as archive:
                info = tarfile.TarInfo(member)
                info.size = len(binary)
                info.mode = 0o755
                archive.addfile(info, io.BytesIO(binary))
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            if filename == asset['filename']:
                asset['sha256'] = digest
            else:
                config['make']['sha256'] = digest
        return config

    def execute(self, root, config, platforms, **variables):
        (root / 'config.json').write_text(json.dumps(config))
        (root / 'plan.json').write_text(json.dumps({'platforms': platforms}))
        environment = dict(os.environ, PATH=str(root / 'bin') + os.pathsep + os.environ['PATH'], DARWIN_FIXTURE=str(root))
        environment.update(variables)
        result = subprocess.run([
            '/bin/bash', str(ROOT / '.github/scripts/prepare-darwin.sh'),
            '--config', str(root / 'config.json'), '--plan', str(root / 'plan.json'),
            '--platform', 'darwin-aarch64', '--work-root', str(root / 'work with spaces'),
        ], text=True, capture_output=True, env=environment, cwd=ROOT)
        events = [json.loads(line) for line in (root / 'events').read_text().splitlines()] if (root / 'events').exists() else []
        return result, events

    def test_brew_formula_selection(self):
        for formulas in [[], ['pkg-config', 'libomp', 'pkg-config']]:
            with self.subTest(formulas=formulas), tempfile.TemporaryDirectory(prefix='brew formula ') as temporary:
                root = Path(temporary)
                environment_file = root / 'github-env'
                platforms = [dict(id='linux-x86_64', roots=['other'], system_dependencies=dict(brew=['unrelated'])),
                             dict(id='darwin-aarch64', roots=['lz4@1.10.0'], system_dependencies=dict(brew=formulas))]
                result, events = self.execute(root, self.fixture(root), platforms, GITHUB_ENV=str(environment_file),
                                              CMAKE_PREFIX_PATH='/existing/prefix', PKG_CONFIG_PATH='/existing/pc')
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(set(json.loads(result.stdout)), {'cmake_command', 'make_program', 'budget'})
                brew = [args for name, args in events if name == 'brew']
                exports = dict(line.split('=', 1) for line in environment_file.read_text().splitlines())
                if formulas:
                    self.assertEqual([args for args in brew if args[:1] == ['install']], [['install', 'pkg-config', 'libomp']])
                    self.assertIn(['list', '--versions', 'pkg-config', 'libomp'], brew)
                    self.assertIn('libomp 21.1.0', result.stderr)
                    self.assertEqual(exports['CMAKE_PREFIX_PATH'], str(root / 'brew/pkg-config') + ':' + str(root / 'brew/libomp') + ':/existing/prefix')
                    self.assertEqual(exports['PKG_CONFIG_PATH'], ':'.join(str(root / 'brew' / formula / suffix) for formula in ['pkg-config', 'libomp'] for suffix in ['lib/pkgconfig', 'share/pkgconfig']) + ':/existing/pc')
                else:
                    self.assertEqual(brew, [])
                    self.assertNotIn('CMAKE_PREFIX_PATH', exports)
                    self.assertNotIn('PKG_CONFIG_PATH', exports)

    def test_brew_failure_is_fatal(self):
        for formulas, variables in [(['pkg-config', 'libomp'], dict(FIXTURE_BREW_FAILURE='1')),
                                   (['--cask'], {}), (['tap/formula'], {}), (['libomp;echo bad'], {}), ([''], {}), (False, {}), (None, {}), ('libomp', {})]:
            with self.subTest(formulas=formulas), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                result, events = self.execute(root, self.fixture(root), [dict(id='darwin-aarch64', roots=['lz4@1.10.0'], system_dependencies=dict(brew=formulas))], **variables)
                self.assertNotEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout, '')
                if not variables:
                    self.assertNotIn('brew', [name for name, _ in events])

    def test_tools_only_filtered_and_library_work(self):
        for roots, filtered, cpu, budget in (([], [], '4', 0), ([], ['librealsense@2.58.1'], '4', 0), (['lz4@1.10.0'], [], '4', 4), (['lz4@1.10.0'], [], '0', 1)):
            with self.subTest(roots=roots, filtered=filtered, cpu=cpu), tempfile.TemporaryDirectory(prefix='darwin preparation ') as temporary:
                root = Path(temporary)
                config = self.fixture(root)
                result, events = self.execute(root, config, [{'id': 'darwin-aarch64', 'roots': roots, 'filtered_roots': filtered}], FIXTURE_CPU=cpu)
                self.assertEqual(result.returncode, 0, result.stderr)
                output = json.loads(result.stdout)
                self.assertEqual(set(output), {'cmake_command', 'make_program', 'budget'})
                self.assertEqual(output['budget'], budget)
                self.assertTrue(Path(output['cmake_command']).is_absolute())
                self.assertTrue(Path(output['cmake_command']).is_file())
                self.assertTrue((root / 'work with spaces/autolib-ci').is_file())
                downloads = ' '.join(' '.join(args) for name, args in events if name == 'curl')
                if roots:
                    self.assertTrue(Path(output['make_program']).is_file())
                    self.assertIn('make-4.4.1.tar.gz', downloads)
                    self.assertIn('Apple clang', result.stderr)
                    self.assertIn('bootstrap-make', [name for name, _ in events])
                else:
                    self.assertEqual(output['make_program'], '')
                    self.assertNotIn('make-4.4.1.tar.gz', downloads)
                    self.assertNotIn('bootstrap-make', [name for name, _ in events])

    def test_rejects_wrong_host_and_invalid_plan_before_download(self):
        for variables, platforms in (
            ({'FIXTURE_OS': 'Linux'}, [{'id': 'darwin-aarch64', 'roots': []}]),
            ({'FIXTURE_ARCH': 'x86_64'}, [{'id': 'darwin-aarch64', 'roots': []}]),
            ({}, []),
            ({}, [{'id': 'darwin-aarch64', 'roots': []}] * 2),
        ):
            with self.subTest(variables=variables, platforms=platforms), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                result, events = self.execute(root, self.fixture(root), platforms, **variables)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, '')
                self.assertNotIn('curl', [name for name, _ in events])
                self.assertTrue(result.stderr)

    def test_bad_digest_prevents_archive_program_execution(self):
        for tool in ('cmake', 'make'):
            with self.subTest(tool=tool), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                config = self.fixture(root)
                if tool == 'cmake':
                    next(a for a in config['cmake']['assets'] if a['platform'] == 'darwin-aarch64')['sha256'] = '0' * 64
                else:
                    config['make']['sha256'] = '0' * 64
                result, events = self.execute(root, config, [{'id': 'darwin-aarch64', 'roots': ['lz4@1.10.0']}])
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, '')
                self.assertNotIn('cmake' if tool == 'cmake' else 'configure', [name for name, _ in events])
                self.assertIn('checksum', result.stderr.lower())

    def test_rejects_wrong_tool_versions(self):
        for variable in ('FIXTURE_CMAKE_VERSION', 'FIXTURE_MAKE_VERSION'):
            with self.subTest(variable=variable), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                result, _ = self.execute(root, self.fixture(root), [{'id': 'darwin-aarch64', 'roots': ['lz4@1.10.0']}], **{variable: '0.0.0'})
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, '')
                self.assertIn('version', result.stderr.lower())

    def test_configure_failure_retains_linker_diagnostic(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            result, events = self.execute(root, self.fixture(root), [{'id': 'darwin-aarch64', 'roots': ['lz4@1.10.0']}], FIXTURE_CONFIGURE_FAILURE='1')
            self.assertEqual(result.returncode, 77)
            self.assertEqual(result.stdout, '')
            self.assertIn('fixture linker diagnostic: library not found', result.stderr)
            self.assertNotIn('bootstrap-make', [name for name, _ in events])

    def test_make_uses_selected_macos_sdk(self):
        with tempfile.TemporaryDirectory(prefix='darwin SDK ') as temporary:
            root = Path(temporary)
            result, _ = self.execute(root, self.fixture(root), [{'id': 'darwin-aarch64', 'roots': ['lz4@1.10.0']}], FIXTURE_REQUIRE_SDK='1', SDKROOT='/wrong/inherited/SDK')
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue(Path(json.loads(result.stdout)['make_program']).is_file())


if __name__ == '__main__':
    unittest.main()
