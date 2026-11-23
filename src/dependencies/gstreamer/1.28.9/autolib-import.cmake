#[=[
GStreamer 聚合包内 .pc 声明的 SDK 链接库，公共目标为 autolib::gstreamer。
]=]

if(TARGET autolib::gstreamer)
    return()
endif()

set(_gst_root "${CMAKE_CURRENT_LIST_DIR}")
set(_gst_includes "${_gst_root}/include/gstreamer-1.0")
if(IS_DIRECTORY "${_gst_root}/lib/gstreamer-1.0/include")
    list(APPEND _gst_includes "${_gst_root}/lib/gstreamer-1.0/include")
endif()

file(GLOB _gst_pc_files "${_gst_root}/lib/pkgconfig/gstreamer*-1.0.pc")
set(_gst_components "")
foreach(_gst_pc IN LISTS _gst_pc_files)
    file(STRINGS "${_gst_pc}" _gst_link_flags REGEX "^Libs:")
    separate_arguments(_gst_link_flags UNIX_COMMAND "${_gst_link_flags}")
    foreach(_gst_flag IN LISTS _gst_link_flags)
        if(_gst_flag MATCHES "^-l(gst[A-Za-z0-9_-]*-1\\.0)$")
            list(APPEND _gst_components "${CMAKE_MATCH_1}")
        endif()
    endforeach()
endforeach()
if(NOT "gstreamer-1.0" IN_LIST _gst_components)
    message(FATAL_ERROR "No GStreamer core link entry in ${_gst_root}/lib/pkgconfig")
endif()
list(REMOVE_DUPLICATES _gst_components)

set(_gst_libraries "")
foreach(_gst_component IN LISTS _gst_components)
    find_library(_autolib_${_gst_component}_library NAMES "${_gst_component}"
        PATHS "${_gst_root}/lib" NO_DEFAULT_PATH REQUIRED)
    list(APPEND _gst_libraries "${_autolib_${_gst_component}_library}")
endforeach()

if(CMAKE_SYSTEM_NAME STREQUAL "Linux")
    find_package(PkgConfig REQUIRED)
    pkg_check_modules(_autolib_gst_glib REQUIRED IMPORTED_TARGET GLOBAL glib-2.0 gobject-2.0 gio-2.0 gmodule-no-export-2.0)
    list(APPEND _gst_libraries PkgConfig::_autolib_gst_glib)
else()
    foreach(_gst_component IN ITEMS glib gobject gio gmodule)
        find_library(_autolib_gst_${_gst_component}_library NAMES "${_gst_component}-2.0"
            PATHS "${_gst_root}/lib" NO_DEFAULT_PATH REQUIRED)
        list(APPEND _gst_libraries "${_autolib_gst_${_gst_component}_library}")
    endforeach()
    list(APPEND _gst_includes "${_gst_root}/include/glib-2.0" "${_gst_root}/lib/glib-2.0/include")
endif()

add_library(autolib::gstreamer INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::gstreamer INTERFACE ${_gst_includes})
target_link_libraries(autolib::gstreamer INTERFACE ${_gst_libraries})
