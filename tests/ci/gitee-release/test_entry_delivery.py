import argparse
import atexit
from functools import lru_cache
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import shutil
import sys
import tempfile
import unittest


@lru_cache(maxsize=1)
def ci_tool():
    root = Path(__file__).resolve().parents[3]
    temporary = tempfile.TemporaryDirectory(prefix='entry-delivery-ci-')
    atexit.register(temporary.cleanup)
    executable = Path(temporary.name) / ('autolib-ci.exe' if os.name == 'nt' else 'autolib-ci')
    subprocess.run(['cmake', '-P', str(root / 'src/autolib-builder/go-sdk.cmake'), '--', 'build',
                    '-mod=readonly', '-trimpath', '-o', str(executable), './src/autolib-ci'],
                   cwd=root, check=True, capture_output=True)
    return executable


def packaged_inputs(source, installed, builder, plan, identity):
    platform = plan['platforms'][0]['id']
    system = platform.split('-')[0]
    tool = 'autolib-importer-' + platform + ('.exe' if system == 'windows' else '')
    work = installed.parent
    expected = source / 'build/ci' / (identity['run_id'] + '-' + identity['attempt']) / platform
    if work != expected or installed.name != 'install':
        raise AssertionError('native installation must use the CI work root')
    packages = [dict(slot=dict(name=m['name'], version=m['version'],
                              os='any' if m['platform_independent'] else system,
                              arch='any' if m['platform_independent'] else platform.split('-', 1)[1]),
                     built=True, verified=True) for m in plan['platforms'][0]['description']['modules']]
    result = dict(run=identity, platform=platform, complete=True, error='', packages=packages,
                  roots=[dict(root=r, verified=True) for r in plan['platforms'][0]['roots']],
                  importer=dict(file=tool, verified=True),
                  entry=dict(file='autolib.cmake', verified=True,
                             sha256=hashlib.sha256((installed / 'autolib.cmake').read_bytes()).hexdigest()))
    inputs = source / 'build/entry-delivery-inputs'
    suffix = identity['run_id'] + '-' + identity['attempt']
    plan_name, transport_name = 'ci-plan-' + suffix, 'ci-package-' + suffix + '-' + platform
    plan_dir = inputs / plan_name
    plan_dir.mkdir(parents=True)
    for name, value in [('plan.json', plan), ('run.json', identity)]:
        (plan_dir / name).write_text(json.dumps(value), encoding='utf-8')
    (work / 'results').mkdir(exist_ok=True)
    (work / 'results' / (platform + '.json')).write_text(json.dumps(result), encoding='utf-8')
    tool_dir = work / ('build/tools' if packages else 'tools')
    tool_dir.mkdir(parents=True, exist_ok=True)
    shutil.copy2(builder, tool_dir / ('autolib-builder.exe' if system == 'windows' else 'autolib-builder'))
    (inputs / transport_name).mkdir()
    completed = subprocess.run([str(ci_tool()), 'transport', '--repository', identity['repository'], '--source-root', str(source),
                    '--plan', str(plan_dir / 'plan.json'), '--run-identity', str(plan_dir / 'run.json'),
                    '--platform', platform, '--work-root', str(work), '--cmake-command', shutil.which('cmake'),
                    '--output', str(work / 'transport.tar')], capture_output=True, text=True)
    if completed.returncode:
        raise AssertionError(completed.stdout + completed.stderr)
    shutil.copy2(work / 'transport.tar', inputs / transport_name / 'transport.tar')
    return inputs, dict(source_run=identity, plan=plan, explicit_latest={},
                        artifacts=[dict(name=plan_name), dict(name=transport_name)])


def prepare_native_fixture(source, installed, builder, system, arch):
    source, installed, builder = source.resolve(), installed.resolve(), builder.resolve()
    script = Path(__file__).resolve().parents[3] / '.github/scripts/prepare-publication.py'
    spec = importlib.util.spec_from_file_location('entry_delivery_publication', script)
    publication = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(publication)

    def run(*args):
        return subprocess.run([str(a) for a in args], check=True, capture_output=True, text=True).stdout.strip()

    run('git', 'init', source)
    run('git', '-C', source, 'add', 'src/autolib.cmake')
    run('git', '-C', source, '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
        '-c', 'commit.gpgsign=false', 'commit', '-m', 'native publication fixture')
    sha = run('git', '-C', source, 'rev-parse', 'HEAD')
    description = json.loads(run(builder, 'describe', '--source-root=' + str(source),
                                 '--system-name=' + {'linux': 'Linux', 'windows': 'Windows'}[system],
                                 '--architecture=' + arch, '--module=probe@1', '--module=dep@2'))
    platform = system + '-' + arch
    roots = ['probe@1', 'dep@2']
    plan = dict(platforms=[dict(id=platform, roots=roots, description=description,
                               requested_roots=roots, filtered_roots=[],
                               layers=[['dep@1', 'dep@2'], ['probe@1']], system_dependencies={})],
                requested_modules=roots, publisher='')
    identity = dict(repository='controlled-fork/source', ref='refs/heads/native', sha=sha,
                    checkout_sha=sha, run_id='42', attempt='1')
    inputs, selection = packaged_inputs(source, installed, builder, plan, identity)
    def snapshot(root):
        return {str(p.relative_to(root)): (p.lstat().st_mode, p.lstat().st_mtime_ns,
                os.readlink(p) if p.is_symlink() else p.read_bytes() if p.is_file() else None)
                for p in root.rglob('*')}
    before = snapshot(installed), snapshot(inputs)
    manifest = publication.prepare_publication(
        selection, inputs, source, source / 'build/entry-delivery-publication',
        repository='fixture/mirror', tag='中文/release', source_repository='controlled-fork/source')
    if (snapshot(installed), snapshot(inputs)) != before:
        raise AssertionError('production preparation changed native installation or transport inputs')
    print(manifest)


class EntryDeliveryTests(unittest.TestCase):
    def test_production_publication_entry_native_consumer(self):
        environment = dict(os.environ, AUTOLIB_ENTRY_DELIVERY_PYTHON=sys.executable,
                           AUTOLIB_ENTRY_DELIVERY_FIXTURE=str(Path(__file__).resolve()))
        result = subprocess.run(
            ['cmake', '-P', 'src/autolib-builder/go-sdk.cmake', '--', 'test', '-mod=readonly',
             './src/autolib-importer', '-run', '^TestVersionedRemoteLoop$', '-count=1', '-v'],
            env=environment, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        print(result.stdout)
        self.assertIn('production publication entry: fixture/mirror tag=中文/release', result.stdout)


if __name__ == '__main__':
    if len(sys.argv) > 1 and sys.argv[1] == 'prepare-native':
        parser = argparse.ArgumentParser()
        for name in ['source', 'installed', 'builder']:
            parser.add_argument('--' + name, required=True, type=Path)
        for name in ['system', 'arch']:
            parser.add_argument('--' + name, required=True)
        options = parser.parse_args(sys.argv[2:])
        prepare_native_fixture(options.source, options.installed, options.builder, options.system, options.arch)
    else:
        unittest.main()
