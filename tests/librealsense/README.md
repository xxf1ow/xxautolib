# librealsense 无相机验证

Linux 构建前需先安装系统 `libudev-dev`。按[开发指南](../../docs/development.md#准备与配置)通过根入口选择 `BUILD_LIST=librealsense` 完成安装，并准备可被找到的 pkg-config。以下命令在仓库根目录运行，`<安装根绝对路径>` 替换为本副本的 `3rdparty`；生成器及 Windows 的平台、工具集参数与根工程一致，Linux 添加 `-DCMAKE_BUILD_TYPE=Release`。

```text
cmake -S tests/librealsense -B build/check/librealsense "-DREALSENSE_INSTALL_ROOT:PATH=<安装根绝对路径>"
cmake --build build/check/librealsense --config Release
ctest --test-dir build/check/librealsense -C Release --output-on-failure
```

Windows 测试进程的 PATH 需包含安装根下的 `librealsense/bin`，用于加载 SDK DLL。两项 CTest 分别检查安装 SDK 的版本、彩深配置、CPU 对齐对象及标定接口、JSON 版本，以及 pkg-config 解析出的库目录。CMake 包和 JSON 头文件仅从指定安装根查找。

检查不启动相机，不证明真实设备取流、帧同步或对齐精度；来源及依赖接线的证据由 [RSUSB 本地构建记录](../../docs/agent-notes/implemented/architecture/2026-10-27-librealsense-rsusb.md)保存。
