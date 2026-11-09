#[=[
Asio：异步网络纯头文件库，直接包含头文件。
公共目标：autolib::asio。
#]=]

if(TARGET autolib::asio)
    return()
endif()

if(NOT TARGET Threads::Threads)
    find_package(Threads REQUIRED)
    set_property(TARGET Threads::Threads PROPERTY IMPORTED_GLOBAL TRUE)
endif()

add_library(autolib::asio INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::asio INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
target_link_libraries(autolib::asio INTERFACE Threads::Threads)
