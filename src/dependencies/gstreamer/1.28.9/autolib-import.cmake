#[=[
GStreamer：导入包内 core、base、app 和 video 开发库。
公共目标：autolib::gstreamer。
]=]

if(TARGET autolib::gstreamer)
    return()
endif()

set(_gst_root "${CMAKE_CURRENT_LIST_DIR}")
set(_gst_includes "${_gst_root}/include/gstreamer-1.0")
set(_gst_libraries "")
foreach(_gst_component IN ITEMS gstreamer gstbase gstapp gstvideo)
    find_library(_autolib_${_gst_component}_library NAMES "${_gst_component}-1.0"
        PATHS "${_gst_root}/lib" NO_DEFAULT_PATH REQUIRED)
    list(APPEND _gst_libraries "${_autolib_${_gst_component}_library}")
endforeach()

if(CMAKE_SYSTEM_NAME STREQUAL "Linux")
    find_package(PkgConfig REQUIRED)
    pkg_check_modules(_autolib_gst_glib REQUIRED IMPORTED_TARGET GLOBAL glib-2.0 gobject-2.0)
    list(APPEND _gst_libraries PkgConfig::_autolib_gst_glib)
else()
    foreach(_gst_component IN ITEMS glib gobject)
        find_library(_autolib_gst_${_gst_component}_library NAMES "${_gst_component}-2.0"
            PATHS "${_gst_root}/lib" NO_DEFAULT_PATH REQUIRED)
        list(APPEND _gst_libraries "${_autolib_gst_${_gst_component}_library}")
    endforeach()
    list(APPEND _gst_includes "${_gst_root}/include/glib-2.0" "${_gst_root}/lib/glib-2.0/include")
endif()

add_library(autolib::gstreamer INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::gstreamer INTERFACE ${_gst_includes})
target_link_libraries(autolib::gstreamer INTERFACE ${_gst_libraries})
