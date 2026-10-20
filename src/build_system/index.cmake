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
