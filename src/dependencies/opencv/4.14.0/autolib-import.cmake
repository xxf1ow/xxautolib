#[=[
OpenCV：包含 contrib 与字体绘制的视觉库，通过 CMake Config 导入。
公共目标：autolib::opencv。
#]=]

if(TARGET autolib::opencv)
    return()
endif()

set(OpenCV_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/opencv4")
if(WIN32)
    set(OpenCV_DIR "${CMAKE_CURRENT_LIST_DIR}")
endif()
find_package(OpenCV CONFIG REQUIRED)
foreach(_autolib_opencv_target IN LISTS OpenCV_LIBS)
    set_property(TARGET ${_autolib_opencv_target} PROPERTY IMPORTED_GLOBAL TRUE)
endforeach()

add_library(autolib::opencv INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::opencv INTERFACE ${OpenCV_INCLUDE_DIRS})
target_link_libraries(autolib::opencv INTERFACE ${OpenCV_LIBS} autolib::freetype autolib::harfbuzz)
