"""校验明确发布输入和元数据身份；不发现资产、不查询远端。"""

from urllib.parse import quote
import unicodedata
import re


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
