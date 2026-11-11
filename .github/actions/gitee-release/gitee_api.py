"""Gitee 普通请求、确认结果和单层有界重试；附件复用 MIT multipart 本体。"""

from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from json import JSONDecodeError
from pathlib import Path
import re
import threading
import time
from urllib.parse import quote, urlsplit

import requests
from requests_toolbelt import MultipartEncoderMonitor

from gitee_release import Gitee


UPSTREAM = 'https://github.com/xxf1ow/xxautolib'
DESCRIPTION = ('xxautolib 预编译产物发布仓库；源码 ' + UPSTREAM
               + ' 负责编译，发布流程分发指定构建的已有结果，本仓库不编译第三方源码。'
               '项目自身内容适用 MIT；第三方库遵守各自许可，MIT 不替代第三方许可。')
RETRY_DELAYS = (2, 4, 8, 16, 32)


class GiteeAPIError(RuntimeError):
    """携带状态码、固定原因及异常类型，不携带响应正文、URL 或原始异常文本。"""

    def __init__(self, operation, status=None, outcome='failed', *, reason='unspecified', exception_type=None):
        self.operation, self.status, self.outcome = operation, status, outcome
        self.reason, self.exception_type = reason, exception_type
        super().__init__(operation + ':' + outcome)

    def diagnostic(self) -> dict:
        """返回可直接写入日志的字段；尚未收到 HTTP 状态时 http_status 为 None。"""
        return {'operation': self.operation, 'outcome': self.outcome, 'http_status': self.status,
                'reason': self.reason, 'exception_type': self.exception_type}

    @property
    def retryable(self) -> bool:
        """临时网络或服务故障可重试；输入、认证、权限及校验错误不可重试。"""
        return (self.reason in {'connection_error', 'timeout', 'incomplete_response', 'upload_stalled', 'response_timeout'}
                or self.status in {408, 429} or self.status is not None and 500 <= self.status < 600)


class _ResponseValidationError(ValueError):
    """仅携带内部固定校验项，不包含远端返回值。"""


def _id(value):
    return type(value) is int and value > 0


def _branch(value):
    return (isinstance(value, str) and bool(value) and not value.startswith(('-', '/', 'refs/'))
            and not value.endswith(('/', '.')) and '..' not in value and '@{' not in value
            and not re.search(r'[\s~^:?*\[\\\x00-\x1f\x7f]', value)
            and all(p and not p.startswith('.') and not p.endswith('.lock') for p in value.split('/')))


class GiteeAPI:
    """每线程使用独立 Session；协调器在所有 worker 结束后关闭连接。"""

    def __init__(self, token: str, repository: str, *, upload_progress=None):
        """upload_progress 在请求线程同步接收请求体读取计数，不表示服务端确认。"""
        if (not isinstance(token, str) or not token or '\r' in token or '\n' in token
                or not isinstance(repository, str) or not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository)
                or any(p in {'.', '..'} for p in repository.split('/'))):
            raise ValueError('invalid Gitee credentials or repository')
        self.token, self.repository = token, repository
        self.owner, self.repo = repository.split('/')
        self._local, self._lock, self._sessions = threading.local(), threading.Lock(), []
        self._login = None
        self._upload_progress = upload_progress
        self.client = Gitee(self.owner, token, request=self._multipart_request)

    def _session(self):
        if not hasattr(self._local, 'session'):
            self._local.session = requests.Session()
            with self._lock:
                self._sessions.append(self._local.session)
        return self._local.session

    def close(self) -> None:
        """释放拥有的所有连接；调用者须先停止所有请求。"""
        with self._lock:
            sessions, self._sessions = self._sessions, []
        for session in sessions:
            session.close()

    def _request(self, method, path, **kwargs):
        return self._session().request(method, 'https://gitee.com/api/v5/' + path,
                                       headers={'Authorization': 'token ' + self.token, 'Accept': 'application/json'},
                                       timeout=(10, 30), allow_redirects=False, verify=True, **kwargs)

    def _multipart_request(self, method, url, **kwargs):
        if self._upload_progress is not None:
            kwargs['data'] = MultipartEncoderMonitor(kwargs['data'], self._upload_progress)
        return self._session().request(method, url, timeout=(10, 300),
                                       allow_redirects=False, verify=True, **kwargs)

    def _run(self, operation, send, expected, read, *, write=False, missing=False, retry_delays=RETRY_DELAYS):
        for attempt in range(len(retry_delays) + 1):
            status, retry, headers = None, False, {}
            try:
                with send() as response:
                    status, headers = response.status_code, response.headers
                    if missing and status == 404:
                        return None
                    if status == expected:
                        return read(response)
                    retry = not write and status in {429, 502, 503, 504}
                    outcome = 'unknown' if write and status not in {400, 401, 403, 404, 405, 409, 422, 429} else 'failed'
                    error = GiteeAPIError(operation, status, outcome, reason='unexpected_http_status')
            except requests.exceptions.SSLError as cause:
                error = GiteeAPIError(operation, status, 'unknown' if write else 'failed',
                                      reason='tls_error', exception_type=type(cause).__name__)
            except (requests.ConnectionError, requests.Timeout, requests.exceptions.ChunkedEncodingError) as cause:
                retry = not write
                reason = ('timeout' if isinstance(cause, requests.Timeout) else
                          'connection_error' if isinstance(cause, requests.ConnectionError) else 'incomplete_response')
                error = GiteeAPIError(operation, status, 'unknown' if write else 'failed',
                                      reason=reason, exception_type=type(cause).__name__)
            except (requests.RequestException, ValueError, TypeError, KeyError, OSError) as cause:
                if isinstance(cause, JSONDecodeError):
                    reason = 'invalid_json'
                elif isinstance(cause, _ResponseValidationError):
                    reason = str(cause)
                elif isinstance(cause, (ValueError, TypeError, KeyError)):
                    reason = 'invalid_response'
                elif isinstance(cause, requests.RequestException):
                    reason = 'request_error'
                else:
                    reason = 'local_io_error'
                error = GiteeAPIError(operation, status, 'unknown' if write else 'failed',
                                      reason=reason, exception_type=type(cause).__name__)
            if not retry or attempt == len(retry_delays):
                raise error from None
            delay = retry_delays[attempt]
            if status == 429 and 'Retry-After' in headers:
                try:
                    value = headers['Retry-After']
                    wait = int(value) if value.isdigit() else (parsedate_to_datetime(value) - datetime.now(timezone.utc)).total_seconds()
                    if wait > delay:
                        raise error from None
                except (ValueError, TypeError, OverflowError):
                    raise error from None
            time.sleep(delay)

    def _json(self, operation, send, expected, validate, **options):
        return self._run(operation, send, expected, lambda response: validate(response.json()), **options)

    def _repository(self, data, *, created=False):
        if (not isinstance(data, dict) or not _id(data.get('id')) or data.get('full_name') != self.repository
                or type(data.get('private')) is not bool or created and data['private'] is not True):
            raise ValueError('invalid repository identity')
        if not created and (not _branch(data.get('default_branch'))
                            or not isinstance(data.get('permission'), dict) or data['permission'].get('push') is not True):
            raise ValueError('repository requires default branch and permission.push')
        return data

    def repository_info(self) -> dict | None:
        """返回确认的默认分支及写权限；仅明确 404 返回 None，其余错误抛异常。"""
        info = self._json('repository', lambda: self._request('GET', 'repos/' + self.repository),
                          200, self._repository, missing=True)
        return None if info is None else dict(info, login=self._authenticated_login())

    def _authenticated_login(self):
        def user(data):
            if (not isinstance(data, dict) or not _id(data.get('id')) or not isinstance(data.get('login'), str)
                    or not re.fullmatch(r'[A-Za-z0-9_.-]+', data['login'])):
                raise ValueError('invalid authenticated user')
            return data
        if self._login is None:
            self._login = self._json('authentication', lambda: self._request('GET', 'user'), 200, user)['login']
        return self._login

    def create_repository(self) -> dict:
        """创建私有 auto_init 库，不重放任何写入；调用者必须先确认 404，随后重新查询。"""
        path = 'user/repos' if self._authenticated_login() == self.owner else 'orgs/' + self.owner + '/repos'
        return self._json('create_repository', lambda: self._request('POST', path, json={
            'name': self.repo, 'private': True, 'auto_init': True, 'description': DESCRIPTION, 'homepage': UPSTREAM}),
            201, lambda data: self._repository(data, created=True), write=True)

    @staticmethod
    def _release(data, tag):
        if (not isinstance(data, dict) or not _id(data.get('id')) or data.get('tag_name') != tag
                or not isinstance(data.get('name'), str) or not isinstance(data.get('body'), (str, type(None)))):
            raise ValueError('invalid release response')
        return data

    def target_release(self, tag: str) -> dict | None:
        """查询固定 tag；404 或 Gitee 的 200/null 表示缺失，其他异常失败。"""
        return self._json('target_release', lambda: self._request('GET', 'repos/' + self.repository
                          + '/releases/tags/' + quote(tag, safe='')), 200,
                          lambda data: None if data is None else self._release(data, tag), missing=True)

    def create_release(self, tag: str, target: str) -> dict:
        """向已确认目标提交创建 Release；未确认结果抛 unknown，不自动重放。"""
        return self._json('create_release', lambda: self._request('POST', 'repos/' + self.repository + '/releases',
                          json={'tag_name': tag, 'name': tag, 'body': tag, 'target_commitish': target}),
                          201, lambda data: self._release(data, tag), write=True)

    def target_assets(self, release_id: int) -> list[dict]:
        """单次读取全部分页；失败交给附件更新循环，不在 HTTP 层重试。"""
        def assets(data):
            if not isinstance(data, list) or any(not isinstance(a, dict) or not isinstance(a.get('name'), str)
                                                or not a['name'] for a in data):
                raise ValueError('invalid asset list')
            return data
        result, page = [], 1
        while True:
            current = page
            items = self._json('target_assets', lambda: self._request('GET', 'repos/' + self.repository
                               + '/releases/' + str(release_id) + '/attach_files', params={'page': current, 'per_page': 100}),
                               200, assets, retry_delays=())
            result.extend(items)
            if len(items) < 100:
                return result
            page += 1

    def delete_asset(self, release_id: int, asset_id: int) -> None:
        """单次删除，只在收到 204 时确认；失败交给附件更新循环。"""
        self._run('delete_asset', lambda: self._request('DELETE', 'repos/' + self.repository + '/releases/'
                  + str(release_id) + '/attach_files/' + str(asset_id)), 204, lambda response: None, write=True)

    def upload_asset(self, release_id: int, asset: dict, file: Path) -> dict:
        """借用无装饰器 multipart 上传；核对 ID、名称、尺寸和 HTTPS URL 后返回。"""
        def uploaded(data):
            if not isinstance(data, dict):
                raise _ResponseValidationError('invalid_asset_response')
            if not _id(data.get('id')):
                raise _ResponseValidationError('invalid_asset_id')
            if data.get('name') != asset['name']:
                raise _ResponseValidationError('invalid_asset_name')
            if type(data.get('size')) is not int or data['size'] != asset['size']:
                raise _ResponseValidationError('invalid_asset_size')
            url = data.get('browser_download_url')
            if not isinstance(url, str) or any(c.isspace() for c in url):
                raise _ResponseValidationError('invalid_asset_url')
            try:
                parsed = urlsplit(url)
            except ValueError:
                raise _ResponseValidationError('invalid_asset_url') from None
            if parsed.scheme != 'https' or not parsed.hostname or parsed.username or parsed.password:
                raise _ResponseValidationError('invalid_asset_url')
            return data
        return self._json('upload_asset', lambda: self.client.upload_asset_details(
            self.repo, release_id, file_name=asset['name'], file_path=str(file)), 201, uploaded, write=True)
