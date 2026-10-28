# Agent Note: librealsense 的 RSUSB 本地构建

Status: implemented

## 问题

应用需要通过 RealSense 相机获取彩色帧与深度帧、查询标定并执行彩深对齐，不需要上游示例、图形界面、网络设备访问或 ROSBAG2 录制。构建需要复用本仓库依赖，避免 SDK 在配置阶段自行下载 JSON 或重复编译 LZ4。

## 决定

librealsense 2.58.1 固定提交 `bf2778061d5dd29776e9aca8765f75852671760b`，通过普通根 CMake 入口构建安装。只选择 `BUILD_LIST=librealsense` 即准备直接依赖 `nlohmann`、`lz4` 和 `libusb`；不提供 adapter、打包、发布或公共引擎扩展。

独立 `nlohmann` 模块沿用只读参考 `D:/Work/AutoLibBuilder/src/dependencies/nlohmann.cmake` 的纯头文件复制安装方式。v3.12.0 固定完整提交 `55f93686c01528224f448c19128836e7df245f72`，与 librealsense 2.58.1 自身要求的版本一致；不使用浮动分支，不执行没有实际工作的 configure/build。

独立 `libusb` 模块采用 SDK 使用的 libusb-cmake v1.0.27-1，固定提交 `cec1e49eec481a6333c218726ecaba8e4f447a07`，其仓库随附 libusb 1.0.27 源码。构建静态库并开启安装，示例、测试及调试日志保持默认关闭，普通日志和 SYSTEM include 保持默认开启；Linux 保留默认 udev 后端，明确要求系统 `libudev-dev`。Windows 和 Linux 的 SDK RSUSB 路径均链接本地 libusb，Linux 补齐静态库的 udev、线程依赖，Windows 补齐上游声明的 windowsapp 系统库；不扩展平台条件依赖接口。

librealsense 保持共享库交付，启用 `FORCE_RSUSB_BACKEND` 和 `USE_EXTERNAL_LZ4`，关闭 `BUILD_ROSBAG2`、`BUILD_WITH_DDS`、`BUILD_EXAMPLES`、`BUILD_GRAPHICAL_EXAMPLES`、`BUILD_TOOLS`、`BUILD_GLSL_EXTENSIONS` 和 `CHECK_FOR_UPDATES`。Windows 关闭 `BUILD_WITH_STATIC_CRT`，避免上游覆盖公共 CRT 选择。CUDA、OpenMP、语言绑定、所有附加示例、上游测试、ASan 和附加安全标志保持默认关闭。CPU 指令优化、ARM64 NEON、设备命令通道、COM 策略、内置日志、平台日志异步方式及可选 ccache 保持默认；`BUILD_RS2_ALL` 只在静态构建生效，本次不改。公共 Release、工具链、CRT、policy、PIC、C++ 标准及路径仍由现有入口和引擎拥有。

仅用模块内必要补丁将 JSON 与 libusb 接到同一安装根的独立模块；缺少本地输入即失败，不回退系统同名 JSON/libusb、不触发上游内部下载。复用 LZ4 的原生查找接线并适配其 `LZ4::lz4` 导出目标名，核对实际路径。SDK 核心 CPU 对齐及采集接口保持原样，不为缩小库体积进一步剥离上游处理模块或旧式录制代码。Linux 本地安装不执行系统 `ldconfig`，不安装 udev 规则、驱动或固件，不打内核补丁、不装 DKMS。

上游 pkg-config 模板硬编码 `lib/x86_64-linux-gnu`，与本地安装及 ARM64 不符。模块补丁使用 `CMAKE_INSTALL_LIBDIR` 并填写包名，使安装的 `realsense2.pc` 解析到真实库目录。

## 备选方案

**保留 ROSBAG2 默认能力。** 这会引入 Fast-CDR、SQLite 和 yaml-cpp，超出实时采集与对齐需要；本次关闭。

**让 SDK 自行下载 JSON，或复用系统同名包。** 这不能保证使用本仓库的固定安装版本；本次通过直接依赖和局部接线消费本地安装。

**彻底剥离旧式录制和未使用的处理算法。** 这需要维护更多上游源码改动，而关闭示例与非必需依赖已经达到目标；本次保留上游核心结构。

## 验证结果

Windows x64/v143（MSVC 19.42）和 Linux aarch64（127 设备，GCC 9.4）均从根入口完成 librealsense 共享库、独立 libusb 静态库的真实编译安装及 nlohmann 头文件安装；LZ4 在 Windows 实际构建，ARM 复用已核对的本地安装。头文件、共享库及其链接、静态库、realsense2 CMake 配置和 pkg-config 文件均存在。cache、编译包含路径及实际链接输入确认 JSON、LZ4、libusb 来自同一仓库安装根；Linux 另链接系统 udev 和线程库，`ldd` 无缺失依赖，安装清单仅含 SDK 前缀。

两端的[无相机检查](../../../../tests/librealsense/README.md)各通过 2/2 CTest：验证 SDK/JSON 版本、彩深流配置、CPU 对齐对象、合成内参反投影及 pkg-config 库目录，不启动相机。Windows 的缺失本地 JSON 负向检查以明确错误失败，未产生 JSON 下载目录。公共模块声明的 Windows/x64、Linux/x86_64、Linux/aarch64 三项选择均通过；按[测试指南](../../../testing.md)执行三个 Go 包的完整测试及 vet 均通过，Windows 跳过 Linux FIFO 与原生 ABI 探针。

源码取源遵守[基础设施授权](../process/2026-10-19-repository-foundation.md#验证授权)。本次 Windows 验证位于明确要求的 `.worktrees/librealsense`，主工作副本的无关修改保持不变；Linux 直接使用 `/home/lxx/xxautolib`，只覆盖任务输入，保留无关内容和已有安装。

## 影响

编译与无相机检查不证明真实设备取流、USB 带宽、彩深同步精度或 Windows 驱动可用；没有设备时明确保留实机缺口。RSUSB 不要求内核补丁，但 Linux 实际访问相机仍依赖用户已有设备权限。旧式录制代码仍需要 LZ4，关闭 ROSBAG2 不等于完全移除录制。系统 udev、线程库及平台运行库不是自包含交付；不承诺安装树搬迁或未验证工具链。
