"""Controlled HTTP responses exercise authentication and confirmed writes."""

import importlib
import hashlib
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

import requests
from requests_toolbelt.multipart.decoder import MultipartDecoder

ACTION = Path(__file__).resolve().parents[3] / '.github/actions/gitee-release'
sys.path.insert(0, str(ACTION))


class GiteeAPITests(unittest.TestCase):
    def setUp(self):
        self.assertIsNotNone(importlib.util.find_spec('gitee_api'), 'Gitee API is not implemented')
        self.module = importlib.import_module('gitee_api')
        self.api = self.module.GiteeAPI('fake-secret', 'fixture/mirror')
        self.addCleanup(self.api.close)
        self.sent = []

    def response(self, status, data):
        result = requests.Response()
        result.status_code = status
        result._content = json.dumps(data).encode()
        result._content_consumed = True
        return result

    def transport(self, replies):
        def send(method, url, **kwargs):
            self.sent.append((method, url, kwargs))
            reply = replies.pop(0)
            if isinstance(reply, Exception):
                raise reply
            return self.response(*reply)
        return patch('requests.sessions.Session.request', side_effect=send)

    def info(self, **changes):
        return dict({'id': 1, 'full_name': 'fixture/mirror', 'private': True,
                     'default_branch': 'server-default', 'permission': {'push': True}}, **changes)

    def test_create_private_repository_then_requery(self):
        with self.transport([(404, {}), (200, {'id': 1, 'login': 'fixture'}),
                             (201, {'id': 1, 'full_name': 'fixture/mirror', 'private': True}),
                             (200, self.info())]):
            self.assertIsNone(self.api.repository_info())
            self.api.create_repository()
            self.assertEqual(self.api.repository_info()['default_branch'], 'server-default')
        creation = self.sent[2]
        self.assertTrue(creation[1].endswith('/user/repos'))
        self.assertIs(creation[2]['json']['private'], True)
        self.assertIs(creation[2]['json']['auto_init'], True)
        description = creation[2]['json']['description']
        for text in ['https://github.com/xxf1ow/xxautolib', '编译', '发布', 'MIT', '第三方']:
            self.assertIn(text, description)
        for _, _, options in self.sent:
            self.assertEqual(options['headers']['Authorization'], 'token fake-secret')
            self.assertFalse(options['allow_redirects'])
            self.assertNotIn('data', options)
            self.assertNotIn('access_token', options.get('params', {}))

    def test_errors_never_become_missing_or_replay_uncertain_creation(self):
        for reply in [(401, {}), (403, {}), (429, {}), requests.ConnectionError('fake-secret')]:
            with self.subTest(reply=reply), self.transport([reply] * 6), patch('gitee_api.time.sleep'):
                with self.assertRaises(self.module.GiteeAPIError) as failure:
                    self.api.repository_info()
                self.assertNotIn('fake-secret', str(failure.exception))
        for reply in [requests.Timeout('fake-secret'), (503, {}), (201, {})]:
            self.sent.clear()
            self.api._login = None
            with self.subTest(reply=reply), self.transport([(200, {'id': 1, 'login': 'fixture'}), reply]):
                with self.assertRaises(self.module.GiteeAPIError) as failure:
                    self.api.create_repository()
                self.assertEqual(failure.exception.outcome, 'unknown')
            self.assertEqual(sum(method == 'POST' for method, _, _ in self.sent), 1)

    def test_repository_requires_real_branch_and_singular_push_permission(self):
        for info in [self.info(default_branch=''), self.info(permission={'push': False}),
                     self.info(permission=None, permissions={'push': True}), self.info(default_branch='bad..branch')]:
            with self.subTest(info=info), self.transport([(200, info)]), self.assertRaises(self.module.GiteeAPIError):
                self.api.repository_info()

    def test_existing_repository_reports_authenticated_login_for_git(self):
        with self.transport([(200, self.info()), (200, {'id': 7, 'login': 'actual-writer'})]):
            self.assertEqual(self.api.repository_info()['login'], 'actual-writer')

    def test_organization_creation_uses_authenticated_user_not_owner_guess(self):
        with self.transport([(200, {'id': 7, 'login': 'actual-writer'}),
                             (201, {'id': 1, 'full_name': 'fixture/mirror', 'private': True})]):
            self.api.create_repository()
        self.assertTrue(self.sent[1][1].endswith('/orgs/fixture/repos'))

    def test_uncertain_delete_and_invalid_upload_never_succeed_or_replay(self):
        with self.transport([requests.Timeout('fake-secret')]):
            with self.assertRaises(self.module.GiteeAPIError) as failure:
                self.api.delete_asset(3, 4)
            self.assertEqual(failure.exception.outcome, 'unknown')
        self.assertEqual(len(self.sent), 1)
        self.sent.clear()
        with tempfile.TemporaryDirectory() as directory:
            file = Path(directory) / 'asset'
            file.write_bytes(b'bytes')
            with self.transport([(201, {'id': 7, 'name': 'wrong', 'size': 5,
                                        'browser_download_url': 'https://example.invalid/asset'})]):
                with self.assertRaises(self.module.GiteeAPIError) as failure:
                    self.api.upload_asset(3, {'name': 'asset', 'size': 5}, file)
                self.assertEqual(failure.exception.outcome, 'unknown')
            self.assertEqual(len(self.sent), 1)

    def test_write_diagnostics_distinguish_http_and_transport_failures(self):
        cases = [
            ((503, {'message': 'fake-secret'}), 503, 'unexpected_http_status', None, 'unknown'),
            ((403, {'message': 'fake-secret'}), 403, 'unexpected_http_status', None, 'failed'),
            (requests.Timeout('fake-secret'), None, 'timeout', 'Timeout', 'unknown'),
            (requests.ConnectionError('fake-secret'), None, 'connection_error', 'ConnectionError', 'unknown'),
            (requests.exceptions.SSLError('fake-secret'), None, 'tls_error', 'SSLError', 'unknown'),
            (requests.exceptions.ChunkedEncodingError('fake-secret'), None,
             'incomplete_response', 'ChunkedEncodingError', 'unknown'),
        ]
        for reply, status, reason, exception_type, outcome in cases:
            with self.subTest(reason=reason, status=status):
                self.sent.clear()
                with self.transport([reply]), self.assertRaises(self.module.GiteeAPIError) as failure:
                    self.api.delete_asset(3, 4)
                diagnostic = failure.exception.diagnostic()
                self.assertEqual(diagnostic, {'operation': 'delete_asset', 'outcome': outcome,
                                             'http_status': status, 'reason': reason,
                                             'exception_type': exception_type})
                self.assertEqual(len(self.sent), 1)
                self.assertNotIn('fake-secret', json.dumps(diagnostic) + str(failure.exception))

    def test_upload_diagnostics_identify_invalid_json_and_response_field(self):
        valid = {'id': 7, 'name': 'asset', 'size': 5,
                 'browser_download_url': 'https://example.invalid/asset'}
        cases = [
            (dict(valid, id=0), 'invalid_asset_id'),
            (dict(valid, name='fake-secret'), 'invalid_asset_name'),
            (dict(valid, size=6), 'invalid_asset_size'),
            (dict(valid, browser_download_url='https://fake-secret@example.invalid/asset'), 'invalid_asset_url'),
            (['fake-secret'], 'invalid_asset_response'),
        ]
        with tempfile.TemporaryDirectory() as directory:
            file = Path(directory) / 'asset'
            file.write_bytes(b'bytes')
            for data, reason in cases:
                with self.subTest(reason=reason):
                    self.sent.clear()
                    with self.transport([(201, data)]), self.assertRaises(self.module.GiteeAPIError) as failure:
                        self.api.upload_asset(3, {'name': 'asset', 'size': 5}, file)
                    diagnostic = failure.exception.diagnostic()
                    self.assertEqual(diagnostic['http_status'], 201)
                    self.assertEqual(diagnostic['reason'], reason)
                    self.assertEqual(diagnostic['outcome'], 'unknown')
                    self.assertEqual(len(self.sent), 1)
                    self.assertNotIn('fake-secret', json.dumps(diagnostic) + str(failure.exception))
            response = self.response(201, None)
            response._content = b'fake-secret: not JSON'
            with patch('requests.sessions.Session.request', return_value=response), \
                    self.assertRaises(self.module.GiteeAPIError) as failure:
                self.api.upload_asset(3, {'name': 'asset', 'size': 5}, file)
            diagnostic = failure.exception.diagnostic()
            self.assertEqual(diagnostic['http_status'], 201)
            self.assertEqual(diagnostic['reason'], 'invalid_json')
            self.assertEqual(diagnostic['exception_type'], 'JSONDecodeError')
            self.assertNotIn('fake-secret', json.dumps(diagnostic) + str(failure.exception))

    def test_retry_after_cannot_extend_bounded_wait(self):
        response = self.response(429, {})
        response.headers['Retry-After'] = '3600'
        with patch('requests.sessions.Session.request', return_value=response) as send, patch('gitee_api.time.sleep') as sleep:
            with self.assertRaises(self.module.GiteeAPIError):
                self.api.repository_info()
        self.assertEqual(send.call_count, 1)
        sleep.assert_not_called()

    def test_read_retries_are_bounded_and_creation_denial_is_not_replayed(self):
        with self.transport([(503, {})] * 6), patch('gitee_api.time.sleep'):
            with self.assertRaises(self.module.GiteeAPIError):
                self.api.repository_info()
        self.assertEqual(len(self.sent), 6)
        self.sent.clear()
        with self.transport([(200, {'id': 1, 'login': 'fixture'}), (429, {})]), patch('gitee_api.time.sleep'):
            with self.assertRaises(self.module.GiteeAPIError):
                self.api.create_repository()
        self.assertEqual(len(self.sent), 2)

    def test_release_json_and_asset_pagination(self):
        release = {'id': 3, 'tag_name': 'fixed/tag', 'name': 'fixed/tag', 'body': 'fixed/tag'}
        with self.transport([(200, None), (201, release),
                             (200, [{'id': i + 1, 'name': str(i)} for i in range(100)]), (200, []), (204, None)]):
            self.assertIsNone(self.api.target_release('fixed/tag'))
            self.assertEqual(self.api.create_release('fixed/tag', 'a' * 40)['id'], 3)
            self.assertEqual(len(self.api.target_assets(3)), 100)
            self.api.delete_asset(3, 1)
        self.assertIn('fixed%2Ftag', self.sent[0][1])
        self.assertEqual(self.sent[1][2]['json']['target_commitish'], 'a' * 40)
        self.assertEqual(self.sent[3][2]['params']['page'], 2)

    def test_attachment_query_has_no_nested_retry(self):
        with self.transport([(503, {})] * 6), patch('time.sleep') as clock:
            with self.assertRaises(self.module.GiteeAPIError):
                self.api.target_assets(3)
        self.assertEqual(len(self.sent), 1)
        clock.assert_not_called()

    def test_upload_reuses_real_multipart_and_checks_response(self):
        captured = []
        def send(method, url, **kwargs):
            encoder = kwargs['data']
            parts = MultipartDecoder(encoder.to_string(), encoder.content_type).parts
            captured.extend(part.content for part in parts)
            return self.response(201, {'id': 8, 'name': 'asset.tar.xz', 'size': 6,
                                       'browser_download_url': 'https://gitee.com/fixture/mirror/asset'})
        with tempfile.TemporaryDirectory() as directory:
            file = Path(directory) / 'asset.tar.xz'
            file.write_bytes(b'bytes\0')
            with patch('requests.sessions.Session.request', side_effect=send):
                self.assertEqual(self.api.upload_asset(3, {'name': file.name, 'size': 6}, file)['id'], 8)
        self.assertIn(b'bytes\0', captured)
        self.assertIn(b'fake-secret', captured)


if __name__ == '__main__':
    unittest.main()
