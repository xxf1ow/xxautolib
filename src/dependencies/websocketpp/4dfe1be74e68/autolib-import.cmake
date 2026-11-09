#[=[
WebSocket++：WebSocket 纯头文件库，直接包含头文件；传输后端由消费者选择。
公共目标：autolib::websocketpp。
#]=]

if(TARGET autolib::websocketpp)
    return()
endif()

add_library(autolib::websocketpp INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::websocketpp INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
if(WIN32)
    target_link_libraries(autolib::websocketpp INTERFACE ws2_32)
endif()
