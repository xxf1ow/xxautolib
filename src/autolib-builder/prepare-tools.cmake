include("${CMAKE_CURRENT_LIST_DIR}/go-sdk.cmake")

function(xxautolib_prepare_tools root install_root builder_output)
    xxautolib_prepare_go(go_executable go_environment)
    execute_process(COMMAND ${go_environment} "${go_executable}" build -mod=readonly -trimpath
        -o "${builder_output}" ./src/autolib-builder
        WORKING_DIRECTORY "${root}" RESULT_VARIABLE compilation_result
        OUTPUT_VARIABLE compilation_output ERROR_VARIABLE compilation_error)
    if(NOT compilation_result STREQUAL "0")
        message(FATAL_ERROR
            "autolib-builder compilation failed: ${compilation_result}\n"
            "${compilation_output}${compilation_error}\n"
            "Pinned modules are downloaded automatically when needed; check network access or the cache in ${root}/.tools/go/gopath/pkg/mod.")
    endif()

    string(TOLOWER "${CMAKE_HOST_SYSTEM_NAME}" importer_os)
    cmake_host_system_information(RESULT importer_arch QUERY OS_PLATFORM)
    string(TOLOWER "${importer_arch}" importer_arch)
    if(importer_arch MATCHES "^(amd64|x86_64|x64)$")
        set(importer_arch x86_64)
    elseif(importer_arch MATCHES "^(aarch64|arm64)$")
        set(importer_arch aarch64)
    endif()
    set(importer "${install_root}/autolib-importer-${importer_os}-${importer_arch}")
    if(CMAKE_HOST_WIN32)
        string(APPEND importer ".exe")
    endif()
    execute_process(COMMAND ${go_environment} "${go_executable}" build -mod=readonly -trimpath "-ldflags=-s -w"
        -o "${importer}" ./src/autolib-importer
        WORKING_DIRECTORY "${root}" RESULT_VARIABLE compilation_result
        OUTPUT_VARIABLE compilation_output ERROR_VARIABLE compilation_error)
    if(NOT compilation_result STREQUAL "0")
        message(FATAL_ERROR "autolib-importer compilation failed: ${compilation_result}\n${compilation_output}${compilation_error}")
    endif()
    configure_file("${root}/src/autolib.cmake" "${install_root}/autolib.cmake" COPYONLY)
endfunction()

if(CMAKE_SCRIPT_MODE_FILE STREQUAL CMAKE_CURRENT_LIST_FILE)
    foreach(required AUTOLIB_SOURCE_ROOT AUTOLIB_INSTALL_ROOT)
        if(NOT DEFINED ${required} OR "${${required}}" STREQUAL "")
            message(FATAL_ERROR "${required} is required for tool preparation")
        endif()
    endforeach()
    get_filename_component(tool_root "${AUTOLIB_INSTALL_ROOT}/../tools" ABSOLUTE)
    set(builder "${tool_root}/autolib-builder")
    if(CMAKE_HOST_WIN32)
        string(APPEND builder ".exe")
    endif()
    xxautolib_prepare_tools("${AUTOLIB_SOURCE_ROOT}" "${AUTOLIB_INSTALL_ROOT}" "${builder}")
endif()
