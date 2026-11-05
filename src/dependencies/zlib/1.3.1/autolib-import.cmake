#[=[
zlib：通用无损压缩库，通过 CMake FindZLIB Module 导入。
公共目标：autolib::zlib。
#]=]

if(TARGET autolib::zlib)
    return()
endif()

set(ZLIB_ROOT "${CMAKE_CURRENT_LIST_DIR}")
find_package(ZLIB MODULE REQUIRED)

set_property(TARGET ZLIB::ZLIB PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::zlib INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::zlib INTERFACE ZLIB::ZLIB)
