function(xxautolib_generate_modules)
    find_program(UV_EXECUTABLE NAMES uv REQUIRED)
    find_package(Git REQUIRED)

    set_property(DIRECTORY APPEND PROPERTY CMAKE_CONFIGURE_DEPENDS
        "${CMAKE_CURRENT_SOURCE_DIR}/pyproject.toml"
        "${CMAKE_CURRENT_SOURCE_DIR}/uv.lock"
        "${CMAKE_CURRENT_SOURCE_DIR}/.python-version")

    execute_process(
        COMMAND "${CMAKE_COMMAND}" -E env
            "UV_PROJECT_ENVIRONMENT=${CMAKE_CURRENT_SOURCE_DIR}/.venv"
            "${UV_EXECUTABLE}" sync --project "${CMAKE_CURRENT_SOURCE_DIR}" --locked
        WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}"
        RESULT_VARIABLE environment_result)
    if(NOT environment_result STREQUAL "0")
        message(FATAL_ERROR "Root uv environment preparation failed: ${environment_result}")
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
    execute_process(
        COMMAND "${CMAKE_COMMAND}" -E env
            "UV_PROJECT_ENVIRONMENT=${CMAKE_CURRENT_SOURCE_DIR}/.venv"
            "${UV_EXECUTABLE}" run --project "${CMAKE_CURRENT_SOURCE_DIR}" --locked
            python -B "${CMAKE_CURRENT_FUNCTION_LIST_DIR}/generate.py"
            ${module_arguments}
            "--source-root=${CMAKE_CURRENT_SOURCE_DIR}"
            "--build-root=${CMAKE_BINARY_DIR}"
            "--system-name=${CMAKE_SYSTEM_NAME}"
            "--architecture=${architecture}"
            "--generator=${CMAKE_GENERATOR}"
            "--platform=${CMAKE_GENERATOR_PLATFORM}"
            "--toolset=${CMAKE_GENERATOR_TOOLSET}"
            "--instance=${CMAKE_GENERATOR_INSTANCE}"
            "--multi-config=${multi_config}"
            ${runtime_argument}
            ${cmake_defaults}
            "--cmake-command=${CMAKE_COMMAND}"
            "--git-executable=${GIT_EXECUTABLE}"
        WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}"
        RESULT_VARIABLE generation_result)
    if(NOT generation_result STREQUAL "0")
        message(FATAL_ERROR "Module generation failed: ${generation_result}")
    endif()
    include("${CMAKE_BINARY_DIR}/generated/inputs.cmake")
    set_property(DIRECTORY APPEND PROPERTY CMAKE_CONFIGURE_DEPENDS ${XXAUTOLIB_CONFIGURE_INPUTS})
    foreach(module IN LISTS XXAUTOLIB_GENERATED_MODULES)
        include("${module}")
    endforeach()
endfunction()
