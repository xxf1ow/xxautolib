#[=[
Native File Dialog：原生文件对话框库，通过 CMake Config 导入。
公共目标：autolib::nativefiledialog-extended。
#]=]

if(TARGET autolib::nativefiledialog-extended)
    return()
endif()

set(nfd_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/nfd")
find_package(nfd CONFIG REQUIRED)
set_property(TARGET nfd::nfd PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::nativefiledialog-extended INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::nativefiledialog-extended INTERFACE nfd::nfd)
