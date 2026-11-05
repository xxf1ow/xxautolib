#[=[
libusb：跨平台 USB 访问静态库，通过 find_library 导入。
公共目标：autolib::libusb。
#]=]

if(TARGET autolib::libusb)
    return()
endif()

find_library(_autolib_libusb_library NAMES usb-1.0 libusb-1.0 HINTS "${CMAKE_CURRENT_LIST_DIR}/lib" REQUIRED)

add_library(autolib::libusb INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::libusb INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include/libusb-1.0")
target_link_libraries(autolib::libusb INTERFACE "${_autolib_libusb_library}")
if(WIN32)
    target_link_libraries(autolib::libusb INTERFACE windowsapp)
elseif(CMAKE_SYSTEM_NAME STREQUAL "Linux")
    if(NOT TARGET Threads::Threads)
        find_package(Threads REQUIRED)
        set_property(TARGET Threads::Threads PROPERTY IMPORTED_GLOBAL TRUE)
    endif()
    find_library(_autolib_libusb_udev_library NAMES udev REQUIRED)
    target_link_libraries(autolib::libusb INTERFACE "${_autolib_libusb_udev_library}" Threads::Threads)
endif()
