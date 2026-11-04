import argparse
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import unittest


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
    tool = 'autolib-importer-' + platform + ('.exe' if system == 'windows' else '')
    roots = ['probe@1', 'dep@2']
    plan = dict(platforms=[dict(id=platform, roots=roots, description=description)], publisher='')
    identity = dict(repository='controlled-fork/source', ref='refs/heads/native', sha=sha,
                    checkout_sha=sha, run_id='42', attempt='1')
    packages = [dict(name=m['name'], version=m['version'], os=system, arch=arch,
                     dependencies=m['depends'] or [], publish=True) for m in description['modules']]
    transport = dict(run=identity, platform=platform, producer_prefix=str(installed),
                     entry='install/autolib.cmake', importer='install/' + tool, packages=packages)
    result = dict(run=identity, platform=platform, complete=True, error='',
                  packages=[dict(slot={k: p[k] for k in ['name', 'version', 'os', 'arch']},
                                 built=True, verified=True) for p in packages],
                  roots=[dict(root=r, verified=True) for r in roots],
                  importer=dict(file=tool, verified=True),
                  entry=dict(file='autolib.cmake', verified=True,
                             sha256=hashlib.sha256((installed / 'autolib.cmake').read_bytes()).hexdigest()))
    inputs = source / 'build/entry-delivery-inputs'
    plan_name, transport_name = 'ci-plan-42-1', 'ci-install-42-1-' + platform
    (inputs / plan_name).mkdir(parents=True)
    (inputs / plan_name / 'plan.json').write_text(json.dumps(plan), encoding='utf-8')
    (inputs / plan_name / 'run.json').write_text(json.dumps(identity), encoding='utf-8')
    (inputs / transport_name).mkdir()
    with tarfile.open(inputs / transport_name / 'transport.tar', 'w') as archive:
        for name, value in [('transport.json', transport), ('plan.json', plan), ('result.json', result)]:
            data = json.dumps(value).encode()
            member = tarfile.TarInfo(name)
            member.size = len(data)
            archive.addfile(member, io.BytesIO(data))
        archive.add(installed, arcname='install')
    selection = dict(source_run=identity, plan=plan, explicit_latest={},
                     artifacts=[dict(name=plan_name), dict(name=transport_name)])
    def snapshot(root):
        return {str(p.relative_to(root)): (p.lstat().st_mode, p.lstat().st_mtime_ns,
                os.readlink(p) if p.is_symlink() else p.read_bytes() if p.is_file() else None)
                for p in root.rglob('*')}
    before = snapshot(installed), snapshot(inputs)
    manifest = publication.prepare_publication(
        selection, inputs, source, builder, source / 'build/entry-delivery-publication',
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
