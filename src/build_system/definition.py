"""读取纯数据模块声明；执行与 CMake 生成由其他文件负责。"""

from dataclasses import dataclass
from pathlib import Path, PureWindowsPath
import re
import tomllib


@dataclass
class ModuleDefinition:
    path: Path
    name: str
    kind: str
    version: str | None
    source: dict[str, str] | None
    resources: dict[str, dict[str, str]]
    cache: dict[str, object]
    patches: list[Path]
    steps: list[dict[str, object]]
    copies: list[dict[str, object]]


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


def _source(value, where):
    _table(value, {"GIT_REPOSITORY", "GIT_TAG"}, where)
    return {key: _text(value.get(key), f"{where}.{key}")
            for key in ("GIT_REPOSITORY", "GIT_TAG")}


def _cache(value, where):
    _table(value, {"cache"}, where)
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


def load_definition(path: Path, system_name: str) -> ModuleDefinition:
    """读取并选择平台数据；失败携带声明路径，绝不访问未下载的来源。"""
    path = path.resolve()
    try:
        with path.open("rb") as stream:
            data = tomllib.load(stream)
        _table(data, {"name", "type", "version", "source", "resources", "cmake",
                      "patches", "steps", "copy", "platforms"}, "module")
        name = _name(data.get("name"), "name")
        if name != path.parent.name:
            raise ValueError("name: must match the module directory name")
        kind = data.get("type")
        if kind not in ("cmake", "copy"):
            raise ValueError("type: expected cmake or copy")
        version = _text(data["version"], "version") if "version" in data else None
        source = _source(data["source"], "source") if "source" in data else None
        if kind == "cmake" and source is None:
            raise ValueError("source: cmake modules require a primary source")
        resources = data.get("resources", {})
        if not isinstance(resources, dict):
            raise ValueError("resources: expected a table")
        resources = {_name(key, "resources"): _source(value, f"resources.{key}")
                     for key, value in resources.items()}
        cache = dict(_cache(data.get("cmake", {}), "cmake"))
        steps = _steps(data.get("steps", []), path.parent, "steps")
        platforms = _table(data.get("platforms", {}), {"windows", "linux"}, "platforms")
        for platform, options in platforms.items():
            at = f"platforms.{platform}"
            _table(options, {"cmake", "steps"}, at)
            platform_cache = _cache(options.get("cmake", {}), f"{at}.cmake")
            platform_steps = _steps(options.get("steps", []), path.parent, f"{at}.steps")
            if {"Windows": "windows", "Linux": "linux"}.get(system_name) == platform:
                cache.update(platform_cache)
                steps.extend(platform_steps)
        names = [step["name"] for step in steps]
        if len(names) != len(set(names)):
            raise ValueError("steps.name: duplicate step names")
        patches = []
        if not isinstance(data.get("patches", []), list):
            raise ValueError("patches: expected an array of tables")
        for item in data.get("patches", []):
            _table(item, {"file"}, "patches")
            patch = (path.parent / _text(item.get("file"), "patches.file")).resolve()
            if not patch.is_file():
                raise ValueError(f"patches.file: file not found: {patch}")
            patches.append(patch)
        copies = data.get("copy", [])
        if not isinstance(copies, list) or kind == "copy" and not copies:
            raise ValueError("copy: expected nonempty installation mappings for copy modules")
        if kind == "cmake" and copies:
            raise ValueError("copy: installation mappings require type=copy")
        for item in copies:
            _table(item, {"resource", "files", "destination"}, "copy")
            resource = item.get("resource")
            if resource is not None and (not isinstance(resource, str) or resource not in resources):
                raise ValueError(f"copy.resource: unknown resource {resource!r}")
            if resource is None and source is None:
                raise ValueError("copy: mapping requires a source or resource")
            files = _strings(item.get("files"), "copy.files")
            if not files:
                raise ValueError("copy.files: expected at least one file")
            for file in files:
                _relative(file, "copy.files")
            _relative(item.get("destination"), "copy.destination")
        return ModuleDefinition(path, name, kind, version, source, resources,
                                cache, patches, steps, copies)
    except (OSError, ValueError) as error:
        raise ValueError(f"{path}: {error}") from error
