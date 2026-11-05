#[=[
expected：显式结果类型纯头文件库，直接包含头文件。
公共目标：autolib::expected。
#]=]

if(TARGET autolib::expected)
    return()
endif()

add_library(autolib::expected INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::expected INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
