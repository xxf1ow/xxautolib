cmake_minimum_required(VERSION 3.20)
project(librealsense_check LANGUAGES CXX)

if(NOT REALSENSE_INSTALL_ROOT)
    message(FATAL_ERROR "REALSENSE_INSTALL_ROOT must name this work copy's installation root")
endif()

find_package(lz4 CONFIG REQUIRED
    PATHS "${REALSENSE_INSTALL_ROOT}/lz4/lib/cmake/lz4" NO_DEFAULT_PATH)
find_package(realsense2 2.58.1 EXACT CONFIG REQUIRED
    PATHS "${REALSENSE_INSTALL_ROOT}/librealsense/lib/cmake/realsense2" NO_DEFAULT_PATH)

add_executable(librealsense_check main.cpp)
target_compile_features(librealsense_check PRIVATE cxx_std_17)
target_include_directories(librealsense_check PRIVATE "${REALSENSE_INSTALL_ROOT}/nlohmann/include")
target_link_libraries(librealsense_check PRIVATE realsense2::realsense2)

enable_testing()
add_test(NAME librealsense_check COMMAND librealsense_check)
add_test(NAME librealsense_pkgconfig COMMAND "${CMAKE_COMMAND}"
    "-DREALSENSE_INSTALL_ROOT:PATH=${REALSENSE_INSTALL_ROOT}"
    -P "${CMAKE_CURRENT_SOURCE_DIR}/check_pkgconfig.cmake")
