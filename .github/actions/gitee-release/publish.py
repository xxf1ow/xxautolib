"""从源码及有效 Release 缓存同步，信任 Git 摘要并逐项确认。"""

from concurrent.futures import ThreadPoolExecutor, as_completed
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import threading
import time
from typing import Mapping
import unicodedata

from cleanup import execute_cleanup, plan_cleanup
from gitee_api import GiteeAPI, GiteeAPIError
from metadata_repository import commit_metadata, open_repository
from publication_rules import _object, choose_source_latest, latest_mapping, metadata_path

ATTACHMENT_RETRY_DELAYS = (2, 4, 8, 16, 32, 32, 32, 32, 32, 32)


def _read_json(value):
    path = Path(value)
    if not path.is_file() or path.is_symlink():
        raise ValueError('sync input must be a regular JSON file')
    return json.loads(path.read_bytes(), object_pairs_hook=_object)


def resolve_config(environment: Mapping[str, str]) -> dict:
    """绑定可信仓库、普通分支及固定 SHA；latest 模式不读取缓存或下载工具。"""
    host, mode = environment.get('PUBLISH_HOST', ''), environment.get('PUBLISH_MODE', '')
    repository, tag = environment.get('GITEE_REPOSITORY', ''), environment.get('GITEE_RELEASE_TAG', '')
    token = environment.get('GITEE_TOKEN', '')
    if (host != 'gitee' or mode not in {'sync', 'latest'}
            or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*', repository)
            or not tag or any(c.isspace() or unicodedata.category(c) == 'Cc' for c in tag)
            or not token or '\r' in token or '\n' in token):
        raise ValueError('invalid publication host, mode, repository, tag or credentials')
    source_file = environment.get('SYNC_SOURCE', '')
    if not source_file:
        raise ValueError('sync source is required')
    source = _read_json(source_file)
    trusted = environment.get('GITHUB_REPOSITORY', '')
    ref, sha = environment.get('SYNC_REF', ''), environment.get('SYNC_SHA', '')
    if (not isinstance(source, dict) or source.get('schema') != 1
            or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*', trusted)
            or source.get('repository') != trusted or source.get('ref') != ref or source.get('sha') != sha
            or not re.fullmatch(r'[0-9a-f]{40}', sha) or not ref.startswith('refs/heads/')
            or subprocess.run(['git', 'check-ref-format', ref], capture_output=True).returncode
            or not isinstance(source.get('catalog', {}).get('packages'), dict)
            or not isinstance(source.get('inputs', {}).get('fingerprints'), dict)):
        raise ValueError('sync source does not match trusted repository, branch and SHA')
    latest = environment.get('PUBLISH_LATEST', '')
    explicit = json.loads(latest, object_pairs_hook=_object) if latest else {}
    if not isinstance(explicit, dict) or mode == 'latest' and not explicit:
        raise ValueError('latest requires a nonempty object')
    cache, cache_file, tool = None, environment.get('SYNC_CACHE', ''), environment.get('SYNC_CACHE_TOOL', '')
    if mode == 'sync':
        if latest:
            raise ValueError('sync forbids a separate latest mapping')
        if not cache_file or not tool or not Path(tool).is_file() or Path(tool).is_symlink():
            raise ValueError('sync requires cache and a regular cache tool')
        cache = _read_json(cache_file)
        if (not isinstance(cache, dict) or cache.get('repository') != trusted or cache.get('inputs') != source['inputs']
                or not isinstance(cache.get('records'), dict) or not isinstance(cache.get('record_errors'), dict)
                or not isinstance(cache.get('assets'), list)):
            raise ValueError('cache does not match source facts')
    return {'host': host, 'mode': mode, 'repository': repository, 'tag': tag, 'gitee_token': token,
            'source': source, 'cache': cache, 'cache_tool': tool, 'source_file': str(Path(source_file).resolve()),
            'cache_file': str(Path(cache_file).resolve()) if mode == 'sync' else None,
            'explicit_latest': explicit,
            'work_root': Path(tempfile.mkdtemp(prefix='autolib-sync-', dir=Path(source_file).resolve().parent))}


def _cache_command(config, arguments):
    environment = {k: v for k, v in os.environ.items() if k.upper() != 'GITEE_TOKEN'}
    command = [str(config['cache_tool']), *arguments, '--repository', config['source']['repository']]
    result = subprocess.run(command, env=environment, capture_output=True)
    if result.returncode:
        raise RuntimeError('GitHub cache operation failed')
    return result.stdout.decode().strip()


def cache_callbacks(config):
    """只将 GitHub 凭据交给缓存 helper；下载和清理均使用本轮固定输入。"""
    def fetch(asset):
        return _cache_command(config, ['cache-download', '--snapshot', config['cache_file'], '--name', asset['name'],
                                       '--output-root', str(config['work_root'] / 'downloads')])
    def delete(key):
        _cache_command(config, ['cache-delete', '--snapshot', config['cache_file'], '--source-input', config['source_file'],
                                '--key', json.dumps(key, separators=(',', ':'))])
    return config.get('fetch_asset', fetch), config.get('delete_cache', delete)


def cache_candidates(source, cache):
    """转换已由 Go 校验的记录；有错误或无当前指纹的记录只供诊断及清理。"""
    assets = []
    for name, record in sorted(cache['records'].items()):
        if name in cache['record_errors'] or record['fingerprint'] != source['inputs']['fingerprints'].get(name):
            continue
        key, archive = record['key'], record['archive']
        asset = {'kind': key['kind'], 'name': archive['name'], 'size': archive['size'], 'sha256': archive['sha256'],
                 'record': record}
        if key['kind'] == 'library':
            asset['package'] = {field: key[field] for field in ('name', 'version', 'os', 'arch')}
        else:
            asset['platform'] = key['os'] + '-' + key['arch']
        metadata_path(asset)
        if (asset['name'] != name or type(asset['size']) is not int or asset['size'] <= 0
                or not isinstance(asset['sha256'], str) or not re.fullmatch('[0-9a-f]{64}', asset['sha256'])):
            raise ValueError('invalid sync candidate archive')
        assets.append(asset)
    return assets


def _log(event, name=None, **fields):
    print(json.dumps({'event': event, **({'asset': name} if name else {}), **fields}, sort_keys=True), file=sys.stderr, flush=True)


def publish_one(api: GiteeAPI, release_id: int, asset: dict, initial_matches: list[dict], *,
                fetch_asset, confirm_item) -> dict:
    """按需固定并校验本地字节，删除重传；确认回调成功后才报告 uploaded。

    重试刷新附件列表并删除重传，不读取 Gitee 内容。确认失败不重试上传。
    """
    name, operation = asset['name'], 'local_asset'
    try:
        _log('start', name)
        digest_path = metadata_path(asset)
        file = Path(fetch_asset(asset))
        if file.is_symlink() or file.is_junction() or not stat.S_ISREG(file.lstat().st_mode):
            raise ValueError('asset must remain a regular file')
        with tempfile.TemporaryDirectory(prefix='autolib-upload-', dir=file.parent) as directory:
            snapshot = Path(directory) / name
            with file.open('rb') as source, snapshot.open('wb') as destination:
                shutil.copyfileobj(source, destination)
            with snapshot.open('rb') as stream:
                digest = hashlib.file_digest(stream, 'sha256').hexdigest()
            if snapshot.stat().st_size != asset['size'] or digest != asset['sha256']:
                raise ValueError('downloaded asset size or digest mismatch')
            for attempt in range(len(ATTACHMENT_RETRY_DELAYS) + 1):
                try:
                    matches = initial_matches if attempt == 0 else [a for a in api.target_assets(release_id) if a['name'] == name]
                    if (len(matches) > 1 or matches and
                            (type(matches[0].get('id')) is not int or matches[0]['id'] <= 0)):
                        raise GiteeAPIError('select_asset', reason='invalid_or_ambiguous_asset')
                    if matches:
                        api.delete_asset(release_id, matches[0]['id'])
                    api.upload_asset(release_id, asset, snapshot)
                    break
                except GiteeAPIError as error:
                    if not error.retryable or attempt == len(ATTACHMENT_RETRY_DELAYS):
                        raise
                    delay = ATTACHMENT_RETRY_DELAYS[attempt]
                    _log('retry', name, retry=attempt + 1, delay_seconds=delay, **error.diagnostic())
                    time.sleep(delay)
            operation = 'metadata_commit'
            confirm_item(asset)
            _log('success', name)
            return {'name': name, 'metadata_path': digest_path, 'sha256': digest, 'status': 'uploaded'}
    except GiteeAPIError as error:
        result = {'name': name, 'status': 'failed', **error.diagnostic()}
    except (OSError, ValueError, RuntimeError):
        result = {'name': name, 'status': 'failed', 'operation': operation, 'outcome': 'unconfirmed'}
    _log('failed', name, **{key: value for key, value in result.items() if key != 'name'})
    return result


def list_assets(api, release_id):
    for attempt in range(len(ATTACHMENT_RETRY_DELAYS) + 1):
        try:
            return api.target_assets(release_id)
        except GiteeAPIError as error:
            if not error.retryable or attempt == len(ATTACHMENT_RETRY_DELAYS):
                raise
            time.sleep(ATTACHMENT_RETRY_DELAYS[attempt])


def publish_release(config: dict) -> dict:
    """单个进程拥有 Git 副本；并发上传，每项立即提交，push 未确认即停止写入。"""
    result = {'selected': 0, 'uploaded': 0, 'skipped': 0, 'failed': 0, 'items': [], 'coverage': {}, 'errors': {},
              'metadata_committed': False, 'metadata_commit': None}
    api, repository = GiteeAPI(config['gitee_token'], config['repository']), None
    operation = 'repository'
    try:
        repository = open_repository(config, api, Path(config['work_root']))
        previous = repository['head']
        source = config['source']
        if config['mode'] == 'latest':
            existing = {k: v for k, v in repository['existing_latest'].items() if k in config['explicit_latest']}
            latest, removed, errors = choose_source_latest(existing, {}, source, set(), config['explicit_latest'])
            result['errors'].update(errors)
            commit_metadata(repository, {}, latest, remove_paths=tuple(name + '/package.toml' for name in removed))
        else:
            candidates = cache_candidates(source, config['cache'])
            result['selected'] = len(candidates)
            result['coverage'] = {name: config['cache']['record_errors'].get(name, 'pending build')
                                  for name in source['inputs']['fingerprints']
                                  if name not in {a['name'] for a in candidates}}
            fetch, delete_cache = cache_callbacks(config)
            lock, stopped = threading.Lock(), threading.Event()
            operation = 'release'
            release = api.target_release(config['tag'])
            assets = list_assets(api, release['id']) if release else []
            cleanup = execute_cleanup(plan_cleanup(source, config['cache'], repository, assets), delete_cache=delete_cache,
                                      api=api, release_id=release['id'] if release else None, repository=repository, git_lock=lock)
            result['errors'].update(cleanup['errors'])
            deleted = set(cleanup['deleted'])
            assets = [a for a in assets if a['name'] not in deleted]
            pending = []
            for asset in candidates:
                if repository['existing_digests'].get(metadata_path(asset)) == asset['sha256']:
                    result['items'].append({'name': asset['name'], 'status': 'skipped'})
                    _log('skipped', asset['name'])
                else:
                    pending.append(asset)
            if pending and release is None:
                release = api.create_release(config['tag'], repository['head'])
            def confirm(asset):
                with lock:
                    if stopped.is_set():
                        raise RuntimeError('metadata writes stopped')
                    try:
                        candidates = {asset['package']['name']: asset['record']['latest_candidate']} if asset['kind'] == 'library' else {}
                        latest, removed, errors = choose_source_latest(repository['existing_latest'], candidates, source, set(candidates), {})
                        result['errors'].update(errors)
                        commit_metadata(repository, {metadata_path(asset): asset['sha256']}, latest,
                                        remove_paths=tuple(n + '/package.toml' for n in removed))
                    except (OSError, ValueError, RuntimeError):
                        stopped.set()
                        raise
            if pending:
                cpu = len(os.sched_getaffinity(0)) if hasattr(os, 'sched_getaffinity') else os.cpu_count() or 1
                with ThreadPoolExecutor(max_workers=min(len(pending), max(1, cpu * 2))) as pool:
                    futures = [pool.submit(publish_one, api, release['id'], asset,
                               [a for a in assets if a['name'] == asset['name']], fetch_asset=fetch, confirm_item=confirm) for asset in pending]
                    result['items'].extend(f.result() for f in as_completed(futures))
            if stopped.is_set():
                result['errors']['metadata_commit'] = 'metadata writes stopped after unconfirmed push'
        result['metadata_committed'] = repository['head'] != previous
    except (GiteeAPIError, OSError, RuntimeError, ValueError) as error:
        diagnostic = error.diagnostic() if isinstance(error, GiteeAPIError) else {'operation': operation, 'outcome': 'unconfirmed'}
        _log('failed', **diagnostic)
        result['errors'][operation] = diagnostic
    finally:
        if repository is not None:
            result['metadata_commit'] = repository['head']
        api.close()
    for status in ('uploaded', 'skipped', 'failed'):
        result[status] = sum(item['status'] == status for item in result['items'])
    return result


def main() -> int:
    """输出汇总及脱敏日志；逐项失败、独立操作失败或未确认均返回非零。"""
    config = None
    try:
        config = resolve_config(os.environ)
        result = publish_release(config)
        print(json.dumps(result, sort_keys=True))
        return int(bool(result['failed'] or result['errors']) or result['metadata_commit'] is None)
    except (OSError, ValueError, RuntimeError):
        print('publication failed: invalid input or unconfirmed operation', file=sys.stderr)
        return 1
    finally:
        if config is not None:
            def remove_readonly(function, path, error):
                if not isinstance(error, PermissionError):
                    raise error
                os.chmod(path, stat.S_IWRITE)
                function(path)
            shutil.rmtree(config['work_root'], onexc=remove_readonly)


if __name__ == '__main__':
    raise SystemExit(main())
