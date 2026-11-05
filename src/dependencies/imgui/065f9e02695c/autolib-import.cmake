#[=[
ImGui：包含 SDL3/OpenGL3 后端的即时图形界面静态库，通过 find_library 导入。
公共目标：autolib::imgui。
#]=]

if(TARGET autolib::imgui)
    return()
endif()

find_library(_autolib_imgui_library NAMES imgui HINTS "${CMAKE_CURRENT_LIST_DIR}/lib" REQUIRED)

add_library(autolib::imgui INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::imgui INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include" "${CMAKE_CURRENT_LIST_DIR}/include/ImGui")
target_link_libraries(autolib::imgui INTERFACE "${_autolib_imgui_library}" autolib::sdl3)
if(WIN32)
    target_link_libraries(autolib::imgui INTERFACE shell32 imm32 winmm gdi32 setupapi Version)
else()
    if(NOT TARGET Threads::Threads)
        find_package(Threads REQUIRED)
        set_property(TARGET Threads::Threads PROPERTY IMPORTED_GLOBAL TRUE)
    endif()
    if(NOT TARGET OpenGL::GL)
        find_package(OpenGL REQUIRED)
        set_property(TARGET OpenGL::GL PROPERTY IMPORTED_GLOBAL TRUE)
    endif()
    target_link_libraries(autolib::imgui INTERFACE OpenGL::GL ${CMAKE_DL_LIBS} Threads::Threads)
endif()
