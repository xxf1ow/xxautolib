"""精确依赖调度、共用 Git 锁与主进程唯一入口交付。"""

import base64
from concurrent.futures import ThreadPoolExecutor, wait, FIRST_COMPLETED
import hashlib
import os
from pathlib import Path
import threading

from cleanup import execute_cleanup, plan_cleanup
from metadata_repository import commit_metadata
from publication_rules import choose_source_latest, inject_release_root, metadata_path
from publish import cache_candidates, list_assets, publish_one, _log


def _dependencies(source):
    result = {}
    for platform in source['plan']['platforms']:
        modules = {m['name'] + '@' + m['version']: m for m in platform['description']['modules']}
        def archive(module):
            return module['name'] + '-' + module['version'] + '-' + module['package_platform'] + '.tar.zst'
        for module in modules.values():
            result[archive(module)] = [archive(modules[d]) for d in module['depends']]
    return result


def _raw_entry(source, tools):
    expected = source['entry']
    raw = base64.b64decode(expected['bytes'], validate=True)
    if not raw or hashlib.sha256(raw).hexdigest() != expected['sha256']:
        raise ValueError('invalid fixed source entry')
    errors = {}
    for asset in tools:
        if asset['record'].get('entry') != expected:
            errors[asset['name']] = 'tool entry does not match fixed source'
    return raw, errors


def validate_sync_inputs(source, cache, mode, explicit):
    """复用消费规则预检嵌套输入；任何远端读取或初始化前完成。"""
    if mode == 'latest':
        choose_source_latest({}, {}, source, set(), explicit)
        return
    candidates = cache_candidates(source, cache)
    _dependencies(source)
    plan_cleanup(source, cache, {'existing_digests': {}, 'existing_latest': {}}, [])
    tools = [a for a in candidates if a['kind'] == 'importer']
    if tools:
        _raw_entry(source, tools)


def _entry(config, source, tools):
    raw, errors = _raw_entry(source, tools)
    data = inject_release_root(raw, config['repository'], config['tag'])
    path = Path(config['work_root']) / 'entry' / 'autolib.cmake'
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    return {'kind': 'entry', 'name': 'autolib.cmake', 'path': str(path), 'size': len(data),
            'sha256': hashlib.sha256(data).hexdigest()}, errors


def run_sync(config: dict, source: dict, cache: dict, *, api, repository: dict,
             fetch_asset, delete_cache) -> dict:
    """上传成功且摘要 push 确认后释放依赖者；缺缓存单列覆盖缺口，实际失败连锁报告。

    唯一入口在主进程执行，工具 worker 上传后即返回；入口异常不会留下等待线程。
    """
    result = {'selected': 0, 'uploaded': 0, 'skipped': 0, 'failed': 0, 'blocked': 0,
              'items': [], 'coverage': {}, 'errors': {}}
    lock, stopped = threading.Lock(), threading.Event()
    if config['mode'] == 'latest':
        existing = {k: v for k, v in repository['existing_latest'].items() if k in config['explicit_latest']}
        latest, removed, errors = choose_source_latest(existing, {}, source, set(), config['explicit_latest'])
        result['errors'].update(errors)
        with lock:
            commit_metadata(repository, {}, latest, remove_paths=tuple(n + '/package.toml' for n in removed))
        return result
    candidates = {a['name']: a for a in cache_candidates(source, cache)}
    result['selected'] = len(candidates)
    result['coverage'] = {name: cache['record_errors'].get(name, 'pending build')
                          for name in source['inputs']['fingerprints'] if name not in candidates}
    result['coverage'].update(source['inputs']['failures'])
    for p in source['plan']['platforms']:
        for failure in p['description'].get('failed_roots', []):
            result['coverage'][p['id'] + ':' + failure['root']] = failure
    tools = [a for a in candidates.values() if a['kind'] == 'importer']
    entry, entry_errors = _entry(config, source, tools) if tools else (None, {})
    result['coverage'].update(entry_errors)
    for name in entry_errors:
        del candidates[name]
    dependencies = _dependencies(source)
    release = api.target_release(config['tag'])
    remote_assets = list_assets(api, release['id']) if release else []
    cleanup = execute_cleanup(plan_cleanup(source, cache, repository, remote_assets), delete_cache=delete_cache,
                              api=api, release_id=release['id'] if release else None, repository=repository, git_lock=lock)
    result['errors'].update(cleanup['errors'])
    deleted = set(cleanup['deleted'])
    remote_assets = [a for a in remote_assets if a['name'] not in deleted]
    outcomes, pending = {}, set()
    for name, asset in candidates.items():
        if repository['existing_digests'].get(metadata_path(asset)) == asset['sha256']:
            outcomes[name] = {'name': name, 'status': 'skipped'}
            _log('skipped', name)
        else:
            pending.add(name)
    if pending and release is None:
        release = api.create_release(config['tag'], repository['head'])
    def confirm(asset):
        with lock:
            if stopped.is_set():
                raise RuntimeError('metadata writes stopped')
            try:
                initial = {asset['package']['name']: asset['record']['latest_candidate']} if asset['kind'] == 'library' else {}
                latest, removed, errors = choose_source_latest(repository['existing_latest'], initial, source, set(initial), {})
                result['errors'].update(errors)
                commit_metadata(repository, {metadata_path(asset): asset['sha256']}, latest,
                                remove_paths=tuple(n + '/package.toml' for n in removed))
            except Exception:
                stopped.set()
                raise
    entry_result = None
    cpu = len(os.sched_getaffinity(0)) if hasattr(os, 'sched_getaffinity') else os.cpu_count() or 1
    active = {}
    with ThreadPoolExecutor(max_workers=min(max(1, cpu * 2), max(1, len(pending)))) as pool:
        while pending or active:
            for name in sorted(pending):
                if stopped.is_set():
                    outcomes[name] = {'name': name, 'status': 'failed', 'operation': 'metadata_commit'}
                    pending.remove(name)
                    continue
                deps = dependencies.get(name, [])
                blocker = next((d for d in deps if d not in candidates or
                                outcomes.get(d, {}).get('status') in {'failed', 'dependency_failed'}), None)
                if blocker is not None:
                    parent = outcomes.get(blocker, {})
                    failed = parent.get('status') == 'failed' or parent.get('blocking_failure', False)
                    outcomes[name] = {'name': name, 'status': 'dependency_failed', 'blocking_failure': failed,
                                      'dependency_chain': [name, *parent.get('dependency_chain', [blocker])]}
                    result['coverage'][name] = outcomes[name]
                    pending.remove(name)
                    continue
                if not all(outcomes.get(d, {}).get('status') in {'uploaded', 'skipped'} for d in deps):
                    continue
                asset = candidates[name]
                callback = (lambda asset: False) if asset['kind'] == 'importer' else confirm
                future = pool.submit(publish_one, api, release['id'], asset,
                                     [a for a in remote_assets if a['name'] == name], fetch_asset=fetch_asset, confirm_item=callback)
                active[future] = name
                pending.remove(name)
            if not active:
                for name in pending:
                    outcomes[name] = {'name': name, 'status': 'failed', 'operation': 'dependency_graph'}
                break
            done, _ = wait(active, return_when=FIRST_COMPLETED)
            for future in done:
                name = active.pop(future)
                try:
                    item = future.result()
                except Exception:
                    item = {'name': name, 'status': 'failed', 'operation': 'asset_worker'}
                if item['status'] == 'awaiting_entry':
                    if entry_result is None:
                        try:
                            entry_result = publish_one(api, release['id'], entry,
                                [a for a in remote_assets if a['name'] == 'autolib.cmake'],
                                fetch_asset=lambda asset: asset['path'], confirm_item=lambda asset: None)
                        except Exception:
                            entry_result = {'status': 'failed', 'operation': 'entry'}
                        if entry_result['status'] != 'uploaded':
                            result['errors']['entry'] = 'entry delivery failed'
                    if entry_result['status'] == 'uploaded':
                        try:
                            confirm(candidates[name])
                            item['status'] = 'uploaded'
                            _log('success', name)
                        except Exception:
                            item.update(status='failed', operation='metadata_commit')
                    else:
                        item.update(status='failed', operation='entry')
                outcomes[name] = item
    if stopped.is_set():
        result['errors']['metadata_commit'] = 'metadata writes stopped after unconfirmed push'
    else:
        for name, asset in sorted(candidates.items()):
            if asset['kind'] == 'library' and outcomes.get(name, {}).get('status') in {'uploaded', 'skipped'}:
                initial = {asset['package']['name']: asset['record']['latest_candidate']}
                latest, removed, errors = choose_source_latest(repository['existing_latest'], initial, source, set(initial), {})
                result['errors'].update(errors)
                with lock:
                    commit_metadata(repository, {}, latest, remove_paths=tuple(n + '/package.toml' for n in removed))
    result['items'] = list(outcomes.values())
    for status in ('uploaded', 'skipped'):
        result[status] = sum(i['status'] == status for i in result['items'])
    result['blocked'] = sum(i['status'] == 'dependency_failed' for i in result['items'])
    result['failed'] = sum(i['status'] == 'failed' or i.get('blocking_failure', False) for i in result['items'])
    return result
