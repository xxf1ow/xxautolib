"""读取纯数据模块声明；执行与 CMake 生成由其他文件负责。"""

from dataclasses import dataclass, field
from pathlib import Path, PureWindowsPath
import re
import tomllib


@dataclass
class PatchDefinition:
    file: Path
    resource: str | None = None


RESOURCE_REFERENCE = re.compile(r"<RESOURCE_SOURCE_DIR:([^>]*)>")


@dataclass
class ModuleDefinition:
    path: Path
    name: str
    kind: str
    version: str | None
    source: dict[str, str | list[str]] | None
    resources: dict[str, dict[str, str | list[str]]]
    cache: dict[str, object]
    patches: list[PatchDefinition]
    steps: list[dict[str, object]]
    copies: list[dict[str, object]]
    sentinel: str | None = None
    source_subdir: str | None = None
    depends: list[str] = field(default_factory=list)
    config_dir: str | None = None
    pkgconfig_dir: str | None = None
    package_name: str | None = None


def _table(value, allowed, where):
    if not isinstance(value, dict):
        raise ValueError(f"{where}: expected a table")
    unknown = value.keys() - allowed
    if unknown:
        raise ValueError(f"{where}: unknown field {sorted(unknown)[0]}")
    return value


def _text(value, where):
    if not isinstance(value, str) or not value:
        raise ValueError(f"{where}: expected a nonempty string")
    return value


def _strings(value, where):
    if not isinstance(value, list) or not all(isinstance(v, str) for v in value):
        raise ValueError(f"{where}: expected an array of strings")
    return value


def _name(value, where):
    value = _text(value, where)
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.+-]*", value):
        raise ValueError(f"{where}: invalid target name {value!r}")
    return value


def _source(value: object, where: str) -> dict[str, str | list[str]]:
    git = {"GIT_REPOSITORY", "GIT_TAG"}
    archive = {"URL", "URL_HASH", "DOWNLOAD_NAME"}
    _table(value, git | archive, where)
    if value.keys() & git and value.keys() & archive:
        raise ValueError(f"{where}: Git and URL archive fields are mutually exclusive")
    if not value.keys() & archive:
        return {key: _text(value.get(key), f"{where}.{key}")
                for key in ("GIT_REPOSITORY", "GIT_TAG")}
    urls = _strings(value.get("URL"), f"{where}.URL")
    if not urls or any(not url for url in urls):
        raise ValueError(f"{where}.URL: expected a nonempty array of nonempty strings")
    digest = _text(value.get("URL_HASH"), f"{where}.URL_HASH")
    if not re.fullmatch(r"SHA256=[0-9a-fA-F]{64}", digest):
        raise ValueError(f"{where}.URL_HASH: expected SHA256=<64 hexadecimal digits>")
    result = {"URL": urls, "URL_HASH": digest}
    if "DOWNLOAD_NAME" in value:
        name = _text(value["DOWNLOAD_NAME"], f"{where}.DOWNLOAD_NAME")
        if name in (".", "..") or "/" in name or "\\" in name or PureWindowsPath(name).drive:
            raise ValueError(f"{where}.DOWNLOAD_NAME: expected a single filename")
        result["DOWNLOAD_NAME"] = name
    return result


def _cache(value, where, source_subdir=False):
    _table(value, {"cache", "SOURCE_SUBDIR"} if source_subdir else {"cache"}, where)
    cache = value.get("cache", {})
    if not isinstance(cache, dict):
        raise ValueError(f"{where}.cache: expected a table")
    for key, item in cache.items():
        if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
            raise ValueError(f"{where}.cache.{key}: invalid cache key")
        if not (isinstance(item, (str, bool, int)) or
                isinstance(item, list) and all(isinstance(v, str) for v in item)):
            raise ValueError(f"{where}.cache.{key}: expected string, bool, integer or string array")
    return cache


def _input_path(value, directory):
    # ExternalProject 占位符必须由其执行层替换。
    return value if "<" in value else (directory / value).resolve().as_posix()


def _steps(value, directory, where):
    if not isinstance(value, list):
        raise ValueError(f"{where}: expected an array of tables")
    result = []
    for index, step in enumerate(value):
        at = f"{where}[{index}]"
        _table(step, {"name", "DEPENDEES", "DEPENDERS", "COMMAND", "DEPENDS",
                      "WORKING_DIRECTORY"}, at)
        step = dict(step)
        step["name"] = _name(step.get("name"), f"{at}.name")
        command = step.get("COMMAND")
        if not isinstance(command, list) or not command:
            raise ValueError(f"{at}.COMMAND: expected argv or an array of argv arrays")
        commands = [command] if isinstance(command[0], str) else command
        for argv in commands:
            _strings(argv, f"{at}.COMMAND")
            if not argv or not argv[0]:
                raise ValueError(f"{at}.COMMAND: command executable cannot be empty")
        for key in ("DEPENDEES", "DEPENDERS", "DEPENDS"):
            if key in step:
                _strings(step[key], f"{at}.{key}")
        if "DEPENDS" in step:
            step["DEPENDS"] = [_input_path(p, directory) for p in step["DEPENDS"]]
        if "WORKING_DIRECTORY" in step:
            step["WORKING_DIRECTORY"] = _input_path(
                _text(step["WORKING_DIRECTORY"], f"{at}.WORKING_DIRECTORY"), directory)
        result.append(step)
    return result


def _relative(value, where):
    value = _text(value, where)
    path = PureWindowsPath(value)
    if path.is_absolute() or path.drive or path.root or ".." in path.parts:
        raise ValueError(f"{where}: expected a path within its declared directory")
    return value


def _sources(value, where):
    source = _source(value["source"], f"{where}.source") if "source" in value else None
    resources = value.get("resources", {})
    if not isinstance(resources, dict):
        raise ValueError(f"{where}.resources: expected a table")
    return source, {_name(key, f"{where}.resources"): _source(item, f"{where}.resources.{key}")
                    for key, item in resources.items()}


def _metadata(value, where, base=None):
    result = dict(base or {})
    for key in ("config_dir", "pkgconfig_dir", "sentinel", "package_name"):
        if key in value:
            result[key] = (_name if key == "package_name" else _relative)(
                value[key], f"{where}.{key}")
    if "package_name" in result and "config_dir" not in result:
        raise ValueError(f"{where}.package_name: requires config_dir")
    return result


def normalize_architecture(value: str) -> str:
    """规范化已支持的目标别名；未知架构保留原值，不推测其他目标。"""
    return {"x64": "x86_64", "amd64": "x86_64", "x86_64": "x86_64",
            "arm64": "aarch64", "aarch64": "aarch64"}.get(value.lower(), value)


def load_definition(path: Path, system_name: str, architecture: str = "") -> ModuleDefinition:
    """读取并选择平台数据；失败携带声明路径，绝不访问未下载的来源。"""
    path = path.resolve()
    try:
        with path.open("rb") as stream:
            data = tomllib.load(stream)
        _table(data, {"name", "type", "version", "source", "resources", "cmake",
                      "patches", "steps", "copy", "platforms", "sentinel", "depends",
                      "config_dir", "pkgconfig_dir", "package_name"}, "module")
        name = _name(data.get("name"), "name")
        if name != path.parent.name:
            raise ValueError("name: must match the module directory name")
        kind = data.get("type")
        if kind not in ("cmake", "copy"):
            raise ValueError("type: expected cmake or copy")
        version = _text(data["version"], "version") if "version" in data else None
        depends = list(dict.fromkeys(_name(item, "depends")
                                    for item in _strings(data.get("depends", []), "depends")))
        common_metadata = _metadata(data, "module")
        metadata = common_metadata
        source, resources = _sources(data, "module")
        cmake = data.get("cmake", {})
        cache = dict(_cache(cmake, "cmake", source_subdir=True))
        subdir = _relative(cmake["SOURCE_SUBDIR"], "cmake.SOURCE_SUBDIR") if "SOURCE_SUBDIR" in cmake else None
        if kind != "cmake" and subdir is not None:
            raise ValueError("cmake.SOURCE_SUBDIR: requires type=cmake")
        steps = _steps(data.get("steps", []), path.parent, "steps")
        platforms = _table(data.get("platforms", {}), {"windows", "linux"}, "platforms")
        for platform, options in platforms.items():
            at = f"platforms.{platform}"
            _table(options, {"cmake", "steps", "source", "resources", "architectures",
                             "config_dir", "pkgconfig_dir", "package_name", "sentinel"}, at)
            platform_metadata = _metadata(options, at, common_metadata)
            platform_cache = _cache(options.get("cmake", {}), f"{at}.cmake")
            platform_steps = _steps(options.get("steps", []), path.parent, f"{at}.steps")
            platform_source, platform_resources = _sources(options, at)
            selected = {"Windows": "windows", "Linux": "linux"}.get(system_name) == platform
            if selected:
                metadata = platform_metadata
                cache.update(platform_cache)
                steps.extend(platform_steps)
                source = platform_source if platform_source is not None else source
                resources.update(platform_resources)
            architectures = _table(options.get("architectures", {}), {"x86_64", "aarch64"}, f"{at}.architectures")
            for arch, inputs in architectures.items():
                arch_at = f"{at}.architectures.{arch}"
                _table(inputs, {"source", "resources"}, arch_at)
                arch_source, arch_resources = _sources(inputs, arch_at)
                if selected and normalize_architecture(architecture) == arch:
                    source = arch_source if arch_source is not None else source
                    resources.update(arch_resources)
        if kind == "cmake" and source is None:
            raise ValueError(f"source: no primary source for {system_name}/{architecture}")
        for key, value in cache.items():
            for text in value if isinstance(value, list) else [value]:
                if isinstance(text, str):
                    for reference in RESOURCE_REFERENCE.findall(text):
                        if reference not in resources:
                            raise ValueError(f"cmake.cache.{key}: unknown resource {reference!r}")
        names = [step["name"] for step in steps]
        if len(names) != len(set(names)):
            raise ValueError("steps.name: duplicate step names")
        patches = []
        if not isinstance(data.get("patches", []), list):
            raise ValueError("patches: expected an array of tables")
        for item in data.get("patches", []):
            _table(item, {"file", "resource"}, "patches")
            resource = item.get("resource")
            if resource is not None and (not isinstance(resource, str) or resource not in resources):
                raise ValueError(f"patches.resource: unknown resource {resource!r}")
            patch = (path.parent / _text(item.get("file"), "patches.file")).resolve()
            if not patch.is_file():
                raise ValueError(f"patches.file: file not found: {patch}")
            patches.append(PatchDefinition(patch, resource))
        copies = data.get("copy", [])
        if not isinstance(copies, list) or kind == "copy" and not copies:
            raise ValueError("copy: expected nonempty installation mappings for copy modules")
        if kind == "cmake" and copies:
            raise ValueError("copy: installation mappings require type=copy")
        for item in copies:
            _table(item, {"resource", "files", "directories", "destination"}, "copy")
            resource = item.get("resource")
            if resource is not None and (not isinstance(resource, str) or resource not in resources):
                raise ValueError(f"copy.resource: unknown resource {resource!r}")
            if resource is None and source is None:
                raise ValueError("copy: mapping requires a source or resource")
            if ("files" in item) == ("directories" in item):
                raise ValueError("copy: specify either files or directories")
            key = "files" if "files" in item else "directories"
            entries = _strings(item[key], f"copy.{key}")
            if not entries:
                raise ValueError(f"copy.{key}: expected at least one path")
            for entry in entries:
                _relative(entry, f"copy.{key}")
            _relative(item.get("destination"), "copy.destination")
        return ModuleDefinition(path, name, kind, version, source, resources,
                                cache, patches, steps, copies,
                                sentinel=metadata.get("sentinel"), source_subdir=subdir,
                                depends=depends, config_dir=metadata.get("config_dir"),
                                pkgconfig_dir=metadata.get("pkgconfig_dir"),
                                package_name=metadata.get("package_name"))
    except (OSError, ValueError) as error:
        raise ValueError(f"{path}: {error}") from error
