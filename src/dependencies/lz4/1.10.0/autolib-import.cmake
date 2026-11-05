#[=[
LZ4：高速无损压缩静态库，通过 CMake Config 导入。
公共目标：autolib::lz4。
#]=]

if(TARGET autolib::lz4)
    return()
endif()

set(lz4_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/lz4")
find_package(lz4 CONFIG REQUIRED)
set_property(TARGET LZ4::lz4 PROPERTY IMPORTED_GLOBAL TRUE)
set_property(TARGET LZ4::lz4_static PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::lz4 INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::lz4 INTERFACE LZ4::lz4)
