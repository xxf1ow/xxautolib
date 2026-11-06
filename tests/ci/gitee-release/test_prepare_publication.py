import copy
import hashlib
import importlib.util
import io
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import stat
import subprocess
import shutil
import tarfile
import tempfile
import threading
import unittest
from unittest.mock import patch
import zipfile


SCRIPT = Path(__file__).resolve().parents[3] / '.github/scripts/prepare-publication.py'
REPOSITORY = 'xxf1ow/xxautolib'
SHA = 'a' * 40
PLATFORMS = ['linux-x86_64', 'windows-x86_64', 'linux-aarch64']
RUN = dict(repository=REPOSITORY, ref='refs/heads/release', sha=SHA,
           checkout_sha=SHA, run_id='42', attempt='2')
ENTRY = b'# original entry\nset(_autolib_release_root "")\n# retained bytes\n'
RELEASE_ROOT = 'https://gitee.com/fixture/mirror/releases/download/%E4%B8%AD%E6%96%87%2Frelease'


def load_module():
    if not SCRIPT.exists():
        return None
    spec = importlib.util.spec_from_file_location('prepare_publication', SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def module(name, version, universal=False):
    return dict(name=name, version=version, target='fixture_' + name + version,
                depends=[], package_platform='any-any' if universal else 'linux-x86_64')


def plan(platforms):
    modules = [module('eigen', '3.4.0', True), module('eigen', '5.0.1', True),
               module('queue', '1.0')]
    return dict(platforms=[dict(id=p, requested_roots=['eigen@3.4.0', 'eigen@5.0.1', 'queue@1.0'],
                               filtered_roots=[], roots=['eigen@3.4.0', 'eigen@5.0.1', 'queue@1.0'],
                               description=dict(modules=[dict(m, package_platform='any-any' if m['package_platform'] == 'any-any' else p) for m in modules]),
                               layers=[['eigen@3.4.0', 'eigen@5.0.1', 'queue@1.0']])
                           for p in platforms], requested_modules=[], publishers={m['name'] + '@' + m['version']: next(p for p in ['linux-x86_64', 'linux-aarch64', 'windows-x86_64', 'darwin-aarch64'] if p in platforms) for m in modules if m['package_platform'] == 'any-any'} if platforms else {})


def tool(platform):
    return 'autolib-importer-' + platform + ('.exe' if platform.startswith('windows') else '')


def json_bytes(value):
    return json.dumps(value).encode()


def zipped(files):
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w') as archive:
        for name, value in files.items():
            archive.writestr(name, value)
    return output.getvalue()


def make_transport(path, selected_plan, platform, mutate=None, extra=(), identity=RUN):
    packages = []
    os_name, arch = platform.split('-', 1)
    native_plan = next(p for p in selected_plan['platforms'] if p['id'] == platform)
    for item in native_plan['description']['modules']:
        universal = item['package_platform'] == 'any-any'
        packages.append(dict(name=item['name'], version=item['version'],
                             os='any' if universal else os_name,
                             arch='any' if universal else arch, dependencies=[],
                             publish=not universal or selected_plan['publishers'][item['name'] + '@' + item['version']] == platform))
    assets = [dict(kind='library', name=f"{p['name']}-{p['version']}-{p['os']}-{p['arch']}.tar.zst",
                   package={k: p[k] for k in ['name', 'version', 'os', 'arch']})
              for p in packages if p['publish']]
    assets.append(dict(kind='importer', name=tool(platform) + '.tar.xz', platform=platform))
    transport = dict(run=identity.copy(), platform=platform, entry='autolib.cmake',
                     packages=packages, assets=assets)
    result = dict(run=identity.copy(), platform=platform, complete=True, error='',
                  packages=[dict(slot={k: p[k] for k in ['name', 'version', 'os', 'arch']},
                                 built=True, verified=True) for p in packages],
                  roots=[dict(root=r, verified=True) for r in native_plan['roots']],
                  importer=dict(file=tool(platform), verified=True),
                  entry=dict(file='autolib.cmake', sha256=hashlib.sha256(ENTRY).hexdigest(), verified=True))
    files = {'transport.json': json_bytes(transport), 'plan.json': json_bytes(selected_plan),
             'result.json': json_bytes(result), 'autolib.cmake': ENTRY}
    for name in {p['name'] for p in packages}:
        files[f'candidates/{name}/package.toml'] = b'latest = "3.4.0"\n' if name == 'eigen' else b'latest = "1.0"\n'
    for asset in assets:
        body = ('fixed archive bytes: ' + asset['name']).encode()
        asset.update(path='assets/' + asset['name'], size=len(body), sha256=hashlib.sha256(body).hexdigest())
        files[asset['path']] = body
    files['transport.json'] = json_bytes(transport)
    if mutate:
        mutate(transport, result, files)
        files['transport.json'], files['result.json'] = json_bytes(transport), json_bytes(result)
    with tarfile.open(path, 'w') as archive:
        for name, data in files.items():
            member = tarfile.TarInfo(name)
            member.size, member.mode = len(data), 0o644
            archive.addfile(member, io.BytesIO(data))
        for member in extra:
            archive.addfile(member, io.BytesIO(b'x' * member.size))


class PreparationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.api = load_module()

    def setUp(self):
        self.assertIsNotNone(self.api, 'publication preparation implementation is missing')
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def test_publishers_by_package(self):
        selected = dict(platforms=[
            dict(id='windows-x86_64', description=dict(modules=[module('b', '1', True)])),
            dict(id='linux-x86_64', description=dict(modules=[module('a', '1', True)])),
            dict(id='linux-aarch64', description=dict(modules=[module('a', '2', True)])),
            dict(id='darwin-aarch64', description=dict(modules=[module('b', '1', True)]))],
            publishers={'a@1': 'linux-x86_64', 'a@2': 'linux-aarch64', 'b@1': 'windows-x86_64'})
        self.assertEqual(len(self.api._planned_platforms(selected)), 4)
        for change in [None, {}, {'a': 'linux-x86_64'},
                       dict(selected['publishers'], **{'b@1': 'darwin-aarch64'}),
                       dict(selected['publishers'], **{'extra@1': 'linux-x86_64'})]:
            with self.subTest(publishers=change), self.assertRaises(ValueError):
                self.api._planned_platforms(dict(selected, publishers=change))

    def selection(self, platforms=PLATFORMS):
        selected_plan = plan(platforms)
        return dict(source_run=RUN.copy(), plan=selected_plan,
                    artifacts=[dict(id=1, name='ci-plan-42-2')] +
                    [dict(id=n + 2, name='ci-package-42-2-' + p) for n, p in enumerate(platforms)],
                    explicit_latest={})

    def prepare(self, selected, inputs, source, output, **options):
        return self.api.prepare_publication(selected, inputs, source, output,
                                            repository='fixture/mirror', tag='中文/release',
                                            source_repository=options.get('source_repository', REPOSITORY))

    def inputs(self, selection, mutate=None):
        inputs = self.root / 'inputs'
        inputs.mkdir()
        directory = inputs / 'ci-plan-42-2'
        directory.mkdir()
        (directory / 'plan.json').write_bytes(json_bytes(selection['plan']))
        (directory / 'run.json').write_bytes(json_bytes(selection['source_run']))
        for platform in [p['id'] for p in selection['plan']['platforms']]:
            directory = inputs / ('ci-package-42-2-' + platform)
            directory.mkdir()
            make_transport(directory / 'transport.tar', selection['plan'], platform, mutate,
                           identity=selection['source_run'])
        return inputs

    def source_api(self, platforms=PLATFORMS):
        selected_plan = plan(platforms)
        run = dict(id=42, run_attempt=2, status='completed', conclusion='success',
                   path='.github/workflows/build-platform.yml', head_sha=SHA, head_branch='release',
                   event='workflow_dispatch', repository=dict(id=7, full_name=REPOSITORY),
                   head_repository=dict(id=7, full_name=REPOSITORY))
        artifacts = [dict(id=n + 1, name=name, expired=False,
                          workflow_run=dict(id=42, repository_id=7, head_repository_id=7,
                                            head_sha=SHA, head_branch='release'))
                     for n, name in enumerate(['ci-plan-42-2'] + ['ci-package-42-2-' + p for p in platforms])]
        def request(url, token):
            if '/attempts/2' in url:
                return json_bytes(run)
            if '/runs/42/artifacts?' in url:
                page = int(url.split('page=')[-1])
                return json_bytes(dict(total_count=len(artifacts), artifacts=artifacts[page-1:page]))
            if '/artifacts/1/zip' in url:
                return zipped({'plan.json': json_bytes(selected_plan), 'run.json': json_bytes(RUN)})
            raise AssertionError(url)
        return run, artifacts, request

    def test_exact_run_attempt_and_platforms(self):
        env = dict(GITHUB_REPOSITORY=REPOSITORY, SOURCE_RUN_ID='42', SOURCE_ATTEMPT='2')
        for platforms in [PLATFORMS, ['linux-x86_64']]:
            with self.subTest(platforms=platforms):
                run, artifacts, request = self.source_api(platforms)
                with patch.object(self.api, '_request', side_effect=request):
                    selected = self.api.select_source(env)
                self.assertEqual(selected['source_run'], RUN)
                self.assertEqual(len(selected['artifacts']), len(platforms) + 1)
                self.assertNotIn('packages', selected)
        changes = [lambda r, a: a.pop(), lambda r, a: a.append(copy.deepcopy(a[-1])),
                   lambda r, a: a[-1].update(expired=True),
                   lambda r, a: r['repository'].update(full_name='other/repo'),
                   lambda r, a: r.update(path='.github/workflows/other.yml'),
                   lambda r, a: r.update(head_sha='b' * 40),
                   lambda r, a: a[-1]['workflow_run'].update(head_sha='b' * 40),
                   lambda r, a: a[-1]['workflow_run'].update(repository_id=8),
                   lambda r, a: a[-1]['workflow_run'].update(head_repository_id=8),
                   lambda r, a: a[-1].update(name='ci-package-42-1-linux-aarch64'),
                   lambda r, a: r.update(event='pull_request'),
                   lambda r, a: r.update(conclusion='failure'),
                   lambda r, a: r.update(head_branch='refs/tags/v1')]
        for change in changes:
            run, artifacts, request = self.source_api()
            change(run, artifacts)
            with self.subTest(change=change), patch.object(self.api, '_request', side_effect=request):
                with self.assertRaises((ValueError, KeyError)):
                    self.api.select_source(env)

    def test_fork_selection_uses_trusted_repository(self):
        trusted = 'another-fork/source'
        run, artifacts, request = self.source_api()
        run['repository']['full_name'] = run['head_repository']['full_name'] = trusted
        def fork_request(url, token):
            self.assertIn('/repos/' + trusted + '/', url)
            if '/artifacts/1/zip' in url:
                return zipped({'plan.json': json_bytes(plan(PLATFORMS)),
                               'run.json': json_bytes(dict(RUN, repository=trusted))})
            return request(url, token)
        env = dict(GITHUB_REPOSITORY=trusted, SOURCE_RUN_ID='42', SOURCE_ATTEMPT='2')
        with patch.object(self.api, '_request', side_effect=fork_request):
            manual = self.api.select_source(env)
            self.assertEqual(manual['source_run']['repository'], trusted)
            event = self.root / 'event.json'
            event.write_bytes(json_bytes({'workflow_run': run}))
            automatic = dict(GITHUB_REPOSITORY=trusted, GITHUB_EVENT_PATH=str(event))
            self.assertEqual(self.api.select_source(automatic), manual)
            event.write_bytes(json_bytes({'workflow_run': dict(run, head_sha='b' * 40)}))
            with self.assertRaises(ValueError):
                self.api.select_source(automatic)
            run['head_repository'] = dict(id=8, full_name=REPOSITORY)
            with self.assertRaises(ValueError):
                self.api.select_source(env)

    def test_inject_release_root_preserves_bytes_and_encodes_tag(self):
        injected = self.api.inject_release_root(ENTRY, 'fixture/mirror', '中文/release')
        self.assertEqual(injected, ENTRY.replace(b'set(_autolib_release_root "")',
                                                ('set(_autolib_release_root "' + RELEASE_ROOT + '")').encode()))
        self.assertEqual(ENTRY, b'# original entry\nset(_autolib_release_root "")\n# retained bytes\n')
        commented = b'# set(_autolib_release_root "")\n' + ENTRY
        self.assertEqual(self.api.inject_release_root(commented, 'fixture/mirror', '中文/release'),
                         b'# set(_autolib_release_root "")\n' + injected)
        for source in [b'', ENTRY + ENTRY, ENTRY.replace(b'""', b'"nonempty"'),
                       ENTRY.replace(b'set(', b' set('), ENTRY.replace(b')\n', b') # extra\n')]:
            with self.subTest(source=source), self.assertRaises(ValueError):
                self.api.inject_release_root(source, 'fixture/mirror', 'fixed')
        for repository, tag in [('', 'fixed'), ('bad', 'fixed'), ('../repo', 'fixed'),
                                ('-owner/repo', 'fixed'), ('owner/.repo', 'fixed'),
                                ('fixture/mirror', ''), ('fixture/mirror', 'has space'),
                                ('fixture/mirror', 'line\nfeed'), ('fixture/mirror', 'wide\u3000space'),
                                ('fixture/mirror', '\u0080control')]:
            with self.subTest(repository=repository, tag=tag), self.assertRaises(ValueError):
                self.api.inject_release_root(ENTRY, repository, tag)

    def test_safe_transport_unpack(self):
        source = self.root / 'transport.tar'
        selected = plan(['linux-x86_64'])
        make_transport(source, selected, 'linux-x86_64')
        target = self.root / 'new'
        self.api.unpack_transport(source, target)
        self.assertEqual((target / 'assets/queue-1.0-linux-x86_64.tar.zst').read_bytes(),
                         b'fixed archive bytes: queue-1.0-linux-x86_64.tar.zst')
        (target / 'keep').write_text('keep')
        with self.assertRaises((ValueError, FileExistsError)):
            self.api.unpack_transport(source, target)
        self.assertEqual((target / 'keep').read_text(), 'keep')
        for index, (name, kind, link) in enumerate([
            ('../escape', tarfile.REGTYPE, ''), ('/absolute', tarfile.REGTYPE, ''),
            ('C:/absolute', tarfile.REGTYPE, ''), ('install/link', tarfile.SYMTYPE, '../../escape'),
            ('install/hard', tarfile.LNKTYPE, 'install/queue/1.0/payload'),
            ('install/fifo', tarfile.FIFOTYPE, ''),
            ('Assets/queue-1.0-linux-x86_64.tar.zst', tarfile.REGTYPE, ''),
            ('assets/queue-1.0-linux-x86_64.tar.zst', tarfile.REGTYPE, '')]):
            member = tarfile.TarInfo(name)
            member.type, member.linkname = kind, link
            make_transport(source, selected, 'linux-x86_64', extra=[member])
            with self.subTest(name=name), self.assertRaises((ValueError, tarfile.FilterError)):
                self.api.unpack_transport(source, self.root / ('bad' + str(index)))
        if os.name != 'nt':
            link = tarfile.TarInfo('install/queue/1.0/link')
            link.type, link.linkname = tarfile.SYMTYPE, 'payload'
            make_transport(source, selected, 'linux-x86_64', extra=[link])
            with self.assertRaises(ValueError):
                self.api.unpack_transport(source, self.root / 'linked')
            later = tarfile.TarInfo('install/queue/1.0/link/child')
            make_transport(source, selected, 'linux-x86_64', extra=[link, later])
            with self.assertRaises(ValueError):
                self.api.unpack_transport(source, self.root / 'through-link')

    def test_packaged_transport_rejects_corruption_and_extra_assets(self):
        changes = [
            lambda t, r, f: f.pop(t['assets'][0]['path']),
            lambda t, r, f: f.update({t['assets'][0]['path']: b'X' * t['assets'][0]['size']}),
            lambda t, r, f: t['assets'][0].update(size=t['assets'][0]['size'] + 1),
            lambda t, r, f: t['assets'][0].update(sha256='0' * 64),
            lambda t, r, f: t['assets'].append(copy.deepcopy(t['assets'][0])),
            lambda t, r, f: f.update({'assets/extra.tar.zst': b'extra'}),
            lambda t, r, f: t['assets'][-1].update(platform='windows-x86_64'),
            lambda t, r, f: t['assets'][0].update(kind='importer'),
            lambda t, r, f: t['assets'][0]['package'].update(version='wrong'),
            lambda t, r, f: t.update(platform='windows-x86_64'),
            lambda t, r, f: t['run'].update(run_id='43'),
            lambda t, r, f: t['run'].update(attempt='1'),
            lambda t, r, f: f.update({'candidates/extra/package.toml': b'latest="1"'}),
            lambda t, r, f: f.update({'install/header.h': b'legacy'}),
        ]
        for index, change in enumerate(changes):
            with self.subTest(index=index):
                case = self.root / str(index)
                case.mkdir()
                previous, self.root = self.root, case
                selected = self.selection(['linux-x86_64'])
                inputs = self.inputs(selected, change)
                self.root = previous
                with patch.object(self.api.subprocess, 'run', side_effect=self.git_reader([])):
                    with self.assertRaises((ValueError, FileNotFoundError)):
                        self.prepare(selected, inputs, case, case / 'build/output')

    def test_legacy_install_transport_requires_new_build(self):
        env = dict(GITHUB_REPOSITORY=REPOSITORY, SOURCE_RUN_ID='42', SOURCE_ATTEMPT='2')
        _, artifacts, request = self.source_api(['linux-x86_64'])
        artifacts[-1]['name'] = 'ci-install-42-2-linux-x86_64'
        with patch.object(self.api, '_request', side_effect=request), self.assertRaisesRegex(ValueError, 'new build'):
            self.api.select_source(env)
        selected = self.selection(['linux-x86_64'])
        inputs = self.inputs(selected, lambda t, r, f: t.update(producer_prefix='/legacy/install', importer='install/tool'))
        with patch.object(self.api.subprocess, 'run', side_effect=self.git_reader([])), self.assertRaisesRegex(ValueError, 'new build'):
            self.prepare(selected, inputs, self.root, self.root / 'build/output')

    def test_packaged_transport_rejects_links(self):
        for index, kind in enumerate([tarfile.SYMTYPE, tarfile.LNKTYPE]):
            member = tarfile.TarInfo('assets/linked.tar.zst')
            member.type, member.linkname = kind, 'queue-1.0-linux-x86_64.tar.zst'
            path = self.root / 'linked.tar'
            make_transport(path, plan(['linux-x86_64']), 'linux-x86_64', extra=[member])
            with self.subTest(kind=kind), self.assertRaises(ValueError):
                self.api.unpack_transport(path, self.root / str(index))

    def test_filtered_and_nonuniversal_plan(self):
        env = dict(GITHUB_REPOSITORY=REPOSITORY, SOURCE_RUN_ID='42', SOURCE_ATTEMPT='2')
        for platforms in [[], ['linux-x86_64']]:
            run, artifacts, request = self.source_api(platforms)
            original = request
            def queue_plan(url, token):
                if '/artifacts/1/zip' in url:
                    selected = plan(platforms)
                    selected['publishers'] = {}
                    for p in selected['platforms']:
                        p['description']['modules'] = [module('queue', '1.0')]
                        p['roots'] = ['queue@1.0']
                        p['layers'] = [['queue@1.0']]
                    return zipped({'plan.json': json_bytes(selected), 'run.json': json_bytes(RUN)})
                return original(url, token)
            with self.subTest(platforms=platforms), patch.object(self.api, '_request', side_effect=queue_plan):
                selected = self.api.select_source(env)
                if not platforms:
                    inputs = self.inputs(selected)
                    calls = []
                    with patch.object(self.api.subprocess, 'run', side_effect=self.git_reader(calls)):
                        manifest = self.prepare(selected, inputs, self.root, self.root / 'build/empty')
                    self.assertEqual([a['kind'] for a in json.loads(manifest.read_text())['assets']], ['entry'])
                    self.assertEqual(calls, [])

    def git_reader(self, calls):
        def run(command, **kwargs):
            if command[0] == 'git':
                if 'show' in command:
                    self.assertEqual(command[-1], SHA + ':src/autolib.cmake')
                    self.assertFalse(kwargs.get('text', False))
                    return subprocess.CompletedProcess(command, 0, stdout=ENTRY)
                return subprocess.CompletedProcess(command, 0, stdout=SHA + '\n')
            calls.append(command)
            raise AssertionError('preparation must not execute a builder: ' + repr(command))
        return run

    def darwin_selection(self, platforms, empty=False, filtered=False):
        selected = self.selection(platforms)
        selected['plan']['publishers'] = {'queue@1.0': next(p for p in ['linux-x86_64', 'linux-aarch64', 'windows-x86_64', 'darwin-aarch64'] if p in platforms)} if not empty else {}
        for native in selected['plan']['platforms']:
            modules = [] if empty else [module('queue', '1.0', True), module('lz4', '1.10.0')]
            if native['id'].startswith('linux') and not empty:
                modules.append(module('realsense', '2.58.1'))
            roots = [item['name'] + '@' + item['version'] for item in modules]
            native.update(description=dict(modules=[dict(m, package_platform='any-any' if m['package_platform'] == 'any-any' else native['id']) for m in modules]), roots=roots, layers=[roots] if roots else [],
                          requested_roots=roots + (['librealsense@2.58.1'] if filtered else []),
                          filtered_roots=['librealsense@2.58.1'] if filtered else [])
        return selected

    def test_darwin_and_four_platform_delivery(self):
        for platforms, empty, filtered in ((['darwin-aarch64'], False, False),
                                            (PLATFORMS + ['darwin-aarch64'], False, False),
                                            (['darwin-aarch64'], True, False),
                                            (['darwin-aarch64'], True, True)):
            with self.subTest(platforms=platforms, empty=empty, filtered=filtered):
                case = self.root / str(len(list(self.root.iterdir())))
                case.mkdir()
                previous, self.root = self.root, case
                selected = self.darwin_selection(platforms, empty, filtered)
                inputs = self.inputs(selected)
                self.root = previous
                with patch.object(self.api.subprocess, 'run', side_effect=self.git_reader([])):
                    manifest = self.prepare(selected, inputs, case, case / 'build/output')
                assets = json.loads(manifest.read_text())['assets']
                names = [asset['name'] for asset in assets]
                expected = {'autolib.cmake'} | {tool(p) + '.tar.xz' for p in platforms}
                if not empty:
                    expected |= {'queue-1.0-any-any.tar.zst'} | {'lz4-1.10.0-' + p + '.tar.zst' for p in platforms}
                    expected |= {'realsense-2.58.1-' + p + '.tar.zst' for p in platforms if p.startswith('linux')}
                self.assertEqual(set(names), expected)
                self.assertEqual(len(names), len(expected))
                for asset in assets:
                    self.assertEqual(hashlib.sha256((manifest.parent / asset['path']).read_bytes()).hexdigest(), asset['sha256'])

    def test_darwin_delivery_rejects_incomplete_or_mixed_inputs(self):
        changes = [
            lambda t, r, f: f.pop(t['assets'][1]['path']),
            lambda t, r, f: t['assets'].append(copy.deepcopy(t['assets'][0])),
            lambda t, r, f: f.update({'assets/lz4-1.10.0-darwin-x86_64.tar.zst': b'extra'}),
            lambda t, r, f: f.update({'assets/lz4-1.10.0-unknown-aarch64.tar.zst': b'extra'}),
            lambda t, r, f: t['run'].update(run_id='43'),
            lambda t, r, f: t['run'].update(attempt='1'),
            lambda t, r, f: t.update(platform='linux-aarch64'),
            lambda t, r, f: t['assets'][1].update(sha256='0' * 64),
        ]
        for index, change in enumerate(changes):
            with self.subTest(index=index):
                case = self.root / str(index)
                case.mkdir()
                previous, self.root = self.root, case
                selected = self.darwin_selection(['darwin-aarch64'])
                inputs = self.inputs(selected, change)
                self.root = previous
                with patch.object(self.api.subprocess, 'run', side_effect=self.git_reader([])), self.assertRaises((ValueError, FileNotFoundError)):
                    self.prepare(selected, inputs, case, case / 'build/output')
                self.assertFalse(list(case.rglob('publication.json')))
        selected = self.darwin_selection(PLATFORMS + ['darwin-aarch64'])
        inputs = self.inputs(selected)
        shutil.rmtree(inputs / 'ci-package-42-2-darwin-aarch64')
        with patch.object(self.api.subprocess, 'run', side_effect=self.git_reader([])), self.assertRaises((ValueError, FileNotFoundError)):
            self.prepare(selected, inputs, self.root, self.root / 'build/missing-output')

    def test_prepared_archive_bytes_are_stable_without_builder(self):
        selection = self.selection()
        inputs = self.inputs(selection)
        before = {p: (p.read_bytes(), p.stat().st_mtime_ns) for p in inputs.rglob('*') if p.is_file()}
        source = self.root / 'source'
        source.mkdir()
        latest = source / 'src/dependencies/eigen/package.toml'
        latest.parent.mkdir(parents=True)
        latest.write_text('latest = "5.0.1"')
        calls = []
        with patch.object(self.api.subprocess, 'run', side_effect=self.git_reader(calls)):
            first = self.prepare(selection, inputs, source, source / 'build/publication')
            publication = json.loads(first.read_text())
            self.assertEqual(publication['initial_latest'], {'eigen': '3.4.0', 'queue': '1.0'})
            self.assertEqual(len(publication['assets']), 9)
            entry = next(a for a in publication['assets'] if a['kind'] == 'entry')
            self.assertEqual(set(entry), {'kind', 'name', 'path', 'size', 'sha256'})
            self.assertEqual(entry['name'], 'autolib.cmake')
            self.assertIn(RELEASE_ROOT.encode(), (first.parent / entry['path']).read_bytes())
            self.assertEqual(sum(a['name'] == 'eigen-3.4.0-any-any.tar.zst' for a in publication['assets']), 1)
            for asset in publication['assets']:
                self.assertEqual(asset['sha256'], hashlib.sha256((first.parent / asset['path']).read_bytes()).hexdigest())
            second = self.prepare(selection, inputs, source, source / 'build/publication')
            self.assertNotEqual(first, second)
            again = json.loads(second.read_text())
            def contents(path, value):
                return {a['name']: ((path.parent / a['path']).read_bytes(), a['size'], a['sha256']) for a in value['assets']}
            self.assertEqual(len(contents(first, publication)), len(publication['assets']))
            self.assertEqual(contents(first, publication), contents(second, again))
            different = self.api.prepare_publication(selection, inputs, source, source / 'build/other',
                                                     repository='other/mirror', tag='fixed', source_repository=REPOSITORY)
            changed = json.loads(different.read_text())
            for name, value in contents(first, publication).items():
                if name == 'autolib.cmake':
                    self.assertNotEqual(value, contents(different, changed)[name])
                else:
                    self.assertEqual(value, contents(different, changed)[name])
        self.assertEqual(calls, [])
        self.assertEqual({p: (p.read_bytes(), p.stat().st_mtime_ns) for p in before}, before)

    def test_invalid_candidates_and_result_fail_before_builder(self):
        for index, mutation in enumerate([
            lambda t, r, f: f.pop('candidates/queue/package.toml'),
            lambda t, r, f: f.update({'candidates/eigen/package.toml': b'latest = 3'}),
            lambda t, r, f: f.update({'candidates/eigen/package.toml': b'latest = "../bad"'}),
            lambda t, r, f: f.update({'candidates/eigen/package.toml': b'latest = "_bad"'}),
            lambda t, r, f: f.update({'candidates/eigen/package.toml': b'latest = "5.0.1"' if t['platform'] == 'windows-x86_64' else b'latest = "3.4.0"'}),
            lambda t, r, f: r['roots'][0].update(verified=False),
            lambda t, r, f: t['run'].update(attempt='1'),
            lambda t, r, f: t['packages'][0].update(dependencies=['wrong@1']),
            lambda t, r, f: t['packages'][0].update(publish=True),
        ]):
            case = self.root / str(index)
            case.mkdir()
            old_root, self.root = self.root, case
            selected = self.selection()
            inputs = self.inputs(selected, mutation)
            self.root = old_root
            calls = []
            with self.subTest(index=index), patch.object(self.api.subprocess, 'run', side_effect=self.git_reader(calls)):
                with self.assertRaises((ValueError, FileNotFoundError)):
                    self.prepare(selected, inputs, case, case / 'build/output')
                self.assertEqual(calls, [])

    def test_tool_only_three_platforms_publish_all_tools_and_one_entry(self):
        selected = self.selection()
        selected['plan']['publishers'] = {}
        for platform in selected['plan']['platforms']:
            platform.update(roots=[], requested_roots=[], filtered_roots=[], layers=[])
            platform['description']['modules'] = []
        inputs = self.inputs(selected)
        calls = []
        with patch.object(self.api.subprocess, 'run', side_effect=self.git_reader(calls)):
            path = self.prepare(selected, inputs, self.root, self.root / 'build/tools')
        publication = json.loads(path.read_text())
        self.assertEqual(sorted(a['name'] for a in publication['assets']),
                         sorted(['autolib.cmake'] + [tool(p) + '.tar.xz' for p in PLATFORMS]))
        self.assertEqual(publication['initial_latest'], {})
        self.assertEqual(calls, [])

    def test_every_transport_entry_matches_original_commit_before_builder(self):
        changes = [lambda t, r, f: f.pop('autolib.cmake'),
                   lambda t, r, f: f.update({'autolib.cmake': b'changed'}),
                   lambda t, r, f: t.update(entry='other.cmake'),
                   lambda t, r, f: r['entry'].update(verified=False),
                   lambda t, r, f: r['entry'].update(verified=1),
                   lambda t, r, f: r['entry'].update(sha256='0' * 64),
                   lambda t, r, f: r['entry'].update(file='wrong.cmake'),
                   lambda t, r, f: (f.update({'autolib.cmake': b'self-reported'}),
                                     r['entry'].update(sha256=hashlib.sha256(b'self-reported').hexdigest()))]
        for index, change in enumerate(changes):
            with self.subTest(index=index):
                case = self.root / str(index)
                case.mkdir()
                old_root, self.root = self.root, case
                selected = self.selection()
                def mutate(t, r, f):
                    if t['platform'] == PLATFORMS[-1]:
                        change(t, r, f)
                inputs = self.inputs(selected, mutate)
                self.root = old_root
                before = {p: p.read_bytes() for p in inputs.rglob('*') if p.is_file()}
                calls = []
                with patch.object(self.api.subprocess, 'run', side_effect=self.git_reader(calls)):
                    with self.assertRaises((ValueError, FileNotFoundError)):
                        self.prepare(selected, inputs, case, case / 'build/output')
                self.assertEqual(calls, [])
                self.assertEqual({p: p.read_bytes() for p in before}, before)

    def test_linked_or_directory_entry_is_rejected_before_builder(self):
        for kind in [tarfile.SYMTYPE, tarfile.DIRTYPE]:
            with self.subTest(kind=kind):
                case = self.root / str(kind)
                case.mkdir()
                old_root, self.root = self.root, case
                selected = self.selection(['linux-x86_64'])
                inputs = self.inputs(selected)
                self.root = old_root
                special = tarfile.TarInfo('autolib.cmake')
                special.type, special.linkname = kind, 'entry-copy'
                def mutate(t, r, f):
                    f.pop('autolib.cmake')
                    f['install/entry-copy'] = ENTRY
                make_transport(inputs / 'ci-package-42-2-linux-x86_64/transport.tar',
                               selected['plan'], 'linux-x86_64', mutate, extra=[special])
                calls = []
                with patch.object(self.api.subprocess, 'run', side_effect=self.git_reader(calls)):
                    with self.assertRaises(ValueError):
                        self.prepare(selected, inputs, case, case / 'build/output')
                self.assertEqual(calls, [])

    def test_checkout_sha_is_checked_before_builder(self):
        selected = self.selection(['linux-x86_64'])
        inputs = self.inputs(selected)
        calls = []
        original = self.git_reader(calls)
        def wrong_checkout(command, **kwargs):
            if command[0] == 'git' and 'rev-parse' in command:
                return subprocess.CompletedProcess(command, 0, stdout='b' * 40 + '\n')
            return original(command, **kwargs)
        with patch.object(self.api.subprocess, 'run', side_effect=wrong_checkout):
            with self.assertRaises(ValueError):
                self.prepare(selected, inputs, self.root, self.root / 'build/output')
        self.assertEqual(calls, [])

    def test_preparation_uses_trusted_fork_and_original_git_bytes(self):
        selected = self.selection(['linux-x86_64'])
        selected['source_run']['repository'] = 'fork/source'
        inputs = self.inputs(selected)
        (self.root / 'src').mkdir()
        (self.root / 'src/autolib.cmake').write_bytes(b'modified working tree')
        calls = []
        with patch.object(self.api.subprocess, 'run', side_effect=self.git_reader(calls)):
            with self.assertRaises(ValueError):
                self.prepare(selected, inputs, self.root, self.root / 'build/wrong')
            manifest = self.prepare(selected, inputs, self.root, self.root / 'build/fork',
                                    source_repository='fork/source')
        entry = next(a for a in json.loads(manifest.read_text())['assets'] if a['kind'] == 'entry')
        self.assertTrue((manifest.parent / entry['path']).read_bytes().startswith(b'# original entry\n'))
        self.assertEqual((self.root / 'src/autolib.cmake').read_bytes(), b'modified working tree')

    @unittest.skipUnless(os.environ.get('AUTOLIB_PUBLICATION_BUILDER'), 'real builder integration runs on authorized 109 only')
    def test_public_cli_packaged_artifacts(self):
        builder = Path(os.environ['AUTOLIB_PUBLICATION_BUILDER']).resolve()
        source = self.root / 'original source'
        source.mkdir()
        def put(relative, body):
            path = source / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(body, encoding='utf-8')
        def run(*args, **kwargs):
            completed = subprocess.run([str(a) for a in args], capture_output=True, text=True, **kwargs)
            self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
            return completed.stdout.strip()
        put('go.mod', 'module fixture\n')
        put('go.sum', '')
        original_entry = (SCRIPT.parents[2] / 'src/autolib.cmake').read_bytes()
        put('src/autolib.cmake', original_entry.decode('utf-8'))
        (source / 'src/autolib-builder').mkdir(parents=True)
        put('src/dependencies/queue/package.toml', 'latest="1.0"\n')
        put('src/dependencies/queue/1.0/module.toml', 'name="queue"\nversion="1.0"\ntype="cmake"\n[source]\nGIT_REPOSITORY="unused"\nGIT_TAG="fixed"\n')
        put('src/dependencies/queue/1.0/autolib-import.cmake', '# fixture adapter\n')
        put('payload/input.h', '#define QUEUE_FIXTURE 1\n')
        put('CMakeLists.txt', 'cmake_minimum_required(VERSION 3.20)\nproject(publication_fixture NONE)\ninstall(FILES payload/input.h DESTINATION queue/1.0/include)\n')
        fixup = source / 'src/autolib-importer/fixup-metadata.cmake'
        fixup.parent.mkdir(parents=True)
        shutil.copyfile(os.environ['AUTOLIB_PUBLICATION_FIXUP'], fixup)
        run('git', 'init', source)
        run('git', '-C', source, 'add', '.')
        run('git', '-C', source, '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
            '-c', 'commit.gpgsign=false', 'commit', '-m', 'fixture')
        sha = run('git', '-C', source, 'rev-parse', 'HEAD')
        installed = source / 'build/ci/42-2/linux-x86_64/install'
        run('cmake', '-S', source, '-B', source / 'build/native', '-G', 'Unix Makefiles')
        run('cmake', '--install', source / 'build/native', '--prefix', installed)
        run(builder, 'prepare-consumption', '--source-root=' + str(source), '--install-root=' + str(installed),
            '--name=queue', '--version=1.0', '--os=linux', '--arch=x86_64', '--phase=installed')
        producer_prefix = str(installed)
        config = installed / 'queue/1.0/config.cmake'
        config.write_text('set(FIXTURE_PREFIX "' + producer_prefix + '/queue/1.0")\n')
        (installed / 'queue/1.0/header-link').symlink_to('include/input.h')
        importer = installed / tool('linux-x86_64')
        importer.write_bytes(b'fixture importer must not execute\n')
        importer.chmod(0o755)
        (installed / 'autolib.cmake').write_bytes(original_entry)
        dirty_entry = original_entry + b'# working tree change must not be published\n'
        description = json.loads(run(builder, 'describe', '--source-root=' + str(source), '--system-name=Linux',
                                     '--architecture=x86_64', '--module=queue@1.0'))
        selected_plan = dict(platforms=[dict(id='linux-x86_64', roots=['queue@1.0'], description=description,
                                            requested_roots=['queue@1.0'], filtered_roots=[],
                                            layers=[['queue@1.0']], system_dependencies={})],
                             requested_modules=['queue@1.0'], publishers={})
        identity = dict(RUN, sha=sha, checkout_sha=sha)
        from test_entry_delivery import packaged_inputs
        inputs, _ = packaged_inputs(source, installed, builder, selected_plan, identity)
        (source / 'src/autolib.cmake').write_bytes(dirty_entry)
        def snapshot(root):
            return {str(p.relative_to(root)): (p.lstat().st_mode,
                    p.lstat().st_mtime_ns,
                    os.readlink(p) if p.is_symlink() else hashlib.sha256(p.read_bytes()).hexdigest() if p.is_file() else '')
                    for p in root.rglob('*')}
        before = snapshot(installed), snapshot(inputs)
        api_run, artifacts, _ = self.source_api(['linux-x86_64'])
        api_run['head_sha'] = sha
        for a in artifacts:
            a['workflow_run']['head_sha'] = sha
        methods = []
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                methods.append(self.command)
                if '/attempts/2' in self.path:
                    body = json_bytes(api_run)
                elif '/runs/42/artifacts?' in self.path:
                    body = json_bytes(dict(total_count=len(artifacts), artifacts=artifacts))
                elif '/artifacts/1/zip' in self.path:
                    body = zipped({'plan.json': json_bytes(selected_plan), 'run.json': json_bytes(identity)})
                else:
                    self.send_error(404)
                    return
                self.send_response(200)
                self.end_headers()
                self.wfile.write(body)
            def log_message(self, *args):
                pass
        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        env = dict(os.environ, GITHUB_REPOSITORY=REPOSITORY, SOURCE_RUN_ID='42', SOURCE_ATTEMPT='2',
                   GITHUB_API_URL=f'http://127.0.0.1:{server.server_port}', GITHUB_EVENT_PATH='',
                   GITHUB_TOKEN='', LATEST='')
        selection = self.root / 'selection.json'
        run(os.sys.executable, SCRIPT, 'select', '--output', selection, env=env)
        paths = []
        for _ in range(2):
            manifest = Path(run(os.sys.executable, SCRIPT, 'prepare', '--selection', selection,
                                '--input-root', inputs, '--source-root', source,
                                '--output-root', source / 'build/publication', '--repository', 'fixture/mirror',
                                '--tag', '中文/release', '--source-repository', REPOSITORY))
            paths.append(manifest)
            publication = json.loads(manifest.read_text())
            self.assertEqual(publication['initial_latest'], {'queue': '1.0'})
            self.assertEqual(len(publication['assets']), 3)
            for asset in publication['assets']:
                self.assertEqual(asset['sha256'], hashlib.sha256((manifest.parent / asset['path']).read_bytes()).hexdigest())
                if asset['kind'] == 'library':
                    extracted = manifest.parent / 'inspect-library'
                    extracted.mkdir()
                    run('cmake', '-E', 'tar', 'xf', manifest.parent / asset['path'], cwd=extracted)
                    relocated = extracted / 'queue/1.0'
                    self.assertNotIn(producer_prefix, (relocated / 'config.cmake').read_text())
                    self.assertEqual(os.readlink(relocated / 'header-link'), 'include/input.h')
                elif asset['kind'] == 'importer':
                    with tarfile.open(manifest.parent / asset['path'], 'r:xz') as archive:
                        self.assertEqual(archive.getnames(), [tool('linux-x86_64')])
                        self.assertEqual(archive.extractfile(tool('linux-x86_64')).read(), importer.read_bytes())
                else:
                    self.assertEqual((manifest.parent / asset['path']).read_bytes(),
                                     self.api.inject_release_root(original_entry, 'fixture/mirror', '中文/release'))
        self.assertNotEqual(paths[0], paths[1])
        def asset_bytes(path):
            return {a['name']: (path.parent / a['path']).read_bytes() for a in json.loads(path.read_text())['assets']}
        self.assertEqual(asset_bytes(paths[0]), asset_bytes(paths[1]))
        self.assertEqual((snapshot(installed), snapshot(inputs)), before)
        self.assertEqual((source / 'src/autolib.cmake').read_bytes(), dirty_entry)
        self.assertEqual(set(methods), {'GET'})
        print('real CLI select/prepare: two preparations preserve the same three final assets; source/install/input snapshots unchanged')

        selected_plan = plan(PLATFORMS)
        selected_plan['publishers'] = {}
        for platform in selected_plan['platforms']:
            platform.update(roots=[], requested_roots=[], filtered_roots=[], layers=[])
            platform['description']['modules'] = []
        tool_inputs = self.root / 'tool-only inputs'
        tool_plan = tool_inputs / 'ci-plan-42-2'
        tool_plan.mkdir(parents=True)
        (tool_plan / 'plan.json').write_bytes(json_bytes(selected_plan))
        (tool_plan / 'run.json').write_bytes(json_bytes(identity))
        for platform in PLATFORMS:
            directory = tool_inputs / ('ci-package-42-2-' + platform)
            directory.mkdir()
            def entry_from_commit(t, r, f):
                f['autolib.cmake'] = original_entry
                r['entry']['sha256'] = hashlib.sha256(original_entry).hexdigest()
            make_transport(directory / 'transport.tar', selected_plan, platform, entry_from_commit, identity=identity)
        chosen = dict(source_run=identity, plan=selected_plan, explicit_latest={},
                      artifacts=[dict(id=1, name='ci-plan-42-2')] +
                      [dict(id=i + 2, name='ci-package-42-2-' + p) for i, p in enumerate(PLATFORMS)])
        selection.write_bytes(json_bytes(chosen))
        before = snapshot(tool_inputs)
        manifest = Path(run(os.sys.executable, SCRIPT, 'prepare', '--selection', selection,
                            '--input-root', tool_inputs, '--source-root', source,
                            '--output-root', source / 'build/tool-publication', '--repository', 'fixture/mirror',
                            '--tag', '中文/release', '--source-repository', REPOSITORY))
        publication = json.loads(manifest.read_text())
        self.assertEqual(publication['initial_latest'], {})
        self.assertEqual(sorted(a['name'] for a in publication['assets']),
                         sorted(['autolib.cmake'] + [tool(p) + '.tar.xz' for p in PLATFORMS]))
        for asset in publication['assets']:
            payload = manifest.parent / asset['path']
            self.assertEqual(asset['sha256'], hashlib.sha256(payload.read_bytes()).hexdigest())
            self.assertEqual(asset['size'], payload.stat().st_size)
            if asset['kind'] == 'importer':
                self.assertEqual(payload.read_bytes(), ('fixed archive bytes: ' + asset['name']).encode())
        self.assertEqual(snapshot(tool_inputs), before)
        self.assertEqual((source / 'src/autolib.cmake').read_bytes(), dirty_entry)
        print('tool-only CLI protocol fixture: 3 opaque importer archives + 1 entry; no native cross-platform execution claimed')
        print('production manifest returned: ' + str(manifest))


if __name__ == '__main__':
    unittest.main()
