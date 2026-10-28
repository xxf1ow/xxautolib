cmake_minimum_required(VERSION 3.20)
find_program(PKG_CONFIG_EXECUTABLE NAMES pkg-config REQUIRED)

execute_process(
    COMMAND "${PKG_CONFIG_EXECUTABLE}" --variable=libdir
        "${REALSENSE_INSTALL_ROOT}/librealsense/lib/pkgconfig/realsense2.pc"
    RESULT_VARIABLE result
    OUTPUT_VARIABLE library_directory
    ERROR_VARIABLE error
    OUTPUT_STRIP_TRAILING_WHITESPACE)
if(NOT result EQUAL 0)
    message(FATAL_ERROR "Installed realsense2 pkg-config lookup failed: ${error}")
endif()

get_filename_component(expected "${REALSENSE_INSTALL_ROOT}/librealsense/lib" ABSOLUTE)
file(TO_CMAKE_PATH "${library_directory}" library_directory)
if(NOT library_directory STREQUAL expected OR NOT IS_DIRECTORY "${library_directory}")
    message(FATAL_ERROR "Installed realsense2 libdir '${library_directory}' must resolve to '${expected}'")
endif()
