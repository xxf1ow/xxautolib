import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import unittest


def prepare_native_fixture(source, installed, builder, system, arch):
    source, installed = source.resolve(), installed.resolve()
    sys.path.insert(0, str(Path(__file__).resolve().parents[3] / '.github/actions/gitee-release'))
    from publication_rules import inject_release_root, delivery_record
    output = source / 'build/entry-delivery-publication'
    output.mkdir(parents=True)
    original = (installed / 'autolib.cmake').read_bytes()
    published = inject_release_root(original, 'fixture/mirror', '中文/release')
    (output / 'autolib.cmake').write_bytes(published)
    assets = [dict(kind='entry',name='autolib.cmake',path='autolib.cmake',size=len(published),
                   sha256=hashlib.sha256(published).hexdigest())]
    tool = 'autolib-importer-' + system + '-' + arch + ('.exe' if system == 'windows' else '') + '.tar.xz'
    for package, version in [('probe','1'),('dep','1'),('dep','2'),('autolib-importer','')]:
        name = tool if not version else package + '-' + version + '-' + system + '-' + arch + '.tar.zst'
        data = (source / 'build/release' / name).read_bytes()
        (output / name).write_bytes(data)
        assets.append(dict(kind='library' if version else 'importer',name=name,path=name,size=len(data),
                           sha256=hashlib.sha256(data).hexdigest(),package=dict(name=package,version=version)))
    for asset in assets:
        if asset['kind'] != 'entry':
            identity = dict(asset, platform=system + '-' + arch)
            if asset['kind'] == 'library':
                identity['package'] = dict(asset['package'], os=system, arch=arch)
            asset['delivery'] = delivery_record(identity, repository='fixture/mirror', tag='中文/release', github_repository='xxf1ow/xxautolib')
            # A small real archive exercises the GitHub-only consumer path without a synthetic byte size.
            if asset['package'] == dict(name='dep', version='1'):
                asset['delivery']['urls'] = asset['delivery']['urls'][1:]
    if (installed / 'autolib.cmake').read_bytes() != original:
        raise AssertionError('entry injection changed native installation')
    manifest=output/'consumer-assets.json'
    manifest.write_text(json.dumps({'assets':assets}))
    print(manifest)


class EntryInjectionTests(unittest.TestCase):
    def test_injection_changes_only_the_empty_assignment(self):
        sys.path.insert(0, str(Path(__file__).resolve().parents[3] / '.github/actions/gitee-release'))
        from publication_rules import inject_release_root
        raw=b'# prefix\nset(_autolib_release_root "")\n# suffix\n'
        result=inject_release_root(raw,'fixture/mirror','中文/release')
        self.assertEqual(result,b'# prefix\nset(_autolib_release_root "https://gitee.com/fixture/mirror/releases/download/%E4%B8%AD%E6%96%87%2Frelease")\n# suffix\n')
        for bad in [b'# no assignment',raw+raw,raw.replace(b'""',b'"already-set"')]:
            with self.subTest(bad=bad),self.assertRaises(ValueError):inject_release_root(bad,'fixture/mirror','fixed')


class EntryDeliveryTests(unittest.TestCase):
    def test_production_publication_entry_native_consumer(self):
        environment = dict(os.environ, AUTOLIB_ENTRY_DELIVERY_PYTHON=sys.executable,
                           AUTOLIB_ENTRY_DELIVERY_FIXTURE=str(Path(__file__).resolve()))
        result = subprocess.run(
            ['cmake', '-P', 'src/autolib-builder/go-sdk.cmake', '--', 'test', '-mod=readonly', '-p=1', '-parallel=1',
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
