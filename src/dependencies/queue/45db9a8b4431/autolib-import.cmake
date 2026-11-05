#[=[
Queue：并发队列纯头文件集合，直接包含头文件。
公共目标：autolib::queue。
#]=]

if(TARGET autolib::queue)
    return()
endif()

add_library(autolib::queue INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::queue INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
