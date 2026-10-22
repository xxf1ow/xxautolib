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
set(CMAKE_INSTALL_PREFIX ${PROJECT_SOURCE_DIR}/3rdparty)
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
    string(REPLACE "," ";" BUILD_MODULES "${BUILD_LIST}")
else()
    file(GLOB TEMP_MODULES CONFIGURE_DEPENDS "${CMAKE_CURRENT_SOURCE_DIR}/src/dependencies/*/module.toml")
    foreach(file_path IN LISTS TEMP_MODULES)
        get_filename_component(module_directory "${file_path}" DIRECTORY)
        get_filename_component(filename "${module_directory}" NAME)
        list(APPEND BUILD_MODULES "${filename}")
    endforeach()
endif()

set(POTENTIAL_LATE_MODULES "sherpa-onnx;fastdeploy;nndeploy")
foreach(LATE_MODULES IN LISTS POTENTIAL_LATE_MODULES)
    list(FIND BUILD_MODULES ${LATE_MODULES} INDEX)
    if(${INDEX} GREATER_EQUAL 0)
        list(REMOVE_ITEM BUILD_MODULES ${LATE_MODULES})
        list(APPEND BUILD_MODULES ${LATE_MODULES})
    endif()
endforeach()

message(STATUS "  [Info] BUILD_MODULES: ${BUILD_MODULES}\n")

file(WRITE "${CMAKE_BINARY_DIR}/tp-manifest.txt" "")
set(TP_INCLUDED "" CACHE INTERNAL "modules already included this configure")
foreach(MODULE IN LISTS BUILD_MODULES)
    string(STRIP "${MODULE}" MODULE)
    if(MODULE IN_LIST TP_INCLUDED)
        continue()
    endif()
    set(_MODFILE "${CMAKE_CURRENT_SOURCE_DIR}/src/dependencies/${MODULE}/module.toml")
    if(NOT EXISTS "${_MODFILE}")
        message(FATAL_ERROR "Module file NOT found: ${_MODFILE}")
    endif()
    list(APPEND TP_INCLUDED "${MODULE}")
    set(TP_INCLUDED "${TP_INCLUDED}" CACHE INTERNAL "")
endforeach()
set(BUILD_MODULES ${TP_INCLUDED})
xxautolib_generate_modules()

message(STATUS "======================================================================")
