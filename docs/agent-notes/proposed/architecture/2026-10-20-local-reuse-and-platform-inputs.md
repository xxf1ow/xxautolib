# Agent Note: 本地复用与平台输入的参考迁入设计

Status: proposed

## 问题

现有流水线已验证 zlib、queue 和 FreeType 的本地构建安装，但 Git 输入和下载归档仍随 build 保存，安装结果没有 sentinel 短路，模块也不能声明源码子目录或按平台、架构选择来源。接入参考仓库的 lz4 和 ONNX Runtime 需要这些能力；应沿用已存在的参考行为，不同时引入新的缓存或恢复系统。

## 方案

本记录独立拥有本批的接口、模块范围、验证要求与风险；总体迁入约束和其他模块方向见[模块扩展与依赖接线的沿用决定](2026-10-20-module-expansion.md)。以下均为待实施设计，不表示当前仓库已经支持。

### 目标与参考对应

本批一次实现并验收本地输入复用、安装 sentinel、源码子目录和平台/架构来源选择，同时接入 lz4 与 onnxruntime；不拆成多个实施阶段。模块依赖、复杂资源、下游导入、二进制交付和 CI 仍是后续范围。

基线为本地参考仓库 `D:/Work/AutoLibBuilder` 的实际源码，不修改参考仓库或上游库。先保留参考行为，仅作现有 TOML、Python 生成器和原生 ExternalProject 的必要接口适配；不以迁入为由重新设计缓存、安装有效性或失败恢复。

| 本批内容 | 参考位置 | 必要适配 |
| --- | --- | --- |
| 持久 Git 来源与归档复用 | `src/build-system/tp_source.cmake` 的 `tp_source`、`_tp_url_download` | 公共生成器输出对应原生参数，不引入旧 helper API。 |
| 安装 sentinel | `src/build-system/tp_setup.cmake` 的存在性守卫 | TOML 显式登记安装相对路径，生成 CMake 存在性分支与空目标。 |
| lz4 子目录和静态配置 | `src/dependencies/lz4.cmake` | 用 `SOURCE_SUBDIR` 表达参考配置命令中的 `build/cmake`。 |
| ONNX Runtime 平台归档 | `src/dependencies/onnxruntime.cmake` | 将三份归档及 SHA256 写成纯数据，使用既有 copy 类型安装。 |
| queue 的两个 Git 输入 | `src/dependencies/queue.cmake` 的 `_queue_src` | 由公共资源处理保留本地优先行为，持久路径保留模块局部资源身份。 |

### 本地输入复用

持久输入位于根 `git/`，沿用根 CMake 已有的 `CMAKE_GIT_REPOSITORY` 位置。主 Git 来源使用 `git/<模块名>/`；Git 资源使用 `git/_resources/<模块名>/<资源名>/`，避免不同模块的局部资源互相占用源码目录。后一位置是现有模块局部资源模型的路径适配，不把资源拆成独立模块。URL 归档的 `DOWNLOAD_DIR` 统一指向根 `git/`，`DOWNLOAD_NAME` 及缺省文件名沿用原生规则，不另加内容寻址目录或缓存索引。

Git 来源沿用参考的本地优先判断：已有 `.git` 文件或目录时，直接使用该源码并关闭下载；不自动 fetch、checkout、reset 或 clean，也不将 `.git` 存在解释为声明提交已经匹配。目录不存在时，由 ExternalProject 在 build 阶段首次克隆到持久位置，继续使用固定提交和空 `UPDATE_COMMAND`。现有模块的完整提交不变，不为本批增加浅克隆策略。

已有目录没有 `.git` 时，真正为空的目录可沿用参考处理后重新克隆；非空目录明确失败，保留全部内容交人工判断，包括仅含隐藏文件的目录。来源检查与必要的空目录处理由生成的 CMake 执行，Python 不下载、克隆或删除来源。主来源与 Git 资源采用相同规则。

归档仍由 ExternalProject 校验、下载和解压，镜像顺序与摘要规则不变；本地归档摘要匹配时原生复用，不匹配时仍沿用原生下载行为。归档解压目录保持 `build/ep/<模块名>/src/`，资源归档保持 `build/ep/<模块名>/res/<资源名>/src/`；编译、stamp 和临时目录继续留在各自 build 内。新 build 因而可以复用持久输入，不复用另一 build 的编译状态。

不搬迁或删除已有 `build/ep/` 中的源码，不把参考仓库的源码或安装产物复制为本仓库验收结果。源码位置切换、来源或版本变更仍由维护者先保护本地修改，再人工准备受影响模块的持久来源、build 状态和安装树；不自动升级或修复它们。实际验证不得覆盖未检查的已有目录。

### 安装 sentinel

模块顶层增加可选 `sentinel` 字符串，含义为 `3rdparty/<模块名>/` 下的相对文件路径；绝对路径、盘符和越出安装前缀的路径在声明校验时失败。未声明的模块沿用普通 ExternalProject 流程。本批给五个模块登记参考中的实际路径：

| 模块 | `sentinel` |
| --- | --- |
| zlib | `include/zlib.h` |
| queue | `include/queue/readerwriterqueue.h` |
| freetype | `include/freetype2/ft2build.h` |
| lz4 | `include/lz4.h` |
| onnxruntime | `include/onnxruntime_c_api.h` |

生成 CMake 在 configure 时使用参考的 `if(EXISTS ...)` 语义。命中时只定义同名空目标并显示 cached 摘要，不定义该模块的资源目标、ExternalProject、补丁或自定义步骤；未命中时生成正常执行流程。判断留在生成的 CMake 中，不把当次文件存在性烘焙为 Python 生成结果。普通 TOML 声明校验仍执行，sentinel 不允许使用无效声明。

安装后需重新 configure 才切换为空目标；同一 build 的普通重复构建仍使用原生 stamp。单文件存在性不校验版本、工具链、ABI 或其余安装文件，不增加指纹、文件全集校验或状态数据库。文件缺失只意味着不采用 sentinel 短路，不承诺自动修复已有 stamp 对应的残缺安装。换版本、改构建选项或重建安装时仍由维护者人工准备受影响状态。

### 源码子目录与平台来源声明

以下字段是本批拟新增接口，不是当前已支持的操作示例。`[cmake]` 增加可选 `SOURCE_SUBDIR` 相对路径，默认使用源码根；声明必须留在来源目录内，copy 类型不能使用。该字段直接生成原生 `SOURCE_SUBDIR`，不修改 `SOURCE_DIR`、补丁根或安装位置，也不在 configure 时检查尚未获取的子目录。

平台数据沿用现有 `platforms.windows`、`platforms.linux`。平台表增加 `source`、`resources` 和 `architectures`；架构表键使用 `x86_64`、`aarch64`，其内容仅增加本批需要的 `source` 与 `resources`，不扩展架构专用 cache、steps、copy 或任意条件表达式。例如 ONNX Runtime 的三个来源分别登记在 `platforms.windows.architectures.x86_64.source`、`platforms.linux.architectures.x86_64.source` 和 `platforms.linux.architectures.aarch64.source`。

来源选择顺序为公共、匹配平台、匹配平台下的架构。某层提供主来源时整组替换上一层来源；资源按名称选择，每个被覆盖资源的来源同样整组替换，不逐字段混合 Git、URL、摘要或文件名。各来源分支继续共用既有 Git/URL 类型校验。选择后再检查 cmake 主来源和 copy 引用是否具备有效输入；没有当前组合可用来源时明确失败，不选择其他架构的归档。既有平台 cache 覆盖和 steps 追加语义不变。

公共入口向生成器增加实际架构输入，来源为 CMake 的目标系统处理器；Visual Studio 显式选择平台时以该平台为准，不能用宿主处理器掩盖所选目标。仅将本批所需的 `x64`、`AMD64`、`x86_64` 对齐为 `x86_64`，`ARM64`、`arm64`、`aarch64` 对齐为 `aarch64`，不探测 GPU、自动切换后端或增加交叉编译支持。没有按架构选源的既有模块不因此新增支持平台限制。

### 复制安装与模块定义

`copy` 映射保留原有 `files`，增加与其二选一的非空 `directories` 列表。两类路径均相对被选来源，`destination` 仍相对安装前缀；目录复制保留目录内的层次，不允许路径越界。使用 `directories=["."]`、`destination="."` 表达复制整个已解压来源，由生成的原生 CMake 安装命令执行，不在 configure 时枚举尚不存在的内容。queue 继续使用原有明确文件列表。

lz4 新增 `src/dependencies/lz4/module.toml`，类型为 `cmake`，版本为 `1.10.0`，Git 仓库为 `https://github.com/lz4/lz4.git`。参考本地源码的 `v1.10.0` 标签解析为完整提交 `ebb370ca83af193212df4dcbadcc5d87bc0de2f0`，本仓库固定该提交。`SOURCE_SUBDIR` 为 `build/cmake`，库特定选项保留 `BUILD_SHARED_LIBS=OFF`、`BUILD_STATIC_LIBS=ON`、`LZ4_BUILD_CLI=OFF`、`LZ4_BUILD_LEGACY_LZ4C=OFF`；policy、PIC、C++ 标准、Release、生成器和工具链沿用公共传递，不逐库重复配置。实际核对公开头文件、静态库、CMake 包文件及 pkg-config 文件，不改变上游安装布局。

ONNX Runtime 新增 `src/dependencies/onnxruntime/module.toml`，类型为 `copy`，版本为 `1.24.4`。只迁入参考默认版本，不增加 `1.15.1` 或命令行版本覆盖。下载基址为 `https://github.com/microsoft/onnxruntime/releases/download/v1.24.4/`，各平台声明对应完整 URL、相同的下载文件名及以下参考 SHA256：

| 平台与架构 | 归档名 | SHA256 |
| --- | --- | --- |
| Windows x86_64 | `onnxruntime-win-x64-1.24.4.zip` | `d2319fddfb6ea4db99ccc4b60c85c517bcd855721f5daa6a06d40d7cb2ee2357` |
| Linux x86_64 | `onnxruntime-linux-x64-1.24.4.tgz` | `3a211fbea252c1e66290658f1b735b772056149f28321e71c308942cdb54b747` |
| Linux aarch64 | `onnxruntime-linux-aarch64-1.24.4.tgz` | `866109a9248d057671a039b9d725be4bd86888e3754140e6701ec621be9d4d7e` |

参考将归档直接解压到安装目录；本仓库按既有两层职责先由 ExternalProject 解压到 build，再通过目录复制将完整内容安装到 `3rdparty/onnxruntime/`。保留头文件、库、运行文件、版本和许可证等归档内容，不执行 configure/build，不增加 GPU 输入、发布 adapter 或下游导入接口。核对平台实际库名和 Linux 共享库链接的可用性，不要求原生归档自带某种 CMake 包配置，也不复制参考安装树中另行生成的 `autolib-import.cmake`。

### 验证与交接

新增行为在现有 `tests/build_system/` 补公共回归，不另建缓存测试框架或为两个库强制新增专用测试工程。定向开发验证受影响的声明解析、参数生成与原生执行；本批全部完成后运行公共测试全集。沿用既有本地 Git、归档、动态端口 HTTP fixture，不以公网故障注入测试分支。

公共验证从可观察结果检查：新 build 在不依赖旧 build stamp 的情况下复用 Git 输入且保留本地修改；归档复用不请求 HTTP，错误摘要沿用原生重新获取或失败；主来源和资源使用各自正确目录；非空无 `.git` 的目录失败且不丢数据。安装 sentinel 命中时获取、构建、资源和自定义步骤均不执行，未命中且状态干净时确实安装产物，重新 configure 后转为空目标。小型子目录项目完成真实配置安装；三种平台归档选择、无匹配来源失败、目录复制的安装层次均可核对。测试覆盖正常路径和会破坏上述结果的反例，不穷举输入组合或网络错误。

两个真实模块均从普通根 CMake 入口选择构建安装。Windows x64 与既有授权的 Linux aarch64 设备 `lxx@192.168.0.127`、目录 `/home/lxx/xxautolib` 核对 lz4 编译安装和 ONNX Runtime 下载复制安装，再核对输入复用及 sentinel 命中；不以 fixture 或 configure 成功代替真实模块结果。为区分输入复用与安装短路，公共测试使用隔离的新 build 和安装状态，不删除真实工作副本产物。Linux x86_64 本批只验证声明选择，实机获取安装仍是明确缺口；不以另两端成功替代。v142 实机一致性继续保留人工验收。

传输和已有数据保护遵守[基础设施与验证授权](../../implemented/process/2026-10-19-repository-foundation.md)。该授权不扩展到其他机器、目录、推送、发布或远端 CI；本批不操作托管平台。实现完成时更新[架构](../../../architecture.md)、[开发指南](../../../development.md)、[测试指南](../../../testing.md)、[路线图](../../../roadmap.md)和[新增模块技能](../../../../.agents/skills/add-third-party-module/SKILL.md)中受影响的事实，不将本批未涉及的路线图项目标成完成。

## 备选方案

**将安装 sentinel 扩展为版本和完整性校验。** 本批沿用参考的单文件存在性判断，不把 sentinel 改成另一套安装资格机制；版本、工具链或构建选项变更由维护者人工准备。独立缓存数据库、自动重置源码、补丁回滚和通用恢复服务不属于参考的本地构建能力，本批不新增；参考的空 Git 目录处理保留在输入复用内。

**把 ONNX Runtime 直接解压到安装树。** 参考使用这种布局；本仓库已经将来源准备与 copy 安装分开，因此只作先解压、再完整目录复制的必要适配，避免为单库新增绕过安装阶段的专用类型。库内容与选中的原生归档保持一致，不重写其构建或发布逻辑。

## 验收标准

- lz4 `1.10.0` 和 onnxruntime `1.24.4` 在 Windows x64、Linux aarch64 从根入口完成真实构建或复制安装，来源、参考选项及实际产物可核对；Linux x86_64 仅有声明选择证据，不宣称实机通过。
- 干净的新 build 能复用持久 Git 输入和通过摘要校验的归档；本地源码修改保留，非空异常目录失败且不被清理；没有源码自动迁移、更新或回滚。
- 五个模块显式登记参考 sentinel；命中时只有同名空目标，不执行模块资源、获取、构建、安装或自定义步骤，重新 configure 后的状态可核对；不承诺版本和安装完整性校验。
- 源码子目录、完整来源替换、三种平台组合、缺失来源失败及完整目录复制有公共回归；旧 queue 文件复制和既有公共参数规则保持有效。
- 本批只有必要的 TOML、通用生成与原生执行适配，不重构根 CMake，不引入独立缓存数据库、通用恢复、模块依赖、下游导入、打包或 CI；公共测试与文档、技能检查通过。

## 风险

当前平台覆盖表仅支持 cache 和 steps；平台/架构来源和目录复制是本批拟新增接口，不能提前当成已实现能力。模块依赖及复杂资源消费仍由后续真实模块确定。库的系统包、编译要求和预编译 ABI 各异，参考模块存在不等于新环境构建成功。首次迁移先保持参考配置并形成基线，必要优化另行判断。

sentinel 可能让旧版本或残缺安装被当作可复用，这是参考存在性判断的已知限制，不用新增数据库消除。持久 Git 来源会保留补丁和人工修改，不能因新 build 而假定源码干净；来源与选项变化需要人工准备。ONNX Runtime 是预编译运行库，复制成功不等于本机所有运行时依赖均满足；本批核对安装内容和实际库依赖，不承诺重新编译、自包含或模型推理功能。

上游地址可能超时、重定向或失效，原生镜像回退不保证公网下载一定成功；本批不承诺额外重试次数或通用恢复服务。Linux x86_64 实机安装与 v142 实机工具集一致性保留为明确验证缺口，不以已验证平台代替。
