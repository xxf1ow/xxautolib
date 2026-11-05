#[=[
ZeroMQ：消息通信引擎，通过 CMake Config 导入。
公共目标：autolib::libzmq。
#]=]

if(TARGET autolib::libzmq)
    return()
endif()

set(ZeroMQ_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/ZeroMQ")
if(WIN32)
    set(ZeroMQ_DIR "${CMAKE_CURRENT_LIST_DIR}/CMake")
endif()
find_package(ZeroMQ CONFIG REQUIRED)
set_property(TARGET libzmq PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::libzmq INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::libzmq INTERFACE libzmq)
