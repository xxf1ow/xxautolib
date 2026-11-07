# FLANN 的头文件执行 OpenMP 调用，Config 消费者也须获得编译参数和包内运行库。
if(NOT USE_OPENMP)
    message(FATAL_ERROR "Darwin FLANN requires OpenMP")
endif()
list(LENGTH OpenMP_CXX_LIBRARIES runtime_count)
if(NOT runtime_count EQUAL 1)
    message(FATAL_ERROR "Expected one libomp runtime, got: ${OpenMP_CXX_LIBRARIES}")
endif()
list(GET OpenMP_CXX_LIBRARIES 0 runtime)
file(REAL_PATH "${runtime}" runtime_file)
get_filename_component(runtime_libdir "${runtime_file}" DIRECTORY)
get_filename_component(runtime_prefix "${runtime_libdir}" DIRECTORY)
if(NOT EXISTS "${runtime_prefix}/LICENSE.TXT")
    message(FATAL_ERROR "Missing libomp license: ${runtime_prefix}/LICENSE.TXT")
endif()
execute_process(COMMAND otool -D -X "${runtime}" OUTPUT_VARIABLE runtime_id COMMAND_ERROR_IS_FATAL ANY)
string(STRIP "${runtime_id}" runtime_id)

add_library(flann::openmp INTERFACE IMPORTED GLOBAL)
set_property(TARGET flann::openmp PROPERTY INTERFACE_LINK_LIBRARIES OpenMP::OpenMP_CXX)
foreach(target IN ITEMS flann_cpp flann)
    target_link_libraries(${target} flann::openmp)
    set_target_properties(${target} PROPERTIES INSTALL_NAME_DIR "@rpath")
endforeach()

separate_arguments(openmp_options NATIVE_COMMAND "${OpenMP_CXX_FLAGS}")
file(WRITE "${CMAKE_CURRENT_BINARY_DIR}/flann-openmp.cmake"
"if(NOT TARGET flann::openmp)
    add_library(flann::openmp INTERFACE IMPORTED GLOBAL)
    set_target_properties(flann::openmp PROPERTIES
        INTERFACE_COMPILE_OPTIONS \"${openmp_options}\"
        INTERFACE_INCLUDE_DIRECTORIES \"\${CMAKE_CURRENT_LIST_DIR}/../../../include/flann-openmp\"
        INTERFACE_LINK_LIBRARIES \"\${CMAKE_CURRENT_LIST_DIR}/../../../lib/libomp.dylib\")
endif()
")
install(FILES "${CMAKE_CURRENT_BINARY_DIR}/flann-openmp.cmake" DESTINATION lib/cmake/flann)
install(FILES "${runtime}" DESTINATION lib RENAME libomp.dylib)
install(FILES "${runtime_prefix}/include/omp.h" DESTINATION include/flann-openmp)
install(FILES "${runtime_prefix}/LICENSE.TXT" DESTINATION share/licenses/libomp)

# 只修改本次安装副本；原始 Homebrew 运行库保持不变。
install(CODE "
    execute_process(COMMAND install_name_tool -id @rpath/libomp.dylib
        \"\${CMAKE_INSTALL_PREFIX}/lib/libomp.dylib\" COMMAND_ERROR_IS_FATAL ANY)
    foreach(name IN ITEMS libflann_cpp.1.9.2.dylib libflann.1.9.2.dylib)
        execute_process(COMMAND install_name_tool -change \"${runtime_id}\" @loader_path/libomp.dylib
            \"\${CMAKE_INSTALL_PREFIX}/lib/\${name}\" COMMAND_ERROR_IS_FATAL ANY)
        execute_process(COMMAND codesign --force --sign - \"\${CMAKE_INSTALL_PREFIX}/lib/\${name}\"
            COMMAND_ERROR_IS_FATAL ANY)
    endforeach()
    execute_process(COMMAND codesign --force --sign - \"\${CMAKE_INSTALL_PREFIX}/lib/libomp.dylib\"
        COMMAND_ERROR_IS_FATAL ANY)
")
