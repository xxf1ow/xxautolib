"""按完整源码事实清理明确归属的库身份；未知内容保持原样。"""

from collections import defaultdict

from metadata_repository import commit_metadata
from publication_rules import PLATFORMS, choose_source_latest, component, metadata_path


def _asset(key):
    package = {field: key[field] for field in ('name', 'version', 'os', 'arch')}
    asset = {'kind': 'library', 'package': package,
             'name': key['name'] + '-' + key['version'] + '-' + key['os'] + '-' + key['arch'] + '.tar.zst'}
    metadata_path(asset)
    return asset


def _absent(packages, key):
    package = packages.get(key['name'])
    if package is None:
        return True
    versions = package['versions']
    if versions is None:
        return False
    version = versions.get(key['version'])
    if version is None:
        return True
    platforms = version['platforms']
    return platforms is not None and key['os'] + '-' + key['arch'] not in platforms


def plan_cleanup(source: dict, cache: dict, repository: dict, assets: list[dict]) -> dict:
    """仅完整快照可产生删除计划；声明未知时保护该项，身份歧义逐项报告。"""
    packages = source.get('catalog', {}).get('packages')
    if (source.get('schema') != 1 or source.get('repository') != cache.get('repository')
            or not isinstance(packages, dict) or not isinstance(assets, list)
            or not isinstance(cache.get('assets'), list) or not isinstance(cache.get('records'), dict)):
        raise ValueError('cleanup requires complete matching source and asset snapshots')
    keys, by_name = {}, defaultdict(set)
    def add(key):
        if key.get('kind') != 'library':
            return
        asset = _asset(key)
        identity = tuple(key[field] for field in ('name', 'version', 'os', 'arch'))
        keys[identity] = dict(key)
        by_name[asset['name']].add(identity)
    for name, package in packages.items():
        component(name)
        versions = package['versions']
        if versions is None:
            if not package['error']:
                raise ValueError('unknown source versions require an error')
            continue
        if not isinstance(versions, dict):
            raise ValueError('invalid source versions')
        for version, declaration in versions.items():
            component(version)
            platforms = declaration['platforms']
            if platforms is not None and (not isinstance(platforms, list) or not platforms
                                          or set(platforms) - (PLATFORMS | {'any-any'})):
                raise ValueError('invalid source platforms')
            for platform in PLATFORMS | {'any-any'}:
                os_name, arch = platform.split('-')
                add(dict(kind='library', name=name, version=version, os=os_name, arch=arch))
    for record in cache['records'].values():
        add(record['key'])
    for path in repository['existing_records']:
        parts = path.split('/')
        if len(parts) != 3:
            continue
        for platform in PLATFORMS | {'any-any'}:
            os_name, arch = platform.split('-')
            key = dict(kind='library', name=parts[0], version=parts[1], os=os_name, arch=arch)
            if metadata_path(_asset(key)) == path:
                add(key)
    gitee, github = defaultdict(list), defaultdict(list)
    for item in assets:
        if not isinstance(item, dict) or not isinstance(item.get('name'), str):
            raise ValueError('invalid Gitee asset snapshot')
        gitee[item['name']].append(item)
    for item in cache['assets']:
        github[item['name']].append(item)
    groups, errors = {}, {}
    for identity, key in sorted(keys.items()):
        if not _absent(packages, key):
            continue
        asset = _asset(key)
        name, path = asset['name'], metadata_path(asset)
        matches = gitee[name]
        cached = github[name]
        cache_record = name in cache['records']
        if not matches and not cached and not cache_record and path not in repository['existing_records']:
            continue
        if (len(by_name[name]) != 1 or len(matches) > 1
                or len(github[name]) > 1
                or any(type(a.get('id')) is not int or a['id'] <= 0 for a in matches)):
            errors[name] = 'ambiguous managed deletion identity'
            continue
        group = groups.setdefault(key['name'], {'items': [], 'latest': {}, 'remove_latest': False})
        group['items'].append({'key': key, 'name': name, 'github': cached, 'cache_record': cache_record, 'gitee': matches,
                               'metadata_paths': [path] if path in repository['existing_records'] else []})
    updates, removed, latest_errors = choose_source_latest(repository['existing_latest'], {}, source, set(), {})
    errors.update({name + '/package.toml': error for name, error in latest_errors.items()})
    for name in updates.keys() | removed:
        group = groups.setdefault(name, {'items': [], 'latest': {}, 'remove_latest': False})
        group['latest'] = {name: updates[name]} if name in updates else {}
        group['remove_latest'] = name in removed
    return {'groups': groups, 'errors': errors}


def execute_cleanup(plan: dict, *, delete_cache, api, release_id: int | None,
                    repository: dict, git_lock) -> dict:
    """逐身份删除两端；已确认的 Gitee 删除及同包指针修复一次 push，失败不回滚。"""
    result = {'deleted': [], 'errors': dict(plan['errors'])}
    for name, group in sorted(plan['groups'].items()):
        removed = []
        for item in group['items']:
            cache_ok, gitee_ok = True, True
            try:
                for asset in item['gitee']:
                    if release_id is None:
                        raise ValueError('missing Gitee release for deletion')
                    api.delete_asset(release_id, asset['id'])
            except (OSError, RuntimeError, ValueError):
                gitee_ok = False
                result['errors'][item['name'] + ':gitee'] = 'Gitee deletion unconfirmed'
            # 无摘要的残留可能仅由缓存记录证明归属，Gitee 确认前须保留该证据。
            if gitee_ok and (item['github'] or item['cache_record']):
                try:
                    delete_cache(item['key'])
                except (OSError, RuntimeError, ValueError):
                    cache_ok = False
                    result['errors'][item['name'] + ':github'] = 'cache deletion unconfirmed'
            if gitee_ok:
                removed.extend(item['metadata_paths'])
            if cache_ok and gitee_ok:
                result['deleted'].append(item['name'])
        if group['remove_latest']:
            removed.append(name + '/package.toml')
        if removed or group['latest']:
            with git_lock:
                commit_metadata(repository, {}, group['latest'], remove_paths=tuple(removed))
    return result
