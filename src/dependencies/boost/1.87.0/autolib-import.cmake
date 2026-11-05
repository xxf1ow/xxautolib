#[=[
Boost：C++ 基础库集合，通过 CMake Config 导入。
公共目标：autolib::boost。
#]=]

if(TARGET autolib::boost)
    return()
endif()

set(Boost_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/Boost-1.87.0")
set(ZLIB_ROOT "${AUTOLIB_PACKAGE_DIR_zlib}")
find_package(Boost CONFIG REQUIRED COMPONENTS system filesystem thread date_time iostreams serialization chrono regex)
set_property(TARGET Boost::system PROPERTY IMPORTED_GLOBAL TRUE)
set_property(TARGET Boost::filesystem PROPERTY IMPORTED_GLOBAL TRUE)
set_property(TARGET Boost::thread PROPERTY IMPORTED_GLOBAL TRUE)
set_property(TARGET Boost::date_time PROPERTY IMPORTED_GLOBAL TRUE)
set_property(TARGET Boost::iostreams PROPERTY IMPORTED_GLOBAL TRUE)
set_property(TARGET Boost::serialization PROPERTY IMPORTED_GLOBAL TRUE)
set_property(TARGET Boost::chrono PROPERTY IMPORTED_GLOBAL TRUE)
set_property(TARGET Boost::regex PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::boost INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::boost INTERFACE Boost::system Boost::filesystem Boost::thread Boost::date_time Boost::iostreams Boost::serialization Boost::chrono Boost::regex autolib::zlib)
