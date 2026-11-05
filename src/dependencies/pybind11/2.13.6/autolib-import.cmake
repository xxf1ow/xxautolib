#[=[
pybind11：Python/C++ 绑定头文件库，直接包含头文件。
公共目标：autolib::pybind11。
#]=]

if(TARGET autolib::pybind11)
    return()
endif()

add_library(autolib::pybind11 INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::pybind11 INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
