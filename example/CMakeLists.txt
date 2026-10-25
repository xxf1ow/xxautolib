cmake_minimum_required(VERSION 3.21.0)
set(CURRENT_TARGET_NAME autolib_example)
project(${CURRENT_TARGET_NAME} LANGUAGES C CXX)

add_executable(${CURRENT_TARGET_NAME})

#### >>>>> Configure Source Code <<<<< ######################################
aux_source_directory(${CMAKE_CURRENT_SOURCE_DIR}/src CURRENT_SRC_LIST)
target_sources(${CURRENT_TARGET_NAME} PRIVATE ${CURRENT_SRC_LIST})

#### >>>>> Configure Target <<<<< ###########################################
include("${CMAKE_CURRENT_LIST_DIR}/3rdparty/autolib.cmake")
autolib_import(TARGET ${CURRENT_TARGET_NAME} PRIVATE queue flann)
