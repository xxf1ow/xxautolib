#[=[
FreeType：字体加载与光栅化静态引擎，通过 CMake Config 导入。
公共目标：autolib::freetype。
#]=]

if(TARGET autolib::freetype)
    return()
endif()

set(freetype_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/freetype")
find_package(freetype CONFIG REQUIRED)
set_property(TARGET freetype PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::freetype INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::freetype INTERFACE freetype autolib::zlib)
