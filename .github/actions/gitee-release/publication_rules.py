"""校验明确发布输入和元数据身份；不发现资产、不查询远端。"""

import hashlib
import json
from pathlib import Path, PurePosixPath
from urllib.parse import quote
import unicodedata
import re
import stat


PLATFORMS = {'windows-x86_64', 'linux-x86_64', 'linux-aarch64', 'darwin-aarch64'}
IMPORTERS = {platform: 'autolib-importer-' + platform + ('.exe' if platform.startswith('windows-') else '') + '.tar.xz'
             for platform in PLATFORMS}


def component(value):
    """返回跨平台安全的包名或版本；非法值抛 ValueError，不改写身份。"""
    if (not isinstance(value, str) or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.+\-]*', value)
            or value.endswith('.') or value.lower() == 'package.toml'
            or re.fullmatch(r'CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9]', value.split('.')[0].upper())):
        raise ValueError('invalid package name or version')
    return value


def check_case(paths):
    """拒绝文件或祖先目录仅大小写不同的身份。"""
    seen = {}
    for path in paths:
        parts = path.split('/')
        for count in range(1, len(parts) + 1):
            prefix = '/'.join(parts[:count])
            if seen.setdefault(prefix.lower(), prefix) != prefix:
                raise ValueError('case-colliding metadata identity')


def latest_mapping(value):
    """校验库包选择映射；工具目录不得具有 latest。"""
    if not isinstance(value, dict):
        raise ValueError('latest must be an object')
    for name, version in value.items():
        component(name)
        component(version)
        if name.lower() == 'autolib-importer':
            raise ValueError('importer does not have latest')
    check_case(value)
    return value


def metadata_path(asset: dict) -> str | None:
    """按明确身份返回摘要路径；入口返回 None，非法身份抛 ValueError。"""
    if not isinstance(asset, dict):
        raise ValueError('asset must be an object')
    if asset.get('kind') == 'entry':
        if asset.get('name') != 'autolib.cmake' or 'package' in asset or 'platform' in asset:
            raise ValueError('invalid entry asset identity')
        return None
    if asset.get('kind') == 'importer':
        expected = IMPORTERS.get(asset.get('platform'))
        if not expected or asset.get('name') != expected:
            raise ValueError('invalid importer asset identity')
        return 'autolib-importer/' + expected + '.sha256'
    package = asset.get('package')
    if asset.get('kind') != 'library' or not isinstance(package, dict) or set(package) != {'name', 'version', 'os', 'arch'}:
        raise ValueError('invalid library asset identity')
    name, version = component(package['name']), component(package['version'])
    latest_mapping({name: version})
    platform = str(package['os']) + '-' + str(package['arch'])
    expected = name + '-' + version + '-' + platform + '.tar.zst'
    if platform not in PLATFORMS | {'any-any'} or asset.get('name') != expected:
        raise ValueError('library asset name does not match identity')
    return name + '/' + version + '/' + expected + '.sha256'


def choose_latest(existing: dict[str, str], candidates: dict[str, str], succeeded: set[str],
                  explicit: dict[str, str]) -> dict[str, str]:
    """保留已有值，仅成功库包初始化缺失值，显式映射优先且不依赖资产。

    返回完整选择映射，不改变输入；任一非法映射都失败，不能用显式值修复坏输入。
    """
    for mapping in (existing, candidates, explicit):
        latest_mapping(mapping)
    check_case([*existing, *candidates, *explicit])
    result = dict(existing)
    for name in succeeded:
        if name in candidates and name not in result:
            result[name] = candidates[name]
    result.update(explicit)
    return result


def choose_source_latest(existing: dict, candidates: dict, source: dict, succeeded: set[str],
                         explicit: dict) -> tuple[dict, set[str], dict]:
    """返回源码合法的指针更新、已悬空指针删除及逐包错误；未知集合保留。"""
    latest_mapping(existing)
    packages = source['catalog']['packages']
    updates, removed, errors = {}, set(), {}
    check_case([*existing, *candidates, *explicit, *packages])
    for name in sorted(set(existing) | set(candidates) | set(explicit)):
        package = packages.get(name)
        if package is None:
            if name in existing:
                removed.add(name)
            if name in explicit:
                errors[name] = 'explicit latest package is absent from source'
            continue
        versions = package['versions']
        if versions is None:
            errors[name] = package['error'] or 'source versions are unknown'
            continue
        def valid(version):
            item = versions.get(version)
            return item is not None and not item['error'] and item['platforms'] is not None
        old = existing.get(name)
        dangling = old is not None and old not in versions
        if dangling:
            removed.add(name)
        try:
            if name in explicit:
                latest_mapping({name: explicit[name]})
                selected = explicit[name]
            elif old is not None and not dangling:
                continue
            elif dangling:
                selected = package['latest']
            elif name in succeeded and name in candidates:
                selected = candidates[name]
                if selected not in versions:
                    selected = package['latest']
            else:
                continue
            if not valid(selected):
                raise ValueError(package['error'] or 'latest does not select a valid source version')
            if selected != old:
                updates[name] = selected
            removed.discard(name)
        except (ValueError, TypeError):
            errors[name] = 'latest does not select a valid source version'
    return updates, removed, errors


def _object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError('duplicate publication key')
        value[key] = item
    return value


def _source_run(run, repository):
    fields = {'repository', 'ref', 'sha', 'checkout_sha', 'run_id', 'attempt'}
    if (not isinstance(run, dict) or set(run) != fields or any(not isinstance(v, str) for v in run.values())
            or not isinstance(repository, str)
            or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*', repository)
            or run['repository'] != repository or not re.fullmatch(r'[0-9a-f]{40}', run['sha'])
            or run['checkout_sha'] != run['sha']
            or any(not re.fullmatch(r'[1-9][0-9]*', run[key]) for key in ('run_id', 'attempt'))):
        raise ValueError('invalid publication source run')
    ref = run['ref']
    branch = ref.removeprefix('refs/heads/')
    if (not ref.startswith('refs/heads/') or not branch or branch.startswith(('-', 'refs/', '/'))
            or branch.endswith(('/', '.')) or '..' in branch or '@{' in branch
            or re.search(r'[\s~^:?*\[\\\x00-\x1f\x7f]', branch)
            or any(not part or part.startswith('.') or part.endswith('.lock') for part in branch.split('/'))):
        raise ValueError('publication source must be an ordinary branch')


def _local_file(root, value):
    if (not isinstance(value, str) or not value or '\\' in value or ':' in value
            or PurePosixPath(value).is_absolute() or any(p in {'', '.', '..'} for p in value.split('/'))):
        raise ValueError('publication asset path must be a safe relative path')
    target = root
    for part in value.split('/'):
        target /= part
        if target.is_symlink() or target.is_junction():
            raise ValueError('publication asset path cannot contain links')
    if not stat.S_ISREG(target.lstat().st_mode) or not target.resolve().is_relative_to(root):
        raise ValueError('publication asset must be a contained regular file')
    return target


def load_publication(path: Path, *, source_repository: str) -> dict:
    """读取清单，核对身份、安全文件路径、尺寸和 SHA256，再返回绝对资产路径。

    不写文件；输入缺失、重复、非法或与实际字节不符均抛 ValueError/OSError。
    source_repository 必须来自可信运行上下文，不得从清单自身取得。
    """
    path = Path(path)
    if not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError('publication manifest must be a regular file')
    document = json.loads(path.read_bytes(), object_pairs_hook=_object)
    if not isinstance(document, dict) or set(document) != {'source_run', 'assets', 'initial_latest', 'explicit_latest'}:
        raise ValueError('invalid publication fields')
    _source_run(document['source_run'], source_repository)
    latest_mapping(document['initial_latest'])
    latest_mapping(document['explicit_latest'])
    assets = document['assets']
    if not isinstance(assets, list):
        raise ValueError('publication assets must be a list')
    root = path.resolve().parent
    names, paths, packages, metadata = set(), set(), set(), []
    for asset in assets:
        key = metadata_path(asset)
        fields = {'kind', 'name', 'path', 'size', 'sha256'}
        if asset['kind'] != 'entry':
            fields.add('package' if asset['kind'] == 'library' else 'platform')
        if (set(asset) != fields or type(asset.get('size')) is not int or asset['size'] < 0
                or not isinstance(asset.get('sha256'), str) or not re.fullmatch(r'[0-9a-f]{64}', asset['sha256'])
                or asset['name'] in names or asset.get('path') in paths):
            raise ValueError('invalid or duplicate publication asset')
        names.add(asset['name'])
        paths.add(asset['path'])
        if key is not None:
            metadata.append(key)
        if asset['kind'] == 'library':
            packages.add(asset['package']['name'])
        file = _local_file(root, asset['path'])
        with file.open('rb') as stream:
            digest = hashlib.file_digest(stream, 'sha256').hexdigest()
        if file.stat().st_size != asset['size'] or digest != asset['sha256']:
            raise ValueError('publication asset size or digest mismatch')
        asset['path'] = str(file)
    check_case(metadata + [name + '/package.toml' for name in document['initial_latest']]
               + [name + '/package.toml' for name in document['explicit_latest']])
    if set(document['initial_latest']) != packages:
        raise ValueError('initial latest must match selected library packages')
    return document


def inject_release_root(source: bytes, repository: str, tag: str) -> bytes:
    """在上传副本中注入目标；除唯一空来源赋值外，原始字节保持不变。"""
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*', repository):
        raise ValueError('invalid entry repository')
    if not isinstance(tag, str) or not tag or any(c.isspace() or unicodedata.category(c) == 'Cc' for c in tag):
        raise ValueError('invalid entry tag')
    empty = b'set(_autolib_release_root "")'
    assignments = re.findall(rb'(?m)^[ \t]*set[ \t]*\([ \t]*_autolib_release_root\b[^\r\n]*', source)
    if assignments != [empty] or not re.search(rb'(?m)^' + re.escape(empty) + rb'(?=\n|\Z)', source):
        raise ValueError('entry requires exactly one complete empty release-root assignment')
    root = 'https://gitee.com/' + repository + '/releases/download/' + quote(tag, safe='-._~$&+:=@')
    return re.sub(rb'(?m)^' + re.escape(empty) + rb'(?=\n|\Z)',
                  ('set(_autolib_release_root "' + root + '")').encode('ascii'), source, count=1)
