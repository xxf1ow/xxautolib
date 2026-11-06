# xxautolib

把预编译或本地安装的第三方 C/C++ 库接入 CMake 工程：声明包名和可选精确版本，自动准备依赖并完成链接。

```cmake
cmake_minimum_required(VERSION 3.21.0)
set(CURRENT_TARGET_NAME autolib_example)
project(${CURRENT_TARGET_NAME} LANGUAGES C CXX)

add_executable(${CURRENT_TARGET_NAME})
aux_source_directory(${CMAKE_CURRENT_SOURCE_DIR}/src CURRENT_SRC_LIST)
target_sources(${CURRENT_TARGET_NAME} PRIVATE ${CURRENT_SRC_LIST})

message(STATUS "========================= Third Party Summary ========================")
set(AUTOLIB_URL "https://gitee.com/xxf1ow/autolib-release/releases/download/autolib-release/autolib.cmake")
set(AUTOLIB_PATH "${CMAKE_CURRENT_LIST_DIR}/3rdparty/autolib.cmake")
if(NOT EXISTS ${AUTOLIB_PATH})
    file(DOWNLOAD ${AUTOLIB_URL} ${AUTOLIB_PATH} TLS_VERIFY ON STATUS AUTOLIB_DOWNLOAD_STATUS)
    list(GET AUTOLIB_DOWNLOAD_STATUS 0 AUTOLIB_DOWNLOAD_CODE)
    if(NOT AUTOLIB_DOWNLOAD_CODE EQUAL 0)
        file(REMOVE ${AUTOLIB_PATH})
        message(FATAL_ERROR "autolib.cmake download failed: ${AUTOLIB_DOWNLOAD_STATUS}")
    endif()
endif()
include(${AUTOLIB_PATH})
autolib_import(TARGET ${CURRENT_TARGET_NAME} PRIVATE queue flann)
message(STATUS "======================================================================")
```

只请求 queue、flann，就能使用队列、FLANN C/C++ 和它记录的精确 LZ4 依赖，无需另列链接目标。首次配置按需下载，后续复用 `3rdparty/` 缓存；消费现成包不需要 Go、Python 或重新编译第三方库。将入口和缓存目录加入工程的版本控制忽略规则。

[完整示例](example/CMakeLists.txt) · [架构](docs/architecture.md) · [发行下载](https://gitee.com/xxf1ow/autolib-release/releases/tag/autolib-release)

## 使用前后对比

| 接入工作 | 自行集成第三方库 | 使用 xxautolib |
| --- | --- | --- |
| 准备依赖 | 分别获取、编译、安装各个库 | 动动小手写下三方库列表 |
| 处理依赖 | 确认 FLANN 依赖 LZ4，并配置查找路径 | 自动处理 |
| 配置链接 | 分别设置头文件、上游目标和 C/C++ 库 | 自动链接 |
| 离线复用 | 自行维护本地安装和查找配置 | 自动复用完整安装或入口缓存 |

## 快速开始

以下以原生 Linux 为例，需要 CMake 3.21+、Git、Make、兼容所用包的 C/C++ 编译器和 OpenMP 运行库。在源码仓库根执行，首次配置需联网获取发布入口及缺失的工具和包：

```sh
cmake -S example -B build/example -DCMAKE_BUILD_TYPE=Release
cmake --build build/example --parallel
./build/example/autolib_example
```

每次成功导入打印 `-- [Info] [<消费目标>] <库名> : <版本>`，包括传递依赖和缓存包；仅实际下载、解压打印过程，入口和工具获取静默。

运行成功时输出：

```text
queue: OK
lz4: OK
flann: OK
```

[示例源码](example/src/main.cpp)实际执行队列入出队、LZ4 压缩解压和 FLANN 最近邻查询，失败时返回非零退出码。准备好的包保留在 `example/3rdparty/<包名>/<版本>/`，后续配置直接复用。

## 接入自己的 CMake 工程

按首页示例下载发布入口，在其他同名库被发现之前调用。`TARGET` 后是已创建的消费目标，`PRIVATE` 可按 CMake 链接规则换成 `PUBLIC` 或 `INTERFACE`；每包统一提供 `autolib::<name>`，无需直接链接上游目标。

快捷导入只适用于已接入构建配方和消费适配、且已有本地安装或发布包的库，不能填写任意第三方库名。新增库见[贡献新包](CONTRIBUTING.md#贡献新包)。

`autolib.cmake` 的实际所在目录是唯一工具、包和下载缓存根，须专用、可写且路径不含分号。发布入口内置对应 Release 地址，不提供来源覆盖参数。请求可写 `eigen@3.4.0`；裸名读取 `package.toml` 的 latest，不排序版本。同次 configure 不允许同包不同版本。

工具和包先复用同根已有结果，只有缺失时才按入口内置来源获取；损坏、身份错误或校验失败立即报错，不自动覆盖，不回退系统库或源码构建。已有缓存和 latest 不自动更新，精确请求不改 latest。刷新前保护修改，操作见[缓存更新说明](docs/development.md#本地打包与消费)。

### 离线使用

完整有效的工具、包及精确依赖闭包可直接离线复用。严格禁止缺输入时联网，应将首页的入口路径换成[自定义编译](#自定义编译)所得原安装根的 `autolib.cmake`，并省略下载步骤，不要复制入口。本地入口内置远端为空，缺输入直接失败；远程入口缺输入时仍可能联网，没有离线开关。

## 自定义编译

需要自行调整编译选项或构建尚未发布的模块时，在仓库根目录执行（Linux，需 Git、CMake 3.20+、Make 及 C/C++ 编译器；固定 Go SDK 自动准备）：

```sh
cmake -S . -B build -G "Unix Makefiles" -DCMAKE_BUILD_TYPE=Release "-DBUILD_LIST=queue,flann"
cmake --build build --parallel
```

`BUILD_LIST` 以逗号分隔 `包名@版本`，裸包名读取包级 `package.toml` 的 `latest`，留空或省略则选择全部配方版本；依赖自动补齐，flann 会同时构建 lz4。默认安装到 `3rdparty/<包名>/<版本>/`。FLANN 还需 pkg-config 和 OpenMP，其他模块要求见各自声明。

自定义编译参数、库特定选项及重建方法见[开发指南](docs/development.md#准备与配置)；已有安装标记可能跳过构建。

安装结果包含入口和宿主工具；自定义安装根、打包与消费见[开发指南](docs/development.md#本地打包与消费)。

## 二进制发布

CI 验证后生成库和工具归档，独立发布流程将成品分发到 [Gitee 产物仓库](https://gitee.com/xxf1ow/autolib-release)，并在发布入口中注入实际目标地址。发布配置见[开发指南](docs/development.md#独立-gitee-发布)，验收范围见[测试指南](docs/testing.md#真实交付与手动验收)。

## 支持范围

支持 Windows x86_64、Linux x86_64/aarch64 和 macOS 15+（Apple Silicon）。Darwin 首轮仅 queue/lz4，流水线固定 macos-15、Apple Clang、arm64/15.0，不提供 Intel 或交叉编译包。构建与发布支持不同，实际供给以 Release 资产为准。原样 example 依赖未支持 Darwin 的 FLANN，尚未验证；queue/lz4 公共入口证据见[测试指南](docs/testing.md#ci-验证)，各库状态见[路线图](docs/roadmap.md#模块迁移候选)。

编译器、CRT、glibc、C++ ABI 及 OpenMP 必须兼容，使用者负责本地产物的兼容性；importer 不安装系统运行库或判定 ABI。Windows需提供实际DLL搜索路径。版本化本地闭环的平台及最低 CMake 缺口见[测试指南](docs/testing.md)。没有版本范围求解、工具链变体或整套发行快照。

只使用信任的发行源、importer 和包内 CMake 文件。下载及归档校验用于发现内容不匹配或意外损坏，不认证发布者，也不提供恶意代码沙箱。

## 参与贡献

欢迎修复问题、改进文档或接入第三方库。先搜索已有 [Issue](https://github.com/xxf1ow/xxautolib/issues)；涉及公共接口或较大改动时，先说明场景与方案。

Fork 后从 `master` 新建任务分支，完成针对性测试，再向本仓库 `master` 提交 PR。PR 请说明改动目的、验证命令与结果、实际验证的平台及未验证项；不要提交下载缓存、编译产物或凭据。完整步骤见[贡献指南](CONTRIBUTING.md)。

## 许可证

本仓库代码采用 [MIT License](LICENSE)。第三方库遵循各自上游许可证，再分发时需一并遵守。
