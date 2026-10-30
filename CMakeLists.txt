cmake_minimum_required(VERSION 3.20)
project(xxautolib)

# 查看 sha256: certutil -hashfile ./file_path sha256
# ExternalProject: mkdir, download, update, patch, configure, build, install, test
# 问题: Failed to checkout tag: 'origin/release-3.2.x', 解决: git switch release-3.2.x

## >>>>> 设置编译模式/编码格式/生成目录 <<<<< ######################################
# 设置MSVC编码格式 (要设置 MSVC 版本, 需要手动在 CMD 复制命令行中打印的 cmake 命令, 并修改 -T v142 表示使用 VS2019)
if(CMAKE_HOST_WIN32 AND CMAKE_CXX_COMPILER_ID STREQUAL "MSVC")
    add_compile_options("$<$<CXX_COMPILER_ID:MSVC>:/source-charset:utf-8>")
endif()
# Configure defult build type
if(NOT CMAKE_BUILD_TYPE)
    set(CMAKE_BUILD_TYPE "Release" CACHE STRING "Choose the type of build." FORCE)
endif()
# 设置从URL下载的外部项目, 解压出来的文件的时间戳, 使用提取时的时间戳, 而不是压缩包中原有的时间戳
if(CMAKE_VERSION VERSION_GREATER_EQUAL "3.24.0")
    cmake_policy(SET CMP0135 NEW)
endif()
# set CMAKE_INSTALL_PREFIX
if(DEFINED ENV{AUTOLIB_INSTALL_ROOT} AND NOT "$ENV{AUTOLIB_INSTALL_ROOT}" STREQUAL "")
    get_filename_component(CMAKE_INSTALL_PREFIX "$ENV{AUTOLIB_INSTALL_ROOT}" ABSOLUTE BASE_DIR "${PROJECT_SOURCE_DIR}")
else()
    set(CMAKE_INSTALL_PREFIX "${PROJECT_SOURCE_DIR}/3rdparty")
endif()
set(CMAKE_GIT_REPOSITORY ${PROJECT_SOURCE_DIR}/git)
# print information
string(TIMESTAMP CODE_COMPILE_DATE "%Y-%m-%d")
message(STATUS "========================== Building Summary ==========================")
message(STATUS "  [Info] Compile Date      : ${CODE_COMPILE_DATE}")
message(STATUS "  [Info] CMake Version     : ${CMAKE_VERSION}")
message(STATUS "  [Info] Operation System  : ${CMAKE_SYSTEM}")
message(STATUS "  [Info] CPU Architecture  : ${CMAKE_SYSTEM_PROCESSOR}")
message(STATUS "  [Info] Generator Toolset : ${CMAKE_GENERATOR} ${CMAKE_GENERATOR_TOOLSET} ${CMAKE_GENERATOR_PLATFORM}")
message(STATUS "  [Info] Platform Toolset  : ${CMAKE_CXX_COMPILER_ID} ${CMAKE_CXX_COMPILER_VERSION}")
message(STATUS "  [Info] Install Directory : ${CMAKE_INSTALL_PREFIX}")
message(STATUS "======================================================================")

# setting third party
include(ExternalProject)
include(src/autolib-builder/index.cmake)
message(STATUS "========================= Third Party Summary ========================")

set(BUILD_MODULES)
if(DEFINED BUILD_LIST AND NOT "${BUILD_LIST}" STREQUAL "")
    string(REPLACE "," ";" requested_modules "${BUILD_LIST}")
    foreach(module IN LISTS requested_modules)
        string(STRIP "${module}" module)
        if(NOT "${module}" STREQUAL "")
            list(APPEND BUILD_MODULES "${module}")
        endif()
    endforeach()
endif()

message(STATUS "  [Info] BUILD_MODULES: ${BUILD_MODULES}\n")

xxautolib_generate_modules()

message(STATUS "======================================================================")
