#[=[
yaml-cpp：YAML 解析与生成库，通过 CMake Config 导入。
公共目标：autolib::yaml-cpp。
#]=]

if(TARGET autolib::yaml-cpp)
    return()
endif()

set(yaml-cpp_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/yaml-cpp")
find_package(yaml-cpp CONFIG REQUIRED)
set_property(TARGET yaml-cpp::yaml-cpp PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::yaml-cpp INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::yaml-cpp INTERFACE yaml-cpp::yaml-cpp)
