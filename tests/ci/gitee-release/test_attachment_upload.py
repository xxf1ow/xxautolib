"""真实 TLS 上传受墙钟预算限制，超时不确认元数据且回收请求进程。"""

import contextlib
import hashlib
import importlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from sync_fixture import Remote, TOKEN

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / '.github/actions/gitee-release'))


class AttachmentUploadTests(unittest.TestCase):
    @contextlib.contextmanager
    def scripted_workers(self, scripts):
        popen, processes = subprocess.Popen, []
        scripts = iter(scripts)
        prefix = '''import json, sys, time
payload = json.load(sys.stdin)
def emit(event, **fields):
    print(json.dumps(dict(event=event, at=time.monotonic(), **fields)), flush=True)
'''
        def start(*args, **kwargs):
            process = popen([sys.executable, '-c', prefix + next(scripts)], **kwargs)
            processes.append(process)
            return process
        with patch('attachment_upload.subprocess.Popen', side_effect=start):
            yield processes
        self.assertTrue(all(p.poll() is not None for p in processes), 'upload worker was not reaped')

    def upload_scripted(self, **options):
        import attachment_upload
        api = SimpleNamespace(token=TOKEN, repository='fixture/mirror')
        return attachment_upload.upload_attachment(api, 42, dict(name='probe.tar.zst', size=100),
                                                   Path('probe.tar.zst'), [], **options)

    def test_stalled_or_unconfirmed_attempt_is_reaped_then_retried(self):
        import attachment_upload
        for reason, body in [
            ('upload_stalled', '''emit('upload_start')
for _ in range(30):
    emit('progress', bytes_read=1, total_bytes=100)
    time.sleep(0.05)
'''),
            ('response_timeout', '''emit('upload_start')
emit('progress', bytes_read=100, total_bytes=100)
time.sleep(2)
'''),
        ]:
            with self.subTest(reason=reason), self.scripted_workers([body, '''assert payload['initial_matches'] is None
emit('complete')
''']) as processes, \
                    patch('attachment_upload.ATTACHMENT_IDLE_SECONDS', 0.3, create=True), \
                    patch('attachment_upload.ATTACHMENT_RESPONSE_SECONDS', 0.3, create=True), \
                    patch('attachment_upload.ATTACHMENT_RETRY_DELAY_SECONDS', 0.01, create=True), \
                    patch('attachment_upload._log') as log:
                self.upload_scripted(budget_seconds=5)
                self.assertEqual(len(processes), 2)
                self.assertNotEqual(processes[0].returncode, 0)
                retries = [c.kwargs for c in log.call_args_list if c.args[0] == 'retry']
                self.assertEqual([c['reason'] for c in retries], [reason])

    def test_progress_keeps_slow_upload_alive_and_logs_at_intervals(self):
        script = '''emit('upload_start')
for count in range(1, 11):
    emit('progress', bytes_read=count * 10, total_bytes=100)
    time.sleep(0.08)
time.sleep(0.4)
emit('complete')
'''
        events = []
        def record(event, name=None, **fields):
            if event == 'gitee_upload_progress':
                events.append((time.monotonic(), fields))
        with self.scripted_workers([script]) as processes, \
                patch('attachment_upload.ATTACHMENT_IDLE_SECONDS', 0.3, create=True), \
                patch('attachment_upload.ATTACHMENT_RESPONSE_SECONDS', 1, create=True), \
                patch('attachment_upload.ATTACHMENT_LOG_SECONDS', 0.2, create=True), \
                patch('attachment_upload._log', side_effect=record):
            self.upload_scripted(budget_seconds=5)
        self.assertEqual(len(processes), 1)
        self.assertGreaterEqual(len(events), 3)
        self.assertTrue(all(b[0] - a[0] >= 0.18 for a, b in zip(events, events[1:])))
        self.assertTrue(any(0 < e['bytes_read'] < e['total_bytes'] for _, e in events))
        self.assertTrue(any(e['phase'] == 'awaiting_response' for _, e in events))
        self.assertNotIn(TOKEN, json.dumps(events))

    def test_total_budget_stops_even_progressing_upload(self):
        import attachment_upload
        script = '''emit('upload_start')
for count in range(100):
    emit('progress', bytes_read=count, total_bytes=1000)
    time.sleep(0.05)
'''
        with self.scripted_workers([script]) as processes, \
                patch('attachment_upload.ATTACHMENT_IDLE_SECONDS', 0.3, create=True), \
                self.assertRaises(attachment_upload.GiteeAPIError) as caught:
            self.upload_scripted(budget_seconds=0.7)
        self.assertEqual(caught.exception.reason, 'total_timeout')
        self.assertEqual(len(processes), 1)

    def test_blocked_upload_is_killed_without_confirmation_then_replaced(self):
        self.assertIsNotNone(importlib.util.find_spec('attachment_upload'), 'attachment deadline is not implemented')
        import attachment_upload
        import publish
        with tempfile.TemporaryDirectory() as directory:
            remote = Remote(directory)
            self.addCleanup(remote.close)
            entered, release = threading.Event(), threading.Event()
            self.addCleanup(release.set)
            name = 'queue-1-any-any.tar.zst'
            file = Path(directory) / name
            file.write_bytes(b'archive bytes')
            asset = dict(name=name, kind='library', size=13, sha256=hashlib.sha256(file.read_bytes()).hexdigest(),
                         package=dict(name='queue', version='1', os='any', arch='any'))
            remote.pause_uploads[name] = entered, release
            api = publish.GiteeAPI(TOKEN, 'fixture/mirror')
            self.addCleanup(api.close)
            confirmed, processes = [], []
            popen = subprocess.Popen
            def start(*args, **kwargs):
                process = popen(*args, **kwargs)
                processes.append(process)
                return process
            upload = attachment_upload.upload_attachment
            def bounded(*args):
                return upload(*args, budget_seconds=2)
            with patch.dict(os.environ, remote.environment()), \
                    patch('attachment_upload.subprocess.Popen', side_effect=start), \
                    patch('attachment_upload.ATTACHMENT_LOG_SECONDS', 0.2), \
                    patch('attachment_upload._log') as log, \
                    patch('publish.upload_attachment', side_effect=bounded):
                started = time.monotonic()
                result = publish.publish_one(api, 42, asset, [], fetch_asset=lambda _: file,
                                             confirm_item=lambda item: confirmed.append(item))
                self.assertTrue(entered.is_set(), 'deadline must interrupt an actual HTTP upload')
                self.assertLess(time.monotonic() - started, 5)
                self.assertEqual((result['status'], result['reason']), ('failed', 'total_timeout'))
                self.assertEqual(confirmed, [])
                self.assertEqual(len(processes), 1)
                progress = [c.kwargs for c in log.call_args_list if c.args[0] == 'gitee_upload_progress']
                self.assertTrue(any(p['phase'] == 'awaiting_response' and p['bytes_read'] == p['total_bytes']
                                    and p['bytes_read'] > asset['size'] for p in progress))
                self.assertIsNotNone(processes[0].returncode)
                if os.name == 'posix':
                    with self.assertRaises(ChildProcessError):
                        os.waitpid(processes[0].pid, os.WNOHANG)
                release.set()
                remote.pause_uploads.clear()
                result = publish.publish_one(api, 42, asset, list(remote.targets), fetch_asset=lambda _: file,
                                             confirm_item=lambda item: confirmed.append(item))
            self.assertEqual(result['status'], 'uploaded')
            self.assertEqual(confirmed, [asset])
            self.assertEqual([a['name'] for a in remote.targets], [name])
            self.assertEqual(remote.uploads, [name, name])
            self.assertTrue(any(method == 'DELETE' for method, _, _ in remote.calls))
            self.assertEqual(remote.errors, [])


if __name__ == '__main__':
    unittest.main()
