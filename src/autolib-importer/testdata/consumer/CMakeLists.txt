cmake_minimum_required(VERSION 3.20)
project(autolib_consumer NONE)
include("${CMAKE_CURRENT_LIST_DIR}/autolib.cmake")
autolib_import(URI "${FIXTURE_RELEASE}" WORK_ROOT "${FIXTURE_WORK}"
    TARGET_OS "${FIXTURE_OS}" TARGET_ARCH "${FIXTURE_ARCH}" PACKAGES demo demo)
if(NOT TARGET AutoLib::demo)
    message(FATAL_ERROR "missing prepared target AutoLib::demo")
endif()
file(WRITE "${CMAKE_BINARY_DIR}/observed.txt" "${AUTOLIB_PACKAGE_DIR_demo}")
autolib_import(URI "${FIXTURE_RELEASE}" WORK_ROOT "${FIXTURE_WORK}"
    TARGET_OS "${FIXTURE_OS}" TARGET_ARCH "${FIXTURE_ARCH}" PACKAGES demo)
