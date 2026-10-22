function(xxautolib_generate_modules)
    find_program(GO_EXECUTABLE NAMES go DOC "Go SDK executable (version pinned in go.mod)")
    if(NOT GO_EXECUTABLE OR NOT EXISTS "${GO_EXECUTABLE}")
        message(FATAL_ERROR "Go SDK missing: set GO_EXECUTABLE to the SDK required by go.mod")
    endif()
    find_package(Git REQUIRED)
    set(root "${CMAKE_CURRENT_SOURCE_DIR}")
    file(STRINGS "${root}/go.mod" go_directive REGEX "^go [0-9]+\\.[0-9]+\\.[0-9]+$")
    if(NOT go_directive)
        message(FATAL_ERROR "go.mod must pin a full Go SDK version")
    endif()
    string(REGEX REPLACE "^go " "" required_go "${go_directive}")
    set(go_environment "${CMAKE_COMMAND}" -E env "GOTOOLCHAIN=local" "CGO_ENABLED=0")
    execute_process(COMMAND ${go_environment} "${GO_EXECUTABLE}" version
        WORKING_DIRECTORY "${root}" RESULT_VARIABLE sdk_result
        OUTPUT_VARIABLE sdk_version ERROR_VARIABLE sdk_error OUTPUT_STRIP_TRAILING_WHITESPACE)
    if(NOT sdk_result STREQUAL "0")
        message(FATAL_ERROR "Go SDK cannot execute: ${GO_EXECUTABLE}\n${sdk_error}")
    endif()
    if(NOT sdk_version MATCHES "^go version go${required_go} ")
        message(FATAL_ERROR "Go SDK version mismatch: require ${required_go}; ${GO_EXECUTABLE}: ${sdk_version}")
    endif()
    execute_process(COMMAND ${go_environment} "${GO_EXECUTABLE}" env GOHOSTOS GOHOSTARCH
        WORKING_DIRECTORY "${root}" RESULT_VARIABLE host_result
        OUTPUT_VARIABLE go_host ERROR_VARIABLE host_error OUTPUT_STRIP_TRAILING_WHITESPACE)
    if(NOT host_result STREQUAL "0")
        message(FATAL_ERROR "Go SDK host query failed: ${GO_EXECUTABLE}\n${host_error}")
    endif()
    string(REPLACE "\r" "" go_host "${go_host}")
    string(REPLACE "\n" ";" go_host "${go_host}")
    list(GET go_host 0 go_host_os)
    list(GET go_host 1 go_host_arch)
    list(APPEND go_environment "GOOS=${go_host_os}" "GOARCH=${go_host_arch}")

    file(GLOB go_sources CONFIGURE_DEPENDS "${CMAKE_CURRENT_FUNCTION_LIST_DIR}/*.go")
    list(FILTER go_sources EXCLUDE REGEX "_test\\.go$")
    set_property(DIRECTORY APPEND PROPERTY CMAKE_CONFIGURE_DEPENDS
        ${go_sources} "${root}/go.mod" "${root}/go.sum"
        "${CMAKE_CURRENT_FUNCTION_LIST_DIR}/apply_patch.cmake"
        "${CMAKE_CURRENT_FUNCTION_LIST_DIR}/configure_dependencies.cmake")
    set(builder "${CMAKE_BINARY_DIR}/tools/autolib-builder")
    if(CMAKE_HOST_WIN32)
        string(APPEND builder ".exe")
    endif()
    execute_process(COMMAND ${go_environment} "${GO_EXECUTABLE}" build -mod=readonly -trimpath
        -o "${builder}" ./src/autolib-builder
        WORKING_DIRECTORY "${root}" RESULT_VARIABLE compilation_result
        OUTPUT_VARIABLE compilation_output ERROR_VARIABLE compilation_error)
    if(NOT compilation_result STREQUAL "0")
        message(FATAL_ERROR
            "autolib-builder compilation failed: ${compilation_result}\n"
            "${compilation_output}${compilation_error}\n"
            "For offline builds, prepare the pinned modules in GOMODCACHE before configuring.")
    endif()

    set(module_arguments)
    foreach(module IN LISTS BUILD_MODULES)
        list(APPEND module_arguments "--module=${module}")
    endforeach()
    if(CMAKE_CONFIGURATION_TYPES)
        set(multi_config ON)
    else()
        set(multi_config OFF)
    endif()
    set(runtime_argument)
    set(architecture "${CMAKE_SYSTEM_PROCESSOR}")
    if(CMAKE_GENERATOR MATCHES "^Visual Studio" AND CMAKE_GENERATOR_PLATFORM)
        string(REGEX REPLACE ",.*$" "" architecture "${CMAKE_GENERATOR_PLATFORM}")
    endif()
    if(DEFINED CMAKE_MSVC_RUNTIME_LIBRARY)
        list(APPEND runtime_argument "--msvc-runtime-library=${CMAKE_MSVC_RUNTIME_LIBRARY}")
    endif()
    set(cmake_defaults)
    foreach(key CMAKE_POLICY_VERSION_MINIMUM CMAKE_POSITION_INDEPENDENT_CODE CMAKE_CXX_STANDARD CMAKE_PROJECT_INCLUDE)
        if(DEFINED ${key})
            string(REPLACE ";" "\\;" default_argument "--cmake-default=${key}=${${key}}")
            list(APPEND cmake_defaults "${default_argument}")
        endif()
    endforeach()
    execute_process(COMMAND "${builder}" generate ${module_arguments}
        "--source-root=${root}" "--build-root=${CMAKE_BINARY_DIR}"
        "--system-name=${CMAKE_SYSTEM_NAME}" "--architecture=${architecture}"
        "--generator=${CMAKE_GENERATOR}" "--platform=${CMAKE_GENERATOR_PLATFORM}"
        "--toolset=${CMAKE_GENERATOR_TOOLSET}" "--instance=${CMAKE_GENERATOR_INSTANCE}"
        "--multi-config=${multi_config}" ${runtime_argument} ${cmake_defaults}
        "--cmake-command=${CMAKE_COMMAND}" "--git-executable=${GIT_EXECUTABLE}"
        WORKING_DIRECTORY "${root}" RESULT_VARIABLE generation_result)
    if(NOT generation_result STREQUAL "0")
        message(FATAL_ERROR "Module generation failed: ${generation_result}")
    endif()
    include("${CMAKE_BINARY_DIR}/generated/inputs.cmake")
    set_property(DIRECTORY APPEND PROPERTY CMAKE_CONFIGURE_DEPENDS ${XXAUTOLIB_CONFIGURE_INPUTS})
    foreach(module IN LISTS XXAUTOLIB_GENERATED_MODULES)
        include("${module}")
    endforeach()
endfunction()
