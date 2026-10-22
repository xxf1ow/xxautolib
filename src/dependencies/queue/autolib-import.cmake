if(TARGET AutoLib::queue)
    return()
endif()
if(NOT IS_DIRECTORY "${CMAKE_CURRENT_LIST_DIR}/include/queue")
    message(FATAL_ERROR "AutoLib queue requires package-owned include/queue: ${CMAKE_CURRENT_LIST_DIR}")
endif()
add_library(AutoLib::queue INTERFACE IMPORTED GLOBAL)
set_target_properties(AutoLib::queue PROPERTIES
    INTERFACE_INCLUDE_DIRECTORIES "${CMAKE_CURRENT_LIST_DIR}/include")
