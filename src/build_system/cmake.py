"""将已校验声明生成可直接阅读的原生 ExternalProject 调用。"""

from dataclasses import dataclass
from pathlib import Path

from definition import ModuleDefinition


@dataclass
class BuildContext:
    source_root: Path
    build_root: Path
    system_name: str
    generator: str
    platform: str
    toolset: str
    instance: str
    multi_config: bool
    msvc_runtime_library: str | None
    cmake_command: Path
    git_executable: Path


def cmake_argument(value: str) -> str:
    """引用一个 CMake 字面参数，不展开变量、转义序列或引号。"""
    equals = ""
    while f"]{equals}]" in value or value.endswith(f"]{equals}"):
        equals += "="
    return f"[{equals}[{value}]{equals}]"


def _resource_target(module, resource):
    # 长度前缀区分 a-b/c 与 a/b-c 等相同拼接结果。
    return f"_al_resource_{len(module)}_{module}_{resource}"


def _directories(prefix, install):
    return [("PREFIX", [prefix.as_posix()]),
            ("SOURCE_DIR", [(prefix / "src").as_posix()]),
            ("BINARY_DIR", [(prefix / "bin").as_posix()]),
            ("STAMP_DIR", [(prefix / "stamp").as_posix()]),
            ("TMP_DIR", [(prefix / "tmp").as_posix()]),
            ("DOWNLOAD_DIR", [(prefix / "dl").as_posix()]),
            ("INSTALL_DIR", [install.as_posix()])]


def _cache_argument(key, value):
    if isinstance(value, bool):
        return f"-D{key}:BOOL={'ON' if value else 'OFF'}"
    if isinstance(value, list):
        value = ";".join(value)
    return f"-D{key}:STRING={value}"


def render_module(module: ModuleDefinition, context: BuildContext) -> str:
    """返回完整声明；不获取来源，不执行命令，也不更新任何输出文件。"""
    prefix = context.build_root / "ep" / module.name
    install = context.source_root / "3rdparty" / module.name
    cmake = context.cmake_command.as_posix()
    separator = "__AL_LIST__"
    values = repr(module) + repr(context)
    while separator in values:
        separator += "_"
    lines = [f"# Generated from {module.path.name}; do not edit.", ""]

    def emit(command, positional, options):
        lines.append(f"{command}(")
        lines.extend(f"    {cmake_argument(str(value))}" for value in positional)
        for key, args in options:
            lines.append(f"    {key}")
            lines.extend("        " + cmake_argument(str(value).replace(";", separator))
                         for value in args)
        lines.extend([")", ""])

    for name, source in module.resources.items():
        options = _directories(prefix / "res" / name, install)
        options += [(key, value if isinstance(value, list) else [value])
                    for key, value in source.items()]
        options += [("LIST_SEPARATOR", [separator]), ("UPDATE_COMMAND", [""]),
                    ("CONFIGURE_COMMAND", [""]), ("BUILD_COMMAND", [""]),
                    ("INSTALL_COMMAND", [""])]
        emit("ExternalProject_Add", [_resource_target(module.name, name)], options)

    options = _directories(prefix, install)
    options += [("LIST_SEPARATOR", [separator]), ("UPDATE_COMMAND", [""])]
    if module.source:
        options += [(key, value if isinstance(value, list) else [value])
                    for key, value in module.source.items()]
    else:
        options.append(("DOWNLOAD_COMMAND", [""]))
    if module.resources:
        options.append(("DEPENDS", [_resource_target(module.name, name)
                                    for name in module.resources]))

    if module.kind == "cmake":
        options += [("CMAKE_COMMAND", [cmake]), ("CMAKE_GENERATOR", [context.generator])]
        for key, value in (("CMAKE_GENERATOR_PLATFORM", context.platform),
                           ("CMAKE_GENERATOR_TOOLSET", context.toolset),
                           ("CMAKE_GENERATOR_INSTANCE", context.instance)):
            if value:
                options.append((key, [value]))
        cache = dict(module.cache)
        # 配置身份与安装位置由公共入口拥有，不让模块选项覆盖。
        for key in ("CMAKE_INSTALL_PREFIX", "CMAKE_BUILD_TYPE", "CMAKE_MSVC_RUNTIME_LIBRARY",
                    "CMAKE_GENERATOR", "CMAKE_GENERATOR_PLATFORM", "CMAKE_GENERATOR_TOOLSET",
                    "CMAKE_GENERATOR_INSTANCE"):
            cache.pop(key, None)
        args = [_cache_argument(key, value) for key, value in cache.items()]
        args.append("-DCMAKE_INSTALL_PREFIX:PATH=<INSTALL_DIR>")
        if not context.multi_config:
            args.append("-DCMAKE_BUILD_TYPE:STRING=Release")
        if context.msvc_runtime_library is not None:
            args.append(_cache_argument("CMAKE_MSVC_RUNTIME_LIBRARY", context.msvc_runtime_library))
        options += [("CMAKE_ARGS", args),
                    ("BUILD_COMMAND", [cmake, "--build", "<BINARY_DIR>", "--config", "Release"]),
                    ("INSTALL_COMMAND", [cmake, "--build", "<BINARY_DIR>", "--target", "install",
                                         "--config", "Release"])]
    else:
        options += [("CONFIGURE_COMMAND", [""]), ("BUILD_COMMAND", [""])]
        first = True
        for mapping in module.copies:
            destination = (install / mapping["destination"]).as_posix()
            resource = mapping.get("resource")
            source = prefix / "res" / resource / "src" if resource else prefix / "src"
            options.append(("INSTALL_COMMAND" if first else "COMMAND",
                            [cmake, "-E", "make_directory", destination]))
            options.append(("COMMAND", [cmake, "-E", "copy",
                                       *[(source / file).as_posix() for file in mapping["files"]],
                                       destination]))
            first = False

    driver = Path(__file__).with_name("apply_patch.cmake").resolve()
    for index, patch in enumerate(module.patches):
        options.append(("PATCH_COMMAND" if index == 0 else "COMMAND",
                        [cmake, f"-DGIT_EXECUTABLE={context.git_executable.as_posix()}",
                         "-DSOURCE_DIR=<SOURCE_DIR>", f"-DPATCH_FILE={patch.as_posix()}",
                         "-P", driver.as_posix()]))
    emit("ExternalProject_Add", [module.name], options)
    if module.patches:
        emit("ExternalProject_Add_StepDependencies",
             [module.name, "patch", *[p.as_posix() for p in module.patches], driver.as_posix()], [])

    for step in module.steps:
        options = []
        for key in ("DEPENDEES", "DEPENDERS", "DEPENDS"):
            if key in step:
                options.append((key, step[key]))
        if "WORKING_DIRECTORY" in step:
            options.append(("WORKING_DIRECTORY", [step["WORKING_DIRECTORY"]]))
        commands = step["COMMAND"]
        if isinstance(commands[0], str):
            commands = [commands]
        options.extend(("COMMAND", argv) for argv in commands)
        emit("ExternalProject_Add_Step", [module.name, step["name"]], options)
    return "\n".join(lines)
