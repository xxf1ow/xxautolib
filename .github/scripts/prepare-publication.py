"""Validate exact CI inputs and ask their builder to produce Gitee assets."""

import argparse
from collections.abc import Mapping
import hashlib
import io
import json
import ntpath
import os
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess
import tarfile
import tempfile
import tomllib
import urllib.request
import zipfile


REPOSITORY = 'xxf1ow/xxautolib'
PLATFORMS = {'windows-x86_64', 'linux-x86_64', 'linux-aarch64'}


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


def _identity(value):
    _require(isinstance(value, dict) and set(value) == {
        'repository', 'ref', 'sha', 'checkout_sha', 'run_id', 'attempt'}, 'invalid run identity fields')
    _require(all(isinstance(v, str) for v in value.values()), 'run identity must contain strings')
    _require(value['repository'] == REPOSITORY and re.fullmatch(r'[0-9a-f]{40}', value['sha'])
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
    _require(environment.get('GITHUB_REPOSITORY') == REPOSITORY, 'publication requires the source repository')
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
    base = environment.get('GITHUB_API_URL', 'https://api.github.com').rstrip('/') + '/repos/' + REPOSITORY
    run = json.loads(_request(f'{base}/actions/runs/{run_id}/attempts/{attempt}', token))
    _require(run['id'] == int(run_id) and run['run_attempt'] == int(attempt)
             and run['status'] == 'completed' and run['conclusion'] == 'success', 'source attempt is not successful')
    _require(run['repository']['full_name'] == REPOSITORY and run['head_repository']['full_name'] == REPOSITORY
             and run['repository']['id'] == run['head_repository']['id'], 'source repository mismatch')
    _require(run['path'] == '.github/workflows/build-platform.yml' and run['event'] == 'workflow_dispatch', 'unexpected source workflow or event')
    identity = _identity(dict(repository=REPOSITORY, ref='refs/heads/' + run['head_branch'], sha=run['head_sha'],
                              checkout_sha=run['head_sha'], run_id=run_id, attempt=attempt))
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
        _require(len(matches) == 1 and matches[0].get('expired') is False, f'{name}: missing, duplicate or expired artifact')
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
    selected = [plan_artifact] + [exact(f'ci-install-{run_id}-{attempt}-{p}') for p in _planned_platforms(plan)]
    _require(len({a['id'] for a in selected}) == len(selected), 'duplicate selected artifact id')
    packages = [p.strip() for p in environment.get('PACKAGES', '').split(',') if p.strip()]
    return dict(source_run=identity, plan=plan, artifacts=selected, packages=packages,
                explicit_latest=_latest(json.loads(environment.get('LATEST') or '{}')))


def unpack_transport(archive: Path, destination: Path) -> dict:
    """Extract into a new directory using data_filter and explicit object checks.

    Existing destinations are never changed. Failure can leave partial data in
    the newly created directory. Hard links, duplicate members and writes through
    symbolic links are rejected; legal relative symlinks and execute bits survive.
    """
    destination.mkdir(parents=False, exist_ok=False)
    seen, links = set(), set()
    with tarfile.open(archive, 'r:*') as source:
        for member in source:
            name = member.name.rstrip('/')
            parts = name.split('/')
            _require(name and not name.startswith('/') and '\\' not in name and ':' not in name
                     and all(p not in {'', '.', '..'} for p in parts), f'{member.name}: unsafe transport path')
            _require(name not in seen and not any('/'.join(parts[:n]) in links for n in range(1, len(parts))), f'{name}: duplicate member or write through link')
            _require(member.isdir() or member.isreg() or member.issym(), f'{name}: unsupported transport object')
            if member.issym():
                link = member.linkname
                _require(link and not PurePosixPath(link).is_absolute() and '\\' not in link and ':' not in link,
                         f'{name}: transport links must be relative')
                target = (destination / name).parent.joinpath(link).resolve()
                _require(target.is_relative_to(destination.resolve()), f'{name}: transport link escapes destination')
                _require(not any(s.startswith(name + '/') for s in seen), f'{name}: link replaces extracted parent')
                links.add(name)
            seen.add(name)
            source.extract(member, destination, filter=tarfile.data_filter)
    return _json_file(destination / 'transport.json')


def _tool(platform):
    return 'autolib-importer-' + platform + ('.exe' if platform == 'windows-x86_64' else '')


def _validate_transport(root, transport, selected, platform):
    plan, run = selected['plan'], selected['source_run']
    _require(transport['run'] == run and transport['platform'] == platform['id'], 'transport run or platform mismatch')
    _require(_json_file(root / 'plan.json') == plan, 'transport plan mismatch')
    prefix = transport['producer_prefix']
    _require(isinstance(prefix, str) and prefix and '\x00' not in prefix, 'missing producer prefix')
    if platform['id'].startswith('windows'):
        drive, tail = ntpath.splitdrive(prefix)
        _require(bool(drive) and tail.startswith(('\\', '/')), 'Windows producer prefix must be absolute')
    else:
        _require(prefix.startswith('/'), 'Linux producer prefix must be absolute')
    importer = 'install/' + _tool(platform['id'])
    _require(transport['importer'] == importer and stat.S_ISREG((root / importer).lstat().st_mode), 'invalid transported importer')
    result = _json_file(root / 'result.json')
    _require(result['run'] == run and result['platform'] == platform['id'] and result['complete'] is True
             and result['error'] == '', 'incomplete or mismatched platform result')
    _require(result['importer'] == dict(file=_tool(platform['id']), verified=True), 'unverified importer')
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
    _require(sorted(normalized, key=lambda p: (p['name'], p['version'])) == sorted(expected, key=lambda p: (p['name'], p['version'])), 'transport packages differ from plan')
    results = [dict(slot={k: p[k] for k in ['name', 'version', 'os', 'arch']}, built=True, verified=True) for p in expected]
    _require(sorted(result['packages'], key=lambda p: (p['slot']['name'], p['slot']['version'])) == sorted(results, key=lambda p: (p['slot']['name'], p['slot']['version'])), 'unverified exact package result')
    roots = [r['root'] for r in result['roots']]
    _require(len(roots) == len(set(roots)) and sorted(roots) == sorted(platform['roots'])
             and all(r['verified'] is True for r in result['roots']), 'unverified independent root result')
    return expected


def prepare_publication(selection: dict, input_root: Path, source_root: Path,
                        builder_file: Path, output_root: Path) -> Path:
    """Repack validated snapshots in a fresh output directory and return its manifest.

    input_root contains one directory per selected artifact; plan.json/run.json
    and transport.tar retain their uploaded names. output_root must be inside
    source_root/build and outside input_root. Inputs remain read-only; repeated
    calls rebuild all selected assets and hash their actual new bytes.
    """
    run = _identity(selection['source_run'])
    platforms = _planned_platforms(selection['plan'])
    explicit = _latest(selection['explicit_latest'])
    input_root, source_root, output_root = input_root.resolve(), source_root.resolve(), output_root.resolve()
    _require(output_root.is_relative_to(source_root / 'build') and not output_root.is_relative_to(input_root)
             and not input_root.is_relative_to(output_root), 'output must be under source/build and separate from inputs')
    checkout = subprocess.run(['git', '-C', str(source_root), 'rev-parse', 'HEAD'], check=True, capture_output=True, text=True)
    _require(checkout.stdout.strip() == run['sha'], 'source checkout differs from original build commit')
    plan_name = f"ci-plan-{run['run_id']}-{run['attempt']}"
    names = [plan_name] + [f"ci-install-{run['run_id']}-{run['attempt']}-{p}" for p in platforms]
    _require(sorted(a['name'] for a in selection['artifacts']) == sorted(names), 'selection artifacts differ from planned platforms')
    _require(_json_file(input_root / plan_name / 'plan.json') == selection['plan']
             and _json_file(input_root / plan_name / 'run.json') == run, 'downloaded plan identity mismatch')
    output_root.mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix='publication-', dir=output_root))
    candidates, inputs, available = {}, [], set()
    for platform in selection['plan']['platforms']:
        name = f"ci-install-{run['run_id']}-{run['attempt']}-{platform['id']}"
        root = work / ('input-' + platform['id'])
        transport = unpack_transport(input_root / name / 'transport.tar', root)
        packages = _validate_transport(root, transport, selection, platform)
        for name in {p['name'] for p in packages}:
            pointer = root / 'install' / name / 'package.toml'
            _require(stat.S_ISREG(pointer.lstat().st_mode), f'{pointer}: candidate must be a regular file')
            candidate = tomllib.loads(pointer.read_text(encoding='utf-8'))
            _require(set(candidate) == {'latest'}, f'{pointer}: expected only latest')
            version = _component(candidate['latest'])
            _require(name not in candidates or candidates[name] == version, f'{name}: conflicting transported candidates')
            candidates[name] = version
        publishable = [p for p in packages if p['publish']]
        available.update(p['name'] + '@' + p['version'] for p in publishable)
        if platform['roots']:
            available.add('autolib-importer')
        inputs.append((platform, root, transport, publishable))
    requested = selection['packages']
    _require(isinstance(requested, list) and all(isinstance(p, str) and p in available for p in requested), 'unmatched exact package or importer selection')
    chosen = set(requested) if requested else available
    assets, initial = [], {}
    for platform, root, transport, packages in inputs:
        libraries = [p for p in packages if p['name'] + '@' + p['version'] in chosen]
        importer = 'autolib-importer' in chosen and bool(platform['roots'])
        if not libraries and not importer:
            continue
        system, arch = platform['id'].split('-', 1)
        output = work / ('assets-' + platform['id'])
        command = [str(builder_file), 'package', '--source-root=' + str(source_root),
                   '--install-root=' + str(root / 'install'), '--producer-prefix=' + transport['producer_prefix'],
                   '--output-root=' + str(output), '--os=' + system, '--arch=' + arch,
                   '--host-os=' + system, '--host-arch=' + arch,
                   '--importer-file=' + str(root / transport['importer']), '--cmake-command=cmake']
        command.extend('--module=' + p['name'] + '@' + p['version'] for p in libraries)
        subprocess.run(command, check=True)
        descriptions = [dict(kind='library', name=f"{p['name']}-{p['version']}-{p['os']}-{p['arch']}.tar.zst",
                             package={k: p[k] for k in ['name', 'version', 'os', 'arch']}) for p in libraries]
        if importer:
            descriptions.append(dict(kind='importer', name=_tool(platform['id']) + '.tar.xz', platform=platform['id']))
        for asset in descriptions:
            path = output / asset['name']
            _require(stat.S_ISREG(path.lstat().st_mode) and path.resolve().is_relative_to(work), 'builder asset must be an internal regular file')
            with path.open('rb') as file:
                digest = hashlib.file_digest(file, 'sha256').hexdigest()
            asset.update(path=path.relative_to(work).as_posix(), size=path.stat().st_size, sha256=digest)
            assets.append(asset)
        initial.update({p['name']: candidates[p['name']] for p in libraries})
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
    for option in ['selection', 'input-root', 'source-root', 'builder-file', 'output-root']:
        prepare.add_argument('--' + option, type=Path, required=True)
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
                                      args.builder_file, args.output_root))
    except (ValueError, KeyError, OSError, tarfile.TarError, zipfile.BadZipFile, subprocess.CalledProcessError) as error:
        parser.exit(1, f'publication preparation failed: {error}\n')


if __name__ == '__main__':
    main()
