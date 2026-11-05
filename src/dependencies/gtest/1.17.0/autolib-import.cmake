#[=[
GoogleTest：C++ 测试框架，通过 CMake Config 导入。
公共目标：autolib::gtest。
#]=]

if(TARGET autolib::gtest)
    return()
endif()

set(GTest_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/GTest")
find_package(GTest CONFIG REQUIRED)
set_property(TARGET GTest::gtest PROPERTY IMPORTED_GLOBAL TRUE)
set_property(TARGET GTest::gtest_main PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::gtest INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::gtest INTERFACE GTest::gtest GTest::gtest_main)
