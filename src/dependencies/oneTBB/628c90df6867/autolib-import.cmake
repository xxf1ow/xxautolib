#[=[
oneTBB：任务并行共享运行库，通过 CMake Config 导入。
公共目标：autolib::oneTBB。
#]=]

if(TARGET autolib::oneTBB)
    return()
endif()

set(TBB_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/TBB")
find_package(TBB CONFIG REQUIRED COMPONENTS tbb)
set_property(TARGET TBB::tbb PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::oneTBB INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::oneTBB INTERFACE TBB::tbb)
