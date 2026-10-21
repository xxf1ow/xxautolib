from dataclasses import replace
import os
import unittest

from test_cmake import PipelineCase, render_module


class DependencyTests(PipelineCase):
    def project_modules(self, modules, suffix="", context=None):
        by_name = {module.name: module for module in modules}
        generated = self.source / "modules.cmake"
        generated.write_text("\n".join(render_module(
            module, context or self.context, tuple(by_name[name] for name in module.depends))
            for module in modules), encoding="utf-8")
        (self.source / "CMakeLists.txt").write_text(
            'cmake_minimum_required(VERSION 3.20)\nproject(fixture NONE)\n'
            'include(ExternalProject)\ninclude(modules.cmake)\n' + suffix)
        args = ["cmake", "-S", self.source, "-B", self.context.build_root,
                "-G", self.context.generator]
        if self.context.platform:
            args += ["-A", self.context.platform, "-T", self.context.toolset]
        self.run_command(*args)

    def child(self, name, content="", dependencies=(), metadata="", files=None, subdir=""):
        import json
        repo = self.repository(name, {
            **(files or {}),
            (subdir + "/" if subdir else "") + "CMakeLists.txt":
            'cmake_minimum_required(VERSION 3.20)\nproject(child CXX)\n' + content +
            '\nfile(WRITE "${CMAKE_BINARY_DIR}/marker" "installed")\n'
            'install(FILES "${CMAKE_BINARY_DIR}/marker" DESTINATION .)\n'})
        return self.definition('depends=' + json.dumps(list(dependencies)) + '\n' + metadata +
                               '[source]\n' + repo +
                               (f'[cmake]\nSOURCE_SUBDIR="{subdir}"\n' if subdir else ""), name=name)

    def test_leaf_waits_for_dependency_install(self):
        dep = self.child("dep")
        marker = (self.source / "3rdparty/dep/marker").as_posix()
        leaf = self.child("leaf", f'if(NOT EXISTS "{marker}")\nmessage(FATAL_ERROR "dependency not installed")\nendif()', ["dep"])
        self.project_modules([dep, leaf])
        self.build()
        self.assertEqual((self.source / "3rdparty/leaf/marker").read_text(), "installed")

    def test_dependency_configure_keeps_configuration_identity(self):
        dep = self.child("dep")
        leaf = self.child("leaf", dependencies=["dep"])
        context = replace(self.context, system_name="Windows", generator="Visual Studio 17 2022",
                          platform="x64", toolset="v142,host=x64", instance="C:/VS instance",
                          multi_config=True, msvc_runtime_library="MultiThreaded$<$<CONFIG:Debug>:Debug>DLL")
        self.project_modules([dep, leaf], context=context)
        setup = self.context.build_root / "generated/leaf-dependency-setup.cmake"
        inspect = self.source / "inspect.cmake"
        inspect.write_text(f'include("{setup.as_posix()}")\n'
                           'foreach(arg IN LISTS _al_command)\nmessage("ARG=${arg}")\nendforeach()\n')
        result = self.run_command("cmake", "-P", inspect)
        received = [line.removeprefix("ARG=") for line in result.stderr.splitlines() if line.startswith("ARG=")]
        for value in ("Visual Studio 17 2022", "-A", "x64", "-T", "v142,host=x64",
                      "-DCMAKE_GENERATOR_INSTANCE:INTERNAL=C:/VS instance",
                      "-DCMAKE_MSVC_RUNTIME_LIBRARY:STRING=MultiThreaded$<$<CONFIG:Debug>:Debug>DLL"):
            self.assertIn(value, received)
        self.assertFalse(any("CMAKE_BUILD_TYPE" in value for value in received))
        self.project_modules([dep, leaf], context=replace(context, generator="Unix Makefiles",
            platform="", toolset="", instance="", multi_config=False, msvc_runtime_library=None))
        result = self.run_command("cmake", "-P", inspect)
        self.assertIn("ARG=-DCMAKE_BUILD_TYPE:STRING=Release", result.stderr)
        self.assertNotIn("CMAKE_MSVC_RUNTIME_LIBRARY", result.stderr)

    def test_direct_paths_do_not_include_siblings(self):
        shared = self.child("shared", metadata='pkgconfig_dir="lib/pkgconfig"\n')
        dep = self.child("dep", dependencies=["shared"],
                         metadata='config_dir="lib/cmake/Dep"\npackage_name="Dep"\npkgconfig_dir="lib/pkgconfig"\n')
        sibling = self.child("sibling", metadata='pkgconfig_dir="lib/pkgconfig"\n')
        leaf = self.child("leaf", '''file(WRITE "${CMAKE_BINARY_DIR}/paths"
"${CMAKE_PREFIX_PATH}|${Dep_DIR}|$ENV{PKG_CONFIG_PATH}")
install(FILES "${CMAKE_BINARY_DIR}/paths" DESTINATION .)
''', ["dep"])
        leaf.cache["CMAKE_PREFIX_PATH"] = ["caller path"]
        self.project_modules([shared, dep, sibling, leaf])
        environment = {**os.environ, "PKG_CONFIG_PATH": "existing-pc-path"}
        self.run_command("cmake", "--build", self.context.build_root, "--config", "Release", env=environment)
        prefix = (self.source / "3rdparty/dep").as_posix()
        separator = ";" if os.name == "nt" else ":"
        self.assertEqual((self.source / "3rdparty/leaf/paths").read_text(),
                         f"{prefix};caller path|{prefix}/lib/cmake/Dep|{prefix}/lib/pkgconfig{separator}existing-pc-path")
        self.assertEqual(environment["PKG_CONFIG_PATH"], "existing-pc-path")

    def test_dependency_configure_preserves_arguments_and_include(self):
        dep = self.child("dep", 'install(FILES dep.h DESTINATION include)', files={"dep.h": "#define DEP 17\n"})
        caller = self.source / "caller include.cmake"
        caller.write_text('file(WRITE "${CMAKE_BINARY_DIR}/include-marker.txt" "caller include preserved")\n')
        leaf = self.child("leaf", '''add_library(check STATIC check.cpp)
foreach(key ONE MANY)
file(WRITE "${CMAKE_BINARY_DIR}/${key}.txt" "${${key}}")
install(FILES "${CMAKE_BINARY_DIR}/${key}.txt" DESTINATION .)
endforeach()
install(FILES "${CMAKE_BINARY_DIR}/include-marker.txt" DESTINATION .)
''', ["dep"], files={"sub/check.cpp": '#include <dep.h>\nint value() { return DEP; }\n'}, subdir="sub")
        leaf.cache.update(ONE="a;b", MANY=["first path", "second path"],
                          CMAKE_PROJECT_INCLUDE=caller.as_posix())
        self.project_modules([dep, leaf])
        self.build()
        output = self.source / "3rdparty/leaf"
        self.assertEqual((output / "ONE.txt").read_text(), "a;b")
        self.assertEqual((output / "MANY.txt").read_text(), "first path;second path")
        self.assertEqual((output / "include-marker.txt").read_text(), "caller include preserved")

    def test_configure_failure_propagates(self):
        dep = self.child("dep")
        leaf = self.child("leaf", 'message(FATAL_ERROR "intentional child failure")', ["dep"])
        self.project_modules([dep, leaf])
        result = self.build(success=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("intentional child failure", result.stdout + result.stderr)
        self.assertFalse((self.source / "3rdparty/leaf/marker").exists())

    def test_cached_targets_keep_dependencies_without_resource_work(self):
        dep = self.child("dep")
        cached = self.child("cached", dependencies=["dep"], metadata='sentinel="marker"\nconfig_dir="lib/cmake/Cached"\n')
        cached.resources["unavailable"] = {"GIT_REPOSITORY": "unavailable", "GIT_TAG": "missing"}
        installed = self.source / "3rdparty/cached"
        installed.mkdir(parents=True)
        (installed / "marker").write_text("cached")
        marker = (self.source / "3rdparty/dep/marker").as_posix()
        leaf = self.child("leaf", f'''if(NOT EXISTS "{marker}")
message(FATAL_ERROR "cached dependency order lost")
endif()
if(NOT cached_DIR STREQUAL "{installed.as_posix()}/lib/cmake/Cached")
message(FATAL_ERROR "cached metadata lost")
endif()
''', ["cached"])
        self.project_modules([dep, cached, leaf],
            'get_target_property(deps cached MANUALLY_ADDED_DEPENDENCIES)\n'
            'file(WRITE "${CMAKE_BINARY_DIR}/cached-deps" "${deps}")\n')
        self.assertEqual((self.context.build_root / "cached-deps").read_text(), "dep")
        self.build()
        self.assertFalse((self.source / "git/_resources/cached/unavailable").exists())
        self.assertTrue((self.source / "3rdparty/leaf/marker").exists())


if __name__ == "__main__":
    unittest.main()
