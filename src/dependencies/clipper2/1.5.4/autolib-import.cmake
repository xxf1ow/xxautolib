#[=[
Clipper2：多边形布尔运算库，通过 CMake Config 导入。
公共目标：autolib::clipper2。
#]=]

if(TARGET autolib::clipper2)
    return()
endif()

set(Clipper2_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/clipper2")
find_package(Clipper2 CONFIG REQUIRED)
set_property(TARGET Clipper2::Clipper2 PROPERTY IMPORTED_GLOBAL TRUE)
set_property(TARGET Clipper2::Clipper2Z PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::clipper2 INTERFACE IMPORTED GLOBAL)
# 默认使用普通坐标域；Z 坐标消费者可显式链接 Clipper2::Clipper2Z。
target_link_libraries(autolib::clipper2 INTERFACE Clipper2::Clipper2)
