"""独立同步的事件校验、固定分支快照和凭据接线。"""

import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[3]
WORKFLOW = ROOT / '.github/workflows/publish-artifacts.yml'
ACTION = ROOT / '.github/actions/gitee-release/action.yml'


def step(document, title):
    return document.split('      - ' + title, 1)[1].split('\n      - ', 1)[0]


def script(document, title):
    block = step(document, 'name: ' + title).split('        run: |\n', 1)[1]
    return '\n'.join(line[10:] for line in block.splitlines())


class WorkflowTests(unittest.TestCase):
    def setUp(self):
        self.document = WORKFLOW.read_text()
        self.action = ACTION.read_text()

    def snapshot(self, event_name, event, ref='refs/heads/topic/slash', repository='fork/source'):
        text = script(self.document, 'Fix current branch snapshot')
        code = text.split("python - <<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
        root = ROOT / 'build' / 'workflow-tests'
        root.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=root) as directory:
            event_file, output = Path(directory) / 'event.json', Path(directory) / 'output'
            event_file.write_text(json.dumps(event))
            environment = dict(GITHUB_EVENT_NAME=event_name, GITHUB_EVENT_PATH=str(event_file),
                GITHUB_REPOSITORY=repository, GITHUB_REF=ref, GITHUB_TOKEN='fixture', GITHUB_OUTPUT=str(output),
                PUBLISH_HOST='gitee', PUBLISH_MODE='sync', GITEE_REPOSITORY='fixture/mirror', GITEE_RELEASE_TAG='fixed')
            requests = []
            def response(request, **kwargs):
                requests.append(request.full_url)
                return io.BytesIO(json.dumps({'sha': 'b' * 40}).encode())
            with patch.dict(os.environ, environment), patch('urllib.request.urlopen', side_effect=response):
                try:
                    exec(compile(code, 'workflow snapshot', 'exec'), {})
                except (ValueError, SystemExit):
                    return False, requests, ''
            return True, requests, output.read_text()

    def test_manual_and_successful_build_resolve_latest_branch_once(self):
        for name, event in [('workflow_dispatch', {}), ('workflow_run', {'workflow_run': {
            'conclusion': 'success', 'head_branch': 'topic/slash', 'head_sha': 'a'*40,
            'head_repository': {'full_name': 'fork/source'}}})]:
            with self.subTest(name=name):
                ok, requests, output = self.snapshot(name, event)
                self.assertTrue(ok)
                self.assertEqual(requests, ['https://api.github.com/repos/fork/source/commits/refs%2Fheads%2Ftopic%2Fslash'])
                self.assertIn('sha=' + 'b'*40, output)
                self.assertIn('ref=refs/heads/topic/slash', output)
                self.assertNotIn('a'*40, output)

    def test_invalid_event_branch_and_foreign_build_never_resolve(self):
        cases = [('workflow_dispatch', {}, ref) for ref in ['refs/tags/v1', 'refs/heads/', 'refs/heads/a..b', 'refs/pull/1/merge']]
        for conclusion, repo, branch in [('failure','fork/source','topic'), ('cancelled','fork/source','topic'),
                                        ('success','other/source','topic'), ('success','fork/source','bad\nbranch')]:
            cases.append(('workflow_run', {'workflow_run': {'conclusion': conclusion, 'head_branch': branch,
                         'head_repository': {'full_name': repo}}}, 'refs/heads/master'))
        for name, event, ref in cases:
            with self.subTest(event=event, ref=ref):
                ok, requests, _ = self.snapshot(name, event, ref)
                self.assertFalse(ok)
                self.assertEqual(requests, [])

    def test_single_source_flow_and_scoped_credentials(self):
        for text in ('workflow_run:', 'workflows: [Representative releases]', 'types: [completed]',
                     'options: [sync, latest]', 'contents: write', 'queue: max', 'cancel-in-progress: false',
                     'group: ${{ github.repository }}-autolib-release'):
            self.assertIn(text, self.document)
        for forbidden in ('source_run_id', 'source_attempt', 'download-artifact', 'prepare-publication', ' transport '):
            self.assertNotIn(forbidden, self.document)
        self.assertEqual(self.document.count('uses: actions/checkout@'), 1)
        self.assertIn('ref: ${{ steps.snapshot.outputs.sha }}', self.document)
        for title in ['Fix current branch snapshot', 'Build sync tools', 'Describe fixed source', 'Install publication dependencies']:
            self.assertNotIn('GITEE_TOKEN', step(self.document, 'name: ' + title))
        self.assertIn('go-sdk.cmake', script(self.document, 'Build sync tools'))
        self.assertIn("env.PUBLISH_MODE == 'sync'", step(self.document, 'name: Read cache'))
        self.assertIn('GITEE_TOKEN: ${{ secrets.GITEE_TOKEN }}', step(self.document, 'name: Read cache'))
        self.assertIn('GITEE_REPOSITORY: ${{ vars.GITEE_REPOSITORY }}', step(self.document, 'name: Read cache'))
        publish = step(self.document, 'name: Synchronize Gitee')
        self.assertIn('GITEE_TOKEN: ${{ secrets.GITEE_TOKEN }}', publish)
        self.assertIn('GITHUB_TOKEN: ${{ github.token }}', publish)
        for key, env in [('source','SYNC_SOURCE'),('cache','SYNC_CACHE'),('cache-tool','SYNC_CACHE_TOOL'),
                         ('ref','SYNC_REF'),('sha','SYNC_SHA'),('mode','PUBLISH_MODE')]:
            self.assertIn(env + ': ${{ inputs.' + key + ' }}', self.action)
        in_run = False
        for line in self.document.splitlines():
            if line.startswith('        run: |'): in_run = True
            elif line.strip() and not line.startswith('          '): in_run = False
            elif in_run: self.assertNotIn('${{', line)


if __name__ == '__main__':
    unittest.main()
