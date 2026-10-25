# xxautolib

把第三方 C/C++ 库接入 CMake 工程：声明需要的库，自动获取预编译包、准备依赖并完成链接。也可使用统一的构建配方从源码编译、安装和发布这些库。

```cmake
include("${PROJECT_SOURCE_DIR}/3rdparty/autolib.cmake")
add_executable(app src/main.cpp)
autolib_import(TARGET app PRIVATE queue flann)
```

只请求 queue、flann，就能使用队列、FLANN C/C++ 和它依赖的 LZ4；不需要再列一遍链接目标。首次配置按需下载，后续优先复用本地包。消费预编译包不需要安装 Go、Python，也不需要重新编译第三方库。

[快速开始](#快速开始) · [完整示例](example/CMakeLists.txt) · [开发指南](docs/development.md) · [参与贡献](CONTRIBUTING.md) · [发行下载](https://github.com/xxf1ow/xxautolib/releases/tag/autolib-release)

## 使用前后对比

以一个同时使用队列、FLANN 和 LZ4 的程序为例：

| 接入工作 | 自行集成第三方库 | 使用 xxautolib |
| --- | --- | --- |
| 准备依赖 | 分别获取、编译、安装各个库 | 动动小手写下三方库列表 |
| 处理依赖 | 确认 FLANN 依赖 LZ4，并配置查找路径 | 自动处理 |
| 配置链接 | 分别设置头文件、上游目标和 C/C++ 库 | 自动链接 |
| 离线复用 | 自行维护本地安装和查找配置 | 自动复用本地目录或归档 |

这减少的是依赖接入与链接配置，不替代编译器、系统运行库或 ABI 兼容要求；自行集成也可使用其他包管理工具。

## 快速开始

以下以原生 Linux（x86_64 或 aarch64）为例，需要 CMake 3.21+、Make、兼容发行包的 C/C++ 编译器和 OpenMP 运行库。先获取本仓库源码，在仓库根目录执行命令；首次使用需要能访问所选发行源。

```sh
mkdir -p example/3rdparty
cp src/autolib.cmake example/3rdparty/
cmake -S example -B build/example -DCMAKE_BUILD_TYPE=Release
cmake --build build/example --parallel
./build/example/autolib_example
```

单文件入口自动准备宿主 importer，再根据包内声明获取依赖、校验和解包，无需手动安装下载工具、逐个下载依赖或配置上游链接目标。

成功时输出：

```text
queue: OK
lz4: OK
flann: OK
```

[示例源码](example/src/main.cpp)实际执行队列入出队、LZ4 压缩解压和 FLANN 最近邻查询，失败时返回非零退出码。准备好的包保留在 `example/3rdparty/packages/<os>-<arch>/`，后续配置直接复用。

## 接入自己的 CMake 工程

将 [autolib.cmake](src/autolib.cmake) 放入工程的 `3rdparty/`，在其他同名库被发现之前，按首页代码调用即可。`TARGET` 后是已创建的消费目标，`PRIVATE` 可按 CMake 链接规则换成 `PUBLIC` 或 `INTERFACE`；每包统一提供 `autolib::<name>`，无需直接链接上游目标。

快捷导入只适用于已在本仓库接入构建配方（`module.toml`）和消费适配（`autolib-import.cmake`），并已生成本地包或发布到所选发行源的库。不能填写任意第三方库名；缺包时不会回退到源码编译。新增库见[贡献新包](CONTRIBUTING.md#贡献新包)。

本地目录默认 `${PROJECT_SOURCE_DIR}/3rdparty`，远端默认使用专用 [Gitee Release 镜像](https://gitee.com/xxf1ow/autolib-release/releases)。可在 include 前设置变量或通过 `-D` 覆盖；首次 include 后来源固定。改用 GitHub 时：

```cmake
set(AUTOLIB_REMOTE_ROOT "https://github.com/xxf1ow/xxautolib/releases/tag/autolib-release")
include("${PROJECT_SOURCE_DIR}/3rdparty/autolib.cmake")
```

查找顺序为本地已解包目录、本地归档、所选远端。本地工具及依赖齐全时不联网、不检查更新；只有候选不存在时才继续，损坏或身份错误立即失败，不回退系统同名库或其他发行源。公共 Release 消费不需要 token。

### 离线使用

将宿主 importer 和所需 `.tar.zst` 包按发行文件名放入本地根，或复制已经准备好的完整本地根，再设 `AUTOLIB_REMOTE_ROOT` 为空。Linux 手动提供的 importer 需要可执行权限；归档不必手动解包，也不需要清单或额外摘要文件。默认本地根下的示例可这样配置：

```sh
cmake -S example -B build/example -DCMAKE_BUILD_TYPE=Release -DAUTOLIB_REMOTE_ROOT=
```

缺包时离线配置直接失败。修改 `AUTOLIB_LOCAL_ROOT` 可复用其他本地根；更新归档不会覆盖已解包目录，刷新前先保护本地修改，再按[缓存更新说明](docs/development.md#本地打包与消费)处理对应包。

## 自定义编译

需要自行调整编译选项或构建尚未发布的模块时，在仓库根目录执行（Linux，需 Git、CMake 3.20+、Make 及 C/C++ 编译器；固定 Go SDK 自动准备）：

```sh
cmake -S . -B build -G "Unix Makefiles" -DCMAKE_BUILD_TYPE=Release "-DBUILD_LIST=queue,flann"
cmake --build build --parallel
```

`BUILD_LIST` 以逗号分隔，留空或省略则选择全部模块；依赖自动补齐，flann 会同时构建 lz4。默认安装到 `3rdparty/<模块名>/`。FLANN 还需 pkg-config 和 OpenMP，其他模块要求见各自声明。

通用 CMake 参数通过 `-D` 或 toolchain 设置；库特定选项修改对应 `src/dependencies/<name>/module.toml`，模块显式配置优先于父级参数。变更版本或选项后，按[重建说明](docs/development.md#准备与配置)处理受影响的源码、构建状态和安装树；已有安装标记可能跳过构建。

安装树不是 importer 的包缓存。要快捷导入自编译产物，还需[使用 builder 打包](docs/development.md#本地打包与消费)，再将包放入消费工程的本地根。

## 二进制发布

独立 CI 在三平台原生构建、打包并验证真实消费，将固定文件名的产物发布到 GitHub Release，再同步到专用 Gitee 镜像。平台无关的头文件包只发布一份，机器码包和 importer 按平台区分。本地编译和打包不依赖 CI。

## 支持范围

支持 Windows x86_64、Linux x86_64、Linux aarch64；不提供 macOS 发行物或通用交叉编译环境。源码构建与预编译发布是不同能力，包的实际供给范围以 Release 资产为准。queue、lz4、flann 的完整消费示例见上文；OpenCV、Boost、PCL 等复杂模块也通过相同构建体系管理来源与依赖，不要求全部库都作为二进制发行物。

Windows 包使用 VS 2022、v143、Release、动态 CRT（`/MD`），运行时需提供实际依赖的 DLL 搜索路径；Linux 需满足对应 glibc、C++ ABI 和 OpenMP 要求。系统运行库不由 importer 安装。每包只维护当前槽位，没有多版本求解、工具链变体或整套发行快照，本地包也不会自动追随远端更新。

只使用信任的发行源、importer 和包内 CMake 文件。下载及归档校验用于发现内容不匹配或意外损坏，不认证发布者，也不提供恶意代码沙箱。

## 参与贡献

欢迎修复问题、改进文档或接入第三方库。先搜索已有 [Issue](https://github.com/xxf1ow/xxautolib/issues)；涉及公共接口或较大改动时，先说明场景与方案。

Fork 后从 `master` 新建任务分支，完成针对性测试，再向本仓库 `master` 提交 PR。PR 请说明改动目的、验证命令与结果、实际验证的平台及未验证项；不要提交下载缓存、编译产物或凭据。完整步骤见[贡献指南](CONTRIBUTING.md)。

## 开发文档

- [开发指南](docs/development.md)：环境准备、源码构建、本地打包。
- [测试指南](docs/testing.md)：测试选择、执行命令与真实消费验证。
- [架构说明](docs/architecture.md)：builder、importer 与 CI 的职责。

## 许可证

本仓库代码采用 [MIT License](LICENSE)。第三方库遵循各自上游许可证，再分发时需一并遵守。
