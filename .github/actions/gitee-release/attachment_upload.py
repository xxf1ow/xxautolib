"""在可终止进程中更新单个附件；Git 确认由调用者负责。"""

import json
import os
from pathlib import Path
import subprocess
import sys
import time

from gitee_api import GiteeAPI, GiteeAPIError

ATTACHMENT_RETRY_DELAYS = (10,) * 10
ATTACHMENT_BUDGET_SECONDS = 600


def _log(event, name=None, **fields):
    print(json.dumps({'event': event, **({'asset': name} if name else {}), **fields}, sort_keys=True), file=sys.stderr, flush=True)


def _update_attachment(api, release_id, asset, snapshot, initial_matches):
    """单层重试删除及上传；每次重试重新定位同名附件，不回读内容。"""
    name = asset['name']
    for attempt in range(len(ATTACHMENT_RETRY_DELAYS) + 1):
        try:
            matches = initial_matches if attempt == 0 else [a for a in api.target_assets(release_id) if a['name'] == name]
            if (len(matches) > 1 or matches and
                    (type(matches[0].get('id')) is not int or matches[0]['id'] <= 0)):
                raise GiteeAPIError('select_asset', reason='invalid_or_ambiguous_asset')
            if matches:
                api.delete_asset(release_id, matches[0]['id'])
            _log('gitee_upload_start', name, attempt=attempt + 1)
            api.upload_asset(release_id, asset, snapshot)
            _log('gitee_upload_complete', name, attempt=attempt + 1)
            return
        except GiteeAPIError as error:
            if not error.retryable or attempt == len(ATTACHMENT_RETRY_DELAYS):
                raise
            delay = ATTACHMENT_RETRY_DELAYS[attempt]
            _log('retry', name, retry=attempt + 1, delay_seconds=delay, **error.diagnostic())
            time.sleep(delay)


def upload_attachment(api, release_id, asset, snapshot, initial_matches, *, budget_seconds=ATTACHMENT_BUDGET_SECONDS):
    """总预算包含全部 Gitee 请求和等待；超时或调用者退出时杀死并回收进程。

    超时可能已经写入远端，调用者不得确认 Git，下次仍须删除重传。
    凭据只经 stdin 交付，不放入命令行或临时文件。
    """
    payload = dict(token=api.token, repository=api.repository, release_id=release_id,
                   asset={key: asset[key] for key in ('name', 'size')},
                   snapshot=str(snapshot), initial_matches=initial_matches)
    environment = {key: value for key, value in os.environ.items()
                   if key.upper() not in {'GITEE_TOKEN', 'GITHUB_TOKEN', 'GH_TOKEN',
                                          'AUTOLIB_GIT_TOKEN', 'AUTOLIB_GIT_USERNAME', 'GIT_ASKPASS'}}
    started = time.monotonic()
    with subprocess.Popen([sys.executable, str(Path(__file__).resolve())], stdin=subprocess.PIPE,
                          stdout=subprocess.PIPE, text=True, env=environment) as process:
        try:
            output, _ = process.communicate(json.dumps(payload), timeout=max(0, budget_seconds - (time.monotonic() - started)))
        except subprocess.TimeoutExpired:
            raise GiteeAPIError('attachment_update', outcome='unknown', reason='total_timeout') from None
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
        if process.returncode != 0:
            try:
                error = json.loads(output)
                raise GiteeAPIError(error['operation'], error['http_status'], error['outcome'],
                                    reason=error['reason'], exception_type=error['exception_type'])
            except (ValueError, KeyError, TypeError):
                raise GiteeAPIError('attachment_update', outcome='unknown', reason='worker_failed') from None


def main():
    api = None
    try:
        payload = json.load(sys.stdin)
        api = GiteeAPI(payload['token'], payload['repository'])
        _update_attachment(api, payload['release_id'], payload['asset'], Path(payload['snapshot']), payload['initial_matches'])
        return 0
    except GiteeAPIError as error:
        print(json.dumps(error.diagnostic()), flush=True)
        return 1
    except Exception:
        print(json.dumps(GiteeAPIError('attachment_update', outcome='unknown', reason='worker_failed').diagnostic()), flush=True)
        return 1
    finally:
        if api is not None:
            api.close()


if __name__ == '__main__':
    raise SystemExit(main())
