import argparse
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys


FFMPEG_COMPONENTS = ("libavcodec", "libavformat", "libavutil", "libavfilter")
GLIB_COMPONENTS = ("glib-2.0", "gobject-2.0", "gmodule-2.0", "gmodule-no-export-2.0", "gio-2.0", "gthread-2.0")
SVE2_TARGET = "armv8.5-a+dotprod+i8mm+sve2"


def run(*args, cwd=None, env=None, capture=False):
    command = [str(arg) for arg in args]
    result = subprocess.run(command, cwd=cwd, env=env, check=True, text=True, stdout=subprocess.PIPE if capture else None)
    return result.stdout.strip() if capture else None


def prepend_path(env, key, *paths):
    env[key] = os.pathsep.join([*(str(path) for path in paths), *filter(None, (env.get(key),))])


def variables(config):
    env = os.environ.copy()
    prepend_path(env, "PATH", config.cmake.parent)
    env.update(CC=str(config.cc), CXX=str(config.cxx), VCPKG_DISABLE_METRICS="1", VCPKG_MAX_CONCURRENCY="4")
    cache = config.work / "port-cache"
    cache.mkdir(exist_ok=True)
    env["VCPKG_DEFAULT_BINARY_CACHE"] = str(cache)
    if config.sdk:
        env.update(SDKROOT=str(config.sdk), MACOSX_DEPLOYMENT_TARGET=config.deployment)
        for formula in ("bison", "flex"):
            prepend_path(env, "PATH", Path(run("brew", "--prefix", formula, capture=True)) / "bin")
    if config.binutils:
        assembler = config.work / "assembler"
        for key, compiler in (("CC", config.cc), ("CXX", config.cxx)):
            env[key] = str(assembler / compiler.name)
    return env


def prepare_assembler(config, env):
    build = config.work / "gas"
    build.mkdir(exist_ok=True)
    if not (build / "gas/as-new").exists():
        compiler_env = dict(env, CC=str(config.cc), CXX=str(config.cxx))
        run(config.binutils / "configure", "--disable-nls", "--disable-werror", cwd=build, env=compiler_env)
        run(config.make, "-j4", "MAKEINFO=true", "all-gas", cwd=build, env=compiler_env)
    tools = config.work / "assembler"
    tools.mkdir(exist_ok=True)
    for name in ("as", "aarch64-linux-gnu-as"):
        path = tools / name
        if not path.exists():
            path.symlink_to(build / "gas/as-new")
    for compiler in (config.cc, config.cxx):
        wrapper = tools / compiler.name
        wrapper.write_text(f'#!/bin/sh\nexec {shlex.quote(str(compiler))} {shlex.quote(f"-B{tools}/")} "$@"\n')
        wrapper.chmod(0o755)
    probe = build / "sve2.s"
    probe.write_text(".text\n eor3 z0.d, z0.d, z1.d, z2.d\n")
    run(env["CC"], f"-march={SVE2_TARGET}", "-c", probe, "-o", build / "sve2.o", env=env)


def triplet(config):
    arch = "arm64" if config.sdk or config.binutils else "x64"
    system = "windows" if os.name == "nt" else "osx" if config.sdk else "linux"
    return f"gst-{arch}-{system}"


def install_ports(config, env):
    windows = os.name == "nt"
    port_triplet = triplet(config)
    tool = config.vcpkg / ("vcpkg.exe" if windows else "vcpkg")
    if not tool.exists():
        bootstrap = ("cmd", "/d", "/c", "bootstrap-vcpkg.bat") if windows else ("sh", "bootstrap-vcpkg.sh")
        run(*bootstrap, "-disableMetrics", cwd=config.vcpkg, env=env)
    triplets = config.work / "triplets"
    triplets.mkdir(exist_ok=True)
    values = [
        f"set(VCPKG_TARGET_ARCHITECTURE {port_triplet.split('-')[1]})",
        "set(VCPKG_CRT_LINKAGE dynamic)", "set(VCPKG_BUILD_TYPE release)", "set(VCPKG_LIBRARY_LINKAGE dynamic)",
    ]
    if not windows:
        values += [f"set(VCPKG_CMAKE_SYSTEM_NAME {'Darwin' if config.sdk else 'Linux'})",
                   'if(PORT STREQUAL "x264")', "    set(VCPKG_LIBRARY_LINKAGE static)", "endif()"]
    else:
        values.append("set(VCPKG_PLATFORM_TOOLSET v143)")
    if config.sdk:
        values.append(f"set(VCPKG_OSX_DEPLOYMENT_TARGET {config.deployment})")
    (triplets / f"{port_triplet}.cmake").write_text("\n".join(values) + "\n")
    ports = [f"{name}:{port_triplet}" for name in ("x264[asm]", "x265", "librtmp")]
    if windows:
        ports += [f"glib:{port_triplet}", "pkgconf:x64-windows"]
    options = [f"--overlay-triplets={triplets}", f"--x-install-root={config.work / 'ports'}"]
    if config.binutils:
        overlay = config.work / "overlay/x265"
        shutil.copytree(config.vcpkg / "ports/x265", overlay, dirs_exist_ok=True)
        pack = overlay / "portfile.cmake"
        content = pack.read_text()
        if content.count("        neon.diff\n") != 1:
            raise RuntimeError("x265 port does not declare the expected upstream patches")
        pack.write_text(content.replace("        neon.diff\n", "        neon.diff\n        focal-sve2.diff\n"))
        shutil.copy2(config.source / "x265-focal-sve2.diff", overlay / "focal-sve2.diff")
        options.append(f"--overlay-ports={overlay}")
    run(tool, "install", *ports, *options, cwd=config.work, env=env)
    if config.binutils:
        ninja = (config.vcpkg / "buildtrees/x265" / f"{port_triplet}-rel/build.ninja").read_text()
        for flag in (f"-march={SVE2_TARGET}", f"-march={SVE2_TARGET}-bitperm"):
            if not re.search(rf"{re.escape(flag)}(?:\s|$)", ninja):
                raise RuntimeError(f"x265 release build is missing {flag}")
            print(f"x265 release build uses {flag}")


def pkg_path(program, env, module, variable):
    # pkgconf escapes spaces in reported paths, including on Windows.
    return Path(run(program, f"--variable={variable}", module, env=env, capture=True).replace(r"\ ", " ")).resolve()


def require_pkg_prefix(program, env, prefix, modules):
    pc_dir = (prefix / "lib/pkgconfig").resolve()
    for module in modules:
        path = pkg_path(program, env, module, "pcfiledir")
        if path != pc_dir:
            raise RuntimeError(f"{module}.pc resolved to {path}; expected {pc_dir}")
        version = run(program, "--modversion", module, env=env, capture=True)
        libs = run(program, "--libs", module, env=env, capture=True)
        print(f"{module} {version}: {path} ({libs})", flush=True)


def meson_environment(config, env):
    ports = config.work / "ports" / triplet(config)
    prefixes = [config.ffmpeg / "lib/pkgconfig", ports / "lib/pkgconfig", ports / "share/pkgconfig"]
    if config.sdk:
        prefixes.append(Path(run("brew", "--prefix", "glib", capture=True)) / "lib/pkgconfig")
    prepend_path(env, "PKG_CONFIG_PATH", *prefixes)
    if os.name == "nt":
        pkgconf = config.work / "ports/x64-windows/tools/pkgconf/pkgconf.exe"
        env["PKG_CONFIG"] = str(pkgconf)
        prepend_path(env, "PATH", pkgconf.parent, ports / "bin", config.ffmpeg / "bin")
    return env.get("PKG_CONFIG", "pkg-config")


def configure(config, env, meson):
    if config.binutils:
        prepare_assembler(config, env)
    install_ports(config, env)
    pkgconf = meson_environment(config, env)
    require_pkg_prefix(pkgconf, env, config.ffmpeg, FFMPEG_COMPONENTS)
    require_pkg_prefix(pkgconf, env, config.work / "ports" / triplet(config), ("x264", "x265", "librtmp"))
    shutil.copytree(config.orc, config.source / "subprojects/orc", dirs_exist_ok=True)
    options = [
        "--prefix", config.prefix, "--libdir", "lib", "--libexecdir", "libexec", "--buildtype", "release",
        "--wrap-mode=nodownload", "-Dbuild-tools-source=system", "-Ddefault_library=shared",
        "-Dtests=disabled", "-Dexamples=disabled", "-Dintrospection=disabled", "-Dgpl=enabled", "-Dorc=enabled",
        "-Dorc-source=subproject", "-Dorc:default_library=shared", "-Dgst-full=disabled",
        "-Dbase=enabled", "-Dgood=enabled", "-Dbad=enabled", "-Dugly=enabled", "-Dlibav=enabled", "-Drtsp_server=enabled",
        "-Ddevtools=disabled", "-Dges=disabled", "-Dgst-examples=disabled", "-Dpython=disabled",
        "-Dlibnice=disabled", "-Dtls=disabled", "-Dgstreamer:tools=enabled", "-Dgstreamer:check=enabled",
        "-Dgstreamer:ptp-helper=disabled", "-Dgst-plugins-bad:cuda-nvmm=disabled",
    ]
    for module, plugins in {
        "base": ("app", "playback", "typefind", "videoconvertscale", "videotestsrc"),
        "good": ("autodetect", "flv", "isomp4", "matroska", "rtp", "rtpmanager", "rtsp"),
        "bad": ("videoparsers", "rtmp", "x265"),
        "ugly": ("x264",),
    }.items():
        options.extend(f"-Dgst-plugins-{module}:{plugin}=enabled" for plugin in plugins)
    if os.name == "nt":
        options += ["--vsenv", "-Dgst-plugins-bad:mediafoundation=enabled", "-Dgst-plugins-bad:d3d11=enabled",
                    "-Dgst-plugins-bad:nvcodec=disabled"]
    elif config.sdk:
        # 只选择原生 Cocoa OpenGL。Vulkan SDK 头文件不在可搬迁包中。
        options += ["-Dgst-plugins-base:gl=enabled", "-Dgst-plugins-base:gl_platform=cgl",
                    "-Dgst-plugins-base:gl_winsys=cocoa", "-Dgst-plugins-bad:applemedia=enabled",
                    "-Dgst-plugins-bad:vulkan=disabled"]
    else:
        nvcodec = "disabled" if config.binutils else "enabled"
        options.append(f"-Dgst-plugins-bad:nvcodec={nvcodec}")
        # tensordecoders 使用 GLib 2.72 的 g_alloca0，Focal 提供 GLib 2.64。
        options.append("-Dgst-plugins-bad:tensordecoders=disabled")
    build = config.work / "meson"
    if (build / "build.ninja").exists():
        options.append("--reconfigure")
    run(*meson, "setup", build, config.source, *options, env=env)


def copy_runtime(source, destination, pattern):
    destination.mkdir(parents=True, exist_ok=True)
    for library in source.glob(pattern):
        target = destination / library.name
        if library.is_symlink():
            if target.is_symlink():
                target.unlink()
            target.symlink_to(Path(os.readlink(library)).name)
        else:
            shutil.copy2(library, target)


def copy_glib_development(config, pkgconf, env):
    for variable, relative in (("includedir", "include/glib-2.0"), ("libdir", "lib/glib-2.0/include")):
        root = pkg_path(pkgconf, env, "glib-2.0", variable)
        suffix = "glib-2.0" if variable == "includedir" else "glib-2.0/include"
        shutil.copytree(root / suffix, config.prefix / relative, dirs_exist_ok=True)
    libdir = pkg_path(pkgconf, env, "glib-2.0", "libdir")
    libraries = ("glib-2.0", "gobject-2.0", "gio-2.0", "gmodule-2.0", "gthread-2.0")
    if os.name == "nt":
        for name in libraries:
            shutil.copy2(libdir / f"{name}.lib", config.prefix / "lib")
        gio_module, gio_headers = "gio-windows-2.0", "gio-win32-2.0"
    else:
        for name in libraries:
            copy_runtime(libdir, config.prefix / "lib", f"lib{name}*.dylib")
        gio_module = gio_headers = "gio-unix-2.0"
    gio_include = pkg_path(pkgconf, env, gio_module, "includedir") / gio_headers
    shutil.copytree(gio_include, config.prefix / "include" / gio_headers, dirs_exist_ok=True)
    for name in (*GLIB_COMPONENTS, gio_module):
        source = pkg_path(pkgconf, env, name, "pcfiledir") / f"{name}.pc"
        (config.prefix / "lib/pkgconfig" / source.name).write_text(shared_glib_metadata(source.read_text(), name))
    return libdir


def shared_glib_metadata(content, name):
    # 这里只交付共享库。GLib 的私有静态依赖和构建机 gettext 路径不属于公开接口。
    library = {
        "gmodule-no-export-2.0": "gmodule-2.0",
        "gio-unix-2.0": "gio-2.0",
        "gio-windows-2.0": "gio-2.0",
    }.get(name, name)
    fields = []
    for line in content.splitlines():
        if line.startswith(("Requires.private:", "Libs.private:")):
            continue
        if line.startswith("Libs:"):
            line = f"Libs: -L${{libdir}} -l{library}"
        if line.startswith("Cflags:"):
            line = re.sub(r"-I(?:/[^\s]+|[A-Za-z]:/[^\s]+)(?:\s+|$)", "", line).rstrip()
        fields.append(line)
    return "\n".join(fields) + "\n"


def runtime_binaries(prefix, pattern):
    candidates = [*prefix.glob(f"lib/**/{pattern}"), *prefix.glob("bin/*"), *prefix.glob("libexec/**/*")]
    return sorted(path for path in candidates if path.is_file() and not path.is_symlink())


def relocate_linux(prefix, ffmpeg):
    for binary in runtime_binaries(prefix, "*.so*"):
        if not binary.is_file():
            continue
        with binary.open("rb") as contents:
            if contents.read(4) != b"\x7fELF":
                continue
        rpaths = [f"$ORIGIN/{os.path.relpath(root, binary.parent)}" for root in (prefix / "lib", ffmpeg / "lib")]
        run("patchelf", "--set-rpath", ":".join(rpaths), binary)


def relocate_darwin(prefix, ffmpeg):
    library_dir = prefix / "lib"
    pending = [(path, path) for path in runtime_binaries(prefix, "*.dylib")]
    visited = set()
    providers = set()
    while pending:
        binary, origin = pending.pop()
        if binary in visited or not binary.is_file():
            continue
        visited.add(binary)
        # 部分 Homebrew 运行库同时携带 x86_64，Darwin 发布身份是单一 ARM64。
        with binary.open("rb") as contents:
            magic = contents.read(4)
        universal = magic in (b"\xca\xfe\xba\xbe", b"\xca\xfe\xba\xbf")
        if not universal and magic != b"\xcf\xfa\xed\xfe":
            continue
        if universal:
            arm64 = binary.with_suffix(".arm64")
            run("lipo", "-thin", "arm64", binary, "-output", arm64)
            arm64.replace(binary)
        loads = run("otool", "-L", binary, capture=True).splitlines()[1:]
        rpaths = re.findall(r"cmd LC_RPATH\s+cmdsize \d+\s+path (.+?) \(offset \d+\)",
                           run("otool", "-arch", "arm64", "-l", origin, capture=True))
        edits = []
        if binary.suffix == ".dylib":
            edits += ["-id", f"@rpath/{binary.name}"]
            loads = loads[1:]
        for line in loads:
            name = line.strip().split(" (", 1)[0]
            if name.startswith(("/usr/lib/", "/System/Library/")):
                continue
            original = Path(name)
            candidates = (library_dir / original.name, ffmpeg / "lib" / original.name)
            if name.startswith("@loader_path/"):
                relative = name[len("@loader_path/"):]
                installed = (binary.parent / relative).resolve()
                if installed.is_file() and (prefix in installed.parents or ffmpeg in installed.parents):
                    continue
                original = origin.parent / relative
            if name.startswith("@rpath/"):
                name_in_rpath = name[len("@rpath/"):]
                original_paths = [Path(path.replace("@loader_path", str(origin.parent))) / name_in_rpath for path in rpaths]
                original = next((path for path in original_paths if path.is_file()), origin.parent / name_in_rpath)
            bundled = next((path for path in candidates if path.is_file()), None)
            if bundled is None:
                if not original.is_absolute() or not original.is_file():
                    raise RuntimeError(f"Unresolved dependency {name} in {binary}")
                bundled = library_dir / original.name
                shutil.copy2(original, bundled)
                bundled.chmod(0o755)
                providers.add(original.resolve())
                pending.append((bundled, original))
            edits += ["-change", name, f"@loader_path/{os.path.relpath(bundled, binary.parent)}"]
        for rpath in rpaths:
            if Path(rpath).is_absolute():
                edits += ["-delete_rpath", rpath]
        if edits:
            run("install_name_tool", *edits, binary)
        if edits or universal:
            run("codesign", "--force", "--sign", "-", binary)
    return providers


def copy_homebrew_notices(licenses, providers):
    kegs = {parent for origin in providers for parent in origin.parents if (parent / "INSTALL_RECEIPT.json").is_file()}
    for keg in sorted(kegs):
        dest = licenses / "homebrew" / keg.parent.name / keg.name
        dest.mkdir(parents=True, exist_ok=True)
        for pattern in ("COPYING*", "COPYRIGHT*", "LICENSE*", "LICENCE*", "LGPL-*.txt", "NOTICE*", "LEGAL*",
                        "sbom.spdx.json", ".brew/*.rb"):
            for source in keg.glob(pattern):
                if source.is_file():
                    shutil.copyfile(source, dest / source.name)
        source = f"https://formulae.brew.sh/formula/{keg.parent.name} {keg.name}\n"
        (dest / "source.txt").write_text(source)


def install(config, env, meson):
    pkgconf = meson_environment(config, env)
    run(*meson, "install", "-C", config.work / "meson", "--no-rebuild", env=env)
    ports = config.work / "ports" / triplet(config)
    prefix = config.prefix
    # 上游的 GDB 自动加载入口使用生产机的绝对安装路径。
    gdb_autoload = prefix / "share/gdb/auto-load"
    if gdb_autoload.is_dir():
        shutil.rmtree(gdb_autoload)
    licenses = prefix / "share/licenses"
    for pattern in ("*/copyright", "*/vcpkg.spdx.json"):
        for item in (ports / "share").glob(pattern):
            dest = licenses / item.parent.name
            dest.mkdir(parents=True, exist_ok=True)
            shutil.copy2(item, dest)
    for directory in (config.orc, config.source / "subprojects/gstreamer", config.source / "subprojects/gst-plugins-base",
                      config.source / "subprojects/gst-plugins-good", config.source / "subprojects/gst-plugins-bad",
                      config.source / "subprojects/gst-plugins-ugly", config.source / "subprojects/gst-libav",
                      config.source / "subprojects/gst-rtsp-server"):
        dest = licenses / directory.name
        dest.mkdir(parents=True, exist_ok=True)
        for item in directory.glob("COPYING*"):
            shutil.copy2(item, dest)
    if os.name == "nt" or config.sdk:
        glib_libdir = copy_glib_development(config, pkgconf, env)
    for header in ("zlib.h", "zconf.h"):
        shutil.copyfile(ports / "include" / header, prefix / "include" / header)
    shutil.copyfile(ports / "lib/pkgconfig/zlib.pc", prefix / "lib/pkgconfig/zlib.pc")
    if os.name == "nt":
        copy_runtime(ports / "bin", prefix / "bin", "*.dll")
        shutil.copy2(ports / "lib/libz.lib", prefix / "lib")
    elif config.sdk:
        copy_runtime(ports / "lib", prefix / "lib", "*.dylib")
        providers = relocate_darwin(prefix, config.ffmpeg)
        copy_homebrew_notices(licenses, {*providers, glib_libdir.resolve()})
    else:
        copy_runtime(ports / "lib", prefix / "lib", "*.so*")
        relocate_linux(prefix, config.ffmpeg)
    for path in (prefix / "lib/pkgconfig").glob("*.pc"):
        content = re.sub(r"^prefix=.*$", lambda _: "prefix=${pcfiledir}/../..", path.read_text(), flags=re.MULTILINE)
        path.write_text(content)
    plugins = sorted(path.name for path in (prefix / "lib/gstreamer-1.0").glob("*") if path.suffix in (".so", ".dylib", ".dll"))
    (prefix / "share/gstreamer-1.0/plugins.txt").write_text("\n".join(plugins) + "\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("step", choices=("configure", "build", "install"))
    for key in ("source", "work", "prefix", "ffmpeg", "meson", "orc", "vcpkg", "cmake", "make", "cc", "cxx"):
        parser.add_argument(f"--{key}", required=True, type=Path)
    parser.add_argument("--binutils", type=Path)
    parser.add_argument("--sdk", type=Path)
    parser.add_argument("--deployment")
    config = parser.parse_args()
    env = variables(config)
    meson = [sys.executable, config.meson / "meson.py"]
    if config.step == "configure":
        configure(config, env, meson)
    elif config.step == "build":
        meson_environment(config, env)
        run(*meson, "compile", "-C", config.work / "meson", "-j", "4", env=env)
    else:
        install(config, env, meson)


if __name__ == "__main__":
    main()
