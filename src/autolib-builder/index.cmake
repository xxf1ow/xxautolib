include("${CMAKE_CURRENT_LIST_DIR}/go-sdk.cmake")

function(xxautolib_generate_modules)
    find_package(Git REQUIRED)
    set(root "${CMAKE_CURRENT_SOURCE_DIR}")
    xxautolib_prepare_go(go_executable go_environment)

    # GLOB 检测源码增删；文件内容变化统一由生成器的输入清单登记。
    file(GLOB go_sources CONFIGURE_DEPENDS "${CMAKE_CURRENT_FUNCTION_LIST_DIR}/*.go")
    set(builder "${CMAKE_BINARY_DIR}/tools/autolib-builder")
    if(CMAKE_HOST_WIN32)
        string(APPEND builder ".exe")
    endif()
    execute_process(COMMAND ${go_environment} "${go_executable}" build -mod=readonly -trimpath
        -o "${builder}" ./src/autolib-builder
        WORKING_DIRECTORY "${root}" RESULT_VARIABLE compilation_result
        OUTPUT_VARIABLE compilation_output ERROR_VARIABLE compilation_error)
    if(NOT compilation_result STREQUAL "0")
        message(FATAL_ERROR
            "autolib-builder compilation failed: ${compilation_result}\n"
            "${compilation_output}${compilation_error}\n"
            "Pinned modules are downloaded automatically when needed; check network access or the cache in ${root}/.tools/go/gopath/pkg/mod.")
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
