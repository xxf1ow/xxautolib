"""专用 Gitee 仓库的单份 Git 副本、树校验及协调器元数据提交。"""

import json
import os
from pathlib import Path
import re
import stat
import subprocess
import tempfile
import tomllib
from urllib.parse import quote

from publication_rules import IMPORTERS, PLATFORMS, check_case, component, latest_mapping


IDENTITY = {'type': 'xxautolib-artifacts', 'schema': 1, 'upstream': 'https://github.com/xxf1ow/xxautolib'}
README = """# xxautolib 预编译产物发布仓库

本仓库发布和分发 [xxautolib](https://github.com/xxf1ow/xxautolib) 的预编译第三方库与 autolib-importer，供下游 CMake 工程直接获取和使用。

## 编译与发布

源码仓库维护构建配方，并通过原生构建流程编译、安装和验证第三方库。CI 生成库和工具归档，发布流程核对后原样上传，不重新编译或打包。本发布仓库不编译第三方源码。

Git 默认分支保存版本选择、独立 SHA256 摘要和仓库身份；入口、库与工具归档通过同仓库固定 Release 分发。源码、构建配置及使用说明见 [xxautolib](https://github.com/xxf1ow/xxautolib)。

## 使用

需要 CMake 3.21+、Git、原生构建工具及兼容预编译包的 C/C++ 编译器和系统运行库；以下 FLANN 示例还需 OpenMP。在工程 `src/` 中准备源文件，使用以下 `CMakeLists.txt`：

```cmake
cmake_minimum_required(VERSION 3.21.0)
set(CURRENT_TARGET_NAME autolib_example)
project(${CURRENT_TARGET_NAME} LANGUAGES C CXX)

add_executable(${CURRENT_TARGET_NAME})
aux_source_directory(${CMAKE_CURRENT_SOURCE_DIR}/src CURRENT_SRC_LIST)
target_sources(${CURRENT_TARGET_NAME} PRIVATE ${CURRENT_SRC_LIST})

message(STATUS "========================= Third Party Summary ========================")
set(AUTOLIB_URL "@AUTOLIB_URL@")
set(AUTOLIB_PATH "${CMAKE_CURRENT_LIST_DIR}/3rdparty/autolib.cmake")
if(NOT EXISTS ${AUTOLIB_PATH})
    file(DOWNLOAD ${AUTOLIB_URL} ${AUTOLIB_PATH} TLS_VERIFY ON STATUS AUTOLIB_DOWNLOAD_STATUS)
    list(GET AUTOLIB_DOWNLOAD_STATUS 0 AUTOLIB_DOWNLOAD_CODE)
    if(NOT AUTOLIB_DOWNLOAD_CODE EQUAL 0)
        file(REMOVE ${AUTOLIB_PATH})
        message(FATAL_ERROR "autolib.cmake download failed: ${AUTOLIB_DOWNLOAD_STATUS}")
    endif()
endif()
include(${AUTOLIB_PATH})
autolib_import(TARGET ${CURRENT_TARGET_NAME} PRIVATE queue flann)
message(STATUS "======================================================================")
```

入口仅在不存在时下载；失败会删除本次残留并报错。发布入口已内置本仓库的获取地址，不需要配置来源变量。仅请求 queue、flann，入口也会准备 flann 记录的精确 lz4 依赖，无需另写链接目标。

`autolib.cmake` 所在的 `3rdparty/` 是工具、包及下载缓存根，应专用、可写、路径不含分号，并加入版本控制忽略规则。已有有效工具和包直接复用；只有缺失时才下载，损坏或校验失败直接报错，不自动覆盖，也不回退到源码编译。消费现成包无需 Go 或 Python。

每次成功导入打印 `-- [Info] [<消费目标>] <库名> : <版本>`，包括传递依赖和缓存包；仅实际下载、解压打印过程，入口和工具获取静默。可用 `eigen@3.4.0` 指定精确版本；裸名读取 latest，缓存及 latest 不自动更新。

完整有效缓存可离线复用；远程入口缺输入时仍可能尝试联网。严格离线请使用本地构建安装的入口。完整运行示例、平台及 ABI 要求见[源码仓库 README](https://github.com/xxf1ow/xxautolib#快速开始)。

新建 Gitee 仓库默认为私有，发布流程不要求公开。匿名下游消费前，由仓库所有者手动将仓库改为公开；消费端不配置令牌。

## 许可证

根目录 LICENSE 是 xxautolib 项目自身内容的 MIT 许可证，不替代各第三方库随包提供的许可。使用第三方库时须遵守各自许可条款。
"""


def _git(arguments, *, path=None, auth=None):
    environment = {key: value for key, value in os.environ.items()
                   if key.upper() not in {'GITEE_TOKEN', 'GITHUB_TOKEN'}}
    environment['GIT_TERMINAL_PROMPT'] = '0'
    with tempfile.TemporaryDirectory(prefix='autolib-askpass-') as directory:
        if auth:
            askpass = Path(directory) / 'askpass'
            askpass.write_text('#!/bin/sh\ncase "$1" in\n*Username*) printf "%s\\n" "$AUTOLIB_GIT_USERNAME";;\n'
                               '*) printf "%s\\n" "$AUTOLIB_GIT_TOKEN";;\nesac\n', encoding='utf-8', newline='\n')
            askpass.chmod(0o700)
            environment.update(auth, GIT_ASKPASS=str(askpass))
        command = ['git', '-c', 'credential.helper=', '-c', 'core.autocrlf=false']
        if path is not None:
            command.extend(['-C', str(path)])
        result = subprocess.run(command + arguments, env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        # Remote diagnostics and hooks can echo credentials or URLs.
        raise RuntimeError('metadata git ' + arguments[0] + ' failed')
    return result.stdout


def _repository_url(config):
    repository = config['repository']
    if (config.get('host') != 'gitee' or not isinstance(repository, str)
            or not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository)
            or any(p in {'.', '..'} for p in repository.split('/'))):
        raise ValueError('invalid Gitee metadata repository')
    return 'https://gitee.com/' + repository + '.git'


def _kind(name):
    if name in {'README.md', 'LICENSE', '.autolib-repository.json'}:
        return 'identity'
    if name in {'autolib-importer/' + value + '.sha256' for value in IMPORTERS.values()}:
        return 'digest'
    parts = name.split('/')
    if len(parts) in {2, 3}:
        package = component(parts[0])
        latest_mapping({package: '1'})
        if len(parts) == 2 and parts[1] == 'package.toml':
            return 'latest'
        if len(parts) == 3:
            version = component(parts[1])
            if parts[2] in {package + '-' + version + '-' + platform + '.tar.zst.sha256'
                            for platform in PLATFORMS | {'any-any'}}:
                return 'digest'
    raise ValueError('unexpected metadata repository path')


def _tree(path):
    paths = []
    for record in _git(['ls-tree', '-r', '-z', 'HEAD'], path=path).split(b'\0')[:-1]:
        match = re.fullmatch(rb'(?:100644|100755) blob [0-9a-f]+\t([^\t]+)', record)
        if not match:
            raise ValueError('metadata tree requires regular blobs')
        try:
            paths.append(match[1].decode('ascii'))
        except UnicodeDecodeError:
            raise ValueError('metadata tree requires ASCII paths') from None
    check_case(paths)
    return paths


def _regular_target(root, name):
    target = root
    parts = name.split('/')
    for index, part in enumerate(parts):
        target /= part
        try:
            info = target.lstat()
        except FileNotFoundError:
            continue
        if (stat.S_ISLNK(info.st_mode) or getattr(info, 'st_file_attributes', 0) & stat.FILE_ATTRIBUTE_REPARSE_POINT
                or not (stat.S_ISREG(info.st_mode) if index == len(parts) - 1 else stat.S_ISDIR(info.st_mode))):
            raise ValueError('metadata target must be a regular file with ordinary directories')
    return target


def _validate(path, paths):
    kinds = {name: _kind(name) for name in paths}
    if '.autolib-repository.json' not in paths:
        raise ValueError('metadata repository identity is missing')
    for name in paths:
        _regular_target(path, name)
    try:
        identity = json.loads((path / '.autolib-repository.json').read_bytes())
        if (not isinstance(identity, dict) or type(identity.get('schema')) is not int
                or any(type(identity.get(key)) is not type(value) or identity[key] != value for key, value in IDENTITY.items())):
            raise ValueError('metadata repository identity mismatch')
        existing = {}
        for name, kind in kinds.items():
            if kind == 'latest':
                data = tomllib.loads((path / name).read_text(encoding='utf-8'))
                if set(data) != {'latest'}:
                    raise ValueError('package.toml must only contain latest')
                existing[name.split('/')[0]] = data['latest']
        latest_mapping(existing)
    except (UnicodeError, json.JSONDecodeError, tomllib.TOMLDecodeError):
        raise ValueError('invalid metadata identity or latest') from None
    return existing


def _push(repository, head):
    _git(['push', 'origin', head + ':refs/heads/' + repository['branch']],
         path=repository['path'], auth=repository['_auth'])


def open_repository(config: dict, api, work_root: Path) -> dict:
    """创建或克隆唯一工作副本，返回 path/branch/已确认 head/existing_latest。

    仅查询明确 404 才建库；新库初始化并确认 push 后返回。已有非法树在 Release 写入前失败，
    不认领或清理已有内容。工作目录已存在、API/Git 错误或非法元数据均失败。
    """
    url = _repository_url(config)
    path = Path(work_root) / 'repository'
    if path.exists() or path.is_symlink():
        raise RuntimeError('metadata work copy already exists')
    info = api.repository_info()
    created = info is None
    if created:
        api.create_repository()
        info = api.repository_info()
    if (not isinstance(info, dict) or not isinstance(info.get('default_branch'), str)
            or not isinstance(info.get('permission'), dict) or info['permission'].get('push') is not True):
        raise ValueError('repository requires a default branch and permission.push')
    branch = info['default_branch']
    _git(['check-ref-format', 'refs/heads/' + branch])
    login = info.get('login')
    if not isinstance(login, str) or not re.fullmatch(r'[A-Za-z0-9_.-]+', login):
        raise ValueError('repository requires the authenticated Git username')
    auth = {'AUTOLIB_GIT_USERNAME': login, 'AUTOLIB_GIT_TOKEN': config['gitee_token']}
    path.parent.mkdir(parents=True, exist_ok=True)
    _git(['clone', '--single-branch', '--branch', branch, '--', url, str(path)], auth=auth)
    _git(['config', 'user.name', 'xxautolib publisher'], path=path)
    _git(['config', 'user.email', 'xxautolib-publisher@users.noreply.github.com'], path=path)
    paths = _tree(path)
    if created:
        allowed = {'README.md', 'README.en.md', 'LICENSE', '.autolib-repository.json'}
        if set(paths) - allowed:
            raise ValueError('unexpected service initialization tree')
        for name in allowed:
            _regular_target(path, name)
        autolib_url = ('https://gitee.com/' + config['repository'] + '/releases/download/'
                       + quote(config['tag'], safe='-._~$&+:=@') + '/autolib.cmake')
        (path / 'README.md').write_bytes(README.replace('@AUTOLIB_URL@', autolib_url).encode())
        (path / 'LICENSE').write_bytes((Path(__file__).resolve().parents[3] / 'LICENSE').read_bytes())
        (path / '.autolib-repository.json').write_bytes((json.dumps(IDENTITY, separators=(',', ':')) + '\n').encode())
        selected = ['README.md', 'LICENSE', '.autolib-repository.json']
        if (path / 'README.en.md').exists():
            (path / 'README.en.md').unlink()
            selected.append('README.en.md')
        _git(['add', '--', *selected], path=path)
        _git(['commit', '-m', 'Initialize xxautolib artifact repository', '--', *selected], path=path)
        paths = _tree(path)
    existing = _validate(path, paths)
    head = _git(['rev-parse', 'HEAD'], path=path).decode().strip()
    repository = {'path': path, 'branch': branch, 'head': head, 'existing_latest': existing, '_auth': auth, '_paths': paths}
    if created:
        _push(repository, head)
    return repository


def commit_metadata(repository: dict, digests: dict[str, str], latest: dict[str, str]) -> str:
    """一次提交并 push 选中摘要和 latest，确认后返回 SHA；未选择文件保持原样。

    无变化返回已确认 SHA。非法目标在写文件前失败；push 未确认则抛错并保留旧 confirmed head，
    不回滚附件或伪报元数据已发布。调用者不得在失败后继续复用本次副本发布。
    """
    path, updates = repository['path'], {}
    for name, digest in digests.items():
        if _kind(name) != 'digest' or not isinstance(digest, str) or not re.fullmatch(r'[0-9a-f]{64}', digest):
            raise ValueError('invalid selected metadata digest')
        updates[name] = (digest + '\n').encode()
    latest_mapping(latest)
    for name, version in latest.items():
        target = name + '/package.toml'
        if _kind(target) != 'latest':
            raise ValueError('invalid selected latest path')
        updates[target] = ('latest = "' + version + '"\n').encode()
    check_case(repository['_paths'] + list(updates))
    targets = {name: _regular_target(path, name) for name in updates}
    changed = []
    for name, content in updates.items():
        target = targets[name]
        if target.exists() and target.read_bytes() == content:
            continue
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(content)
        changed.append(name)
    if not changed:
        return repository['head']
    _git(['add', '--', *changed], path=path)
    _git(['commit', '-m', 'Update artifact metadata', '--', *changed], path=path)
    head = _git(['rev-parse', 'HEAD'], path=path).decode().strip()
    _push(repository, head)
    repository.update(head=head, existing_latest={**repository['existing_latest'], **latest},
                      _paths=list(set(repository['_paths']) | set(updates)))
    return head
