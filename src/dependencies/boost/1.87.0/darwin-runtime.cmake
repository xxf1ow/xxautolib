# 保留 Boost.Iostreams 的 zstd 共享目标及上游 Config 的静态目标完整性检查。
find_package(zstd CONFIG REQUIRED)
get_target_property(zstd_runtime zstd::libzstd_shared IMPORTED_LOCATION_RELEASE)
file(REAL_PATH "${zstd_runtime}" zstd_runtime_file)
get_filename_component(zstd_libdir "${zstd_runtime_file}" DIRECTORY)
get_filename_component(zstd_prefix "${zstd_libdir}" DIRECTORY)
get_filename_component(zstd_runtime_name "${zstd_runtime_file}" NAME)
file(GLOB zstd_libraries "${zstd_prefix}/lib/libzstd*.dylib" "${zstd_prefix}/lib/libzstd.a")
file(GLOB zstd_headers "${zstd_prefix}/include/*.h")
install(FILES ${zstd_libraries} DESTINATION lib)
install(FILES ${zstd_headers} DESTINATION include)
install(DIRECTORY "${zstd_prefix}/lib/cmake/zstd" DESTINATION lib/cmake)
install(FILES "${zstd_prefix}/LICENSE" "${zstd_prefix}/COPYING" DESTINATION share/licenses/zstd)

# dylib 名称保持上游导出布局；只重写本包副本的绝对 install name。
execute_process(COMMAND otool -D -X "${zstd_runtime_file}" OUTPUT_VARIABLE zstd_id COMMAND_ERROR_IS_FATAL ANY)
string(STRIP "${zstd_id}" zstd_id)
get_filename_component(zstd_soname "${zstd_id}" NAME)
install(CODE "
    execute_process(COMMAND install_name_tool -id \"@rpath/${zstd_soname}\"
        \"\${CMAKE_INSTALL_PREFIX}/lib/${zstd_runtime_name}\" COMMAND_ERROR_IS_FATAL ANY)
    execute_process(COMMAND codesign --force --sign -
        \"\${CMAKE_INSTALL_PREFIX}/lib/${zstd_runtime_name}\" COMMAND_ERROR_IS_FATAL ANY)
")
