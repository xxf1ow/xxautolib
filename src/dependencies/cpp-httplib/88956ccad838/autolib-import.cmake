#[=[
cpp-httplib：HTTP 纯头文件库，直接包含头文件。
公共目标：autolib::cpp-httplib。
#]=]

if(TARGET autolib::cpp-httplib)
    return()
endif()

if(NOT TARGET Threads::Threads)
    find_package(Threads REQUIRED)
    set_property(TARGET Threads::Threads PROPERTY IMPORTED_GLOBAL TRUE)
endif()

add_library(autolib::cpp-httplib INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::cpp-httplib INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
target_link_libraries(autolib::cpp-httplib INTERFACE Threads::Threads)
