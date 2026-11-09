#[=[
Eigen：线性代数头文件库，通过本包 Config 复用上游目标及其使用要求。
公共目标：autolib::eigen。
#]=]

if(TARGET autolib::eigen)
    return()
endif()

find_package(Eigen3 CONFIG REQUIRED PATHS "${CMAKE_CURRENT_LIST_DIR}/share/eigen3/cmake" NO_DEFAULT_PATH)
set_property(TARGET Eigen3::Eigen PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::eigen INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::eigen INTERFACE Eigen3::Eigen)
