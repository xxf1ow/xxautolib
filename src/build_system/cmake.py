"""将已校验声明生成可直接阅读的原生 ExternalProject 调用。"""

from dataclasses import dataclass, field
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
    cmake_defaults: dict[str, str] = field(default_factory=dict)


def cmake_argument(value: str) -> str:
    """引用一个 CMake 字面参数，不展开变量、转义序列或引号。"""
    equals = ""
    while f"]{equals}]" in value or value.endswith(f"]{equals}"):
        equals += "="
    return f"[{equals}[{value}]{equals}]"


def _resource_target(module, resource):
    # 长度前缀区分 a-b/c 与 a/b-c 等相同拼接结果。
    return f"_al_resource_{len(module)}_{module}_{resource}"


def _directories(prefix, install, source, download):
    return [("PREFIX", [prefix.as_posix()]),
            ("SOURCE_DIR", [source.as_posix()]),
            ("BINARY_DIR", [(prefix / "bin").as_posix()]),
            ("STAMP_DIR", [(prefix / "stamp").as_posix()]),
            ("TMP_DIR", [(prefix / "tmp").as_posix()]),
            ("DOWNLOAD_DIR", [download.as_posix()]),
            ("INSTALL_DIR", [install.as_posix()])]


def _cache_argument(key, value):
    if isinstance(value, bool):
        return f"-D{key}:BOOL={'ON' if value else 'OFF'}"
    if isinstance(value, list):
        value = ";".join(value)
    return f"-D{key}:STRING={value}"


def render_module(module: ModuleDefinition, context: BuildContext,
                  dependencies: tuple[ModuleDefinition, ...] = ()) -> str:
    """返回完整声明；dependencies 仅含直接依赖，不执行命令或更新输出文件。"""
    prefix = context.build_root / "ep" / module.name
    install = context.source_root / "3rdparty" / module.name
    cmake = context.cmake_command.as_posix()
    separator = "__AL_LIST__"
    values = repr(module) + repr(context)
    while separator in values:
        separator += "_"
    lines = [f"# Generated from {module.path.name}; do not edit.", ""]
    if module.sentinel:
        lines += [f"if(EXISTS {cmake_argument((install / module.sentinel).as_posix())})",
                  f"    message(STATUS {cmake_argument(f'  [Info] [{module.name}] cached')})",
                  f"    add_custom_target({cmake_argument(module.name)} ALL)"]
        if dependencies:
            lines.append("    add_dependencies(" + " ".join(
                cmake_argument(name) for name in [module.name, *[dep.name for dep in dependencies]]) + ")")
        lines.append("else()")

    def emit(command, positional, options):
        lines.append(f"{command}(")
        lines.extend(f"    {cmake_argument(str(value))}" for value in positional)
        for key, args in options:
            if key == "_SOURCE_ARGS":
                lines.append("    ${_al_source_args}")
                continue
            lines.append(f"    {key}")
            lines.extend("        " + cmake_argument(str(value).replace(";", separator))
                         for value in args)
        lines.extend([")", ""])

    persistent = context.source_root / "git"

    def source_directory(source, local, prefix):
        return local if source and "GIT_REPOSITORY" in source else prefix / "src"

    def source_options(source, local):
        if not source:
            return [("DOWNLOAD_COMMAND", [""])]
        options = [(key, value if isinstance(value, list) else [value])
                   for key, value in source.items()]
        if "GIT_REPOSITORY" not in source:
            return options
        location = cmake_argument(local.as_posix())
        lines.extend([f"if(EXISTS {cmake_argument((local / '.git').as_posix())})",
                      '    set(_al_source_args DOWNLOAD_COMMAND "")', "else()",
                      f"    if(EXISTS {location})",
                      f"        if(NOT IS_DIRECTORY {location})",
                      f"            message(FATAL_ERROR {cmake_argument(f'{local.as_posix()}: expected a Git directory')})",
                      "        endif()",
                      f"        file(GLOB _al_entries LIST_DIRECTORIES TRUE {cmake_argument(local.as_posix() + '/*')})",
                      "        if(_al_entries)",
                      f"            message(FATAL_ERROR {cmake_argument(f'{local.as_posix()}: nonempty directory without .git; preserve or complete it manually')})",
                      "        endif()", "    endif()"])
        emit("set", ["_al_source_args"], options)
        lines.append("endif()")
        return [("_SOURCE_ARGS", [])]

    for name, source in module.resources.items():
        resource_prefix = prefix / "res" / name
        local = persistent / "_resources" / module.name / name
        options = _directories(resource_prefix, install, source_directory(source, local, resource_prefix), persistent)
        options += source_options(source, local)
        options += [("LIST_SEPARATOR", [separator]), ("UPDATE_COMMAND", [""]),
                    ("CONFIGURE_COMMAND", [""]), ("BUILD_COMMAND", [""]),
                    ("INSTALL_COMMAND", [""])]
        emit("ExternalProject_Add", [_resource_target(module.name, name)], options)

    local = persistent / module.name
    options = _directories(prefix, install, source_directory(module.source, local, prefix), persistent)
    options += [("LIST_SEPARATOR", [separator]), ("UPDATE_COMMAND", [""])]
    options += source_options(module.source, local)
    targets = [_resource_target(module.name, name) for name in module.resources]
    targets.extend(dep.name for dep in dependencies)
    if targets:
        options.append(("DEPENDS", targets))

    if module.kind == "cmake":
        if module.source_subdir is not None:
            options.append(("SOURCE_SUBDIR", [module.source_subdir]))
        options += [("CMAKE_COMMAND", [cmake]), ("CMAKE_GENERATOR", [context.generator])]
        for key, value in (("CMAKE_GENERATOR_PLATFORM", context.platform),
                           ("CMAKE_GENERATOR_TOOLSET", context.toolset),
                           ("CMAKE_GENERATOR_INSTANCE", context.instance)):
            if value:
                options.append((key, [value]))
        cache = {"CMAKE_POLICY_VERSION_MINIMUM": "3.5",
                 "CMAKE_POSITION_INDEPENDENT_CODE": True,
                 "CMAKE_CXX_STANDARD": "17",
                 **context.cmake_defaults, **module.cache}
        # 配置身份与安装位置由公共入口拥有，不让模块选项覆盖。
        for key in ("CMAKE_INSTALL_PREFIX", "CMAKE_BUILD_TYPE", "CMAKE_MSVC_RUNTIME_LIBRARY",
                    "CMAKE_GENERATOR", "CMAKE_GENERATOR_PLATFORM", "CMAKE_GENERATOR_TOOLSET",
                    "CMAKE_GENERATOR_INSTANCE"):
            cache.pop(key, None)
        setup_path = context.build_root / "generated" / f"{module.name}-dependency-setup.cmake"
        if dependencies:
            prefixes = [(context.source_root / "3rdparty" / dep.name).as_posix() for dep in dependencies]
            previous = cache.get("CMAKE_PREFIX_PATH", [])
            if isinstance(previous, str):
                previous = previous.split(";")
            cache["CMAKE_PREFIX_PATH"] = list(dict.fromkeys([*prefixes, *previous]))
            for dep, location in zip(dependencies, prefixes):
                if dep.config_dir:
                    cache[f"{dep.package_name or dep.name}_DIR"] = f"{location}/{dep.config_dir}"
            caller_include = cache.get("CMAKE_PROJECT_INCLUDE", [])
            if isinstance(caller_include, str):
                caller_include = caller_include.split(";")
            cache["CMAKE_PROJECT_INCLUDE"] = setup_path.as_posix()
        args = [_cache_argument(key, value) for key, value in cache.items()]
        args.append("-DCMAKE_INSTALL_PREFIX:PATH=<INSTALL_DIR>")
        if not context.multi_config:
            args.append("-DCMAKE_BUILD_TYPE:STRING=Release")
        if context.msvc_runtime_library is not None:
            args.append(_cache_argument("CMAKE_MSVC_RUNTIME_LIBRARY", context.msvc_runtime_library))
        if dependencies:
            command = [cmake, "-S", "<SOURCE_DIR>" + (f"/{module.source_subdir}" if module.source_subdir else ""),
                       "-B", "<BINARY_DIR>", "-G", context.generator]
            for flag, value in (("-A", context.platform), ("-T", context.toolset)):
                if value:
                    command += [flag, value]
            if context.instance:
                command.append(f"-DCMAKE_GENERATOR_INSTANCE:INTERNAL={context.instance}")
            command += [arg.replace("<INSTALL_DIR>", install.as_posix()) for arg in args]
            # setup 同时供脚本代理和子项目 project include 使用；字面分号保持单个 argv。
            setup = ["if(CMAKE_SCRIPT_MODE_FILE)", "    set(_al_command)"]
            setup.extend("    list(APPEND _al_command " + cmake_argument(arg.replace(";", "\\;")) + ")"
                         for arg in command)
            pc_paths = [f"{location}/{dep.pkgconfig_dir}" for dep, location in zip(dependencies, prefixes)
                        if dep.pkgconfig_dir]
            setup.append("    set(_al_pkgconfig " + cmake_argument(
                (";" if context.system_name == "Windows" else ":").join(dict.fromkeys(pc_paths))) + ")")
            setup.append("else()")
            setup.extend("    include(" + cmake_argument(value) + ")" for value in caller_include)
            setup.append("    include_directories(" + " ".join(
                cmake_argument(f"{location}/include") for location in prefixes) + ")")
            setup.append("endif()\n")
            content = "\n".join(setup).replace("$", "$<1:$>")
            lines.append(f"file(GENERATE OUTPUT {cmake_argument(setup_path.as_posix())} CONTENT {cmake_argument(content)})")
            proxy = Path(__file__).with_name("configure_dependencies.cmake").resolve()
            options.append(("CONFIGURE_COMMAND", [cmake, f"-DSETUP_FILE={setup_path.as_posix()}",
                           "-DSOURCE_DIR=<SOURCE_DIR>", "-DBINARY_DIR=<BINARY_DIR>", "-P", proxy.as_posix()]))
        else:
            options.append(("CMAKE_ARGS", args))
        options += [
                    ("BUILD_COMMAND", [cmake, "--build", "<BINARY_DIR>", "--config", "Release"]),
                    ("INSTALL_COMMAND", [cmake, "--build", "<BINARY_DIR>", "--target", "install",
                                         "--config", "Release"])]
    else:
        options += [("CONFIGURE_COMMAND", [""]), ("BUILD_COMMAND", [""])]
        first = True
        for mapping in module.copies:
            destination = (install / mapping["destination"]).as_posix()
            resource = mapping.get("resource")
            source = source_directory(module.resources[resource], persistent / "_resources" / module.name / resource,
                                      prefix / "res" / resource) if resource else source_directory(module.source, local, prefix)
            options.append(("INSTALL_COMMAND" if first else "COMMAND",
                            [cmake, "-E", "make_directory", destination]))
            key = "files" if "files" in mapping else "directories"
            options.append(("COMMAND", [cmake, "-E", "copy" if key == "files" else "copy_directory",
                                       *[(source / entry).as_posix() for entry in mapping[key]],
                                       destination]))
            first = False

    driver = Path(__file__).with_name("apply_patch.cmake").resolve()
    for index, patch in enumerate(module.patches):
        options.append(("PATCH_COMMAND" if index == 0 else "COMMAND",
                        [cmake, f"-DGIT_EXECUTABLE={context.git_executable.as_posix()}",
                         "-DSOURCE_DIR=<SOURCE_DIR>", f"-DPATCH_FILE={patch.as_posix()}",
                         "-P", driver.as_posix()]))
    emit("ExternalProject_Add", [module.name], options)
    if dependencies and module.kind == "cmake":
        emit("ExternalProject_Add_StepDependencies", [module.name, "configure", setup_path.as_posix(), proxy.as_posix()], [])
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
    if module.sentinel:
        lines.append("endif()")
    return "\n".join(lines)
