"""Validate exact CI packaged artifacts and prepare their Gitee publication."""

import argparse
from collections.abc import Mapping
import hashlib
import io
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import tarfile
import tempfile
import tomllib
import unicodedata
from urllib.parse import quote
import urllib.request
import zipfile


PLATFORMS = {'windows-x86_64', 'linux-x86_64', 'linux-aarch64', 'darwin-aarch64'}


def _require(condition, message):
    if not condition:
        raise ValueError(message)


def _component(value):
    _require(isinstance(value, str) and re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.+-]*', value)
             and not value.endswith('.'), 'invalid package name or version')
    stem = value.split('.')[0].upper()
    _require(stem not in {'CON', 'PRN', 'AUX', 'NUL'}
             and not re.fullmatch(r'(COM|LPT)[1-9]', stem)
             and value.lower() != 'package.toml', 'reserved package name or version')
    return value


def _latest(value):
    _require(isinstance(value, dict), 'latest must be a JSON object')
    for name, version in value.items():
        _component(name)
        _component(version)
    return value


def _repository(value):
    _require(isinstance(value, str) and re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*', value),
             'repository requires a valid owner/name')
    return value


def inject_release_root(source: bytes, repository: str, tag: str) -> bytes:
    """Return a copy with one complete empty assignment replaced by the encoded target.

    Reject invalid targets and missing, repeated or nonempty release-root assignments.
    All bytes outside that assignment remain unchanged.
    """
    _repository(repository)
    _require(isinstance(tag, str) and tag and not any(c.isspace() or unicodedata.category(c) == 'Cc' for c in tag),
             'Release tag must be nonempty and contain no whitespace or controls')
    empty = b'set(_autolib_release_root "")'
    assignments = re.findall(rb'(?m)^[ \t]*set[ \t]*\([ \t]*_autolib_release_root\b[^\r\n]*', source)
    _require(assignments == [empty] and re.search(rb'(?m)^' + re.escape(empty) + rb'(?=\n|\Z)', source),
             'entry requires exactly one complete empty release-root assignment')
    root = 'https://gitee.com/' + repository + '/releases/download/' + quote(tag, safe='-._~$&+:=@')
    return re.sub(rb'(?m)^' + re.escape(empty) + rb'(?=\n|\Z)',
                  ('set(_autolib_release_root "' + root + '")').encode('ascii'), source, count=1)


def _identity(value, repository):
    _repository(repository)
    _require(isinstance(value, dict) and set(value) == {
        'repository', 'ref', 'sha', 'checkout_sha', 'run_id', 'attempt'}, 'invalid run identity fields')
    _require(all(isinstance(v, str) for v in value.values()), 'run identity must contain strings')
    _require(value['repository'] == repository and re.fullmatch(r'[0-9a-f]{40}', value['sha'])
             and value['checkout_sha'] == value['sha'], 'run repository or commit mismatch')
    for key in ['run_id', 'attempt']:
        _require(re.fullmatch(r'[1-9][0-9]*', value[key]), 'run and attempt must be positive integers')
    ref = value['ref']
    _require(ref.startswith('refs/heads/') and _branch(ref[len('refs/heads/'):]), 'source must be an ordinary branch')
    return value


def _branch(value):
    return bool(value and not value.startswith(('refs/', '/', '-'))
                and not value.endswith(('/', '.', '.lock')) and '..' not in value
                and '@{' not in value and not re.search(r'[\s~^:?*\[\\\x00-\x1f\x7f]', value)
                and all(p and not p.startswith('.') and not p.endswith('.lock') for p in value.split('/')))


class _ReadRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        redirected = super().redirect_request(request, fp, code, msg, headers, newurl)
        if redirected is not None:
            redirected.remove_header('Authorization')
        return redirected


def _request(url, token):
    headers = {'Accept': 'application/vnd.github+json', 'X-GitHub-Api-Version': '2026-03-10'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    with urllib.request.build_opener(_ReadRedirect()).open(
            urllib.request.Request(url, headers=headers, method='GET'), timeout=60) as response:
        return response.read()


def _json_file(path):
    _require(stat.S_ISREG(path.lstat().st_mode), f'{path}: expected a regular file')
    return json.loads(path.read_text(encoding='utf-8'))


def _planned_platforms(plan):
    _require(isinstance(plan, dict) and isinstance(plan.get('platforms'), list), 'invalid CI plan')
    platforms = [p['id'] for p in plan['platforms']]
    _require(len(platforms) == len(set(platforms)) and set(platforms) <= PLATFORMS, 'invalid or duplicate planned platforms')
    universal = any(m['platform_independent'] for p in plan['platforms'] for m in p['description']['modules'])
    _require(plan.get('publisher') in platforms if universal else plan.get('publisher', '') == '', 'invalid any-any publisher')
    return platforms


def select_source(environment: Mapping[str, str]) -> dict:
    """Read the exact successful attempt and its plan; perform no remote writes.

    SOURCE_RUN_ID/SOURCE_ATTEMPT select manual input. workflow_run events supply
    both when absent. Artifacts are run-scoped, so names and embedded identities
    must also identify the attempt. Missing, expired or ambiguous inputs fail.
    """
    repository = _repository(environment.get('GITHUB_REPOSITORY'))
    _require(environment.get('PUBLISH_HOST', 'gitee') == 'gitee', 'only gitee publication is supported')
    _require(environment.get('PUBLISH_MODE', 'artifacts') == 'artifacts', 'latest-only does not select build inputs')
    event = {}
    if environment.get('GITHUB_EVENT_PATH'):
        event = _json_file(Path(environment['GITHUB_EVENT_PATH']))
    triggered = event.get('workflow_run', {})
    run_id = environment.get('SOURCE_RUN_ID') or str(triggered.get('id', ''))
    attempt = environment.get('SOURCE_ATTEMPT') or str(triggered.get('run_attempt', ''))
    _require(re.fullmatch(r'[1-9][0-9]*', run_id) and re.fullmatch(r'[1-9][0-9]*', attempt), 'exact source run and attempt are required')
    token = environment.get('GITHUB_TOKEN', '')
    base = environment.get('GITHUB_API_URL', 'https://api.github.com').rstrip('/') + '/repos/' + repository
    run = json.loads(_request(f'{base}/actions/runs/{run_id}/attempts/{attempt}', token))
    _require(run['id'] == int(run_id) and run['run_attempt'] == int(attempt)
             and run['status'] == 'completed' and run['conclusion'] == 'success', 'source attempt is not successful')
    _require(run['repository']['full_name'] == repository and run['head_repository']['full_name'] == repository
             and run['repository']['id'] == run['head_repository']['id'], 'source repository mismatch')
    _require(run['path'] == '.github/workflows/build-platform.yml' and run['event'] == 'workflow_dispatch', 'unexpected source workflow or event')
    identity = _identity(dict(repository=repository, ref='refs/heads/' + run['head_branch'], sha=run['head_sha'],
                              checkout_sha=run['head_sha'], run_id=run_id, attempt=attempt), repository)
    if triggered:
        _require(all(triggered.get(k) == run[k] for k in ['id', 'run_attempt', 'head_sha', 'head_branch', 'path']), 'triggered run differs from selected attempt')
    artifacts = []
    page = 1
    while True:
        listing = json.loads(_request(f'{base}/actions/runs/{run_id}/artifacts?per_page=100&page={page}', token))
        artifacts.extend(listing['artifacts'])
        if len(artifacts) >= listing['total_count']:
            break
        _require(listing['artifacts'], 'artifact pagination ended before total_count')
        page += 1

    def exact(name):
        matches = [a for a in artifacts if a['name'] == name]
        _require(len(matches) == 1 and matches[0].get('expired') is False,
                 f'{name}: missing, duplicate or expired artifact; packaged artifacts require a new build')
        artifact = matches[0]
        origin = artifact['workflow_run']
        _require(origin['id'] == int(run_id) and origin['head_sha'] == identity['sha']
                 and origin['repository_id'] == run['repository']['id']
                 and origin['head_repository_id'] == run['repository']['id']
                 and origin['head_branch'] == run['head_branch'], f'{name}: artifact source mismatch')
        _require(type(artifact['id']) is int and artifact['id'] > 0, 'invalid artifact id')
        return dict(id=artifact['id'], name=name)

    plan_artifact = exact(f'ci-plan-{run_id}-{attempt}')
    with zipfile.ZipFile(io.BytesIO(_request(f"{base}/actions/artifacts/{plan_artifact['id']}/zip", token))) as archive:
        names = archive.namelist()
        _require(sorted(names) == ['plan.json', 'run.json'], 'plan artifact requires only plan.json and run.json')
        plan = json.loads(archive.read('plan.json'))
        _require(json.loads(archive.read('run.json')) == identity, 'plan run identity mismatch')
    selected = [plan_artifact] + [exact(f'ci-package-{run_id}-{attempt}-{p}') for p in _planned_platforms(plan)]
    _require(len({a['id'] for a in selected}) == len(selected), 'duplicate selected artifact id')
    return dict(source_run=identity, plan=plan, artifacts=selected,
                explicit_latest=_latest(json.loads(environment.get('LATEST') or '{}')))


def unpack_transport(archive: Path, destination: Path) -> dict:
    """Extract the file-only transport into a new directory using data_filter.

    Existing destinations are never changed. Failure can leave partial data in
    the newly created directory. Links, duplicate or case-conflicting paths and
    unsupported objects are rejected. Internal archives are not extracted.
    """
    destination.mkdir(parents=False, exist_ok=False)
    seen, spellings = set(), {}
    with tarfile.open(archive, 'r:*') as source:
        for member in source:
            name = member.name.rstrip('/')
            parts = name.split('/')
            _require(name and not name.startswith('/') and '\\' not in name and ':' not in name
                     and all(p not in {'', '.', '..'} for p in parts), f'{member.name}: unsafe transport path')
            _require(name not in seen, f'{name}: duplicate transport member')
            for n in range(1, len(parts) + 1):
                prefix = '/'.join(parts[:n])
                _require(spellings.get(prefix.lower(), prefix) == prefix, f'{name}: case-conflicting transport path')
                spellings[prefix.lower()] = prefix
            _require(member.isdir() or member.isreg(), f'{name}: transport requires regular files or directories, not links')
            seen.add(name)
            source.extract(member, destination, filter=tarfile.data_filter)
    return _json_file(destination / 'transport.json')


def _tool(platform):
    return 'autolib-importer-' + platform + ('.exe' if platform == 'windows-x86_64' else '')


def _validate_transport(root, transport, selected, platform, source_entry):
    plan, run = selected['plan'], selected['source_run']
    _require(isinstance(transport, dict) and set(transport) == {'run', 'platform', 'packages', 'entry', 'assets'},
             'transport requires packaged artifacts; legacy installation transport needs a new build')
    _require(transport['run'] == run and transport['platform'] == platform['id'], 'transport run or platform mismatch')
    _require(_json_file(root / 'plan.json') == plan, 'transport plan mismatch')
    result = _json_file(root / 'result.json')
    _require(result['run'] == run and result['platform'] == platform['id'] and result['complete'] is True
             and result['error'] == '', 'incomplete or mismatched platform result')
    _require(result['importer'] == dict(file=_tool(platform['id']), verified=True), 'unverified importer')
    entry = root / 'autolib.cmake'
    _require(transport.get('entry') == 'autolib.cmake'
             and stat.S_ISREG(entry.lstat().st_mode), 'invalid transported entry')
    _require(isinstance(result.get('entry'), dict) and result['entry'].get('verified') is True
             and result['entry'] == dict(file='autolib.cmake', sha256=hashlib.sha256(source_entry).hexdigest(), verified=True)
             and entry.read_bytes() == source_entry, 'transported entry differs from verified original commit')
    expected = []
    system, arch = platform['id'].split('-', 1)
    for module in platform['description']['modules']:
        name, version = _component(module['name']), _component(module['version'])
        universal = module['platform_independent']
        _require(type(universal) is bool and module['target'], 'invalid planned module')
        expected.append(dict(name=name, version=version, os='any' if universal else system,
                             arch='any' if universal else arch, dependencies=module['depends'] or [],
                             publish=not universal or plan['publisher'] == platform['id']))
    _require(len({(p['name'], p['version']) for p in expected}) == len(expected), 'duplicate planned package')
    normalized = [dict(p, dependencies=p['dependencies'] or []) for p in transport['packages']]
    _require(all(type(p['publish']) is bool for p in normalized), 'invalid package publish flag')
    _require(sorted(normalized, key=lambda p: (p['name'], p['version'])) == sorted(expected, key=lambda p: (p['name'], p['version'])), 'transport packages differ from plan')
    results = [dict(slot={k: p[k] for k in ['name', 'version', 'os', 'arch']}, built=True, verified=True) for p in expected]
    _require(sorted(result['packages'], key=lambda p: (p['slot']['name'], p['slot']['version'])) == sorted(results, key=lambda p: (p['slot']['name'], p['slot']['version'])), 'unverified exact package result')
    roots = [r['root'] for r in result['roots']]
    _require(len(roots) == len(set(roots)) and sorted(roots) == sorted(platform['roots'])
             and all(r['verified'] is True for r in result['roots']), 'unverified independent root result')
    descriptions = [dict(kind='library', name=f"{p['name']}-{p['version']}-{p['os']}-{p['arch']}.tar.zst",
                         package={k: p[k] for k in ['name', 'version', 'os', 'arch']}) for p in expected if p['publish']]
    descriptions.append(dict(kind='importer', name=_tool(platform['id']) + '.tar.xz', platform=platform['id']))
    descriptions = [dict(a, path='assets/' + a['name']) for a in descriptions]
    assets = transport['assets']
    _require(isinstance(assets, list) and all(isinstance(a, dict) for a in assets)
             and len(assets) == len(descriptions), 'transport asset count differs from plan')
    _require(len({a['name'].lower() for a in assets}) == len(assets), 'duplicate transport asset name')
    _require(sorted(({k: v for k, v in a.items() if k not in {'size', 'sha256'}} for a in assets), key=lambda a: a['name'])
             == sorted(descriptions, key=lambda a: a['name']), 'transport asset identities differ from plan')
    for asset in assets:
        path = root / asset['path']
        _require(type(asset.get('size')) is int and asset['size'] >= 0
                 and isinstance(asset.get('sha256'), str) and re.fullmatch(r'[0-9a-f]{64}', asset['sha256']),
                 f"{asset['name']}: invalid size or SHA256")
        _require(stat.S_ISREG(path.lstat().st_mode) and path.stat().st_size == asset['size'],
                 f"{asset['name']}: missing regular archive or size mismatch")
        with path.open('rb') as file:
            _require(hashlib.file_digest(file, 'sha256').hexdigest() == asset['sha256'], f"{asset['name']}: SHA256 mismatch")
    files = {'transport.json', 'plan.json', 'result.json', 'autolib.cmake'}
    files.update(a['path'] for a in descriptions)
    files.update('candidates/' + p['name'] + '/package.toml' for p in expected)
    directories = {parent.as_posix() for name in files for parent in Path(name).parents if parent != Path('.')}
    actual_files, actual_directories = set(), set()
    for path in root.rglob('*'):
        name = path.relative_to(root).as_posix()
        if path.is_dir():
            actual_directories.add(name)
        else:
            _require(stat.S_ISREG(path.lstat().st_mode), f'{name}: expected a regular transported file')
            actual_files.add(name)
    _require(actual_files == files and actual_directories == directories, 'transport members differ from planned packaged delivery')
    return expected


def prepare_publication(selection: dict, input_root: Path, source_root: Path,
                        output_root: Path, *, repository: str, tag: str,
                        source_repository: str) -> Path:
    """Validate packaged bytes in a fresh directory and return their manifest.

    input_root contains one directory per selected artifact; plan.json/run.json
    and transport.tar retain their uploaded names. output_root must be inside
    source_root/build and outside input_root. Inputs remain read-only; repeated
    calls preserve all archive bytes and hash the actual extracted files.
    source_repository must come from the trusted workflow context. All entry
    transports must match the original Git commit before publication;
    repository/tag are injected only into the unique uploaded entry copy.
    """
    run = _identity(selection['source_run'], source_repository)
    platforms = _planned_platforms(selection['plan'])
    explicit = _latest(selection['explicit_latest'])
    input_root, source_root, output_root = input_root.resolve(), source_root.resolve(), output_root.resolve()
    _require(output_root.is_relative_to(source_root / 'build') and not output_root.is_relative_to(input_root)
             and not input_root.is_relative_to(output_root), 'output must be under source/build and separate from inputs')
    checkout = subprocess.run(['git', '-C', str(source_root), 'rev-parse', 'HEAD'], check=True, capture_output=True, text=True)
    _require(checkout.stdout.strip() == run['sha'], 'source checkout differs from original build commit')
    source_entry = subprocess.run(['git', '-C', str(source_root), 'show', run['sha'] + ':src/autolib.cmake'],
                                  check=True, capture_output=True).stdout
    published_entry = inject_release_root(source_entry, repository, tag)
    plan_name = f"ci-plan-{run['run_id']}-{run['attempt']}"
    names = [plan_name] + [f"ci-package-{run['run_id']}-{run['attempt']}-{p}" for p in platforms]
    _require(sorted(a['name'] for a in selection['artifacts']) == sorted(names), 'selection artifacts differ from planned platforms')
    _require(_json_file(input_root / plan_name / 'plan.json') == selection['plan']
             and _json_file(input_root / plan_name / 'run.json') == run, 'downloaded plan identity mismatch')
    output_root.mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix='publication-', dir=output_root))
    candidates, inputs = {}, []
    for platform in selection['plan']['platforms']:
        name = f"ci-package-{run['run_id']}-{run['attempt']}-{platform['id']}"
        root = work / ('input-' + platform['id'])
        transport = unpack_transport(input_root / name / 'transport.tar', root)
        packages = _validate_transport(root, transport, selection, platform, source_entry)
        for name in {p['name'] for p in packages}:
            pointer = root / 'candidates' / name / 'package.toml'
            _require(stat.S_ISREG(pointer.lstat().st_mode), f'{pointer}: candidate must be a regular file')
            candidate = tomllib.loads(pointer.read_text(encoding='utf-8'))
            _require(set(candidate) == {'latest'}, f'{pointer}: expected only latest')
            version = _component(candidate['latest'])
            _require(name not in candidates or candidates[name] == version, f'{name}: conflicting transported candidates')
            candidates[name] = version
        publishable = [p for p in packages if p['publish']]
        inputs.append((root, transport, publishable))
    assets, initial = [], {}
    for root, transport, libraries in inputs:
        for asset in transport['assets']:
            assets.append(dict(asset, path=(root / asset['path']).relative_to(work).as_posix()))
        initial.update({p['name']: candidates[p['name']] for p in libraries})
    entry = work / 'autolib.cmake'
    entry.write_bytes(published_entry)
    assets.append(dict(kind='entry', name='autolib.cmake', path=entry.name, size=len(published_entry),
                       sha256=hashlib.sha256(published_entry).hexdigest()))
    _require(len({a['name'].lower() for a in assets}) == len(assets), 'duplicate final asset names')
    manifest = work / 'publication.json'
    manifest.write_text(json.dumps(dict(source_run=run, assets=assets, initial_latest=initial,
                                        explicit_latest=explicit), indent=2) + '\n', encoding='utf-8')
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    select = commands.add_parser('select')
    select.add_argument('--output', type=Path, required=True)
    prepare = commands.add_parser('prepare')
    for option in ['selection', 'input-root', 'source-root', 'output-root']:
        prepare.add_argument('--' + option, type=Path, required=True)
    for option in ['repository', 'tag', 'source-repository']:
        prepare.add_argument('--' + option, required=True)
    args = parser.parse_args()
    try:
        if args.command == 'select':
            selected = select_source(os.environ)
            with args.output.open('x', encoding='utf-8') as output:
                json.dump(selected, output, indent=2)
                output.write('\n')
            print(args.output)
        else:
            print(prepare_publication(_json_file(args.selection), args.input_root, args.source_root,
                                      args.output_root, repository=args.repository,
                                      tag=args.tag, source_repository=args.source_repository))
    except (ValueError, KeyError, OSError, tarfile.TarError, zipfile.BadZipFile, subprocess.CalledProcessError) as error:
        parser.exit(1, f'publication preparation failed: {error}\n')


if __name__ == '__main__':
    main()
