"""发布入口、凭据隔离及 workflow shell 接线检查。"""

import os
from pathlib import Path
import shutil
import subprocess
from types import SimpleNamespace
import unittest

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
        self.assertTrue(WORKFLOW.is_file(), 'missing independent publication workflow')
        self.document = WORKFLOW.read_text(encoding='utf-8')
        self.action = ACTION.read_text(encoding='utf-8')

    def bash(self, text, environment):
        bash = 'C:/Program Files/Git/bin/bash.exe' if os.name == 'nt' else shutil.which('bash')
        self.assertTrue(bash, 'Bash required for workflow shell tests')
        clean = {k: v for k, v in os.environ.items() if k.upper() not in {'GITEE_TOKEN', 'GITHUB_TOKEN'}}
        return subprocess.run([bash, '--noprofile', '--norc', '-c', text],
                              env=dict(clean, **environment), capture_output=True, text=True, timeout=15)

    def test_publication_workflow(self):
        for text in ('workflows: [Representative releases]', 'types: [completed]',
                     'workflow_dispatch:', "github.event.workflow_run.conclusion == 'success'",
                     'github.event.workflow_run.repository.full_name == github.repository',
                     'runs-on: ubuntu-24.04', "python-version: '3.12'", 'actions: read'):
            self.assertIn(text, self.document)
        condition = self.document.split('    if: ${{ ', 1)[1].split(' }}', 1)[0]
        for event, conclusion, repository, expected in (
                ('workflow_run', 'success', 'xxf1ow/xxautolib', True),
                ('workflow_run', 'failure', 'xxf1ow/xxautolib', False),
                ('workflow_run', 'cancelled', 'xxf1ow/xxautolib', False),
                ('workflow_run', 'success', 'other/repo', False),
                ('workflow_dispatch', None, None, True)):
            github = SimpleNamespace(event_name=event, repository='xxf1ow/xxautolib', event=SimpleNamespace(
                workflow_run=SimpleNamespace(conclusion=conclusion, repository=SimpleNamespace(full_name=repository))))
            with self.subTest(event=event, conclusion=conclusion, repository=repository):
                self.assertEqual(eval(condition.replace('||', 'or').replace('&&', 'and'),
                                      {'__builtins__': {}}, {'github': github}), expected)
        select = step(self.document, 'name: Select original build')
        self.assertIn('GITHUB_TOKEN: ${{ github.token }}', select)
        self.assertIn('SOURCE_RUN_ID: ${{ inputs.source_run_id }}', select)
        self.assertIn('SOURCE_ATTEMPT: ${{ inputs.source_attempt }}', select)
        self.assertIn('.github/scripts/prepare-publication.py select', select)
        download = step(self.document, 'uses: actions/download-artifact@v8')
        for text in ('artifact-ids: ${{ steps.selection.outputs.artifact_ids }}',
                     'run-id: ${{ steps.selection.outputs.run_id }}', 'merge-multiple: false'):
            self.assertIn(text, download)
        original = step(self.document, 'name: Checkout original build source')
        self.assertIn('ref: ${{ steps.selection.outputs.sha }}', original)
        self.assertIn('path: build/source', original)
        builder = step(self.document, 'name: Build original builder')
        self.assertIn('working-directory: build/source', builder)
        self.assertIn('src/autolib-builder/go-sdk.cmake', builder)
        self.assertIn('-mod=readonly', builder)
        self.assertIn("steps.selection.outputs.has_platforms == 'true'", builder)
        for title in ('Select original build', 'Checkout original build source', 'Build original builder',
                      'Prepare publication', 'Install publication dependencies'):
            self.assertNotIn('GITEE_TOKEN', step(self.document, 'name: ' + title))
        install = step(self.document, 'name: Install publication dependencies')
        for text in ('python -m venv', '-r .github/actions/gitee-release/requirements.txt',
                     '-c .github/actions/gitee-release/constraints.txt'):
            self.assertIn(text, install)
        publish = step(self.document, 'name: Publish selected artifacts or latest')
        self.assertIn('GITEE_TOKEN: ${{ secrets.GITEE_TOKEN }}', publish)
        self.assertNotIn('GITHUB_TOKEN', publish)

    def test_only_gitee_publication_entry(self):
        self.assertFalse((ROOT / '.github/workflows/sync-gitee.yml').exists())
        build = (ROOT / '.github/workflows/build-platform.yml').read_text(encoding='utf-8')
        for forbidden in ('contents: write', 'GITHUB_TOKEN:', 'workflow_dispatches',
                          'Initialize fixed Release', 'autolib-ci init'):
            self.assertNotIn(forbidden, build)
        self.assertNotIn('contents: write', self.document)
        self.assertIn("group: artifact-publication-gitee-${{ vars.GITEE_REPOSITORY || 'xxf1ow/autolib-release' }}", self.document)
        self.assertIn('cancel-in-progress: false', self.document)
        for name, env in (('host', 'PUBLISH_HOST'), ('mode', 'PUBLISH_MODE'), ('input', 'PUBLISH_INPUT'),
                          ('latest', 'PUBLISH_LATEST'), ('repository', 'GITEE_REPOSITORY'), ('tag', 'GITEE_RELEASE_TAG')):
            self.assertIn(env + ': ${{ inputs.' + name + ' }}', self.action)
        self.assertIn('publish.py', self.action)
        self.assertNotIn('pip install', self.action)
        in_run = False
        for line in self.document.splitlines():
            if line.startswith('        run: |'):
                in_run = True
            elif line.strip() and not line.startswith('          '):
                in_run = False
            elif in_run:
                self.assertNotIn('${{', line, 'workflow expressions must not be interpolated into shell')

    def test_manual_guard(self):
        text = script(self.document, 'Validate publication request')
        cases = [('refs/heads/topic', 'gitee', 'artifacts', True),
                 ('refs/heads/topic', 'gitee', 'latest', True),
                 ('refs/heads/', 'gitee', 'artifacts', False),
                 ('refs/tags/v1', 'gitee', 'artifacts', False),
                 ('refs/pull/1/merge', 'gitee', 'artifacts', False),
                 ('refs/heads/topic', 'github', 'artifacts', False),
                 ('refs/heads/topic', 'gitee', 'other', False)]
        for ref, host, mode, expected in cases:
            with self.subTest(ref=ref, host=host, mode=mode):
                result = self.bash(text, dict(GITHUB_REF=ref, PUBLISH_HOST=host, PUBLISH_MODE=mode))
                self.assertEqual(result.returncode == 0, expected, result.stderr)
        self.assertNotIn('${{', text)

    def test_empty_publication_skips_writes(self):
        text = script(self.document, 'Prepare publication')
        self.assertIn('.github/scripts/prepare-publication.py prepare', text)
        self.assertIn('has_writes', text)
        publish = step(self.document, 'name: Publish selected artifacts or latest')
        self.assertIn("env.PUBLISH_MODE == 'latest' || steps.publication.outputs.has_writes == 'true'", publish)
        for title in ('Select original build', 'Checkout original build source', 'Prepare publication'):
            self.assertIn("env.PUBLISH_MODE == 'artifacts'", step(self.document, 'name: ' + title))


if __name__ == '__main__':
    unittest.main()
