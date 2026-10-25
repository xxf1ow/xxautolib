cmake_minimum_required(VERSION 3.21)
project(autolib_example LANGUAGES CXX)

set(AUTOLIB_LOCAL_ROOT "" CACHE PATH "Local directory containing AutoLib packages and the host importer")
include("${CMAKE_CURRENT_LIST_DIR}/../src/autolib.cmake")
autolib_import(LOCAL_ROOT "${AUTOLIB_LOCAL_ROOT}" PACKAGES queue flann)

add_executable(autolib_example src/main.cpp)
target_compile_features(autolib_example PRIVATE cxx_std_17)
target_link_libraries(autolib_example PRIVATE
    AutoLib::queue AutoLib::lz4 AutoLib::flann flann::flann)

if(WIN32)
    add_custom_command(TARGET autolib_example POST_BUILD
        COMMAND "${CMAKE_COMMAND}" -E copy_if_different
            $<TARGET_RUNTIME_DLLS:autolib_example> $<TARGET_FILE_DIR:autolib_example>
        COMMAND_EXPAND_LISTS)
endif()

enable_testing()
add_test(NAME autolib_example COMMAND autolib_example)
