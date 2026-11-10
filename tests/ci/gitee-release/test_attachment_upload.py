"""真实 TLS 上传受墙钟预算限制，超时不确认元数据且回收请求进程。"""

import hashlib
import importlib
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

from sync_fixture import Remote, TOKEN

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / '.github/actions/gitee-release'))


class AttachmentUploadTests(unittest.TestCase):
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
                    patch('publish.upload_attachment', side_effect=bounded):
                started = time.monotonic()
                result = publish.publish_one(api, 42, asset, [], fetch_asset=lambda _: file,
                                             confirm_item=lambda item: confirmed.append(item))
                self.assertTrue(entered.is_set(), 'deadline must interrupt an actual HTTP upload')
                self.assertLess(time.monotonic() - started, 5)
                self.assertEqual((result['status'], result['reason']), ('failed', 'total_timeout'))
                self.assertEqual(confirmed, [])
                self.assertEqual(len(processes), 1)
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
