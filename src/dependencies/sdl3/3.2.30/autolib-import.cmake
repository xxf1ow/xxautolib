#[=[
SDL3：跨平台多媒体静态库，通过 CMake Config 导入。
公共目标：autolib::sdl3。
#]=]

if(TARGET autolib::sdl3)
    return()
endif()

set(SDL3_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/SDL3")
if(WIN32)
    set(SDL3_DIR "${CMAKE_CURRENT_LIST_DIR}/cmake")
endif()
find_package(SDL3 CONFIG REQUIRED)
set_property(TARGET SDL3::SDL3-static PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::sdl3 INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::sdl3 INTERFACE SDL3::SDL3-static)
