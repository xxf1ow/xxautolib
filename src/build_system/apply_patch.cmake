# 公共驱动只应用当前补丁，不恢复源码或替换已应用的旧补丁。
foreach(input GIT_EXECUTABLE SOURCE_DIR PATCH_FILE)
    if(NOT DEFINED ${input} OR "${${input}}" STREQUAL "")
        message(FATAL_ERROR "Patch driver requires ${input}")
    endif()
endforeach()

execute_process(
    COMMAND "${GIT_EXECUTABLE}" apply --check "${PATCH_FILE}"
    WORKING_DIRECTORY "${SOURCE_DIR}"
    RESULT_VARIABLE check_result
    OUTPUT_VARIABLE check_output
    ERROR_VARIABLE check_error)
if(check_result STREQUAL "0")
    execute_process(
        COMMAND "${GIT_EXECUTABLE}" apply --whitespace=nowarn "${PATCH_FILE}"
        WORKING_DIRECTORY "${SOURCE_DIR}"
        RESULT_VARIABLE apply_result
        OUTPUT_VARIABLE apply_output
        ERROR_VARIABLE apply_error)
    if(NOT apply_result STREQUAL "0")
        message(FATAL_ERROR
            "Patch application failed: ${PATCH_FILE}\nSource: ${SOURCE_DIR}\n"
            "${apply_output}${apply_error}")
    endif()
    return()
endif()

execute_process(
    COMMAND "${GIT_EXECUTABLE}" apply --reverse --check "${PATCH_FILE}"
    WORKING_DIRECTORY "${SOURCE_DIR}"
    RESULT_VARIABLE reverse_result
    OUTPUT_VARIABLE reverse_output
    ERROR_VARIABLE reverse_error)
if(reverse_result STREQUAL "0")
    message(STATUS "Patch already applied: ${PATCH_FILE}")
    return()
endif()

message(FATAL_ERROR
    "Patch source mismatch: ${PATCH_FILE}\nSource: ${SOURCE_DIR}\n"
    "Prepare a clean source directory manually before rebuilding this module.\n"
    "Apply check:\n${check_output}${check_error}\n"
    "Reverse check:\n${reverse_output}${reverse_error}")
