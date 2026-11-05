#[=[
PCL：点云处理静态组件库，通过 CMake Config 导入。
公共目标：autolib::pcl。
#]=]

if(TARGET autolib::pcl)
    return()
endif()

set(PCL_DIR "${CMAKE_CURRENT_LIST_DIR}/share/pcl-1.15")
if(WIN32)
    set(PCL_DIR "${CMAKE_CURRENT_LIST_DIR}/cmake")
endif()
set(flann_DIR "${AUTOLIB_PACKAGE_DIR_flann}/lib/cmake/flann")
set(FLANN_ROOT "${AUTOLIB_PACKAGE_DIR_flann}")
set(Eigen3_DIR "${AUTOLIB_PACKAGE_DIR_eigen}/share/eigen3/cmake")
set(Boost_DIR "${AUTOLIB_PACKAGE_DIR_boost}/lib/cmake/Boost-${AUTOLIB_PACKAGE_VERSION_boost}")
set(ZLIB_ROOT "${AUTOLIB_PACKAGE_DIR_zlib}")
set(PCL_FLANN_REQUIRED_TYPE SHARED)
find_package(PCL CONFIG REQUIRED
    COMPONENTS common io filters features registration segmentation sample_consensus kdtree search surface keypoints)
foreach(_autolib_pcl_target IN LISTS PCL_COMPONENTS)
    set_property(TARGET ${_autolib_pcl_target} PROPERTY IMPORTED_GLOBAL TRUE)
endforeach()

add_library(autolib::pcl INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::pcl INTERFACE ${PCL_INCLUDE_DIRS})
target_link_libraries(autolib::pcl INTERFACE ${PCL_LIBRARIES} autolib::lz4 autolib::flann autolib::boost autolib::eigen autolib::zlib)
