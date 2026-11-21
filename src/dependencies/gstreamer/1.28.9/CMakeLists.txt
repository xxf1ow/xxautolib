cmake_minimum_required(VERSION 3.20)
project(gstreamer-build LANGUAGES C CXX)

find_package(Python3 3.8 REQUIRED COMPONENTS Interpreter)
find_package(FFMPEG CONFIG REQUIRED NO_DEFAULT_PATH)
get_filename_component(ffmpeg_prefix "${FFMPEG_LIBRARY_DIR}/.." ABSOLUTE)

set(driver "${CMAKE_CURRENT_SOURCE_DIR}/build-gstreamer.py")
set(args
    --source "${CMAKE_CURRENT_SOURCE_DIR}"
    --work "${CMAKE_CURRENT_BINARY_DIR}"
    --prefix "${CMAKE_INSTALL_PREFIX}"
    --ffmpeg "${ffmpeg_prefix}"
    --meson "${GSTREAMER_MESON_SOURCE}"
    --orc "${GSTREAMER_ORC_SOURCE}"
    --vcpkg "${GSTREAMER_VCPKG_SOURCE}"
    --cmake "${CMAKE_COMMAND}"
    --make "${CMAKE_MAKE_PROGRAM}"
    --cc "${CMAKE_C_COMPILER}"
    --cxx "${CMAKE_CXX_COMPILER}")
if(GSTREAMER_BINUTILS_SOURCE)
    list(APPEND args --binutils "${GSTREAMER_BINUTILS_SOURCE}")
endif()
if(APPLE)
    list(APPEND args --sdk "${CMAKE_OSX_SYSROOT}" --deployment "${CMAKE_OSX_DEPLOYMENT_TARGET}")
endif()

execute_process(COMMAND "${Python3_EXECUTABLE}" "${driver}" configure ${args} COMMAND_ERROR_IS_FATAL ANY)
add_custom_target(gstreamer-build ALL
    COMMAND "${Python3_EXECUTABLE}" "${driver}" build ${args} VERBATIM)
set(install_command "execute_process(COMMAND [==[${Python3_EXECUTABLE}]==] [==[${driver}]==] install")
foreach(arg IN LISTS args)
    string(APPEND install_command " [==[${arg}]==]")
endforeach()
install(CODE "${install_command} COMMAND_ERROR_IS_FATAL ANY)")
