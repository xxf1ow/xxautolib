#[=[
ViSP：模型跟踪与视觉伺服动态库，通过 CMake Config 导入。
公共目标：autolib::visp，包含本包构建的全部公开模块。
#]=]

if(TARGET autolib::visp)
    return()
endif()

set(VISP_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/visp")
if(WIN32)
    set(VISP_DIR "${CMAKE_CURRENT_LIST_DIR}")
endif()
find_package(VISP 3.7.0 EXACT CONFIG REQUIRED)
foreach(_autolib_visp_target IN LISTS VISP_LIB_COMPONENTS)
    set_property(TARGET ${_autolib_visp_target} PROPERTY IMPORTED_GLOBAL TRUE)
endforeach()

add_library(autolib::visp INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::visp INTERFACE ${VISP_INCLUDE_DIRS})
target_link_libraries(autolib::visp INTERFACE ${VISP_LIBRARIES} autolib::opencv)
