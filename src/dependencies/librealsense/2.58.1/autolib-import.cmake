#[=[
RealSense：用户态 USB 深度相机共享 SDK，通过 CMake Config 导入。
公共目标：autolib::librealsense。
#]=]

if(TARGET autolib::librealsense)
    return()
endif()

set(realsense2_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/realsense2")
find_package(realsense2 CONFIG REQUIRED)
set_property(TARGET realsense2::realsense2 PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::librealsense INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::librealsense INTERFACE realsense2::realsense2 autolib::nlohmann autolib::lz4 autolib::libusb)
