#[=[
Ruckig：本地实时轨迹生成静态库，通过 CMake Config 导入。
公共目标为 autolib::ruckig。
#]=]

if(TARGET autolib::ruckig)
    return()
endif()

set(ruckig_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/ruckig")
find_package(ruckig CONFIG REQUIRED)
set_property(TARGET ruckig::ruckig PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::ruckig INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::ruckig INTERFACE ruckig::ruckig)
