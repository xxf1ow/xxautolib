#[=[
FLANN：近似最近邻搜索库，通过 CMake Config 导入。
公共目标：autolib::flann。
#]=]

if(TARGET autolib::flann)
    return()
endif()

set(flann_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/flann")
find_package(flann CONFIG REQUIRED)
set_property(TARGET flann::flann_cpp PROPERTY IMPORTED_GLOBAL TRUE)
set_property(TARGET flann::flann PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::flann INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::flann INTERFACE flann::flann_cpp flann::flann autolib::lz4)
