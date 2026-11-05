#[=[
PortAudio：跨平台音频输入输出库，通过 CMake Config 导入。
公共目标：autolib::portaudio。
#]=]

if(TARGET autolib::portaudio)
    return()
endif()

set(portaudio_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/portaudio")
find_package(portaudio CONFIG REQUIRED)
set_property(TARGET portaudio PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::portaudio INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::portaudio INTERFACE portaudio)
