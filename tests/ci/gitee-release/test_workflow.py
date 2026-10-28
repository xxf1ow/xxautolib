"""独立 workflow 的接线约束与真实 Bash 分支检查。"""

import os
from pathlib import Path
import re
import shutil
import subprocess
from types import SimpleNamespace
import unittest


ROOT = Path(__file__).resolve().parents[3]
WORKFLOW = ROOT / ".github/workflows/sync-gitee.yml"
ACTION = ROOT / ".github/actions/gitee-release/action.yml"


def step(document, title):
    return document.split("      - " + title, 1)[1].split("\n      - ", 1)[0]


class WorkflowTests(unittest.TestCase):
    def setUp(self):
        self.document = WORKFLOW.read_text(encoding="utf-8")
        self.action = ACTION.read_text(encoding="utf-8")

    def test_workflow_trigger_and_checkout(self):
        self.assertIn("workflows: [Representative releases]", self.document)
        self.assertIn("types: [completed]", self.document)
        self.assertIn("workflow_dispatch:", self.document)
        expression = re.search(r"^    if: \$\{\{ (.+) \}\}$", self.document, re.M)[1]
        self.assertIn("github.event.workflow_run.conclusion == 'success'", expression)
        self.assertIn("github.event.workflow_run.repository.full_name == github.repository", expression)
        for event, conclusion, repository, expected in (
                ("workflow_run", "success", "xxf1ow/xxautolib", True),
                ("workflow_run", "failure", "xxf1ow/xxautolib", False),
                ("workflow_run", "cancelled", "xxf1ow/xxautolib", False),
                ("workflow_run", "success", "other/repo", False),
                ("workflow_dispatch", None, None, True)):
            github = SimpleNamespace(event_name=event, repository="xxf1ow/xxautolib", event=SimpleNamespace(
                workflow_run=SimpleNamespace(conclusion=conclusion, repository=SimpleNamespace(full_name=repository))))
            with self.subTest(event=event, conclusion=conclusion, repository=repository):
                self.assertEqual(eval(expression.replace("||", "or").replace("&&", "and"),
                                      {"__builtins__": {}}, {"github": github}), expected)
        checkout = step(self.document, "uses: actions/checkout@v7")
        self.assertIn("ref: ${{ github.sha }}", checkout)
        self.assertIn("persist-credentials: false", checkout)
        self.assertNotIn("head_sha", self.document)
        self.assertNotIn("download-artifact", self.document)

    def test_branch_guard_executes_for_heads_only(self):
        guard = step(self.document, "name: Require branch ref")
        script = "\n".join(line[10:] for line in guard.split("run: |\n", 1)[1].splitlines())
        bash = "C:/Program Files/Git/bin/bash.exe" if os.name == "nt" else shutil.which("bash")
        if not bash or not Path(bash).is_file():
            self.skipTest("Bash unavailable; branch guard execution remains unverified")
        for ref, success in (("refs/heads/task", True), ("refs/heads/", False),
                             ("refs/tags/v1", False), ("refs/pull/1/merge", False)):
            environment = {k: v for k, v in os.environ.items() if k.upper() not in {"GITEE_TOKEN", "GITHUB_TOKEN"}}
            with self.subTest(ref=ref):
                result = subprocess.run([bash, "--noprofile", "--norc", "-c", script],
                                        env=dict(environment, GITHUB_REF=ref), capture_output=True, timeout=10)
                self.assertEqual(result.returncode == 0, success)

    def test_workflow_separates_installation_and_credentials(self):
        install = step(self.document, "name: Install sync dependencies")
        sync = step(self.document, "name: Synchronize Release")
        self.assertLess(self.document.index("name: Install sync dependencies"),
                        self.document.index("name: Synchronize Release"))
        for credential in ("GITEE_TOKEN", "GITHUB_TOKEN", "gitee_token", "secrets."):
            self.assertNotIn(credential, self.document.split("name: Synchronize Release", 1)[0])
        self.assertIn("-r .github/actions/gitee-release/requirements.txt", install)
        self.assertIn("-c .github/actions/gitee-release/constraints.txt", install)
        self.assertIn("python -m venv", install)
        self.assertIn("$GITHUB_PATH", install)
        self.assertIn("GITEE_TOKEN: ${{ secrets.GITEE_TOKEN }}", sync)
        self.assertIn("GITHUB_TOKEN: ${{ github.token }}", sync)
        self.assertIn("uses: ./.github/actions/gitee-release", sync)
        self.assertIn("packages: ${{ inputs.packages }}", sync)
        self.assertNotIn("${{", "\n".join(line for line in install.splitlines() if line.startswith("          ")))
        self.assertNotIn("pip install", self.action)
        self.assertNotIn("gitee_release.py", self.action)
        self.assertIn('python "${{ github.action_path }}/sync.py"', self.action)
        for environment, input_name in (("SYNC_PACKAGES", "packages"), ("GITEE_REPOSITORY", "repository"),
                                         ("GITEE_RELEASE_TAG", "tag")):
            self.assertIn(environment + ": ${{ inputs." + input_name + " }}", self.action)

    def test_workflow_target_serialization_and_limits(self):
        concurrency = self.document.split("concurrency:\n", 1)[1].split("\njobs:", 1)[0]
        sync = step(self.document, "name: Synchronize Release")
        for expression in ("${{ vars.GITEE_REPOSITORY || 'xxf1ow/autolib-release' }}",
                           "${{ vars.GITEE_RELEASE_TAG || 'autolib-release' }}"):
            self.assertIn(expression, concurrency)
            self.assertIn(expression, sync)
        self.assertNotIn("github.ref", concurrency)
        self.assertIn("cancel-in-progress: false", concurrency)
        self.assertIn("group: gitee-release-sync-", concurrency)
        self.assertIn("contents: read", self.document)
        self.assertNotIn("contents: write", self.document)
        self.assertEqual(self.document.count("runs-on:"), 1)
        self.assertIn("runs-on: ubuntu-24.04", self.document)
        self.assertIn("timeout-minutes: 30", self.document)
        self.assertIn("python-version: '3.12'", self.document)


if __name__ == "__main__":
    unittest.main()
