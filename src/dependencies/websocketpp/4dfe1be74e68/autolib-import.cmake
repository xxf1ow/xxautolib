#[=[
WebSocket++：WebSocket 纯头文件库，使用已准备的 Boost 依赖。
公共目标：autolib::websocketpp。
#]=]

if(TARGET autolib::websocketpp)
    return()
endif()

add_library(autolib::websocketpp INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::websocketpp INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
target_link_libraries(autolib::websocketpp INTERFACE autolib::boost)
