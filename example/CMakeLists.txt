cmake_minimum_required(VERSION 3.21.0)
set(CURRENT_TARGET_NAME autolib_example)
project(${CURRENT_TARGET_NAME} LANGUAGES C CXX)


add_executable(${CURRENT_TARGET_NAME})
aux_source_directory(${CMAKE_CURRENT_SOURCE_DIR}/src CURRENT_SRC_LIST)
target_sources(${CURRENT_TARGET_NAME} PRIVATE ${CURRENT_SRC_LIST})


message(STATUS "========================= Third Party Summary ========================")
set(AUTOLIB_URL "https://gitee.com/xxf1ow/autolib-release/releases/download/autolib-release/autolib.cmake")
set(AUTOLIB_PATH "${CMAKE_CURRENT_LIST_DIR}/3rdparty/autolib.cmake")
if(NOT EXISTS ${AUTOLIB_PATH})
    file(DOWNLOAD ${AUTOLIB_URL} ${AUTOLIB_PATH} TLS_VERIFY ON STATUS AUTOLIB_DOWNLOAD_STATUS)
    list(GET AUTOLIB_DOWNLOAD_STATUS 0 AUTOLIB_DOWNLOAD_CODE)
    if(NOT AUTOLIB_DOWNLOAD_CODE EQUAL 0)
        file(REMOVE ${AUTOLIB_PATH})
        message(FATAL_ERROR "autolib.cmake download failed: ${AUTOLIB_DOWNLOAD_STATUS}")
    endif()
endif()
include(${AUTOLIB_PATH})
autolib_import(TARGET ${CURRENT_TARGET_NAME} PRIVATE queue flann)
message(STATUS "======================================================================")
