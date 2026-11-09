# ViSP 的公开 Config 与动态库都使用包内 OpenMP，避免依赖构建机 Homebrew 路径。
if(NOT VISP_HAVE_OPENMP)
    message(FATAL_ERROR "Darwin ViSP requires OpenMP")
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

add_library(visp::openmp INTERFACE IMPORTED GLOBAL)
set_property(TARGET visp::openmp PROPERTY INTERFACE_LINK_LIBRARIES OpenMP::OpenMP_CXX)
foreach(target IN LISTS VISP_MODULES_BUILD)
    get_target_property(links ${target} INTERFACE_LINK_LIBRARIES)
    if(links)
        list(TRANSFORM links REPLACE "^${runtime}$" "visp::openmp")
        set_property(TARGET ${target} PROPERTY INTERFACE_LINK_LIBRARIES "${links}")
    endif()
    # 生成独立安装脚本，避免上游旧 policy 将目标文件名表达式按字面写入安装命令。
    file(GENERATE OUTPUT "${CMAKE_CURRENT_BINARY_DIR}/${target}-runtime.cmake" CONTENT "
        execute_process(COMMAND install_name_tool -change \"${runtime_id}\" @loader_path/libomp.dylib
            \"\${CMAKE_INSTALL_PREFIX}/lib/$<TARGET_FILE_NAME:${target}>\" COMMAND_ERROR_IS_FATAL ANY)
        execute_process(COMMAND codesign --force --sign -
            \"\${CMAKE_INSTALL_PREFIX}/lib/$<TARGET_FILE_NAME:${target}>\" COMMAND_ERROR_IS_FATAL ANY)
    ")
    install(SCRIPT "${CMAKE_CURRENT_BINARY_DIR}/${target}-runtime.cmake")
endforeach()

separate_arguments(openmp_options NATIVE_COMMAND "${OpenMP_CXX_FLAGS}")
file(WRITE "${CMAKE_CURRENT_BINARY_DIR}/visp-openmp.cmake"
"if(NOT TARGET visp::openmp)
    add_library(visp::openmp INTERFACE IMPORTED GLOBAL)
    set_target_properties(visp::openmp PROPERTIES
        INTERFACE_COMPILE_OPTIONS \"${openmp_options}\"
        INTERFACE_INCLUDE_DIRECTORIES \"\${CMAKE_CURRENT_LIST_DIR}/../../../include/visp-openmp\"
        INTERFACE_LINK_LIBRARIES \"\${CMAKE_CURRENT_LIST_DIR}/../../../lib/libomp.dylib\")
endif()
")
install(FILES "${CMAKE_CURRENT_BINARY_DIR}/visp-openmp.cmake" DESTINATION lib/cmake/visp)
install(FILES "${runtime}" DESTINATION lib RENAME libomp.dylib)
install(FILES "${runtime_prefix}/include/omp.h" DESTINATION include/visp-openmp)
install(FILES "${runtime_prefix}/LICENSE.TXT" DESTINATION share/licenses/libomp)
install(CODE "
    execute_process(COMMAND install_name_tool -id @rpath/libomp.dylib
        \"\${CMAKE_INSTALL_PREFIX}/lib/libomp.dylib\" COMMAND_ERROR_IS_FATAL ANY)
    execute_process(COMMAND codesign --force --sign -
        \"\${CMAKE_INSTALL_PREFIX}/lib/libomp.dylib\" COMMAND_ERROR_IS_FATAL ANY)
")
