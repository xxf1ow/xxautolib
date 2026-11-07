cmake_minimum_required(VERSION 3.20)
project(autolib_header_adapter_check LANGUAGES CXX)
enable_testing()

get_filename_component(repository "${CMAKE_CURRENT_LIST_DIR}/../../.." ABSOLUTE)
set(EIGEN_VERSION "3.4.0" CACHE STRING "Eigen version to check in this independent consumer")

# 此夹具验证头文件 wrapper 的包含路径、系统使用要求和跨目录可见性。
set(cases
    "asio|8806a6803cde"
    "cpp-httplib|88956ccad838"
    "expected|1.3.1"
    "eigen|${EIGEN_VERSION}"
    "queue|45db9a8b4431"
    "pybind11|2.13.6"
    "taskflow|83f90a279aaa"
    "websocketpp|4dfe1be74e68"
    "nlohmann|3.12.0"
)

foreach(case IN LISTS cases)
    string(REPLACE "|" ";" fields "${case}")
    list(GET fields 0 name)
    list(GET fields 1 version)
    if(ONLY_REF AND NOT ONLY_REF STREQUAL name)
        continue()
    endif()
    string(MAKE_C_IDENTIFIER "${name}" identifier)
    set(prefix "${CMAKE_CURRENT_BINARY_DIR}/packages/${name}/${version}")
    file(MAKE_DIRECTORY "${prefix}/include" "${prefix}/importer")
    file(WRITE "${prefix}/include/probe_${identifier}.hpp" "inline int probe_${identifier}() { return 42; }\n")
    configure_file("${repository}/src/dependencies/${name}/${version}/autolib-import.cmake"
        "${prefix}/autolib-import.cmake" COPYONLY)
    file(WRITE "${prefix}/importer/CMakeLists.txt"
        "include(\"${prefix}/autolib-import.cmake\")\ninclude(\"${prefix}/autolib-import.cmake\")\n")
    add_subdirectory("${prefix}/importer" "imports/${identifier}")

    if(NOT TARGET autolib::${name})
        message(FATAL_ERROR "${name}: public target is not visible outside the importing directory")
    endif()
    get_target_property(global autolib::${name} IMPORTED_GLOBAL)
    get_target_property(kind autolib::${name} TYPE)
    if(NOT global OR NOT kind STREQUAL "INTERFACE_LIBRARY")
        message(FATAL_ERROR "${name}: expected a GLOBAL INTERFACE imported target")
    endif()
    if(name STREQUAL "eigen")
        if(TARGET Eigen3::Eigen)
            message(FATAL_ERROR "Eigen header-only adapter must not load an upstream Config")
        endif()
        if(version STREQUAL "5.0.1")
            get_target_property(features autolib::eigen INTERFACE_COMPILE_FEATURES)
            if(NOT "cxx_std_14" IN_LIST features)
                message(FATAL_ERROR "Eigen 5.0.1 must propagate its C++14 requirement")
            endif()
        endif()
    endif()
    if(name STREQUAL "asio" OR name STREQUAL "cpp-httplib" OR name STREQUAL "taskflow")
        set(dependency Threads::Threads)
    else()
        set(dependency "")
    endif()
    if(dependency)
        get_target_property(links autolib::${name} INTERFACE_LINK_LIBRARIES)
        if(NOT dependency IN_LIST links)
            message(FATAL_ERROR "${name}: required dependency ${dependency} is not propagated")
        endif()
    endif()

    file(WRITE "${prefix}/probe.cpp"
        "#include <probe_${identifier}.hpp>\nint main() { return probe_${identifier}() == 42 ? 0 : 1; }\n")
    add_executable(consumer_${identifier} "${prefix}/probe.cpp")
    target_link_libraries(consumer_${identifier} PRIVATE autolib::${name})
    add_test(NAME ${identifier}_include_usage COMMAND consumer_${identifier})
endforeach()
