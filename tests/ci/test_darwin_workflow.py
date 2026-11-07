import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / '.github/workflows/build-platform.yml'


def job(source, name):
    match = re.search(r'^  ' + name + r':\n(.*?)(?=^  \w+:\n|\Z)', source, re.M | re.S)
    if match is None:
        raise AssertionError('missing job ' + name)
    return match.group(0)


def step_script(source, name):
    step = source.split('      - name: ' + name + '\n', 1)[1].split('\n      - ', 1)[0]
    return textwrap.dedent(re.split(r'        run: [|>]-?\n', step, maxsplit=1)[1])


class DarwinWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.source = WORKFLOW.read_text()

    def test_recipe_validation_entry(self):
        source = (ROOT / '.github/workflows/validate-macos-recipes.yml').read_text()
        self.assertIn('name: Validate macOS recipes', source)
        self.assertRegex(source, r'(?s)on:\n  push:\n    branches:\n      - task/darwin-aarch64-native-ci\n')
        self.assertIn('contents: read', source)
        self.assertNotIn('workflow_dispatch:', source)
        native = job(source, 'validate')
        self.assertIn('runs-on: macos-15', native)
        self.assertIn('SELECTED_MODULES: ${{ matrix.root }}', native)
        self.assertIn('ref: ${{ github.sha }}', native)
        self.assertIn('persist-credentials: false', native)
        guard = step_script(native, 'Require native macOS 15')
        self.assertIn('arm64', guard)
        self.assertIn('15.*', guard)
        for name in ['Prepare platform', 'Produce dependency layers', 'Verify selected work', 'Package and transport verified artifacts', 'Finalize validation']:
            self.assertIn('autolib-ci', step_script(native, name))
        self.assertIn('if: ${{ always() && !cancelled() }}', native)
        for path in ['runner.log', 'environment.log', '/results/', '/evidence/', '/logs/', 'jobserver.json']:
            self.assertIn(path, native)
        self.assertNotIn('prepare-publication', native)
        self.assertNotIn('gitee', native.lower())
        listener = (ROOT / '.github/workflows/publish-artifacts.yml').read_text()
        self.assertIn('Representative releases', listener)
        self.assertNotIn('Validate macOS recipes', listener)

    def test_recipe_jobs_keep_separate_roots_and_evidence(self):
        native = job((ROOT / '.github/workflows/validate-macos-recipes.yml').read_text(), 'validate')
        self.assertIn('fail-fast: false', native)
        roots = re.findall(r'^          - ([\w.+,@-]+)$', native, re.M)
        self.assertGreaterEqual(len(roots), 1)
        roots = ['flann@f9caaf609d8b,queue@45db9a8b4431', 'boost@1.87.0']
        work_template = re.search(r'^      WORK_ROOT: (.+)$', native, re.M)[1]
        artifact_templates = re.findall(r'^          name: (.+)$', native, re.M)
        self.assertEqual(len(artifact_templates), 3)
        records = []
        with tempfile.TemporaryDirectory(prefix='isolated recipes ') as temporary:
            for index, selected in enumerate(roots[:2]):
                workspace = Path(temporary) / str(index)
                root = workspace / f"recipe-{index}"
                root.mkdir(parents=True)
                replacements = {'github.workspace': str(workspace), 'github.run_id': '123',
                                'github.run_attempt': '2', 'matrix.root': selected, 'strategy.job-index': str(index)}
                def expand(value):
                    return re.sub(r'\$\{\{ ([\w.-]+) \}\}', lambda m: replacements[m[1]], value)
                work = Path(expand(work_template))
                self.assertEqual(work, root / 'build/ci/123-2/darwin-aarch64')
                work.mkdir(parents=True)
                (work / 'results').mkdir()
                (work / 'results/darwin-aarch64.json').write_text('{}')
                tools = root / 'build/tools'
                tools.mkdir(parents=True)
                cmake = tools / 'cmake'
                cmake.write_text('#!/bin/sh\nexit 0\n')
                cmake.chmod(0o755)
                cli = tools / 'autolib-ci'
                (tools / 'autolib-builder').write_text('fixture builder')
                cli.write_text('#!' + sys.executable + '\n' + textwrap.dedent('''
                    import json, sys
                    from pathlib import Path
                    args = sys.argv[1:]
                    options = dict(zip(args[1::2], args[2::2]))
                    assert Path(options['--builder-file']).is_file(), options['--builder-file']
                    Path(options['--output']).write_text(json.dumps({'platforms': [{
                        'id': options['--platforms'], 'requested_roots': options['--modules'].split(','),
                        'roots': options['--modules'].split(','), 'filtered_roots': []}]}))
                '''))
                cli.chmod(0o755)
                env = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ['PATH'],
                           WORK_ROOT=str(work), SELECTED_MODULES=selected, PLATFORM='darwin-aarch64',
                           GITHUB_WORKSPACE=str(workspace), SOURCE_ROOT=str(root), GITHUB_REPOSITORY='fixture/repo', GITHUB_REF='refs/heads/task',
                           GITHUB_SHA='abc', GITHUB_RUN_ID='123', GITHUB_RUN_ATTEMPT='2')
                script = step_script(native, 'Plan recipe validation') + '\n' + step_script(native, 'Require all requested roots')
                result = subprocess.run(['/bin/bash', '-euc', script], cwd=root, env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                plan = json.loads((root / 'build/ci-input/plan.json').read_text())
                self.assertEqual(plan['platforms'][0]['roots'], selected.split(','))
                self.assertEqual(json.loads((root / 'build/ci-input/run.json').read_text())['sha'], 'abc')
                # A failed first root retains its record while the second succeeds.
                (work / 'autolib-ci').write_text('#!/bin/sh\nprintf "%s\\n" "$SELECTED_MODULES" > "$WORK_ROOT/result"\nexit ' + str(index == 0 and 1 or 0) + '\n')
                (work / 'autolib-ci').chmod(0o755)
                env.update(CMAKE_COMMAND='cmake', MAKE_PROGRAM='make', BUDGET='2')
                result = subprocess.run(['/bin/bash', '-euc', step_script(native, 'Verify selected work')], cwd=root, env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 1 if index == 0 else 0, result.stderr)
                records.append((work / 'result', [expand(t) for t in artifact_templates]))
            self.assertEqual([p.read_text().strip() for p, _ in records], roots[:2])
            self.assertTrue(set(records[0][1]).isdisjoint(records[1][1]))
            self.assertNotEqual(records[0][0].parent, records[1][0].parent)

    def test_recipe_roots_must_survive_filtering(self):
        source = (ROOT / '.github/workflows/validate-macos-recipes.yml').read_text()
        script = step_script(job(source, 'validate'), 'Require all requested roots')
        roots = ['queue@45db9a8b4431', 'lz4@1.10.0']
        for active, requested, filtered, expected in [(roots, roots, [], 0),
                                                    (roots[:1], roots, ['lz4@1.10.0'], 1),
                                                    ([], roots, roots, 1), ([], [], [], 1)]:
            with self.subTest(active=active), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                (root / 'build/ci-input').mkdir(parents=True)
                (root / 'build/ci-input/plan.json').write_text(json.dumps(dict(platforms=[dict(id='darwin-aarch64', roots=active, requested_roots=requested, filtered_roots=filtered)])))
                result = subprocess.run(['/bin/bash', '-euc', script], cwd=root,
                                        env=dict(os.environ, PLATFORM='darwin-aarch64', SELECTED_MODULES=','.join(roots)), capture_output=True, text=True)
                self.assertEqual(result.returncode, expected, result.stderr)

    def test_platform_outputs_and_result_collection(self):
        self.assertIn('          - darwin-aarch64\n', self.source)
        plan = job(self.source, 'plan')
        self.assertIn('darwin: ${{ steps.plan.outputs.darwin }}', plan)
        expression = next(line.strip() for line in step_script(plan, 'Plan selected work').splitlines() if line.strip().startswith('jq -cr '))
        for platforms, expected in ((['darwin-aarch64'], ('false', 'false', 'true')),
                                    (['windows-x86_64', 'linux-x86_64', 'linux-aarch64', 'darwin-aarch64'], ('true', 'true', 'true'))):
            with self.subTest(platforms=platforms), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                (root / 'build/ci-input').mkdir(parents=True)
                (root / 'build/ci-input/plan.json').write_text(json.dumps({'platforms': [{'id': p} for p in platforms]}))
                output = root / 'output'
                result = subprocess.run(['/bin/bash', '-euc', expression], cwd=root, env=dict(os.environ, GITHUB_OUTPUT=str(output)), capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                values = dict(line.split('=', 1) for line in output.read_text().splitlines())
                self.assertEqual((values['linux'], values['windows'], values['darwin']), expected)
                self.assertEqual(json.loads(values['linux_matrix'])['include'], [] if len(platforms) == 1 else [
                    {'platform': 'linux-x86_64', 'runner': 'ubuntu-24.04'},
                    {'platform': 'linux-aarch64', 'runner': 'ubuntu-24.04-arm'}])
        finalize = job(self.source, 'finalize')
        self.assertIn('needs: [plan, linux, windows, darwin]', finalize)
        condition = next(line.strip() for line in finalize.splitlines() if 'if:' in line and 'outputs.linux' in line)
        condition = condition.removeprefix('if: ${{ ').removesuffix(' }}')
        for platform in ('linux', 'windows', 'darwin'):
            evaluated = condition
            for candidate in ('linux', 'windows', 'darwin'):
                evaluated = evaluated.replace('needs.plan.outputs.' + candidate, "'true'" if platform == candidate else "'false'")
            result = subprocess.run(['/bin/bash', '-c', '[[ ' + evaluated + ' ]]'])
            self.assertEqual(result.returncode, 0, platform)
        self.assertIn('pattern: ci-result-${{ github.run_id }}-${{ github.run_attempt }}-*', finalize)
        self.assertIn('runs-on: windows-2022', job(self.source, 'windows'))
        self.assertIn('image: ${{ needs.plan.outputs.linux_image }}', job(self.source, 'linux'))

    def test_native_job_identity_and_artifacts(self):
        darwin = job(self.source, 'darwin')
        for fragment in ('runs-on: macos-15', "needs.plan.outputs.darwin == 'true'", 'shell: bash',
                         'ref: ${{ github.sha }}', 'persist-credentials: false',
                         'WORK_ROOT: ${{ github.workspace }}/build/ci/${{ github.run_id }}-${{ github.run_attempt }}/darwin-aarch64',
                         'environment.log', 'jobserver.json', '/evidence/**/*.json', '/logs/'):
            self.assertIn(fragment, darwin)
        for kind in ('package', 'result', 'evidence'):
            self.assertIn('name: ci-' + kind + '-${{ github.run_id }}-${{ github.run_attempt }}-darwin-aarch64', darwin)

    def test_native_guard(self):
        script = step_script(job(self.source, 'darwin'), 'Require native macOS 15')
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for name, body in {'uname': 'echo "${FIXTURE_ARCH:-arm64}"', 'sw_vers': 'echo "${FIXTURE_VERSION:-15.7}"'}.items():
                path = root / name
                path.write_text('#!/bin/bash\n' + body + '\n')
                path.chmod(0o755)
            for variables, expected in (({}, 0), ({'FIXTURE_ARCH': 'x86_64'}, 1), ({'FIXTURE_VERSION': '14.8'}, 1)):
                environment = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'], WORK_ROOT=str(root / 'work'))
                environment.update(variables)
                result = subprocess.run(['/bin/bash', '-euc', script], env=environment, capture_output=True, text=True)
                self.assertEqual(result.returncode, expected, result.stderr)

    def test_preparation_json_becomes_step_outputs(self):
        darwin = job(self.source, 'darwin')
        script = step_script(darwin, 'Prepare native environment and tools')
        with tempfile.TemporaryDirectory(prefix='darwin environment ') as temporary:
            root = Path(temporary)
            (root / '.github/scripts').mkdir(parents=True)
            (root / 'build/ci-input').mkdir(parents=True)
            work = root / 'work with spaces'
            work.mkdir()
            expected = {'cmake_command': str(work / 'cmake'), 'make_program': str(work / 'make'), 'budget': 4}
            preparation = root / '.github/scripts/prepare-darwin.sh'
            preparation.write_text('exec "' + sys.executable + '" - "$@" <<\'PY\'\n' + textwrap.dedent('''
                import json, os, sys
                from pathlib import Path
                assert sys.argv[1:] == ['--config', '.github/ci-tools.json', '--plan', 'build/ci-input/plan.json', '--platform', 'darwin-aarch64', '--work-root', os.environ['WORK_ROOT']]
                work = Path(os.environ['WORK_ROOT'])
                print(json.dumps(dict(cmake_command=str(work / 'cmake'), make_program=str(work / 'make'), budget=4)))
                print('environment diagnostic', file=sys.stderr)
            ''') + 'PY\n')
            output = root / 'output'
            result = subprocess.run(['/bin/bash', '-euc', script], cwd=root, env=dict(os.environ, WORK_ROOT=str(work), PLATFORM='darwin-aarch64', GITHUB_OUTPUT=str(output)), capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads((root / 'build/ci-input/environment.json').read_text()), expected)
            self.assertEqual(dict(line.split('=', 1) for line in output.read_text().splitlines()), {key: str(value) for key, value in expected.items()})
            self.assertIn('environment diagnostic', (work / 'environment.log').read_text())
        for key in ('CMAKE_COMMAND', 'MAKE_PROGRAM', 'BUDGET'):
            self.assertIn(key + ': ${{ steps.environment.outputs.' + key.lower() + ' }}', darwin)

    def test_executor_calls_empty_library_and_failure(self):
        darwin = job(self.source, 'darwin')
        scripts = [step_script(darwin, name) for name in ('Prepare platform', 'Produce dependency layers', 'Verify selected work', 'Package and transport verified artifacts')]
        for layers, failure, expected in (([], '', ['prepare', 'verify', 'transport']),
                                           ([['lz4@1.10.0']], '', ['prepare', 'layer0', 'verify', 'transport']),
                                           ([['lz4@1.10.0']], 'layer0', ['prepare', 'layer0'])):
            with self.subTest(layers=layers, failure=failure), tempfile.TemporaryDirectory(prefix='darwin workflow ') as temporary:
                root = Path(temporary)
                work = root / 'work with spaces'
                work.mkdir()
                (root / 'build/ci-input').mkdir(parents=True)
                (root / 'build/ci-input/plan.json').write_text(json.dumps({'platforms': [{'id': 'darwin-aarch64', 'layers': layers}]}))
                binary = work / 'autolib-ci'
                binary.write_text('#!' + sys.executable + '\n' + textwrap.dedent('''
                    import json, os, sys
                    from pathlib import Path
                    args = sys.argv[1:]
                    options = dict(zip(args[1::2], args[2::2]))
                    phase = options.get('--layer', args[0])
                    with Path(os.environ['CALLS']).open('a') as stream:
                        stream.write(json.dumps([phase, options]) + '\\n')
                    sys.exit(1 if phase == os.environ['FAILURE'] else 0)
                '''))
                binary.chmod(0o755)
                environment = dict(os.environ, WORK_ROOT=str(work), GITHUB_WORKSPACE=str(root), GITHUB_REPOSITORY='fixture/trusted', PLATFORM='darwin-aarch64', CMAKE_COMMAND=str(work / 'cmake'), MAKE_PROGRAM=str(work / 'make') if layers else '', BUDGET='4' if layers else '0', CALLS=str(root / 'calls'), FAILURE=failure)
                result = subprocess.run(['/bin/bash', '-euc', '\n'.join(scripts)], cwd=root, env=environment, capture_output=True, text=True)
                self.assertEqual(result.returncode, 1 if failure else 0, result.stderr)
                calls = [json.loads(line) for line in (root / 'calls').read_text().splitlines()]
                self.assertEqual([phase for phase, _ in calls], expected)
                for phase, options in calls:
                    required = {'--repository': 'fixture/trusted', '--source-root': str(root), '--plan': 'build/ci-input/plan.json', '--run-identity': 'build/ci-input/run.json', '--platform': 'darwin-aarch64', '--work-root': str(work), '--cmake-command': str(work / 'cmake')}
                    if phase == 'transport':
                        required['--output'] = str(work / 'transport.tar')
                    else:
                        required.update({'--layer': phase, '--importer-file': str(work / 'install/autolib-importer-darwin-aarch64'), '--make-program': environment['MAKE_PROGRAM'], '--budget': environment['BUDGET']})
                    self.assertEqual(options, required)


if __name__ == '__main__':
    unittest.main()
