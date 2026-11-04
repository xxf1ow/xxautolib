"""发布明确选定的本地资产，由单协调器登记成功摘要和 latest。"""

from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import sys
import tempfile
from typing import Mapping
import unicodedata

from gitee_api import GiteeAPI, GiteeAPIError
from metadata_repository import commit_metadata, open_repository
from publication_rules import choose_latest, latest_mapping, load_publication, metadata_path


def resolve_config(environment: Mapping[str, str]) -> dict:
    """校验 Action 环境并新建工作目录；非法或冲突输入在远端写入前失败。

    artifacts 仅读取 publication.json；latest 模式不读取构建文件。调用者拥有返回的
    work_root，须在发布结束后清理本次目录，不得复用失败 push 的副本。
    """
    host, mode = environment.get('PUBLISH_HOST', ''), environment.get('PUBLISH_MODE', '')
    repository, tag = environment.get('GITEE_REPOSITORY', ''), environment.get('GITEE_RELEASE_TAG', '')
    token = environment.get('GITEE_TOKEN', '')
    if (host != 'gitee' or mode not in {'artifacts', 'latest'}
            or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*', repository)
            or not tag or any(c.isspace() or unicodedata.category(c) == 'Cc' for c in tag)
            or not token or '\r' in token or '\n' in token):
        raise ValueError('invalid publication host, mode, repository, tag or credentials')
    input_file, latest = environment.get('PUBLISH_INPUT', ''), environment.get('PUBLISH_LATEST', '')
    if mode == 'artifacts':
        if not input_file or latest:
            raise ValueError('artifacts requires input and forbids a separate latest mapping')
        publication = load_publication(Path(input_file), source_repository=environment.get('GITHUB_REPOSITORY', ''))
    else:
        if input_file or not latest:
            raise ValueError('latest requires a nonempty mapping and forbids input')
        explicit = json.loads(latest)
        latest_mapping(explicit)
        if not explicit:
            raise ValueError('latest requires a nonempty mapping')
        publication = {'assets': [], 'initial_latest': {}, 'explicit_latest': explicit}
    return {'host': host, 'mode': mode, 'repository': repository, 'tag': tag, 'gitee_token': token,
            'assets': publication['assets'], 'initial_latest': publication['initial_latest'],
            'explicit_latest': publication['explicit_latest'],
            'work_root': Path(tempfile.mkdtemp(prefix='autolib-publish-'))}


def publish_one(api: GiteeAPI, release_id: int, asset: dict, target_id: int | None) -> dict:
    """核对本次字节，确认删除唯一旧附件后上传；失败不登记摘要或回滚。

    临时快照固定实际 multipart 字节。返回 uploaded 及摘要，或不含异常正文和凭据的
    failed/outcome；未知写入结果仍为失败。HTTP 重试只由 GiteeAPI 拥有。
    """
    name = asset['name']
    try:
        digest_path = metadata_path(asset)
        file = Path(asset['path'])
        if file.is_symlink() or file.is_junction() or not stat.S_ISREG(file.lstat().st_mode):
            raise ValueError('asset must remain a regular file')
        with tempfile.TemporaryDirectory(prefix='autolib-upload-') as directory:
            snapshot = Path(directory) / name
            with file.open('rb') as source, snapshot.open('wb') as destination:
                shutil.copyfileobj(source, destination)
            with snapshot.open('rb') as stream:
                digest = hashlib.file_digest(stream, 'sha256').hexdigest()
            if snapshot.stat().st_size != asset['size'] or digest != asset['sha256']:
                raise ValueError('asset changed after publication preparation')
            if target_id is not None:
                api.delete_asset(release_id, target_id)
            api.upload_asset(release_id, asset, snapshot)
        return {'name': name, 'status': 'uploaded', 'metadata_path': digest_path, 'sha256': digest}
    except GiteeAPIError as error:
        return {'name': name, 'status': 'failed', **error.diagnostic()}
    except (OSError, ValueError):
        return {'name': name, 'status': 'failed', 'operation': 'local_asset', 'outcome': 'failed'}


def publish_release(config: dict) -> dict:
    """使用单 Git 副本和附件池，结束后一次提交成功项及必要 latest。

    部分上传失败仍提交其他成功项；push 未确认不报告 metadata_committed。
    latest-only 不查询 Release。所有 worker 结束后关闭 API 会话。
    """
    assets = config['assets']
    result = {'selected': len(assets), 'uploaded': 0, 'failed': 0,
              'metadata_committed': False, 'metadata_commit': None}
    api = GiteeAPI(config['gitee_token'], config['repository'])
    operation = 'repository'
    try:
        repository = open_repository(config, api, Path(config['work_root']))
        outcomes = []
        if assets:
            operation = 'release'
            release = api.target_release(config['tag'])
            if release is None:
                release = api.create_release(config['tag'], repository['head'])
            targets = {}
            for target in api.target_assets(release['id']):
                targets.setdefault(target['name'], []).append(target)
            work = []
            for asset in assets:
                matches = targets.get(asset['name'], [])
                if (len(matches) > 1 or matches and
                        (type(matches[0].get('id')) is not int or matches[0]['id'] <= 0)):
                    outcomes.append({'name': asset['name'], 'status': 'failed',
                                     'operation': 'select_asset', 'outcome': 'failed'})
                else:
                    work.append((asset, matches[0]['id'] if matches else None))
            cpu_count = len(os.sched_getaffinity(0)) if hasattr(os, 'sched_getaffinity') else (os.cpu_count() or 1)
            workers = min(max(1, cpu_count * 2), max(1, len(work)))
            operation = 'assets'
            with ThreadPoolExecutor(max_workers=workers) as pool:
                futures = [pool.submit(publish_one, api, release['id'], asset, target) for asset, target in work]
                outcomes.extend(future.result() for future in futures)
        for item in outcomes:
            if item['status'] == 'failed':
                diagnostic = {key: item[key] for key in
                              ('operation', 'outcome', 'http_status', 'reason', 'exception_type') if key in item}
                print(json.dumps({'asset': item['name'], **diagnostic}, sort_keys=True), file=sys.stderr)
        successes = {item['name']: item for item in outcomes if item['status'] == 'uploaded'}
        result.update(uploaded=len(successes), failed=len(assets) - len(successes))
        operation = 'metadata_commit'
        digests = {item['metadata_path']: item['sha256'] for item in successes.values()
                   if item['metadata_path'] is not None}
        succeeded = {asset['package']['name'] for asset in assets
                     if asset['kind'] == 'library' and asset['name'] in successes}
        existing = repository['existing_latest']
        selected = choose_latest(existing, config['initial_latest'], succeeded, config['explicit_latest'])
        changes = {name: version for name, version in selected.items() if existing.get(name) != version}
        result['metadata_commit'] = commit_metadata(repository, digests, changes)
        result['metadata_committed'] = True
    except (GiteeAPIError, OSError, RuntimeError, ValueError) as error:
        diagnostic = (error.diagnostic() if isinstance(error, GiteeAPIError)
                      else {'operation': operation, 'outcome': 'unconfirmed'})
        print(json.dumps(diagnostic, sort_keys=True), file=sys.stderr)
        result['failed'] = len(assets) - result['uploaded']
    finally:
        api.close()
    return result


def main() -> int:
    """stdout 输出汇总，stderr 输出脱敏失败诊断；任一失败或未确认返回非零。"""
    config = None
    try:
        try:
            config = resolve_config(os.environ)
        except (json.JSONDecodeError, UnicodeError):
            print('publication input must contain valid UTF-8 JSON', file=sys.stderr)
            return 1
        except ValueError as error:
            # resolve_config 的规则错误均为固定文本，不含输入值或凭据。
            print(f'publication input: {error}', file=sys.stderr)
            return 1
        result = publish_release(config)
        print(json.dumps(result, sort_keys=True))
        return int(bool(result['failed']) or not result['metadata_committed'])
    except (OSError, ValueError, RuntimeError):
        print('publication failed: input could not be read or operation was not confirmed', file=sys.stderr)
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
