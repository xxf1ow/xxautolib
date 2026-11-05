#[=[
Taskflow：任务调度纯头文件库，直接包含头文件。
公共目标：autolib::taskflow。
#]=]

if(TARGET autolib::taskflow)
    return()
endif()

if(NOT TARGET Threads::Threads)
    find_package(Threads REQUIRED)
    set_property(TARGET Threads::Threads PROPERTY IMPORTED_GLOBAL TRUE)
endif()

add_library(autolib::taskflow INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::taskflow INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
target_link_libraries(autolib::taskflow INTERFACE Threads::Threads)
