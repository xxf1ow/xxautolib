# Agent Note: 模块扩展与依赖接线的沿用决定

Status: proposed

## 问题

zlib 和 queue 能验证本地基础流水线，但不能代表 URL 归档、预编译输入、模块依赖或附属资源的全部使用方式。后续模块若只声明构建顺序，可能仍从系统环境找到错误依赖；若将每种消费方式抽成通用规则，又会把模块的专用配置混入引擎。

## 方案

沿用已有模块的职责划分和依赖决定，按真实接入需求扩展 TOML；本记录是本仓库的后续约束，不代表以下能力已经实现。当前字段与执行规则仍由[流水线决定](../../implemented/architecture/2026-10-19-toml-externalproject-zlib.md)拥有，阶段顺序由[路线图](../../../roadmap.md)拥有。

本地输入复用、安装 sentinel、源码子目录、平台/架构来源选择及 lz4、onnxruntime 接入由独立的[本地复用与平台输入设计](../../implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)拥有。本文保留 FreeType 基线和后续模块扩展的总体约束，不重复该批实施方案。

### 首批目标与范围

首批以 FreeType 2.13.2 验证 URL 归档获取、摘要校验、多地址顺序回退及真实构建安装，同时适配参考仓库的新增模块技能。使用者通过现有根 CMake 入口选择 freetype，获得本仓库安装树中的头文件、静态库和安装配置；不要求下游导入或二进制交付。首批完成后即可独立验收，不以其他模块迁移为完成条件。

参考基线为 AutoLibBuilder 的 `src/dependencies/freetype.cmake` 与 `.agents/skills/add-third-party-module/`。FreeType 的版本、三个地址的顺序、SHA256 和库特定构建设置保持参考配置，仅作 TOML 定义与通用生成引擎所需的接口适配；不修改参考仓库或上游 FreeType 源码，不顺手重构根 CMake。

首批不接入 portaudio、平台或架构来源选择、源码子目录字段、跨 build 输入缓存、安装 sentinel、模块依赖解析、发布 adapter、打包和 CI。下文的依赖、复杂资源与系统包方向仍按后续实际模块分别设计，不构成本批实施范围。

### 归档声明与原生执行

`[source]` 和 `[resources.<资源名>]` 继续共用来源解析规则，来源在现有 `GIT_REPOSITORY`、`GIT_TAG` 组合与 URL 归档之间二选一。Git 来源的现有语义不变。归档新增以下字段，不为尚未使用的 ExternalProject 选项预建 Schema：

- `URL`：非空字符串数组，数组元素为非空地址，按声明顺序传给原生 `URL`；单地址也使用数组。
- `URL_HASH`：必填字符串，本批使用 `SHA256=<64 位十六进制摘要>`。所有候选地址必须提供字节相同的归档，共用该摘要；版本号相同但压缩内容不同不构成可替换镜像。
- `DOWNLOAD_NAME`：可选的单个文件名，提供时直接传给原生同名参数；不提供时由 ExternalProject 处理默认文件名。

缺少 URL 或摘要、字段类型错误，以及同时声明 Git 和 URL 来源时，在 configure 的声明解析阶段失败，沿用携带模块路径和失败字段的诊断方式。主来源和资源使用同一套字段校验与参数展开，不在引擎中添加 FreeType 或镜像站点分支。

Python 只解析声明并生成原生参数，不访问网络、下载文件、校验归档或执行解压。CMake 的 ExternalProject 在 build 阶段完成下载、摘要校验与解压；首个地址下载失败时继续尝试后续地址，全部失败则获取步骤失败，未经摘要校验的归档不得进入解压和构建。回退、内部重试及错误输出全部沿用 [CMake 3.20 ExternalProject 的原生 URL 能力](https://cmake.org/cmake/help/v3.20/module/ExternalProject.html#download-step-options)，不新增下载脚本、自定义 `DOWNLOAD_COMMAND`、Python 重试循环或地址健康状态。摘要不匹配不等于立即终止：原生下载脚本会移除不匹配的文件并继续尝试，候选与内部重试均耗尽后才失败；不承诺具体重试次数或间隔，依据见 [CMake 3.20.6 下载脚本](https://github.com/Kitware/CMake/blob/v3.20.6/Modules/ExternalProject-download.cmake.in)。

归档解压保留 `build/ep/<模块名>/src/`，资源保留模块内独立解压目录；下载和 Git 持久输入位置由[本地复用设计](../../implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)拥有。不增加缓存数据库；来源变更仍适用[开发指南](../../../development.md)的人工准备规则，不能覆盖已有源码修改或清空其他模块。

单顶层目录归档沿用原生展开规则：[CMake 3.20.6 的解压实现](https://github.com/Kitware/CMake/blob/v3.20.6/Modules/ExternalProject.cmake#L1445)将唯一顶层目录的内容作为 `SOURCE_DIR`，因此 FreeType 归档中的 `freetype-2.13.2/` 不会额外嵌套在 `src/` 下，根入口位于 `src/CMakeLists.txt`。这不需要新增 `SOURCE_SUBDIR`。`.tar.xz` 由 CMake 原生解压，Windows 实际构建验证该环境的格式支持，不为假设中的精简 CMake 另加探测或解压工具。

### FreeType 定义与安装

新增 `src/dependencies/freetype/module.toml`，类型为 `cmake`，版本为 `2.13.2`。来源数据固定为：

```toml
[source]
URL = [
    "https://download.savannah.gnu.org/releases/freetype/freetype-2.13.2.tar.xz",
    "https://download-mirror.savannah.gnu.org/releases/freetype/freetype-2.13.2.tar.xz",
    "https://sourceforge.net/projects/freetype/files/freetype2/2.13.2/freetype-2.13.2.tar.xz/download",
]
URL_HASH = "SHA256=12991c4e55c506dd7f9b765933e62fd2be2e06d421505d7950a132e4f1bb484d"
DOWNLOAD_NAME = "freetype-2.13.2.tar.xz"
```

定义使用中文维护注释说明用途、版本与摘要的关联，以及地址顺序和统一归档名；不复述公共参数。参考中的位置无关代码与 C++17 设置由项目公共默认值保留，不在模块中重复声明。生成、构建和安装复用现有 `cmake` 类型，由公共引擎传递父生成器、平台、完整工具集、instance、CRT 和固定 Release。安装前缀为 `3rdparty/freetype/`，核对 `include/freetype2/ft2build.h`、FreeType 公开头文件、平台对应的静态库，以及参考声明的 CMake 包配置与 pkg-config 文件。

FreeType 的库特定构建行为保持参考基线，不新增依赖声明，不把可选外部依赖接成仓库依赖链，也不为本批增加依赖查找或后端探测机制。实际构建应记录发现的可选依赖与产物；若必须改变参考选项或引入其他模块才能完成目标，先确认受影响的设计，不自行扩大范围。

[FreeType 2.13.2 的 CMake 配置](https://github.com/freetype/freetype/blob/VER-2-13-2/CMakeLists.txt)默认探测 ZLIB、BZip2、PNG、HarfBuzz 和 BrotliDec；参考基线未设置 `FT_DISABLE_ZLIB`、`FT_DISABLE_BZIP2`、`FT_DISABLE_PNG`、`FT_DISABLE_HARFBUZZ` 或 `FT_DISABLE_BROTLI`。本批不额外启用这些关闭开关，因为它们会改变参考构建的依赖或功能。发现的外部库可能为静态或动态库，静态 FreeType 也不等于依赖自包含；导出的 CMake 配置可能含外部路径，pkg-config 文件可能记录依赖名或链接参数。双平台验收核对 configure/cache 与导出配置，记录实际依赖，不将静态归档的 `ldd` 输出作为自包含证据；隔离依赖与可搬迁包不属于本批承诺。

### 公共 CMake 默认值

`src/build_system/cmake.py` 在 CMake 类型子项目参数组装处提供三个兜底值：`CMAKE_POLICY_VERSION_MINIMUM=3.5`、`CMAKE_POSITION_INDEPENDENT_CODE=ON`、`CMAKE_CXX_STANDARD=17`。`index.cmake` 只转发父级已经定义的同名值，不写入默认值；根 `CMakeLists.txt` 不维护这三个子项目默认项。合并优先级为模块 `[cmake.cache]` 显式值 > 父级已定义值 > 引擎兜底值，最终通过原生 `CMAKE_ARGS` 传给子项目；copy 类型及只取源资源不应用这些默认项。根入口 `-D`、toolchain 或已有 cache 可提供父级值；已有 cache 不自动清理。FreeType 与 zlib 共用公共默认项。

ExternalProject 的子配置不隐式继承父级变量；在子 configure 命令中传入 `-D`，可确保这些值在子项目的 `cmake_minimum_required()` 和目标创建之前可用。默认值不写入父级 cache，避免自动默认项持久化后与用户配置混淆；只把根文件中的 `set(... CACHE ...)` 移到公共入口不能消除这种影响。

policy 参数仅提供下限：上游较高的 policy 声明不降低，低于 3.5 的声明则使用至少 3.5 的行为，须通过实际构建验证兼容性，不保证任意旧库自动兼容。CMake 4.0+ 支持该变量；环境变量只能初始化新构建树中的同名缓存，显式 `-D` 设置则不依赖启动进程环境，也能更新已有子项目的配置。最低 CMake 仍为 3.20，不修改上游源码或其实际 CMake 版本要求。

普通 `cmake -S . -B build` 与 CMake Tools Kit/Variant 使用同一公共入口；仓库不提供会触发 CMake Tools 自动切换模式的根 `CMakePresets.json`，也不依赖 `.vscode/settings.json`、全局环境变量或包装命令。公共回归通过无 preset、无 policy 环境变量的真实入口构建旧 policy 与较高 policy 子项目，并验证公共值、模块覆盖及重新配置后的值。参考 CI、`build-defaults.cmake` toolchain 和固定安装库目录设置不在本批范围。

### 新增模块技能的必要适配

技能放在本仓库 `.agents/skills/add-third-party-module/SKILL.md`，保持参考技能的用途与触发范围：新增、迁移、集成或升级第三方模块时使用；已安装库的运行时用法、纯环境排错、公共引擎改动和发布操作不因此变成模块接入任务。技能可以在新增模块同时需要扩展引擎时提供模块规则，但不替代该扩展的设计。

只适配与当前接口有关的内容：模块路径改为 `src/dependencies/<模块名>/module.toml`，源码构建与复制安装分别使用既有 `cmake`、`copy` 类型，专用文件归模块目录；公共参数和步骤由生成引擎与原生 ExternalProject 拥有。固定来源、摘要、中文维护注释、工具要求和实际构建安装验证沿用本仓库现有决定。技能保留原有的助手核对流程。

不复制依赖旧 `tp_meta`、`tp_setup`、`tp_source` 接口的 CMake 模板，不将 sentinel、持久 `git/` 目录、依赖注入、版本覆盖或发布 adapter 描述为本仓库已支持的能力。原版与实际实现不一致的取源说明不沿用。示例直接引用本仓库的 zlib、queue 和本批 FreeType 定义，不维护同一份完整模块的拷贝；产品规则链接[架构](../../../architecture.md)、[开发指南](../../../development.md)、[测试指南](../../../testing.md)及本记录，不复制成另一份接口规范。后续能力接入时，技能随其实际接口更新，而不提前给出不可执行模板。

技能以说明和既有实例引用为主，不增加安装器、生成器、下载脚本、插件或独立评测框架。验收检查技能元数据、全部采用的本地链接，以及新增源码库、复制安装库和库运行时问题的触发范围；不把措辞匹配测试当成行为正确性的证明。

原版 skill 元数据校验器所需 PyYAML 放入根 uv 的 `dev` 开发依赖组并锁定版本，重复校验使用根环境，不逐次用临时 `--with` 补依赖；生成流水线本身不引入 PyYAML 运行时依赖。

### 首批验证与交接

新增引擎行为在 `tests/build_system/` 补公共回归。解析与参数生成验证 Git 来源不受影响，URL 列表顺序、摘要和归档名正确传递，并检查主来源与资源均能使用归档。小型归档与本地 HTTP fixture 通过生成的 ExternalProject 执行，覆盖首地址成功时不请求备用地址、首地址返回 404 后使用备用地址、全部地址失败、首地址内容摘要错误后备用地址内容正确时成功，以及所有候选内容均不匹配摘要时不安装产物。不将摘要错误断言为立即终止，不固定原生内部重试次数；公网可用性不作为故障注入条件，也不穷举所有网络错误。带单顶层目录的小型归档应直接提供 `src/CMakeLists.txt` 并完成安装。

HTTP fixture 沿用现有 unittest 与 Python 标准库，在 `127.0.0.1:0` 启动服务，将实际绑定端口组成普通 URL 传入声明，不增加引擎端口接口或 pytest 依赖。无论测试成功或失败，均保证从服务线程以外调用 `shutdown()`、关闭套接字并等待服务线程退出，避免端口冲突或线程遗留。

FreeType 从真实根入口选择单模块，先在 Windows x64 实际下载、构建和安装，再在既有授权的 Linux aarch64 设备 `lxx@192.168.0.127` 的 `/home/lxx/xxautolib` 中验证。源码传输、已有数据保护及目录限制继续遵守[基础设施与验证授权](../../implemented/process/2026-10-19-repository-foundation.md)。两端均核对安装头文件、静态库与安装配置，不以 configure 成功或小型 fixture 替代 FreeType 的实际构建，不为该模块强制新增专用测试工程。

局部开发先运行来源解析与 CMake 生成的定向回归；首批全部完成后运行公共测试全集，并验证技能及文档。当前架构与命令在能力实现后分别更新到其文档所有者，路线图记录本批验收范围，不把其他获取安装扩展一并标成完成。只有本批验收结果满足下列标准才结束本批，不自动推进后续缓存或依赖工作。

### 来源与安装

Git 固定来源和 URL 归档是不同输入方式。归档必须配登记的内容摘要；换版本须同时核对地址、摘要和安装布局，不能只改展示版本。预编译输入不执行虚假的 configure/build，沿用获取后复制安装的类型。平台或架构决定不同归档时，必须声明具体数据，不靠宿主硬件自动选择后端。

跨 build 的输入和安装复用见[本地复用与平台输入设计](../../implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)。主来源与资源不自动升级的维护边界继续生效，不能把旧安装树存在视为新版本已安装。

### 模块依赖与资源

独立交付的库只定义一个模块，依赖它时复用该模块，不在使用方重复构建。依赖解析需要发现所需模块、建立原生构建顺序，并传递已安装产物的查找位置。标准通道包括 CMake 包目录、安装前缀和 pkg-config 路径；某库只能接受私有变量或参数时，由它自己的声明或专用文件维护，不向公共引擎加入库名分支。具体字段在首个依赖模块接入时定义，不预留无效空字段。

Boost 和 PCL 都须直接声明并定位本仓库 zlib。PCL 不能依赖 Boost 恰巧把 zlib 的搜索位置带进来，也不能用仅含运行库的系统包代替构建所需头文件和静态库。lz4 → flann → pcl 可验证传递依赖，但下游二进制消费者是否自动取闭包是另一项决定，不混为同一机制。

OpenCV contrib 和 oneTBB 的 HWLOC 是所属模块的附属编译输入；不要求拆成公开模块。引擎负责获取、校验、解压与原生依赖；资源内部的 include、library、DLL、源码子目录及专用选项归所属模块。资源在 configure 时尚未生成不是错误，必须在实际使用前由构建依赖准备好。对资源自身打补丁及引用资源路径的具体声明，在接入该模块时补齐，不宣称当前主目标 steps 已覆盖所有资源步骤。

ImGui 的专用 CMakeLists、sed 修改和安装期修正继续放在模块目录，通过文件引用及原生步骤执行；保持参考逻辑，仅作两层架构必要适配。不能为了 TOML 单文件化而重写这些逻辑。

### 工具与系统依赖

普通 Windows 模块不全局强制 v142。已选父工具集、平台、instance 和 CRT 必须完整传给子项目；确需 CUDA/TensorRT 的 v142 组合属于专项配置，不自动探测后降级或切换。当前 v142 实机验收缺口保持明确。

模块声明自身需要的系统包，不能依赖同批兄弟模块碰巧安装它们；兄弟模块可能命中缓存而不执行准备。bash、sed、pkg-config 等只在实际模块需要时成为前置条件，不要求本地基础构建安装整套 CI/发布工具。涉及 Python 的准备、脚本和测试继续统一使用 uv。

## 备选方案

**自行实现多地址下载与重试。** 可以单独控制重试策略，但当前需求已由 ExternalProject 的有序 URL 列表、摘要校验和解压处理覆盖；保留原生能力，不维护第二套下载流水线。

**原样复制参考新增模块技能及模板。** 参考技能直接依赖旧模块接口和本仓库尚未接入的缓存、依赖及发布能力，会使操作说明不可执行；仅作现有 TOML 架构必需的适配，保留可用规则与核对流程。

**依赖只控制顺序。** 可以先构建依赖，却不保证使用方查找到本次安装；保留标准位置传递及必要的模块专用参数。

**把资源消费规则统一放入公共 helper。** HWLOC 的 DLL/库路径与 contrib 源码入口不是同一种接口；公共层只处理共同获取和依赖，私有消费归模块。

## 验收标准

- 首批 FreeType 2.13.2 在 Windows x64、Linux aarch64 从真实根入口完成构建安装，安装头文件、静态库、CMake 包配置与 pkg-config 文件可核对；参考来源和构建逻辑仅作必要适配。
- 首批公共回归证明多个 URL 按声明顺序尝试，首地址 404 或内容摘要不匹配后，备用地址提供正确归档时可完成获取；全部地址失败或所有候选内容均不匹配摘要时不产生安装结果。单顶层目录归档直接提供源码根，主来源与附属资源的归档参数均正确生成，既有 Git 流程保持通过，HTTP fixture 在失败路径也完成清理。
- 新增模块技能使用当前 TOML 接口，元数据与采用的本地引用有效，保留模块接入和核对规则，不包含旧 helper 模板或未实施能力的操作指令。
- 首批不新增自定义下载器、跨 build 缓存、安装 sentinel、模块依赖、发布接口或每库强制专用测试工程；完成后有公共回归、双平台真实构建及文档和技能检查证据。
- 引擎独立提供 policy、PIC 与 C++ 标准兜底值，普通根入口不向父级 cache 写这些默认项；旧 policy 子项目可构建安装，较高 policy 声明不降低。模块值优先于父级值，父级值优先于引擎兜底值；重新配置更新父级值或移除覆盖后，子项目收到对应有效值，不要求 preset、IDE 设置或逐库重复声明。

以下标准属于后续批次，不是首批额外验收要求：

- 首个归档或预编译模块能通过根入口获取并安装，错误摘要失败；模块自己的平台输入和产物可核对。
- 首个依赖链只选择叶子即可构建所需模块，使用本仓库依赖产物，缺失时不靠系统安装补齐。
- 多资源模块在使用前完成资源准备；所需资源修改、运行文件和安装布局按该模块实际配置验证。
- 参考专用逻辑只作必要适配，不顺手重构根入口；新增引擎行为补公共回归，沿用能力的模块不强制建专用测试工程。

## 风险

平台/架构来源和目录复制的实现与验收状态见[本地复用与平台输入设计](../../implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)。模块依赖及复杂资源消费仍由后续真实模块确定。库的系统包、编译要求和预编译 ABI 各异，参考模块存在不等于新环境构建成功。首次迁移先保持参考配置并形成基线，必要优化另行判断。

上游地址可能超时、重定向或失效，依次尝试备用地址不保证公网下载一定成功；本批不承诺额外重试次数、恢复服务或所有地址的持续健康。FreeType 的可选依赖和生成 metadata 可能受机器环境影响，双平台验收核对实际结果，但不由本批提供隔离依赖或可搬迁包保证。技能只对照已实现接口维护，不能把参考范例的构建成功当成本仓库的验证证据。
