# 架构

根 [CMakeLists.txt](../CMakeLists.txt) 设置编译选项、安装目录并输出构建环境摘要，通过 `BUILD_LIST` 或目录扫描选择模块。它启用 C/C++ 编译器，并通过[公共入口](../src/autolib-builder/index.cmake)探测 Git、自动准备固定 Go SDK 和模块缓存、增量构建 builder，再一次生成并加载所选模块。SDK、编译或生成失败时 configure 直接失败，不加载旧程序或生成结果，不升级工具链或依赖。

## 目录职责

模块定义与流水线引擎分为两层。`src/dependencies/<模块名>/module.toml` 只声明数据，中文注释说明维护约束；补丁与专用执行文件放在同一模块目录。默认发现全部 module.toml，`BUILD_LIST` 使用逗号分隔模块名并去重，缺少声明时报错。

`src/autolib-builder/` 中 definition.go 校验 TOML，generate.go 展开稳定依赖闭包并登记实际 Go/TOML 输入，cmake.go 输出原生 ExternalProject。根 Go module 固定 TOML 解析依赖，SDK 只用于生产端。CMake 负责自动重配置；生成内容不变时保留时间戳。ExternalProject 执行获取、补丁、构建、安装和自定义步骤；apply_patch.cmake 支持主源码或资源补丁，不自动恢复冲突源码。

模块使用 cmake 构建或 copy 安装。独立依赖用 `depends` 声明，contrib 等附属输入归消费模块。原生目标等待直接依赖安装及资源补丁；配置代理只为当前子配置注入直接依赖的前缀、包、pkg-config 和 include 位置，保留调用方 include。窄引用提供资源源码根，不注入 Python 解释器；规则见[依赖接线决定](agent-notes/implemented/architecture/2026-10-20-flann-opencv-dependencies.md)。

子项目沿用父生成器身份、显式 CRT，固定 Release；模块 cache 优先于父级值，再采用引擎 policy、PIC、C++ 标准兜底值，不向父级 cache 写默认值。

各工作副本独立维护 `.tools/go/`、`git/`、`build/` 和 `3rdparty/`，不整体跨平台传输。私有 SDK 与 Go 缓存在 `.tools/go/`，不随 build 清理，也不修改全局环境。主 Git 位于 `git/<模块>/`，Git 资源位于 `git/_resources/<模块>/<资源>/`，归档下载共用根 `git/`；解压、编译和步骤留在 `build/ep/`，builder 留在 `build/tools/`，生成结果留在 `build/generated/`。configure 检查本地 `.git` 与安装 sentinel；命中 sentinel 保留空目标及依赖顺序，声明校验仍执行。复用限制见[本地复用设计](agent-notes/implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)。builder 同目录的 Go 测试验证公共引擎，库专用测试验证安装产物。

## 工具与流水线职责

构建机制与生产发布决策分别维护。三个工具使用独立源码目录和可执行入口，通过命令行协作，不互相导入或混编业务实现。

- autolib-builder 维护构建与本地打包机制；CMake/ExternalProject 执行实际构建，CI 不重写构建规则。
- autolib-importer 维护消费校验和准备，不承担生产缓存策略或远端发布。
- autolib-ci 维护生产策略：根据 builder 提供的事实规划模块依赖层与通用包 publisher，计算 build ID，校验生产缓存，并调用既有工具完成消费验证和安装负载恢复；不重新解析模块声明或复制构建、打包和解包引擎。
- 发布编排、Release 写入和结果汇总归 autolib-ci，触发、runner/container、环境准备、权限、job 依赖与运行串行化归 [workflow](../.github/workflows/build-platform.yml)；接线与验收要求由独立的 [CI 系统设计](agent-notes/proposed/architecture/2026-10-20-ci-system.md)约定，不在 builder 或 importer 中实现。

普通本地构建和离线消费不依赖 CI、GitHub 或 workflow，builder 不读取 CI 配置或凭据。

CI 按固定名资产的 label、digest 和实际包内容复用缓存；未命中才构建，逐槽消费验证后同名替换。各平台按依赖层共用隔离安装根；finalize 只汇总，不写发行清单。

## 本地二进制消费

builder 的 `package` 将显式选择且提供 adapter 的安装树复制到 staging，只在副本中修正本包及声明 sibling 的生产路径，生成固定名 tar.zst 和宿主 importer，不改原安装。每包以四行头声明名称、平台和直接依赖，归档显式启用 Zstd 内容校验；无全局消费清单或外置摘要文件。

下游携带单文件 [autolib.cmake](../src/autolib.cmake)，从显式 LOCAL_ROOT 直接执行固定名宿主 importer。`src/autolib-importer/` 按已解包目录、固定名归档的顺序查找，仅在候选不存在时继续；它校验包头、安全解包并按依赖先序提交完整结果，CMake 随后加载 adapter，提供全局 `AutoLib::<name>`。消费者不需要 Go、Python、uv 或 builder。

queue、lz4、flann 提供精确布局 adapter；仅请求 flann 即取得 sibling lz4，不回退系统包。工具按宿主、包按目标选择；通用包使用 any/any 身份。完整本地目录权威，更新由用户显式刷新；同一本地根的写入由调用方串行。系统运行库不因此成为可下载包。来源、工作根和目标在同次 configure 固定；格式与取舍见[包消费设计](agent-notes/proposed/architecture/2026-10-24-manifestless-package-consumption.md)，调用及限制见开发、测试指南。

## 文档职责

[开发指南](development.md)拥有环境和构建命令，[测试指南](testing.md)拥有验证入口。长期理由与授权见[仓库基础设施决定](agent-notes/implemented/process/2026-10-19-repository-foundation.md)；模块定义与生成规则见[流水线设计](agent-notes/implemented/architecture/2026-10-19-toml-externalproject-zlib.md)。

[路线图](roadmap.md)区分本仓库已验收能力、待迁移能力及待实施方向，不作为当前接口说明。
