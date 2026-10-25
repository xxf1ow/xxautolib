# xxautolib

用 CMake 构建、安装第三方 C/C++ 库，并将预编译包以 Imported Target 导入你的工程。可以从源码构建，也可以手动下载 Release 包后在本地消费，无需在消费端安装 Go 或重新编译依赖库。

[快速开始](#快速开始) · [示例](example/CMakeLists.txt) · [开发指南](docs/development.md) · [路线图](docs/roadmap.md)

## 功能与支持范围

- **源码构建**：通过 `module.toml` 声明来源、补丁和依赖，由 CMake / ExternalProject 完成构建安装。
- **本地包消费**：统一使用 `autolib_import()` 和 `autolib::<name>`，按包内声明自动准备依赖，支持已解包目录和 `.tar.zst` 归档。
- **二进制发布**：CI 为 Windows x86_64、Linux x86_64 和 Linux aarch64 构建发行物，各包按固定文件名更新。

当前 Release 代表集包含以下三个包；其他模块的源码构建能力不等于已经提供可消费的发行包。

| 包 | 内容 | 包依赖 |
| --- | --- | --- |
| queue | ReaderWriterQueue、ConcurrentQueue 头文件，平台无关 | 无 |
| lz4 | 静态压缩库 | 无 |
| flann | 最近邻搜索库，含 C/C++ 动态库 | lz4 |

Windows 发行使用 Visual Studio 2022、v143、x64、Release 和动态 CRT（`/MD`）。Linux 使用原生 GCC 构建，FLANN 需要 OpenMP 运行库；环境与 ABI 要求见 [CI 设计](docs/agent-notes/implemented/architecture/2026-10-20-ci-system.md)。不提供 macOS 发行物。

## 快速开始

本节以 Windows x64 为例，需要 CMake 3.21+ 和 Visual Studio 2022 的 C++ 构建工具。获取本仓库源码后，以下命令均在仓库根目录的 PowerShell 中执行。

### 1. 准备本地发行目录

从 [GitHub Release](https://github.com/xxf1ow/xxautolib/releases/tag/autolib-release) 下载下列四个文件，保持文件名不变，放入本地 `dist/`。归档不需要手动解包，也不需要清单或额外的摘要文件。

```text
dist/
├── autolib-importer-windows-x86_64.exe
├── queue-any-any.tar.zst
├── lz4-windows-x86_64.tar.zst
└── flann-windows-x86_64.tar.zst
```

只运行你信任的 importer 和包内 CMake 文件。Linux 用户需选择相同目标架构的库包和匹配宿主的 importer，并为 importer 设置可执行权限；queue 始终使用同一个 `any-any` 包。

### 2. 编译示例

```powershell
cmake -S example -B build/example -G "Visual Studio 17 2022" -A x64 -T "v143,host=x64" "-DAUTOLIB_LOCAL_ROOT=$PWD/dist"
cmake --build build/example --config Release
```

`AUTOLIB_LOCAL_ROOT` 也可指向其他本地发行目录。示例只请求 queue 和 flann，importer 会从 flann 的依赖声明中找到 lz4；缺包直接报错，不回退系统安装，也不联网补齐。准备好的包位于 `dist/packages/windows-x86_64/`，不写入源码安装树。

### 3. 运行并验证

```powershell
ctest --test-dir build/example -C Release --output-on-failure
./build/example/Release/autolib_example.exe
```

CTest 应通过 `1/1` 项；直接运行示例应输出：

```text
queue: OK
lz4: OK
flann: OK
```

示例实际执行队列入队/出队、LZ4 压缩/解压和 FLANN 最近邻查询；任一步失败均以非零状态退出。Windows 构建会将目标依赖的 FLANN DLL 复制到可执行文件旁。

## 接入自己的 CMake 工程

将 [src/autolib.cmake](src/autolib.cmake) 复制到你的工程，准备本地发行目录，并在发现其他同名库目标之前调用：

```cmake
include("${CMAKE_CURRENT_SOURCE_DIR}/autolib.cmake")
autolib_import(LOCAL_ROOT "${CMAKE_CURRENT_SOURCE_DIR}/dist" PACKAGES queue flann)

target_link_libraries(my_app PRIVATE autolib::queue autolib::lz4 autolib::flann)
```

`my_app` 是你已有的可执行或库目标。`autolib::flann` 同时提供 C/C++ 接口，并传递 lz4 的头文件和链接要求，无需额外链接上游目标。宿主 importer 与目标库平台分别选择，完整参数与目录规则见[本地打包与消费](docs/development.md#本地打包与消费)。

## 从源码构建与参与开发

需要自行构建其他模块或调整配置时，从[开发指南](docs/development.md)开始；模块声明可参考 [lz4/module.toml](src/dependencies/lz4/module.toml)。公共修改的验证入口见[测试指南](docs/testing.md)，组件职责见[架构](docs/architecture.md)。提交问题时请附上平台、编译器、CMake 命令和失败日志。

## 使用限制

- 当前消费入口只支持本地目录；`REMOTE_ROOT` 自动下载尚未启用。可先从 Release 手动下载，再离线导入。
- 每个包只有一个当前平台槽位，不提供版本选择、整套发行快照或 CRT/工具集变体；消费工程需满足对应包的 ABI 和系统运行库要求。
- 已解包目录优先于归档。更新归档不会覆盖已有目录，刷新前需保护本地修改并按[更新说明](docs/development.md#本地打包与消费)移走相应准备目录。
- 归档内容校验用于检测意外损坏，不用于认证发布者或包内代码。

## 许可证

本仓库代码使用 [MIT License](LICENSE)。第三方库遵循各自上游许可证，使用和再分发时需分别遵守。
