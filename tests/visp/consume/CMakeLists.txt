# 从兄弟目录消费首次导入的目标，验证公共目标及其上游依赖的可见性。
add_executable(visp_smoke ../smoke.cpp)
target_compile_features(visp_smoke PRIVATE cxx_std_17)
target_link_libraries(visp_smoke PRIVATE autolib::visp)
add_test(NAME visp_tracking_and_servo
    COMMAND visp_smoke "${CMAKE_CURRENT_SOURCE_DIR}/../plane.cao")
