"""Dependency confirmation and importer entry coordination use real Git state."""

import base64
import hashlib
import importlib
from pathlib import Path
import threading
import unittest
from unittest.mock import patch

import test_publish as publication
from test_metadata_repository import git


class SyncSchedulerTests(unittest.TestCase):
    def setUp(self):
        self.scheduler=importlib.import_module('sync_scheduler')
        self.f=publication.PublishTests()
        self.f.setUp()
        self.addCleanup(self.f.doCleanups)
        self.f.remote()

    def graph(self):
        assets=[self.f.asset(n) for n in ['a','b','c','d']]
        config=self.f.config(assets)
        modules=[]
        for asset in assets:
            name=asset['package']['name']
            deps=['b@1'] if name in {'a','c'} else []
            modules.append(dict(name=name,version='1',package_platform='any-any',depends=deps))
            if deps:
                config['cache']['records'][asset['name']]['dependency_fingerprints']={'b-1-any-any.tar.zst':'a'*64}
        config['source']['plan']={'platforms':[{'id':'linux-x86_64','description':{'modules':modules,'failed_roots':[],'filtered_roots':[]}}]}
        return assets,config

    def test_dependencies_wait_for_confirmed_push_and_independent_item_completes(self):
        assets,config=self.graph()
        completed={n:threading.Event() for n in ['a','b','c','d']}
        entered,release=threading.Event(),threading.Event()
        started=[]
        original=self.scheduler.commit_metadata
        def fetch(asset):
            name=asset['package']['name']
            started.append(name)
            if name=='b' and not completed['d'].wait(10):raise RuntimeError('test timeout')
            return next(a['path'] for a in assets if a['name']==asset['name'])
        def commit(repo,digests,latest,**kwargs):
            if any(path.startswith('b/') for path in digests):
                entered.set()
                if not release.wait(10):raise RuntimeError('test timeout')
            head=original(repo,digests,latest,**kwargs)
            for name in completed:
                if any(p.startswith(name+'/') for p in digests):completed[name].set()
            return head
        config['fetch_asset']=fetch
        results=[]
        with patch('sync_scheduler.commit_metadata',side_effect=commit):
            thread=threading.Thread(target=lambda:results.append(self.f.run_release(config)))
            thread.start()
            try:
                self.assertTrue(entered.wait(10))
                self.assertTrue(completed['d'].is_set())
                self.assertNotIn('a',started)
                self.assertNotIn('c',started)
            finally:release.set();thread.join(10)
        self.assertFalse(thread.is_alive())
        self.assertEqual(results[0]['uploaded'],4)
        self.assertTrue(completed['a'].is_set() and completed['c'].is_set())

    def test_failed_dependency_blocks_only_its_closure(self):
        assets,config=self.graph()
        self.f.server.fail.add('b-1-any-any.tar.zst')
        result=self.f.run_release(config)
        status={i['name']:i for i in result['items']}
        self.assertEqual(status['d-1-any-any.tar.zst']['status'],'uploaded')
        for name in ['a','c']:
            self.assertEqual(status[name+'-1-any-any.tar.zst']['status'],'dependency_failed')
            self.assertEqual(status[name+'-1-any-any.tar.zst']['dependency_chain'],[name+'-1-any-any.tar.zst','b-1-any-any.tar.zst'])
            self.assertNotIn(name+'-1-any-any.tar.zst',self.f.server.bodies)
        self.assertEqual(result['failed'],3)

    def test_missing_dependency_is_coverage_gap_even_when_parent_skips(self):
        assets,config=self.graph()
        self.f.run_release(config)
        config=self.f.config(assets);_,graph=self.graph()
        config['source']['plan']=graph['source']['plan']
        config['cache']['records'].pop('b-1-any-any.tar.zst')
        result=self.f.run_release(config)
        self.assertEqual(result['skipped'],3)
        self.assertIn('b-1-any-any.tar.zst',result['coverage'])
        self.assertEqual(result['uploaded'],0)

    def transitive_graph(self):
        assets,config=self.graph()
        self.f.run_release(self.f.config([assets[1]]))
        for module in config['source']['plan']['platforms'][0]['description']['modules']:
            module['depends']={'a':['b@1'],'b':['c@1']}.get(module['name'],[])
        config['cache']['records']['b-1-any-any.tar.zst']['dependency_fingerprints']={'c-1-any-any.tar.zst':'a'*64}
        config['cache']['records']['c-1-any-any.tar.zst']['dependency_fingerprints']={}
        config['work_root']=self.f.root/'transitive'
        self.f.server.add_asset('a-1-any-any.tar.zst',b'previous A',asset_id=99)
        return assets,config

    def test_skipped_dependency_does_not_hide_missing_transitive_cache(self):
        assets,config=self.transitive_graph()
        config['cache']['records'].pop('c-1-any-any.tar.zst')
        fetched=[]
        def fetch(asset):
            fetched.append(asset['name'])
            return next(a['path'] for a in assets if a['name']==asset['name'])
        config['fetch_asset']=fetch
        result=self.f.run_release(config)
        status={item['name']:item for item in result['items']}
        self.assertEqual(status['b-1-any-any.tar.zst']['status'],'skipped')
        self.assertEqual(status['a-1-any-any.tar.zst']['status'],'dependency_failed')
        self.assertEqual(status['a-1-any-any.tar.zst']['dependency_chain'],
                         ['a-1-any-any.tar.zst','b-1-any-any.tar.zst','c-1-any-any.tar.zst'])
        self.assertNotIn('a-1-any-any.tar.zst',fetched)
        self.assertFalse(any(method=='DELETE' and url.endswith('/99') for method,url in self.f.server.events))
        self.assertEqual(self.f.server.bodies['a-1-any-any.tar.zst'],b'previous A')
        self.assertEqual((result['uploaded'],result['blocked'],result['failed']),(1,1,0))

    def test_skipped_dependency_propagates_transitive_upload_failure(self):
        _,config=self.transitive_graph()
        self.f.server.fail.add('c-1-any-any.tar.zst')
        result=self.f.run_release(config)
        status={item['name']:item for item in result['items']}
        self.assertEqual(status['a-1-any-any.tar.zst']['status'],'dependency_failed')
        self.assertEqual(status['a-1-any-any.tar.zst']['dependency_chain'],
                         ['a-1-any-any.tar.zst','b-1-any-any.tar.zst','c-1-any-any.tar.zst'])
        self.assertEqual(self.f.server.bodies['a-1-any-any.tar.zst'],b'previous A')
        self.assertEqual((result['uploaded'],result['skipped'],result['failed']),(1,1,2))

    def test_skipped_dependency_still_waits_for_transitive_push(self):
        assets,config=self.transitive_graph()
        entered,release,independent=threading.Event(),threading.Event(),threading.Event()
        started=[]
        original=self.scheduler.commit_metadata
        def fetch(asset):
            started.append(asset['name'])
            if asset['name']=='c-1-any-any.tar.zst' and not independent.wait(10):
                raise RuntimeError('test timeout')
            return next(a['path'] for a in assets if a['name']==asset['name'])
        def commit(repo,digests,latest,**kwargs):
            if any(path.startswith('c/') for path in digests):
                entered.set()
                if not release.wait(10):raise RuntimeError('test timeout')
            head=original(repo,digests,latest,**kwargs)
            if any(path.startswith('d/') for path in digests):independent.set()
            return head
        config['fetch_asset']=fetch
        results=[]
        with patch('sync_scheduler.commit_metadata',side_effect=commit):
            thread=threading.Thread(target=lambda:results.append(self.f.run_release(config)))
            thread.start()
            try:
                self.assertTrue(entered.wait(10))
                self.assertNotIn('a-1-any-any.tar.zst',started)
            finally:release.set();thread.join(10)
        self.assertFalse(thread.is_alive())
        self.assertEqual((results[0]['uploaded'],results[0]['skipped'],results[0]['failed']),(3,1,0))

    def tools(self):
        config=self.f.config([self.f.asset('d')])
        raw=b'# original\nset(_autolib_release_root "")\n'
        entry={'bytes':base64.b64encode(raw).decode(),'sha256':hashlib.sha256(raw).hexdigest()}
        config['source']['entry']=entry
        paths={}
        for platform in ['windows-x86_64','linux-x86_64','linux-aarch64','darwin-aarch64']:
            name='autolib-importer-'+platform+('.exe' if platform.startswith('windows') else '')+'.tar.xz'
            body=('tool '+platform).encode()
            path=self.f.root/name;path.write_bytes(body);paths[name]=path
            os_name,arch=platform.split('-')
            config['source']['inputs']['fingerprints'][name]='a'*64
            config['cache']['records'][name]={'key':dict(kind='importer',name='autolib-importer',version='',os=os_name,arch=arch),
                'archive':dict(name=name,size=len(body),sha256=hashlib.sha256(body).hexdigest(),id=1),
                'fingerprint':'a'*64,'dependency_fingerprints':{},'entry':dict(entry),'latest_candidate':''}
        original=config['fetch_asset']
        config['fetch_asset']=lambda a:paths[a['name']] if a['name'] in paths else original(a)
        return raw,config

    def test_four_tools_finish_with_smaller_pool_and_one_entry(self):
        raw,config=self.tools()
        with patch('sync_scheduler.os.sched_getaffinity',return_value={0},create=True):
            result=self.f.run_release(config)
        self.assertEqual(result['failed'],0)
        self.assertEqual(result['uploaded'],5)
        self.assertEqual(sum(a['name']=='autolib.cmake' for a in self.f.server.assets),1)
        self.assertIn(b'https://gitee.com/fixture/mirror/releases/download/fixed',self.f.server.bodies['autolib.cmake'])
        self.assertEqual(base64.b64decode(config['source']['entry']['bytes']),raw)
        self.assertNotIn('autolib.cmake.sha256',git(self.f.bare,'ls-tree','-r','--name-only','HEAD').decode())
        self.f.server.events.clear()
        config['work_root']=self.f.root/'again'
        result=self.f.run_release(config)
        self.assertEqual((result['uploaded'],result['skipped']),(0,5))
        self.assertFalse(any(m=='POST' for m,u in self.f.server.events))

    def test_entry_exception_fails_tools_but_library_remains_confirmed(self):
        raw,config=self.tools()
        original=self.f.server.request
        def send(method,url,**kwargs):
            if method=='POST' and url.endswith('/attach_files'):
                encoder=kwargs['data']
                if 'filename="autolib.cmake"' in str(encoder.fields):raise RuntimeError('controlled entry exception')
            return original(method,url,**kwargs)
        original_upload=self.f.api.__class__.upload_asset
        def upload(api,release,asset,file):
            if asset['kind']=='entry':raise RuntimeError('controlled entry exception')
            return original_upload(api,release,asset,file)
        with patch('gitee_api.GiteeAPI.upload_asset',new=upload):
            result=self.f.run_release(config)
        self.assertEqual(result['uploaded'],1)
        self.assertEqual(result['failed'],4)
        tree=git(self.f.bare,'ls-tree','-r','--name-only','HEAD').decode()
        self.assertIn('d/1/',tree)
        self.assertNotIn('autolib-importer/',tree)
        config['work_root']=self.f.root/'retry'
        result=self.f.run_release(config)
        self.assertEqual((result['uploaded'],result['skipped']),(4,1))

    def test_all_tool_uploads_fail_without_entry_upload(self):
        _,config=self.tools()
        self.f.server.fail.update(n for n in config['cache']['records'] if n.startswith('autolib-importer-'))
        result=self.f.run_release(config)
        self.assertEqual(result['failed'],4)
        self.assertNotIn('autolib.cmake',self.f.server.bodies)

    def test_entry_only_change_with_identical_tool_digests_still_skips(self):
        _,config=self.tools()
        self.f.run_release(config)
        changed=b'# changed\nset(_autolib_release_root "")\n'
        entry={'bytes':base64.b64encode(changed).decode(),'sha256':hashlib.sha256(changed).hexdigest()}
        config['source']['entry']=entry
        for record in config['cache']['records'].values():
            if record['key']['kind']=='importer':record['entry']=dict(entry)
        config['work_root']=self.f.root/'changed-entry'
        self.f.server.events.clear()
        result=self.f.run_release(config)
        self.assertEqual(result['skipped'],5)
        self.assertFalse(any(m=='POST' for m,u in self.f.server.events))


if __name__=='__main__':unittest.main()
