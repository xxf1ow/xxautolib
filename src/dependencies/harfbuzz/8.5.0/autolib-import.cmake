#[=[
HarfBuzz：文本整形引擎，通过 pkg-config 导入。
公共目标：autolib::harfbuzz。
此版本的 Config 导出错误的包含目录；静态依赖由 FreeType、Threads 与 macOS 系统 framework 补齐。
#]=]

if(TARGET autolib::harfbuzz)
    return()
endif()

list(PREPEND CMAKE_PREFIX_PATH "${CMAKE_CURRENT_LIST_DIR}" "${AUTOLIB_PACKAGE_DIR_freetype}" "${AUTOLIB_PACKAGE_DIR_zlib}")
find_package(PkgConfig REQUIRED)
pkg_check_modules(_autolib_harfbuzz REQUIRED IMPORTED_TARGET GLOBAL harfbuzz)
find_package(Threads REQUIRED)

add_library(autolib::harfbuzz INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::harfbuzz INTERFACE PkgConfig::_autolib_harfbuzz autolib::freetype Threads::Threads)
if(APPLE)
    # 上游 .pc 的 Libs.private 声明此 framework；普通 pkg-config 导入目标不携带静态链接项。
    find_library(_autolib_harfbuzz_application_services ApplicationServices REQUIRED)
    target_link_libraries(autolib::harfbuzz INTERFACE "${_autolib_harfbuzz_application_services}")
endif()
