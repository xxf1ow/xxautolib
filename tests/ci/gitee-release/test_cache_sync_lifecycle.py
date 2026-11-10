"""真实 CLI、TLS HTTP、multipart 和 Git 验证跨 run 缓存同步闭环。"""

import base64
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tempfile
import unittest

from sync_fixture import Remote, TOKEN
from test_metadata_repository import IDENTITY, git

ROOT = Path(__file__).resolve().parents[3]


def command(args, *, cwd=None, env=None):
    result = subprocess.run([str(a) for a in args], cwd=cwd, env=env, capture_output=True, text=True, timeout=180)
    if result.returncode:
        raise AssertionError(f'{args[0]} failed ({result.returncode}):\n{result.stdout}\n{result.stderr}')
    return result.stdout.strip()


def source_copy(destination):
    destination.mkdir(parents=True)
    for name in ['CMakeLists.txt', 'go.mod', 'go.sum', '.github/ci-tools.json', 'src/autolib.cmake']:
        target = destination / name; target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(ROOT / name, target)
    for directory in ['src/autolib-builder', 'src/autolib-importer', 'src/autolib-ci', 'src/internal']:
        shutil.copytree(ROOT / directory, destination / directory, ignore=shutil.ignore_patterns('*_test.go'))


class CacheSyncLifecycleTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        base = ROOT / 'build' / 'cache-sync-tests'; base.mkdir(parents=True, exist_ok=True)
        cls.temp = tempfile.TemporaryDirectory(dir=base)
        cls.addClassCleanup(cls.temp.cleanup)
        cls.root = Path(cls.temp.name)
        cls.tools = cls.root / 'tools-source'
        source_copy(cls.tools)
        # SDK、依赖缓存来自仓库；新编译输出和临时文件均在本测试 build 根。
        for name in ['sdk', 'gopath']:
            target = cls.tools / '.tools/go' / name; target.parent.mkdir(parents=True, exist_ok=True)
            target.symlink_to(ROOT / '.tools/go' / name, target_is_directory=True)
        cls.env = dict(os.environ, GOMAXPROCS='4', CMAKE_BUILD_PARALLEL_LEVEL='4',
                       TMPDIR=str(cls.root), GOTMPDIR=str(cls.root), GOPROXY='off', GOSUMDB='off')
        cls.builder, cls.ci = cls.root / 'builder', cls.root / 'autolib-ci'
        for module, output in [('autolib-builder', cls.builder), ('autolib-ci', cls.ci)]:
            command(['cmake','-P',cls.tools/'src/autolib-builder/go-sdk.cmake','--','build','-mod=readonly',
                     '-p=4','-trimpath','-o',output,'./src/'+module], cwd=cls.tools, env=cls.env)

    def setUp(self):
        self.directory = Path(tempfile.mkdtemp(dir=self.root))
        self.addCleanup(shutil.rmtree, self.directory)
        self.source = self.directory / 'source'; source_copy(self.source)
        self.native = platform.system().lower() + '-' + {'arm64':'aarch64','aarch64':'aarch64','x86_64':'x86_64','AMD64':'x86_64'}[platform.machine()]
        self.remote = Remote(self.directory); self.addCleanup(self.remote.close)
        self.bare = self.directory / 'mirror.git'
        seed = self.directory / 'seed'; seed.mkdir()
        git(seed,'init','--initial-branch=server-default'); git(seed,'config','user.name','fixture'); git(seed,'config','user.email','fixture@example.invalid')
        (seed/'.autolib-repository.json').write_text(json.dumps(IDENTITY))
        git(seed,'add','.'); git(seed,'commit','-m','seed'); git(seed,'clone','--bare',str(seed),str(self.bare))
        git(self.source,'init','--initial-branch=topic/slash'); git(self.source,'config','user.name','fixture'); git(self.source,'config','user.email','fixture@example.invalid')
        self.env = dict(self.env, **self.remote.environment(), GITHUB_TOKEN='fixture-github-token', GITEE_TOKEN=TOKEN,
            GITHUB_REPOSITORY='xxf1ow/xxautolib', PUBLISH_HOST='gitee', PUBLISH_MODE='sync',
            GITEE_REPOSITORY='fixture/mirror', GITEE_RELEASE_TAG='fixed', SYNC_REF='refs/heads/topic/slash',
            SYNC_CACHE_TOOL=str(self.ci), GIT_CONFIG_COUNT='2', GIT_CONFIG_KEY_0='url.'+str(self.bare)+'.insteadOf',
            GIT_CONFIG_VALUE_0='https://gitee.com/fixture/mirror.git', GIT_CONFIG_KEY_1='protocol.file.allow', GIT_CONFIG_VALUE_1='always')
        for name in ['a','b','c','d']:
            self.recipe(name, platform=self.native if name=='b' else 'any-any')
        self.snapshot()

    def recipe(self, name, version='1', platform='any-any', revision=1):
        directory = self.source / 'src/dependencies' / name / version; directory.mkdir(parents=True,exist_ok=True)
        deps = ['b@1'] if name in {'a','c'} else []
        (directory/'module.toml').write_text("type='copy'\npackage_platforms="+repr([platform])+"\ndepends="+repr(deps)+"\n[source]\nGIT_REPOSITORY='local-fixture'\nGIT_TAG='"+str(revision)*40+"'\n[[copy]]\nfiles=['header.h']\ndestination='include'\n")
        (directory/'autolib-import.cmake').write_text('add_library(autolib::'+name+' INTERFACE IMPORTED GLOBAL)\n')
        (directory.parent/'package.toml').write_text("latest='1'\n")

    def snapshot(self):
        git(self.source,'add','.'); git(self.source,'commit','--allow-empty','-m','snapshot')
        self.sha = git(self.source,'rev-parse','HEAD').decode().strip()
        self.source_file = self.directory / ('source-'+self.sha+'.json')
        command([self.ci,'sync-source','--source-root',self.source,'--builder-file',self.builder,'--repository','xxf1ow/xxautolib',
                 '--ref','refs/heads/topic/slash','--output',self.source_file], env=self.env)
        self.facts = json.loads(self.source_file.read_bytes())
        self.assertFalse(self.facts['inputs']['failures'])

    def store(self, name, run, body_revision=1, version='1', package_platform=None):
        package_platform = package_platform or (self.native if name=='b' else 'any-any')
        archive = f'{name}-{version}-{package_platform}.tar.zst'
        install = self.directory / ('install-'+str(run)+'-'+name+'-'+version)
        target = install/name/version; target.mkdir(parents=True)
        (install/name/'package.toml').write_text("latest='1'\n")
        (target/'header.h').write_text(f'#define FIXTURE_VALUE {body_revision}\n')
        os_name, arch = package_platform.split('-')
        deps = ['b@1'] if name in {'a','c'} else []
        (target/'autolib-import.cmake').write_text(f'set(AUTOLIB_PACKAGE_NAME "{name}")\nset(AUTOLIB_PACKAGE_VERSION "{version}")\nset(AUTOLIB_OS "{os_name}")\nset(AUTOLIB_ARCH "{arch}")\nset(AUTOLIB_DEPENDENCIES "'+ ';'.join(deps)+'")\n')
        if package_platform != 'any-any':
            cpp = target/'fixture.cpp'; cpp.write_text(f'int fixture_value() {{ return {body_revision}; }}\n')
            command(['c++','-c',cpp,'-o',target/'fixture.o'], env=self.env)
            command(['ar','rcs',target/'libfixture.a',target/'fixture.o'], env=self.env)
        output = self.source / 'build' / ('package-'+str(run)+'-'+name+'-'+version)
        host_os, host_arch = self.native.split('-')
        command([self.builder,'package','--asset-kind','library','--source-root',self.source,'--install-root',install,
                 '--output-root',output,'--module',name+'@'+version,'--os',host_os,'--arch',host_arch,
                 '--host-os',host_os,'--host-arch',host_arch,'--cmake-command',shutil.which('cmake')], env=self.env)
        body = (output/archive).read_bytes()
        return self.record(archive, body, dict(kind='library',name=name,version=version,os=os_name,arch=arch),run,
                           {'b@1':self.facts['inputs']['fingerprints'][f'b-1-{self.native}.tar.zst']} if deps else {})

    def record(self, archive, body, key, run, deps, entry=None):
        asset = self.remote.add_source(archive, body)
        os_name,arch=self.native.split('-')
        record = dict(schema=1,kind=key['kind'],key=key,archive=dict(id=asset['id'],name=archive,size=len(body),sha256=hashlib.sha256(body).hexdigest()),
            origin=dict(repository='xxf1ow/xxautolib',ref='refs/heads/topic/slash',sha=self.sha,checkout_sha=self.sha,run_id=str(run),attempt='1'),
            fingerprint=self.facts['inputs']['fingerprints'][archive],dependency_fingerprints=deps,latest_candidate='1' if key['kind']=='library' else '',
            verification=dict(platform=self.native,context=dict(os=os_name,arch=arch,configuration='Release'),evidence=['local native package fixture'],passed=True))
        if entry:record['entry']=entry
        self.remote.add_source(archive+'.publication.json',json.dumps(record).encode())
        return record,body

    def sync(self, expected=0, mode='sync', explicit=None):
        cache = self.directory / ('cache-'+str(len(self.remote.calls))+'.json')
        if mode=='sync':
            command([self.ci,'sync-cache','--source-input',self.source_file,'--repository','xxf1ow/xxautolib','--output',cache], env=self.env)
        env = dict(self.env,SYNC_SOURCE=str(self.source_file),SYNC_SHA=self.sha,SYNC_CACHE=str(cache),
                   PUBLISH_MODE=mode,PUBLISH_LATEST=json.dumps(explicit) if explicit else '')
        result = subprocess.run([sys.executable,ROOT/'.github/actions/gitee-release/publish.py'],env=env,capture_output=True,text=True,timeout=180)
        self.assertEqual(result.returncode,expected,result.stderr+'\n'+result.stdout)
        self.assertFalse(self.remote.errors)
        data = json.loads(result.stdout)
        self.assertEqual(data['metadata_commit'],git(self.bare,'rev-parse','HEAD').decode().strip())
        return data

    def test_cumulative_delivery_invalidation_and_source_pruning(self):
        b,bbytes=self.store('b',11);d,dbytes=self.store('d',12)
        first=self.sync();self.assertEqual(first['uploaded'],2)
        self.assertEqual({b['origin']['run_id'],d['origin']['run_id']},{'11','12'})
        for name,body in [(b['archive']['name'],bbytes),(d['archive']['name'],dbytes)]:
            self.assertEqual(self.remote.target_bytes[self.remote.target(name)['id']],body)
        self.store('a',13);self.store('c',14)
        second=self.sync();self.assertEqual((second['uploaded'],second['skipped']),(2,2))
        self.remote.downloads.clear();self.remote.uploads.clear()
        unchanged=self.sync()
        self.assertEqual(unchanged['metadata_commit'],second['metadata_commit'])
        self.assertEqual(unchanged['uploaded'],0)
        self.assertFalse([n for n in self.remote.downloads if not n.endswith('.publication.json')])
        self.assertFalse(self.remote.uploads)
        self.recipe('b',platform=self.native,revision=2);self.snapshot()
        invalid=self.sync();self.assertEqual(invalid['skipped'],1)
        for name in ['a-1-any-any.tar.zst','c-1-any-any.tar.zst',b['archive']['name']]:self.assertIn(name,invalid['coverage'])
        self.store('a',21,2);self.store('c',22,2)
        blocked=self.sync();self.assertEqual(blocked['blocked'],2);self.assertEqual(blocked['failed'],0)
        self.store('b',23,2);self.remote.uploads.clear();updated=self.sync()
        self.assertEqual(updated['uploaded'],3)
        self.assertLess(self.remote.uploads.index(b['archive']['name']),self.remote.uploads.index('a-1-any-any.tar.zst'))
        self.assertLess(self.remote.uploads.index(b['archive']['name']),self.remote.uploads.index('c-1-any-any.tar.zst'))
        for name in ['a','b','c','d']:
            archive=f'{name}-1-'+(self.native if name=='b' else 'any-any')+'.tar.zst'
            body=self.remote.target_bytes[self.remote.target(archive)['id']]
            record=json.loads(git(self.bare,'show','HEAD:'+name+'/1/'+archive+'.json'))
            self.assertEqual((record['sha256'],record['size']),(hashlib.sha256(body).hexdigest(),len(body)))
        self.recipe('a',version='2');self.snapshot();self.store('a',30,version='2');self.sync()
        self.remote.add_source('unknown.bin',b'keep');unknown=self.remote.add_target('unknown.bin',b'keep')
        shutil.rmtree(self.source/'src/dependencies/a/1');(self.source/'src/dependencies/a/package.toml').write_text("latest='2'\n")
        shutil.rmtree(self.source/'src/dependencies/d')
        self.recipe('b',platform='any-any',revision=3);self.snapshot()
        self.sync()
        names={a['name'] for a in self.remote.sources};targets={a['name'] for a in self.remote.targets}
        for old in ['a-1-any-any.tar.zst','d-1-any-any.tar.zst',b['archive']['name']]:
            self.assertNotIn(old,names);self.assertNotIn(old+'.publication.json',names);self.assertNotIn(old,targets)
        self.assertEqual(git(self.bare,'show','HEAD:a/package.toml'),b'latest = "2"\n')
        self.assertNotIn('d/package.toml',git(self.bare,'ls-tree','-r','--name-only','HEAD').decode())
        self.assertEqual(self.remote.target_bytes[unknown['id']],b'keep')

    def test_json_delivery_fallback_and_source_pruning(self):
        b, body = self.store('b', 51)
        self.store('a', 52); self.store('d', 53)
        name = b['archive']['name']
        # Only the declared size is synthetic: this branch must never fetch archive bytes.
        b['archive']['size'] = 100000001
        next(a for a in self.remote.sources if a['name'] == name)['size'] = b['archive']['size']
        self.remote.add_source(name + '.publication.json', json.dumps(b).encode())
        self.remote.add_target(name, b'previous small package')
        self.remote.fail_deletes = {name}
        self.remote.fail_uploads = {'d-1-any-any.tar.zst'}
        first = self.sync(expected=1)
        self.assertEqual((first['metadata_only'], first['uploaded'], first['failed']), (1, 1, 1))
        bpath = 'b/1/' + name + '.json'
        record = json.loads(git(self.bare, 'show', 'HEAD:' + bpath))
        self.assertEqual(record['urls'], ['https://github.com/xxf1ow/xxautolib/releases/download/autolib-cache/' + name])
        self.assertEqual(record['size'], 100000001)
        self.assertNotIn(name, self.remote.downloads)
        self.assertNotIn(name, self.remote.uploads)
        arecord = json.loads(git(self.bare, 'show', 'HEAD:a/1/a-1-any-any.tar.zst.json'))
        self.assertEqual([u.split('/')[2] for u in arecord['urls']], ['gitee.com', 'github.com'])
        changes = git(self.bare, 'log', '--reverse', '--format=', '--name-only', first['metadata_commit']).decode()
        self.assertLess(changes.index(bpath), changes.index('a/1/a-1-any-any.tar.zst.json'))
        self.assertIn(name + ':gitee', first['errors'])
        self.remote.fail_deletes.clear(); self.remote.fail_uploads.clear()
        retry = self.sync()
        self.assertEqual((retry['uploaded'], retry['skipped']), (1, 2))
        self.assertNotIn(name, {a['name'] for a in self.remote.targets})
        self.remote.downloads.clear(); self.remote.uploads.clear()
        unchanged = self.sync()
        self.assertEqual(unchanged['metadata_commit'], retry['metadata_commit'])
        self.assertEqual((unchanged['metadata_only'], unchanged['uploaded']), (0, 0))
        self.assertFalse([n for n in self.remote.downloads if not n.endswith('.publication.json')])
        self.assertFalse(self.remote.uploads)
        # Probe public routing only; size/SHA-checked GitHub-only consumption is covered by the native bridge.
        import requests
        session = requests.Session(); session.trust_env = False
        session.proxies = {'https': self.env['HTTPS_PROXY']}
        response = session.get(record['urls'][0], verify=str(self.remote.cert), timeout=10)
        self.assertEqual(response.content, body)
        session.close()
        shutil.rmtree(self.source / 'src/dependencies/b')
        shutil.rmtree(self.source / 'src/dependencies/a')
        shutil.rmtree(self.source / 'src/dependencies/c')
        self.snapshot(); self.sync()
        tree = git(self.bare, 'ls-tree', '-r', '--name-only', 'HEAD').decode()
        self.assertNotIn(bpath, tree); self.assertNotIn('b/package.toml', tree)
        self.assertNotIn(name, {a['name'] for a in self.remote.sources})
        self.assertNotIn(name + '.publication.json', {a['name'] for a in self.remote.sources})

    def test_lost_response_and_entry_failure_recover_next_run(self):
        self.store('d',41)
        name='autolib-importer-'+self.native+('.exe' if self.native.startswith('windows') else '')+'.tar.xz'
        key=dict(kind='importer',name='autolib-importer',version='',os=self.native.split('-')[0],arch=self.native.split('-')[1])
        self.record(name,b'fixture verified tool',key,42,{},self.facts['entry'])
        self.remote.fail_entry=True
        failed=self.sync(expected=1);self.assertEqual((failed['uploaded'],failed['failed']),(1,1))
        self.assertNotIn('autolib-importer/',git(self.bare,'ls-tree','-r','--name-only','HEAD').decode())
        self.remote.fail_entry=False;self.remote.lose_upload=name
        recovered=self.sync();self.assertEqual((recovered['uploaded'],recovered['skipped']),(1,1))
        self.assertEqual(len([a for a in self.remote.targets if a['name']==name]),1)
        self.assertIn(b'https://gitee.com/fixture/mirror/releases/download/fixed',self.remote.target_bytes[self.remote.target('autolib.cmake')['id']])
        self.remote.calls.clear();latest=self.sync(mode='latest',explicit={'d':'1'})
        self.assertFalse(any(h=='api.github.com' or '/releases/' in p for _,h,p in self.remote.calls))
        self.assertEqual(latest['uploaded'],0)


if __name__=='__main__':unittest.main()
