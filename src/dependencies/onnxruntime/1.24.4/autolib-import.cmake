#[=[
ONNX Runtime：CPU 模型推理运行库，通过 find_library 查找库文件。
公共目标：autolib::onnxruntime；运行时需能加载随包提供的动态库。
官方归档的 Config 引用路径与载荷不符，不使用该 Config。
#]=]

if(TARGET autolib::onnxruntime)
    return()
endif()

find_library(_autolib_onnxruntime_library NAMES onnxruntime HINTS "${CMAKE_CURRENT_LIST_DIR}/lib" REQUIRED)

add_library(autolib::onnxruntime INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::onnxruntime INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
target_link_libraries(autolib::onnxruntime INTERFACE "${_autolib_onnxruntime_library}")
