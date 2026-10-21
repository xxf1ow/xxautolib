"""组织本轮声明生成，并向 CMake 报告实际输入；不执行构建。"""

import argparse
from pathlib import Path
import sys

from definition import load_definition
from cmake import BuildContext, cmake_argument, render_module


def write_if_changed(path: Path, content: str) -> bool:
    """内容不同时写入并返回 True；相同时保留文件时间戳并返回 False。"""
    if path.is_file() and path.read_text(encoding="utf-8") == content:
        return False
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8", newline="\n")
    return True


def main(argv: list[str] | None = None) -> int:
    """生成所选模块和输入列表；输入或写入失败时报告对象并返回非零。"""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--module", action="append", default=[])
    for key in ("source-root", "build-root", "cmake-command", "git-executable"):
        parser.add_argument(f"--{key}", type=Path, required=True)
    for key in ("system-name", "generator", "platform", "toolset", "instance"):
        parser.add_argument(f"--{key}", required=True)
    parser.add_argument("--multi-config", choices=("ON", "OFF"), required=True)
    parser.add_argument("--architecture", default="")
    parser.add_argument("--msvc-runtime-library")
    parser.add_argument("--cmake-default", action="append", default=[])
    args = parser.parse_args(argv)
    context = BuildContext(
        source_root=args.source_root.resolve(), build_root=args.build_root.resolve(),
        system_name=args.system_name, generator=args.generator, platform=args.platform,
        toolset=args.toolset, instance=args.instance, multi_config=args.multi_config == "ON",
        msvc_runtime_library=args.msvc_runtime_library,
        cmake_command=args.cmake_command, git_executable=args.git_executable,
        cmake_defaults=dict(value.split("=", 1) for value in args.cmake_default))
    try:
        definitions = [load_definition(context.source_root / "src/dependencies" / name / "module.toml",
                                       context.system_name, args.architecture) for name in args.module]
        outputs = {}
        for module in definitions:
            outputs[context.build_root / "generated" / f"{module.name}.cmake"] = render_module(module, context)
        engine = Path(__file__).resolve().parent
        inputs = {Path(__file__).resolve(), *[module.path for module in definitions]}
        inputs.update(Path(module.__file__).resolve() for module in tuple(sys.modules.values())
                      if getattr(module, "__file__", None)
                      and Path(module.__file__).resolve().parent == engine)
        manifest = "set(XXAUTOLIB_GENERATED_MODULES\n" + "".join(
            f"    {cmake_argument(path.as_posix())}\n" for path in outputs) + ")\n"
        manifest += "set(XXAUTOLIB_CONFIGURE_INPUTS\n" + "".join(
            f"    {cmake_argument(path.as_posix())}\n" for path in sorted(inputs)) + ")\n"
        for path, content in outputs.items():
            write_if_changed(path, content)
        write_if_changed(context.build_root / "generated/inputs.cmake", manifest)
    except (OSError, ValueError) as error:
        print(f"Module generation failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
