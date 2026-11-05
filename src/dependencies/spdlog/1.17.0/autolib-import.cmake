#[=[
spdlog：日志库，通过 CMake Config 导入。
公共目标：autolib::spdlog。
#]=]

if(TARGET autolib::spdlog)
    return()
endif()

set(spdlog_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/spdlog")
find_package(spdlog CONFIG REQUIRED)
set_property(TARGET spdlog::spdlog PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::spdlog INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::spdlog INTERFACE spdlog::spdlog)
