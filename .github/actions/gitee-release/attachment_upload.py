"""在可终止进程中更新单个附件；Git 确认由调用者负责。"""

import json
import os
from pathlib import Path
import queue
import subprocess
import sys
import threading
import time

from gitee_api import GiteeAPI, GiteeAPIError

ATTACHMENT_RETRY_DELAY_SECONDS = 10
ATTACHMENT_BUDGET_SECONDS = 7200
ATTACHMENT_IDLE_SECONDS = 120
ATTACHMENT_RESPONSE_SECONDS = 300
ATTACHMENT_LOG_SECONDS = 30


def _log(event, name=None, **fields):
    print(json.dumps({'event': event, **({'asset': name} if name else {}), **fields}, sort_keys=True), file=sys.stderr, flush=True)


def _update_attachment(api, release_id, asset, snapshot, initial_matches, attempt=1):
    """单次删除及上传；重试时重新定位同名附件，不回读内容。"""
    name = asset['name']
    matches = initial_matches if initial_matches is not None else [a for a in api.target_assets(release_id) if a['name'] == name]
    if (len(matches) > 1 or matches and
            (type(matches[0].get('id')) is not int or matches[0]['id'] <= 0)):
        raise GiteeAPIError('select_asset', reason='invalid_or_ambiguous_asset')
    if matches:
        api.delete_asset(release_id, matches[0]['id'])
    _emit('upload_start')
    _log('gitee_upload_start', name, attempt=attempt)
    api.upload_asset(release_id, asset, snapshot)


def _emit(event, **fields):
    print(json.dumps(dict(event=event, at=time.monotonic(), **fields)), flush=True)


def _upload_attempt(api, release_id, asset, snapshot, initial_matches, deadline, attempt):
    """监督一次请求的字节进展及响应等待；返回或抛异常前回收进程和读线程。"""
    payload = dict(token=api.token, repository=api.repository, release_id=release_id,
                   asset={key: asset[key] for key in ('name', 'size')},
                   snapshot=str(snapshot), initial_matches=initial_matches, attempt=attempt)
    environment = {key: value for key, value in os.environ.items()
                   if key.upper() not in {'GITEE_TOKEN', 'GITHUB_TOKEN', 'GH_TOKEN',
                                          'AUTOLIB_GIT_TOKEN', 'AUTOLIB_GIT_USERNAME', 'GIT_ASKPASS'}}
    messages = queue.Queue()
    with subprocess.Popen([sys.executable, str(Path(__file__).resolve())], stdin=subprocess.PIPE,
                          stdout=subprocess.PIPE, text=True, env=environment) as process:
        def read_output():
            try:
                for line in process.stdout:
                    messages.put(line)
            finally:
                messages.put(None)
        reader = threading.Thread(target=read_output)
        reader.start()
        try:
            process.stdin.write(json.dumps(payload))
            process.stdin.close()
            last_progress = time.monotonic()
            next_log = last_progress + ATTACHMENT_LOG_SECONDS
            phase, bytes_read, total_bytes = 'preparing', 0, 0
            complete, error = False, None
            while True:
                now = time.monotonic()
                if now >= deadline:
                    raise GiteeAPIError('attachment_update', outcome='unknown', reason='total_timeout')
                limit = ATTACHMENT_RESPONSE_SECONDS if phase == 'awaiting_response' else ATTACHMENT_IDLE_SECONDS
                if now - last_progress >= limit:
                    reason = 'response_timeout' if phase == 'awaiting_response' else 'upload_stalled'
                    raise GiteeAPIError('attachment_update', outcome='unknown', reason=reason)
                if now >= next_log:
                    _log('gitee_upload_progress', asset['name'], attempt=attempt, phase=phase,
                         bytes_read=bytes_read, total_bytes=total_bytes,
                         percent=round(bytes_read * 100 / total_bytes, 1) if total_bytes else 0,
                         idle_seconds=round(max(0, now - last_progress), 1))
                    next_log = now + ATTACHMENT_LOG_SECONDS
                try:
                    line = messages.get(timeout=max(0, min(deadline, last_progress + limit, next_log) - time.monotonic()))
                except queue.Empty:
                    continue
                if line is None:
                    process.wait()
                    if error is not None:
                        raise error
                    if process.returncode != 0 or not complete:
                        raise GiteeAPIError('attachment_update', outcome='unknown', reason='worker_failed')
                    return
                message = json.loads(line)
                event = message.get('event')
                if event == 'upload_start':
                    phase, last_progress = 'uploading', message['at']
                elif event == 'progress' and message['bytes_read'] > bytes_read:
                    bytes_read, total_bytes = message['bytes_read'], message['total_bytes']
                    last_progress = message['at']
                    if bytes_read == total_bytes:
                        phase = 'awaiting_response'
                elif event == 'complete':
                    complete = True
                elif 'operation' in message:
                    error = GiteeAPIError(message['operation'], message['http_status'], message['outcome'],
                                          reason=message['reason'], exception_type=message['exception_type'])
        except (ValueError, KeyError, TypeError, OSError):
            raise GiteeAPIError('attachment_update', outcome='unknown', reason='worker_failed') from None
        finally:
            if process.poll() is None:
                process.kill()
            process.wait()
            reader.join()


def upload_attachment(api, release_id, asset, snapshot, initial_matches, *, budget_seconds=ATTACHMENT_BUDGET_SECONDS):
    """总预算覆盖全部请求及等待；临时错误重试，停滞时终止单轮进程。

    超时可能已经写入远端，调用者不得确认 Git，重试仍须删除重传。
    凭据只经 stdin 交付，不放入命令行或临时文件。
    """
    deadline, attempt = time.monotonic() + budget_seconds, 0
    while True:
        if time.monotonic() >= deadline:
            raise GiteeAPIError('attachment_update', outcome='unknown', reason='total_timeout')
        attempt += 1
        try:
            _upload_attempt(api, release_id, asset, snapshot, initial_matches if attempt == 1 else None, deadline, attempt)
            _log('gitee_upload_complete', asset['name'], attempt=attempt)
            return
        except GiteeAPIError as error:
            if not error.retryable:
                raise
            delay = min(ATTACHMENT_RETRY_DELAY_SECONDS, max(0, deadline - time.monotonic()))
            _log('retry', asset['name'], retry=attempt, delay_seconds=delay, **error.diagnostic())
            time.sleep(delay)


def main():
    api = None
    try:
        payload = json.load(sys.stdin)
        def progress(monitor):
            _emit('progress', bytes_read=monitor.bytes_read, total_bytes=monitor.len)
        api = GiteeAPI(payload['token'], payload['repository'], upload_progress=progress)
        _update_attachment(api, payload['release_id'], payload['asset'], Path(payload['snapshot']), payload['initial_matches'], payload['attempt'])
        _emit('complete')
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
