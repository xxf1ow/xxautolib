cmake_minimum_required(VERSION 3.20)
project(ffmpeg-build LANGUAGES C)

set(ffmpeg_build "${CMAKE_CURRENT_BINARY_DIR}/ffmpeg")
file(MAKE_DIRECTORY "${ffmpeg_build}")
set(ffmpeg_options
    "--prefix=${CMAKE_INSTALL_PREFIX}"
    --enable-shared --disable-static --disable-autodetect
    --enable-network --enable-demuxer=rtsp
    --disable-avdevice --disable-avfilter --disable-programs --disable-doc --disable-debug)
if(WIN32)
    find_program(ffmpeg_shell NAMES bash PATHS "$ENV{FFMPEG_MSYS2_ROOT}/usr/bin" REQUIRED NO_DEFAULT_PATH)
    find_program(ffmpeg_make NAMES make PATHS "$ENV{FFMPEG_MSYS2_ROOT}/usr/bin" REQUIRED NO_DEFAULT_PATH)
    find_program(ffmpeg_nasm NAMES nasm PATHS "${FFMPEG_NASM_SOURCE}" REQUIRED NO_DEFAULT_PATH)
    get_filename_component(msvc_tools "${CMAKE_C_COMPILER}" DIRECTORY)
    string(REPLACE ";" "\\;" ffmpeg_path "${msvc_tools};$ENV{FFMPEG_MSYS2_ROOT}/usr/bin;$ENV{PATH}")
    set(ffmpeg_environment "${CMAKE_COMMAND}" -E env "PATH=${ffmpeg_path}")
    list(APPEND ffmpeg_options --toolchain=msvc "--x86asmexe=${ffmpeg_nasm}")
    set(ffmpeg_make_args -j4)
else()
    find_program(ffmpeg_shell NAMES sh REQUIRED)
    set(ffmpeg_make "${CMAKE_MAKE_PROGRAM}")
    list(APPEND ffmpeg_options "--cc=${CMAKE_C_COMPILER}")
    if(FFMPEG_NASM_SOURCE)
        if(NOT EXISTS "${FFMPEG_NASM_SOURCE}/nasm")
            execute_process(COMMAND "${CMAKE_COMMAND}" -E env "CC=${CMAKE_C_COMPILER}"
                "${ffmpeg_shell}" ./configure WORKING_DIRECTORY "${FFMPEG_NASM_SOURCE}" COMMAND_ERROR_IS_FATAL ANY)
            execute_process(COMMAND "${ffmpeg_make}" -j4 nasm
                WORKING_DIRECTORY "${FFMPEG_NASM_SOURCE}" COMMAND_ERROR_IS_FATAL ANY)
        endif()
        list(APPEND ffmpeg_options "--x86asmexe=${FFMPEG_NASM_SOURCE}/nasm")
    endif()
    if(APPLE)
        set(ffmpeg_apple_flags "-isysroot ${CMAKE_OSX_SYSROOT} -mmacosx-version-min=${CMAKE_OSX_DEPLOYMENT_TARGET}")
        list(APPEND ffmpeg_options "--extra-cflags=${ffmpeg_apple_flags}" "--extra-ldflags=${ffmpeg_apple_flags}"
            "--install-name-dir=@rpath" "--extra-ldsoflags=-Wl,-rpath,@loader_path")
    elseif(CMAKE_SYSTEM_NAME STREQUAL "Linux")
        # Make 和 shell 展开后，链接器仍收到动态加载器的 $ORIGIN。
        list(APPEND ffmpeg_options [=[--extra-ldsoflags=-Wl,-rpath,\$$ORIGIN]=])
    endif()
    if(CMAKE_VERSION VERSION_GREATER_EQUAL 3.28)
        set(ffmpeg_jobserver JOB_SERVER_AWARE TRUE)
    endif()
endif()

message(STATUS "Configuring FFmpeg")
execute_process(COMMAND "${CMAKE_COMMAND}" -E time ${ffmpeg_environment}
    "${ffmpeg_shell}" "${CMAKE_CURRENT_SOURCE_DIR}/configure" ${ffmpeg_options}
    WORKING_DIRECTORY "${ffmpeg_build}" COMMAND_ERROR_IS_FATAL ANY)

add_custom_target(ffmpeg-libraries ALL
    COMMAND "${CMAKE_COMMAND}" -E time ${ffmpeg_environment} "${ffmpeg_make}" ${ffmpeg_make_args}
    WORKING_DIRECTORY "${ffmpeg_build}" ${ffmpeg_jobserver} VERBATIM)

configure_file(install-ffmpeg.cmake.in install-ffmpeg.cmake @ONLY)
install(SCRIPT "${CMAKE_CURRENT_BINARY_DIR}/install-ffmpeg.cmake")
install(FILES FFMPEGConfig.cmake DESTINATION lib/cmake/FFMPEG)
install(FILES COPYING.LGPLv2.1 LICENSE.md DESTINATION share/licenses/ffmpeg)
