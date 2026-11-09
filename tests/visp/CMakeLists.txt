cmake_minimum_required(VERSION 3.20)
project(visp_install_check CXX)

enable_testing()
if(AUTOLIB_ENTRY)
    include("${AUTOLIB_ENTRY}")
    add_subdirectory(import)
    add_subdirectory(consume)
    return()
endif()

if(NOT IS_ABSOLUTE "${VISP_INSTALL_PREFIX}")
    message(FATAL_ERROR "VISP_INSTALL_PREFIX must be an absolute installation path")
endif()
if(WIN32)
    set(config_dir "${VISP_INSTALL_PREFIX}")
else()
    set(config_dir "${VISP_INSTALL_PREFIX}/lib/cmake/visp")
endif()
find_package(VISP 3.7.0 EXACT REQUIRED CONFIG COMPONENTS visp_mbt visp_vs
    PATHS "${config_dir}" NO_DEFAULT_PATH)

add_executable(visp_smoke smoke.cpp)
target_compile_features(visp_smoke PRIVATE cxx_std_17)
target_include_directories(visp_smoke PRIVATE ${VISP_INCLUDE_DIRS})
target_link_libraries(visp_smoke PRIVATE ${VISP_LIBRARIES})

add_test(NAME visp_tracking_and_servo
    COMMAND visp_smoke "${CMAKE_CURRENT_SOURCE_DIR}/plane.cao")
