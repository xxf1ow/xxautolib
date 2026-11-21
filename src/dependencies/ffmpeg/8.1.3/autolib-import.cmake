#[=[
FFmpeg：共享编解码器、封装和网络媒体库，通过包内 CMake Config 导入。
公共目标：autolib::ffmpeg。
#]=]

if(TARGET autolib::ffmpeg)
    return()
endif()

set(FFMPEG_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/FFMPEG")
find_package(FFMPEG CONFIG REQUIRED PATHS "${FFMPEG_DIR}" NO_DEFAULT_PATH)
add_library(autolib::ffmpeg INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::ffmpeg INTERFACE ${FFMPEG_TARGETS})
