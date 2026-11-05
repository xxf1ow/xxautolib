#[=[
nlohmann JSON：JSON 纯头文件库，直接包含头文件。
公共目标：autolib::nlohmann。
#]=]

if(TARGET autolib::nlohmann)
    return()
endif()

add_library(autolib::nlohmann INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::nlohmann INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
