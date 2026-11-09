package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUniversalCMakeMetadata(t *testing.T) {
	f := newEntryFixture(t)
	source := `cmake_minimum_required(VERSION 3.20)
project(Portable NONE)
add_library(Portable INTERFACE)
target_include_directories(Portable INTERFACE $<INSTALL_INTERFACE:include>)
target_compile_features(Portable INTERFACE cxx_std_14)
install(TARGETS Portable EXPORT PortableTargets)
install(EXPORT PortableTargets NAMESPACE Portable:: DESTINATION share/portable)
install(FILES include/portable.h DESTINATION include)
install(FILES PortableConfig.cmake DESTINATION share/portable)
`
	repository := f.repository("portable", map[string]string{"CMakeLists.txt": source, "include/portable.h": "#pragma once\ninline auto answer(){return 42;}\n", "PortableConfig.cmake": "include(\"${CMAKE_CURRENT_LIST_DIR}/PortableTargets.cmake\")\n"})
	f.definition("portable", "cmake", "package_platforms=['any-any']\nconfig_dir='share/portable'\npackage_name='Portable'\n[source]\n"+repository)
	adapter := `find_package(Portable CONFIG REQUIRED PATHS "${CMAKE_CURRENT_LIST_DIR}/share/portable" NO_DEFAULT_PATH)
set_property(TARGET Portable::Portable PROPERTY IMPORTED_GLOBAL TRUE)
add_library(autolib::portable INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::portable INTERFACE Portable::Portable)
`
	putFile(t, filepath.Join(f.root, "src/dependencies/portable/1.0/autolib-import.cmake"), adapter)
	f.mustEntry("-DBUILD_LIST=portable")
	f.build()
	consume := func(prefix, label string) {
		dir := filepath.Join(f.root, "consumer-"+label)
		putFile(t, filepath.Join(dir, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.20)
project(consume CXX)
include("`+filepath.ToSlash(filepath.Join(prefix, "portable/1.0/autolib-import.cmake"))+`")
add_executable(consume main.cpp)
target_link_libraries(consume PRIVATE autolib::portable)
`)
		putFile(t, filepath.Join(dir, "main.cpp"), "#include <portable.h>\nint main(){return answer()==42?0:1;}\n")
		build := filepath.Join(dir, "build")
		f.must(f.context.CMakeCommand, "-S", dir, "-B", build, "-DCMAKE_CXX_STANDARD=11")
		f.must(f.context.CMakeCommand, "--build", build, "--config", "Release")
		executable := filepath.Join(build, "consume")
		if f.context.SystemName == "Windows" {
			executable = filepath.Join(build, "Release/consume.exe")
		}
		f.must(executable)
	}
	install := filepath.Join(f.root, "3rdparty")
	consume(install, "direct")
	script := readFile(t, "../autolib-importer/fixup-metadata.cmake")
	putFile(t, filepath.Join(f.root, "src/autolib-importer/fixup-metadata.cmake"), script)
	stage := filepath.Join(f.root, "build/stage")
	system := map[string]string{"Windows": "windows", "Linux": "linux", "Darwin": "darwin"}[f.context.SystemName]
	if _, err := stagePackage(stageOptions{SourceRoot: f.root, InstallRoot: install, StageRoot: stage, Name: "portable", Version: "1.0", OS: system, Arch: f.context.Architecture, CMakeCommand: f.context.CMakeCommand}); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(f.root, "moved")
	if err := os.Rename(stage, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(install, install+"-hidden"); err != nil {
		t.Fatal(err)
	}
	consume(moved, "relocated")
}
