# xxautolib

把本地安装或归档中的第三方 C/C++ 库接入 CMake 工程：声明包名和可选精确版本，自动准备依赖并完成链接。

```cmake
set(AUTOLIB_LOCAL_ROOT "/absolute/path/to/installed-or-archives")
set(AUTOLIB_REMOTE_ROOT "")
include("${PROJECT_SOURCE_DIR}/3rdparty/autolib.cmake")
add_executable(app src/main.cpp)
autolib_import(TARGET app PRIVATE queue flann)
```

只请求 queue、flann，就能使用队列、FLANN C/C++ 和它记录的精确 LZ4 依赖；不需要再列链接目标。首次配置从本地来源复制或解包到入口缓存，后续复用缓存。消费现成包不需要 Go、Python 或重新编译第三方库。

[快速开始](#快速开始) · [完整示例](example/CMakeLists.txt) · [开发](docs/development.md) · [测试](docs/testing.md) · [架构](docs/architecture.md) · [参与贡献](CONTRIBUTING.md) · [发行下载](https://github.com/xxf1ow/xxautolib/releases/tag/autolib-release)

## 使用前后对比

| 接入工作 | 自行集成第三方库 | 使用 xxautolib |
| --- | --- | --- |
| 准备依赖 | 分别获取、编译、安装各个库 | 动动小手写下三方库列表 |
| 处理依赖 | 确认 FLANN 依赖 LZ4，并配置查找路径 | 自动处理 |
| 配置链接 | 分别设置头文件、上游目标和 C/C++ 库 | 自动链接 |
| 离线复用 | 自行维护本地安装和查找配置 | 自动复用本地目录或归档 |

这减少的是依赖接入与链接配置，不替代编译器、系统运行库或 ABI 兼容要求；自行集成也可使用其他包管理工具。

## 快速开始

以下以原生 Linux 为例，需要 CMake 3.21+、Make、兼容所用包的 C/C++ 编译器和 OpenMP 运行库。先按[自定义编译](#自定义编译)准备 queue、flann 安装根，再在仓库根执行命令，将占位路径替换为该安装根的绝对路径：

```sh
mkdir -p example/3rdparty
cp src/autolib.cmake example/3rdparty/
cmake -S example -B build/example -DCMAKE_BUILD_TYPE=Release "-DAUTOLIB_LOCAL_ROOT=<安装根绝对路径>" -DAUTOLIB_REMOTE_ROOT=
cmake --build build/example --parallel
./build/example/autolib_example
```

单文件入口从来源准备宿主 importer，再根据包内声明准备依赖，不修改安装源，也不要求下游持有生产源码。

成功时输出：

```text
queue: OK
lz4: OK
flann: OK
```

[示例源码](example/src/main.cpp)实际执行队列入出队、LZ4 压缩解压和 FLANN 最近邻查询，失败时返回非零退出码。准备好的包保留在 `example/3rdparty/<包名>/<版本>/`，后续配置直接复用。

## 接入自己的 CMake 工程

将 [autolib.cmake](src/autolib.cmake) 放入工程的 `3rdparty/`，在其他同名库被发现之前，按首页代码调用即可。`TARGET` 后是已创建的消费目标，`PRIVATE` 可按 CMake 链接规则换成 `PUBLIC` 或 `INTERFACE`；每包统一提供 `autolib::<name>`，无需直接链接上游目标。

快捷导入只适用于已在本仓库接入构建配方（`module.toml`）和消费适配（`autolib-import.cmake`），并已生成本地包或发布到所选发行源的库。不能填写任意第三方库名；缺包时不会回退到源码编译。新增库见[贡献新包](CONTRIBUTING.md#贡献新包)。

`autolib.cmake` 所在目录是固定缓存；`AUTOLIB_LOCAL_ROOT` 默认空，非空来源必须与缓存为不同真实目录。include 前设置来源，首次 include 后固定。请求可写 `eigen@3.4.0`；裸名读取 `package.toml` 的 latest，不排序版本。同次 configure 不允许同包不同版本，独立工程可分别选择不同版本。

查找顺序为入口缓存、本地已解压安装、本地归档、所选远端；只有不存在才继续，损坏或身份错误立即失败，不回退系统库或源码构建。已有缓存和 latest 不自动更新，精确请求不改 latest。本地用法显式关闭远端；远端需要按[元数据协议](docs/development.md#本地打包与消费)准备专用产物仓库和匹配归档，当前公开仓库尚未按该协议初始化，不能直接用于新消费者。

### 离线使用

将宿主 importer 和版本化库归档及包级 `package.toml` 放入来源根，或使用完整安装根，再显式设置本地来源并关闭远端。Linux 裸 importer 需要可执行权限；也支持单程序 `.tar.xz`。完整入口缓存可在两源均空时消费：

```sh
cmake -S example -B build/example -DCMAKE_BUILD_TYPE=Release -DAUTOLIB_LOCAL_ROOT= -DAUTOLIB_REMOTE_ROOT=
```

缺输入时直接失败。更新来源不会覆盖缓存；刷新前保护修改，再按[缓存更新说明](docs/development.md#本地打包与消费)处理对应版本。删除缓存 latest 目标目录后，裸名请求才重新发现来源选择，不更新无关缓存。

## 自定义编译

需要自行调整编译选项或构建尚未发布的模块时，在仓库根目录执行（Linux，需 Git、CMake 3.20+、Make 及 C/C++ 编译器；固定 Go SDK 自动准备）：

```sh
cmake -S . -B build -G "Unix Makefiles" -DCMAKE_BUILD_TYPE=Release "-DBUILD_LIST=queue,flann"
cmake --build build --parallel
```

`BUILD_LIST` 以逗号分隔 `包名@版本`，裸包名读取包级 `package.toml` 的 `latest`，留空或省略则选择全部配方版本；依赖自动补齐，flann 会同时构建 lz4。默认安装到 `3rdparty/<包名>/<版本>/`。FLANN 还需 pkg-config 和 OpenMP，其他模块要求见各自声明。

通用 CMake 参数通过 `-D` 或 toolchain 设置；库特定选项修改对应 `src/dependencies/<name>/<version>/module.toml`，模块显式配置优先于父级参数。变更版本或选项后，按[重建说明](docs/development.md#准备与配置)处理受影响的源码、构建状态和安装树；已有安装标记可能跳过构建。

版本化安装输出宿主 importer，可直接作为只读本地来源；需要归档时另行打包。自定义安装根、打包和重建操作见[开发指南](docs/development.md#本地打包与消费)。

## 二进制发布

版本化本地打包不依赖 CI。Git 元数据及 Release 归档消费已有受控验证；专用仓库的初始化、独立发布和真实托管验收仍按[后续阶段](docs/roadmap.md)实施，既有 Release 和历史三平台验证不证明新协议已可公开发布或下载。

## 支持范围

支持 Windows x86_64、Linux x86_64、Linux aarch64；不提供 macOS 发行物或通用交叉编译环境。源码构建与预编译发布是不同能力，包的实际供给范围以 Release 资产为准。queue、lz4、flann 的完整消费示例见上文；OpenCV、Boost、PCL 等复杂模块通过相同构建体系管理来源与依赖，不要求全部库都作为二进制发行物。各库迁入及验收状态见[路线图](docs/roadmap.md#模块迁移候选)。

编译器、CRT、glibc、C++ ABI 及 OpenMP 必须兼容，使用者负责本地产物的兼容性；importer 不安装系统运行库或判定 ABI。Windows需提供实际DLL搜索路径。版本化本地闭环的平台及最低 CMake 缺口见[测试指南](docs/testing.md)。没有版本范围求解、工具链变体或整套发行快照。

只使用信任的发行源、importer 和包内 CMake 文件。下载及归档校验用于发现内容不匹配或意外损坏，不认证发布者，也不提供恶意代码沙箱。

## 参与贡献

欢迎修复问题、改进文档或接入第三方库。先搜索已有 [Issue](https://github.com/xxf1ow/xxautolib/issues)；涉及公共接口或较大改动时，先说明场景与方案。

Fork 后从 `master` 新建任务分支，完成针对性测试，再向本仓库 `master` 提交 PR。PR 请说明改动目的、验证命令与结果、实际验证的平台及未验证项；不要提交下载缓存、编译产物或凭据。完整步骤见[贡献指南](CONTRIBUTING.md)。

## 许可证

本仓库代码采用 [MIT License](LICENSE)。第三方库遵循各自上游许可证，再分发时需一并遵守。
