cmake_minimum_required(VERSION 3.20)
project(autolib_harfbuzz_adapter_check LANGUAGES C)

get_filename_component(repository "${CMAKE_CURRENT_LIST_DIR}/../../.." ABSOLUTE)
set(harfbuzz_prefix "${CMAKE_CURRENT_BINARY_DIR}/packages/harfbuzz/8.5.0")
set(AUTOLIB_PACKAGE_DIR_freetype "${CMAKE_CURRENT_BINARY_DIR}/packages/freetype/2.13.2")

# 排除系统包，确保 HarfBuzz 的 .pc 依赖由本次准备的 FreeType 满足。
set(ENV{PKG_CONFIG_PATH} "")
set(ENV{PKG_CONFIG_LIBDIR} "${CMAKE_CURRENT_BINARY_DIR}/no-system-packages")
file(MAKE_DIRECTORY "$ENV{PKG_CONFIG_LIBDIR}"
    "${harfbuzz_prefix}/include/harfbuzz" "${harfbuzz_prefix}/lib/pkgconfig"
    "${AUTOLIB_PACKAGE_DIR_freetype}/include/freetype2" "${AUTOLIB_PACKAGE_DIR_freetype}/lib/pkgconfig")
file(WRITE "${harfbuzz_prefix}/lib/pkgconfig/harfbuzz.pc"
    "Name: harfbuzz\nDescription: Adapter fixture\nVersion: 8.5.0\nRequires.private: freetype2 >= 12.0.6\nCflags: -I${harfbuzz_prefix}/include/harfbuzz\n")
file(WRITE "${AUTOLIB_PACKAGE_DIR_freetype}/lib/pkgconfig/freetype2.pc"
    "Name: freetype2\nDescription: Adapter dependency fixture\nVersion: 26.1.20\nCflags: -I${AUTOLIB_PACKAGE_DIR_freetype}/include/freetype2\n")
configure_file("${repository}/src/dependencies/harfbuzz/8.5.0/autolib-import.cmake"
    "${harfbuzz_prefix}/autolib-import.cmake" COPYONLY)
add_library(autolib::freetype INTERFACE IMPORTED GLOBAL)
file(MAKE_DIRECTORY "${CMAKE_CURRENT_BINARY_DIR}/importer")
file(WRITE "${CMAKE_CURRENT_BINARY_DIR}/importer/CMakeLists.txt"
    "include(\"${harfbuzz_prefix}/autolib-import.cmake\")\ninclude(\"${harfbuzz_prefix}/autolib-import.cmake\")\n")
add_subdirectory("${CMAKE_CURRENT_BINARY_DIR}/importer" imports)

get_target_property(includes PkgConfig::_autolib_harfbuzz INTERFACE_INCLUDE_DIRECTORIES)
if(NOT "${AUTOLIB_PACKAGE_DIR_freetype}/include/freetype2" IN_LIST includes)
    message(FATAL_ERROR "HarfBuzz must resolve the prepared FreeType pkg-config metadata: ${includes}")
endif()
get_target_property(links autolib::harfbuzz INTERFACE_LINK_LIBRARIES)
if(NOT "autolib::freetype" IN_LIST links)
    message(FATAL_ERROR "HarfBuzz must propagate autolib::freetype")
endif()
