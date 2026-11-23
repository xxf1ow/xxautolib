# Agent Note: 全平台消费工具链基线检查

Status: implemented

## 问题

二进制包下游使用过旧工具链时，常在链接或运行阶段才遇到错误。公共 CMake 入口掌握消费者选择的目标、编译器和参数，可以在准备包之前诊断可观察的基线差异。

## 决定

`autolib_import()` 在启动 importer、读取包缓存或下载之前检查已启用的 C/C++ 工具链。低于基线时，配置失败并报告检测值、最低要求和调整方法。无 C/C++ 语言的项目继续导入。

| 目标 | 消费基线 |
| --- | --- |
| Windows x64 | MSVC `19.44`，对应 VS 2022 17.14 的 `v143` 工具集。原生 MSVC 使用实际编译器版本。面向 MSVC ABI 的 Clang 使用 CMake 检测的 MSVC 兼容版本，并在错误中标明兼容模式。MinGW 工具链不适用 MSVC 版本检查。 |
| Linux x64、ARM64 | 活动 C/C++ 工具链使用 glibc `2.31` 或更新版本。通过目标头文件编译探针，GCC 和 Clang 共用规则。 |
| macOS ARM64 | 部署目标 `15.0` 或更新版本。显式目标直接比较，默认目标通过活动编译器的目标宏检查。 |

Clang 的 MSVC 兼容版本可由编译参数覆盖。真实链接运行用于确认包兼容性。Linux 的 `libstdc++` 符号需求与 macOS 的运行时要求继续由各自的产物和消费验证负责。 [MSVC 二进制兼容条件](https://learn.microsoft.com/en-us/cpp/porting/binary-compat-2015-2017#restrictions-on-binary-compatibility) · [CMake 模拟版本](https://cmake.org/cmake/help/latest/variable/CMAKE_LANG_SIMULATE_VERSION.html) · [Linux CI 基线](2026-10-20-ci-system.md#环境与-abi) · [Darwin 运行基线](2026-11-05-darwin-aarch64-native-build-and-ci.md#macos-运行基线)

## 备选方案

**将 Windows 生产切到 `v142`。** 这一选择可覆盖 VS 2019 下游。FFmpeg 8.1.3 的 `sanm.c` 在 MSVC `19.29.30159.0` 下使用 `/O1` 或 `/O2` 均超过 120 秒，`/Od` 可在约 3 秒编译成功。保留 `v143` 避免为旧编译器维护专项优化设置。 [单文件对照结果](https://github.com/xxf1ow/xxautolib/actions/runs/45178742809/artifacts/14819454905)

**逐包声明最低版本。** 公共入口统一检查已知的平台消费基线，本地、下载和缓存复用共用规则。包级差异继续由各包验收覆盖。

## 影响

Windows `v143` 的发布工具链以 VS 2022 17.14 / MSVC `19.44` 为消费下限。下游安装较旧工具集时会收到配置期诊断。Linux 和 macOS 检查目标运行环境，可继续使用适配目标 ABI 的编译器。
