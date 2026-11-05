#[=[
HarfBuzz：文本整形引擎，通过 CMake Config 导入。
公共目标：autolib::harfbuzz。
#]=]

if(TARGET autolib::harfbuzz)
    return()
endif()

set(harfbuzz_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/harfbuzz")
find_package(harfbuzz CONFIG REQUIRED)
set_property(TARGET harfbuzz::harfbuzz PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::harfbuzz INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::harfbuzz INTERFACE harfbuzz::harfbuzz autolib::freetype)
