#[=[
Eigen：线性代数纯头文件库，直接包含头文件。
公共目标：autolib::eigen。
#]=]

if(TARGET autolib::eigen)
    return()
endif()

add_library(autolib::eigen INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::eigen INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
