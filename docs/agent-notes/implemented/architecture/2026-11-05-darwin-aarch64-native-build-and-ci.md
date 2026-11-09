# Agent Note: Darwin ARM64 共用构建基础与 CI 接入

Status: implemented

## 问题

仅覆盖 Windows x86_64、Linux x86_64 与 Linux aarch64 的流水线无法交付 Apple Silicon 产物。增加 Darwin 平台需要贯通 Go SDK、配方资格、构建生成、公共消费入口、工具与包身份及发布准备；仅增加 workflow 作业不能形成可执行的交付流程。

配方的平台覆盖表只表达不同平台的来源和参数，不能说明该配方是否支持某个平台。引擎增加 Darwin 后，不能把所有现有配方自动视为支持 macOS；例如 librealsense 不纳入本次 macOS 支持范围。依赖不支持目标平台时，使用它的根包也不能进入该平台的构建集合。

本地与 CI 必须执行同一套编译流程。先建立共用的原生执行基础，是为了让 CI 有可调用、可验证的构建入口；大量本地编译第三方包不是这一阶段的目标，也不能用另一套本地专用流程证明 CI 可执行。

## 决定

规范身份为 `darwin-aarch64`。CMake 系统名使用 `Darwin`，配方及产物 OS 使用 `darwin`，架构身份使用 `aarch64`；CMake 的 Apple 目标架构参数使用 `arm64`，Go 宿主身份使用 `darwin/arm64`。Darwin 本次只接受 Apple Silicon 原生 ARM64，不增加 Intel 包、Universal 包、Rosetta 执行或交叉编译路径。

### 设计归属

本记录拥有 Darwin 原生支持、配方平台资格及 macOS 部署目标，扩展 [CI 系统决定](../../implemented/architecture/2026-10-20-ci-system.md)的原生平台集合、环境与验证分支，并扩展[本地复用与平台输入决定](../../implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)的平台识别；原有输入复用、sentinel、依赖接线、Windows/Linux 环境和数据保护决定继续有效，两份记录保留原生命周期。

[模块扩展决定](../../proposed/architecture/2026-10-20-module-expansion.md)继续约束配方与引擎的职责；[自定位入口与工具专用 CI](../../implemented/architecture/2026-11-03-entry-delivery-and-tool-only-ci.md)继续拥有模块输入、入口定位及空库交付语义；[成品交接决定](../../implemented/architecture/2026-11-04-ci-packaged-artifact-handoff.md)继续拥有消费验证后打包、成品运输和发布端不重打包的要求。本设计不建立第二种包格式、缓存格式、发布协议或编译引擎。

### 共用的编译安装流程

普通根 CMake 入口继续调用 builder 解析配方、解析精确依赖并生成 ExternalProject，由 CMake 执行取源、补丁、配置、编译及安装。CI 的每个依赖层仍调用这一入口，使用同样的生成器、编译器选择规则、目标参数和配方；不在 workflow 中复制某个包的构建命令，不让 builder 根据 GitHub 环境改变编译或打包行为。

本地与 CI 的宿主系统和 SDK 版本可以不同；相同流程指入口、参数含义、依赖规则和执行实现一致，不要求机器镜像或产物字节一致。CI 可以提供隔离工作根、模块选择和并行预算，并驱动验证及显式打包，这些编排不形成另一套包编译流程。

Darwin 使用 Apple Clang。执行环境通过 `xcrun` 取得当前开发工具目录中的 `clang`、`clang++` 及 macOS SDK，显式选择编译器并核对实际 C/C++ 编译器身份为 `AppleClang`；不通过 PATH 偶然选择 Homebrew LLVM 或 GCC，不为逐包失败切换编译器。记录实际工具版本、SDK 和相关配置，不承诺不同机器的 Apple Clang 补丁版本一致。

普通配置通过 cache 参数在首次 `project()` 前设置 `CMAKE_OSX_ARCHITECTURES=arm64`、`CMAKE_OSX_DEPLOYMENT_TARGET=15.0`。公共生成入口将这些已定义的值传入实际 CMake 子项目；显式选择的 SDK 和编译器也必须在子配置中保持一致。消费验证使用相同目标参数，不只固定生产根的参数。Darwin 本次拒绝非 ARM64 或多架构配置，不静默选择其中一个架构；配方也不能把本轮目标改成 Intel 或更高部署目标后仍算通过。

这些目标参数沿用普通 CMake 的显式输入，不增加环境专用的配置解析器。配方与本轮固定目标冲突时明确失败，公共默认值的其他合并优先级保持不变；不把根传入的 arm64 当成可被模块改写的普通兜底值。

CI 使用 `Unix Makefiles` 和仓库已固定的 GNU Make 4.4.1，同一平台父子构建共享顶层并行预算与 FIFO jobserver。macOS 预算通过原生 CPU 查询取得，最少为 1；不调用 Linux 的 `nproc`。已有 jobserver 探针复用执行及判定逻辑，只适配实际不兼容的宿主命令，例如 macOS `date` 不提供 GNU 纳秒输出。首次真实原生验证检查父子 Make 接线，不能把顶层出现 `-j` 视为预算生效的充分证据。

### 每个配方版本显式声明平台资格

当前单一 `package_platforms`、通用资格及平铺覆盖规则由[字段参考](../../../subsystems/module-recipes.md)拥有，职责与全包验收由[统一模块配方设计](../../implemented/architecture/2026-11-06-module-recipe-contract-and-macos-validation.md)拥有。具体包列出支持的完整平台 ID；通用包单独声明 any-any，各项依赖仍按目标判断资格。builder 与 CI 共用版本配方，不维护包名排除表，也不从来源或头文件属性推断具体平台支持。

本记录的 queue/lz4 首轮执行基础及以下验证证据保持原范围。统一字段后的两个 Eigen 版本使用上游 CMake 安装并交付通用头文件与元数据；其他通用配方按依赖资格判断。未适配的机器码包不自动增加 Darwin，包括 librealsense@2.58.1 和 onnxruntime@1.24.4。macos-15 全包构建调查和 PCL/example 实机消费留待统一设计第二阶段。

平台资格在要求目标专用来源、生成原生目标及取源之前判断。例如 ONNX Runtime 没有 Darwin 来源时按不支持平台处理，不因缺来源使整份 Darwin 计划失败。只有明确的平台不支持可以走资格过滤；未知包、坏 TOML、非法身份、缺依赖、依赖环及其他实际错误继续失败，不用捕获任意错误的方式把它们降级为跳过。

### 选择、依赖与过滤结果

| 入口与选择 | 不支持目标平台时的行为 |
| --- | --- |
| 普通根的显式 `BUILD_LIST` 或 builder `generate` 显式模块请求 | 在配置或规划阶段失败，说明根包、平台及阻断它的包或依赖链，不产生该请求的构建目标。 |
| 普通根未提供模块列表的自动发现 | 排除不支持的根及依赖闭包不合格的根，只生成其余根需要的闭包。 |
| CI 的 `modules=all` 或显式包列表 | 与已有 adapter 资格过滤共同决定有效根；不支持的根及依赖闭包不合格的根列入过滤结果，其余工作继续。 |
| CI 模块输入为空，或资格过滤后零有效库 | 保留所选平台，准备并验证宿主 importer 与入口，运输工具与入口，不执行库构建或库消费。 |

过滤以根包为单位。根包直接或传递依赖不支持目标平台时，过滤该根，重算有效根的完整依赖闭包；共享依赖只构建一次，仍被其他有效根使用的依赖不能删除。计划及最终汇总区分请求根、有效根、过滤根，并说明平台不支持、依赖平台不支持或缺 adapter 的实际原因。过滤不当作构建失败，但不能把被过滤库报告为已构建或已交付。

普通入口和 CI 允许不同的显式选择策略：普通构建对显式不支持请求报错，CI 延续对不合资格根的过滤规则。两者共享平台事实、依赖规则和真正的编译实现，不分别维护配方解释器。

builder 的只读 `describe` 提供平台资格判断、有效闭包及被过滤根的原因，供 CI 复用；它不是显式生成请求，不因已识别的平台不支持而中止整个描述。adapter 资格仍由 CI 按已有职责判断。构建生成与只读描述共享解析和依赖判断，只在对不支持根采用报错还是返回资格结果上有明确区别。

### macOS 运行基线

CI 固定 `runs-on: macos-15`。GitHub 当前将该标准标签分配给 Apple Silicon arm64；平台架构不匹配或环境不满足要求时失败，不自动改用其他系统或 Intel runner。标签信息以 [GitHub runner 文档](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)及[官方镜像列表](https://github.com/actions/runner-images)为外部依据，执行时记录实际镜像及系统版本。

最低运行目标固定为 macOS `15.0`，与 `CMAKE_OSX_ARCHITECTURES=arm64` 分别管理。macOS 的系统 API 和部分 `libc++` 功能存在系统版本要求，不使用 Linux 的 glibc、GLIBCXX 或 ELF 检查。较新的 SDK 可以面向较低 deployment target 编译，但目标参数不会降低已经生成的依赖二进制的最低系统要求，相关依据见 [Apple C++ 支持说明](https://developer.apple.com/xcode/cpp/)及 [CMake deployment target 说明](https://cmake.org/cmake/help/latest/variable/CMAKE_OSX_DEPLOYMENT_TARGET.html)。

验证检查本轮实际生产和消费的架构、部署目标及非系统链接输入。对机器码采用 Mach-O 识别，静态库由实际下游链接及其机器码证据验证；仅安装成功或文件扩展名正确不算通过。对共享库检查实际使用的加载路径及动态依赖，不为首轮静态库额外增加共享库配方。包不得依赖本机 Homebrew 或本轮生产目录中的偶然绝对路径；Apple 系统库和框架按平台系统依赖处理。

Go SDK 使用官方 `darwin-arm64` 归档及摘要，沿用现有固定版本和项目私有 SDK 环境，不使用系统 Go 或交叉构建。宿主 importer 保持 `CGO_ENABLED=0`，但 macOS 上仍允许 Go 必需的系统动态依赖；不能照搬 Linux 纯 Go ELF 无动态库规则，也不能要求其完全不依赖 libSystem。验证其 ARM64 Mach-O 身份和实际可执行性，不允许意外引入第三方 C/C++ 运行库。Go 工具的运行下限必须单独核对固定 SDK 的要求及实际二进制，CMake 参数不控制 Go 编译器的部署目标。

本机当前实测为 macOS 27.2、arm64、macOS SDK 27.2、Apple Clang 21.0.0。本机成功仅证明该环境上的执行基础；`macos-15` 的实际 15.x 运行结果提供 15 系列验证证据。部署目标 15.0 是明确的产物要求，二者都不能被描述为在 15.0 原版系统上实测通过，也不因此增加历史系统环境或兼容矩阵。

### 工具、包与发布身份

builder、importer、公共 `autolib.cmake`、CI 与发布端同步接受 `darwin-aarch64`，保留宿主工具与 C++ 目标身份的原有分工。Darwin importer 裸程序名为 `autolib-importer-darwin-aarch64`，工具归档为 `autolib-importer-darwin-aarch64.tar.xz`；机器码库沿用既有版本化库命名规则，仅新增平台值。入口引导、Git 元数据键、摘要、安全解包和可执行权限检查均走已有流程。

声明为平台无关的包仍交付 `any-any`，不为 Darwin 再复制一个平台槽。publisher 的原有优先顺序保持为 Linux x86_64、Linux aarch64、Windows x86_64，最后追加 Darwin aarch64；仅选 Darwin 时由它交付本轮通用包，`all` 不产生重复通用资产。各平台有效根可以因白名单不同而不同，汇总和发布完整性以原计划的每个平台实际集合为准。

独立 Gitee 发布流程只增加平台身份与完整集合校验，不重新筛选包、不执行构建或重打包，也不改变附件重试、latest 或原提交入口注入。源码入口与安装入口继续不带远端地址，发布时只修改上传副本。

### 工作目录、验证环境与授权

本任务使用当前工作目录中的普通任务分支，不创建 worktree 或改写历史；计划和过程材料保留在忽略的 `.superpowers/`。分阶段先形成共用执行基础、再接入 CI，避免 workflow 与本地维护不同的包编译流程。

[开发指南](../../../development.md#远端构建与源码传输授权)的本机默认禁编译规则在本任务有明确例外：允许在新的任务自有目录准备固定 Darwin 工具、编译必要的 builder/importer 与轻量原生输入，并执行 queue/lz4 构建、消费和打包。不授权本机批量编译其他真实库，不改变其他任务的验证环境。通用定向验证优先使用 109 的现有 SDK、Make 和已授权目录；109 不可用时，本任务必要的共享逻辑增量检查在本机执行，不要求补齐 Linux 实机证据。

所有验证使用新的任务根，不覆盖用户既有安装、来源或缓存，不清空已有目录；源码传输沿用逐文件摘要核对及直接受影响数据保护。设计确认包含相应本地提交及上述验证；push、PR、CI 触发、Gitee 写入和附件删除替换分别需要明确授权。本任务另获任务分支 push 和 Darwin CI 验收授权，没有 Gitee 写入、合并或创建 PR 授权。

## 备选方案

**只在 workflow 增加 macOS 作业。** 改动集中，但 Go SDK、配方、构建及消费入口尚不接受 Darwin，不能形成真实产物；在 workflow 内补写包专用命令又会产生第二套编译流程。选择先补共用执行基础，再接线。

**在 CI 维护排除包名的列表。** 可以先排除 RealSense，但本地构建、只读描述和 CI 会采用不同平台事实，版本间来源差异也难以表达。选择由版本配方统一拥有平台声明。

**默认支持所有平台，只声明不支持的例外。** 可以少改现有配方，但新增平台会使未适配包自动进入候选集合。选择必填白名单，平台增加必须在配方中可见；代价是每个已有版本需要一次明确声明。

**先在本地编译全部包，再接 CI。** 能增加逐包证据，但把共用执行基础变成耗时的上游适配任务，偏离本次目标。选择少量代表输入证明真实入口可执行，其他未适配包先排除，不以全库本地编译为前置条件。

**使用浮动 runner 或更早的部署目标。** `macos-latest` 会迁移系统；更早的部署目标则缺少当前可用 runner 的对应运行证据，并引入尚无要求的旧系统兼容责任。选择 `macos-15` 与 deployment target 15.0，保留实际 15.x 验证范围。

## 第一阶段验证证据

Darwin 宿主 SDK 使用 [Go 官方下载元数据](https://go.dev/dl/?mode=json&include=all)中的 `go1.27.3.darwin-arm64.tar.gz`，SHA256 为 `dd3908cf20959d798c1e06ebe5b72213f0505ce01373b40ce3110975a6cd99c2`。[Go 1.27 要求 macOS 13 或更新系统](https://go.dev/doc/go1.27#darwin)，满足本任务 15.0 基线；该下限独立于 CMake deployment target。

本机 SDK 下载、私有环境、离线复用及下载/摘要/解包失败检查通过；工具准备生成的 importer 为 ARM64 Mach-O，实际执行返回预期 CLI 用法，安装入口与源码逐字节一致。此次验证未提供 Linux 回归证据：109 的 SSH 在返回服务端版本前关闭连接，未传输源码或更改远端数据；也不构成 macos-15 或真实 GitHub Actions 验收。

### 原生代表库闭环

验证源码为 `b757ab4f4ff40b560a2af987316e5d0520498028` 的干净 Git archive，任务根为 `build/darwin-stage1-r16m8kmr/`。实际环境为 macOS 27.2（26B123）、arm64、SDK 27.2、Apple Clang 21.0.0（clang-2100.4.1.1）、Go 1.27.3、CMake 3.31.12、GNU Make 4.4.1。CMake 与 Make 仅准备在任务自有目录，官方归档 SHA256 分别为 `799af7fd545db9bf1b9cfe72f8095880e727a2d4e0df0e3dffc3bc7b95c2d3b0`、`dd16fb1d67bfab79a72f5e8390735c49e3e8e70b4945a15ab1f81ddb78658fb3`。Make 从该源码归档安装，未写系统目录。

普通根使用[开发指南的 Darwin 参数](../../../development.md#准备与配置)，选择 `queue@45db9a8b4431,lz4@1.10.0`；安装根通过 `AUTOLIB_INSTALL_ROOT` 指向隔离源码的 `build/install`。configure/build 成功，实际 lz4 子 cache 与根保持五项 Apple 参数及同一 Make；没有包专用构建入口。以 `-j2 --jobserver-style=fifo --debug=j` 在另一隔离 build 根重新构建，父子 Make 均报告相同的 `Using jobserver controller fifo:…/GMfifo81415`，结束归还全部 token，无 unavailable/reset 警告。轻量并发探针的预算 1 峰值为 1、预算 2 峰值为 2 且出现重叠；CPU 自动预算查询返回 12。

现有 `run-installed.cmake` 分别消费原安装和新根解包结果，两次 configure/build/CTest 均通过。消费者使用相同 Apple/Make 参数，原消费链接输入位于原安装，新消费全部位于搬迁根且不含原安装路径。打包前后递归 bytes、mode、mtime 快照一致；canonical 入口字节和 importer 可执行权限保持。显式请求 RealSense 在新 build 根的配置阶段因不支持 `darwin-aarch64` 失败，没有获取其源码或覆盖成功安装。

lz4 静态库为 arm64，五个机器码成员的 minos 均为 15.0。两个消费者均为 arm64、minos 15.0，动态依赖仅系统 `libc++.1.dylib` 和 `libSystem.B.dylib`。实际 importer 为 arm64、minos 13.0，依赖仅 libSystem、libresolv、CoreFoundation 和 Security，无第三方或 C++ 运行库。轻量共享库 fixture 另覆盖运行加载位置；首轮真实代表库没有共享库。

同提交 builder 的 `package` 生成以下精确资产，并逐一解包搬迁消费成功；摘要来自实际输出与文件复核：

| 资产 | SHA256 |
| --- | --- |
| `autolib-importer-darwin-aarch64.tar.xz` | `f7d30e772e21213eccf01cd47746c947850907ccda55d0978f9af7152383fcfb` |
| `lz4-1.10.0-darwin-aarch64.tar.zst` | `77394abf4fc69369385607fb2d7fa55d62b0b9f8f4e3485f0d8454e4cde504d4` |
| `queue-45db9a8b4431-any-any.tar.zst` | `e5f9725b602afcd933a12d4bc5cb1593b5c2c993483c83c5620ba1f5193dbb2a` |

### 定向回归与覆盖限制

平台声明及依赖过滤、Apple 参数与冲突、公共入口身份、版本化安装与搬迁、CI 原生 prepare/layers/verify、工具专用与过滤后零库、Mach-O 架构/部署目标/动态依赖/路径负例、两个消费 driver 及 FIFO 定向回归均在本机通过。该阶段尚未接入 workflow；四平台接线与实际运行见下文第二阶段证据。规范的定向入口由[测试指南](../../../testing.md#ci-验证)拥有，过程日志与环境详情保留在上述忽略的任务根。

`TestVersionedMetadataAsset/version-case-collision` 因本机文件系统不能同时存放 `V1`/`v1` 而跳过；Git 树大小写冲突用显式 index 输入验证通过，核心 Darwin 工具、生产和消费用例没有跳过。109 不可用期间，必要的共享逻辑定向回归在本机执行，Linux 执行证据仍缺失。未执行其他真实库、全量 Go suite/vet、最低 CMake、macos-15 runner 或 15.0 原版系统验证。本机新 SDK 的 minos 证据只证明目标要求与本机执行结果，不能替代 15.x 运行验收。

## 第二阶段本地验证

四平台默认/all、单选 Darwin、异构有效集合、publisher 优先级及固定资产校验的 Go 定向集合共 14 项顶层测试通过。Darwin 工具准备脚本的 6 项测试执行真实 Bash 3.2、tar 和 SHA256 校验，覆盖空库、过滤后零库、含空格目录、宿主拒绝、坏摘要、错误版本、configure 失败日志和所选 SDK 传递；外部下载与工具编译使用受控替身，不计为 macos-15 环境准备通过。

成品及发布规则的 9 项 Python 定向测试通过，覆盖 Darwin 单选、四平台异构集合、any-any 唯一交付、空库工具与入口、缺失/额外资产、错 run/attempt/platform 和坏摘要。元数据检查只使用临时本地 Git 库，验证已有三平台摘要、latest 及无关文件保全，没有访问真实 Gitee。真实轻量原生打包运输及四平台 finalize 的 4 项 Go 顶层测试通过，无跳过。

workflow 的 5 项测试执行实际 jq 选择表达式和 job shell，验证仅 Darwin 及 all 的选择、工具 JSON 到 step 输出、prepare/layers/verify/transport 参数、空库无 layer、失败停止及 finalize 收集条件；工具专用默认输入和单一 Gitee 发布入口两项直接回归通过。生产 job 固定 macos-15，按同次 run/attempt 保存环境、构建、Mach-O 及 jobserver 证据。本地 Bash 进程替换测试需在允许 /dev/fd 的环境执行。

第一阶段已验收的生产、直接和搬迁消费证据继续有效，本阶段未重复真实库构建、Linux 实机、全量 Go suite/vet 或最低 CMake。原样 `example/` 依赖 queue/flann 并下载远端入口，FLANN 未声明 Darwin 支持，没有本地运行该项目；公共入口的 queue/lz4 消费由既有 driver 验证。真实 macos-15 验收见下一节；本地协议及接线测试不证明 Gitee 发布通过。

## macos-15 实机验收

工具专用[构建 #34](https://github.com/xxf1ow/xxautolib/actions/runs/42171787348)（run `42171787348`、attempt `1`、提交 `faf72c89994383501b6e3e3c279308aef69f2b77`）成功，平台为 `darwin-aarch64`、modules 为空。下载的 plan/result/transport 身份一致，结果没有库，工具归档与唯一 canonical 入口符合原计划，实际 SHA256 和入口源码字节核对通过；没有下载或编译 Make。

代表库[构建 #37](https://github.com/xxf1ow/xxautolib/actions/runs/42174342001)（run `42174342001`、attempt `1`、提交 `2de3418d273f15cd7284e60977b7ce7d234a7da3`）成功。输入为 `queue@45db9a8b4431,lz4@1.10.0,librealsense@2.58.1`，计划和 finalize 的有效根只有 queue/lz4，RealSense 以 `unsupported_platform` 过滤，没有获取其源码。Darwin 与 finalize 均成功，Linux/Windows skipped 不妨碍本次精确平台完整性校验。

两次实际环境均为 macOS 15.8.2（24H232）、arm64、镜像 `macos15`/`20261104.0514.1`、Xcode 16.4、SDK 15.5、Apple Clang 17.0.0（clang-1700.0.13.5）、Go 1.27.3、CMake 3.31.12。代表库使用校验后的 Make 4.4.1，预算为 3；FIFO 探针的峰值为 3、存在并发重叠、Warnings 为空。普通根、lz4 子配置及两个消费者保留五项 Apple 参数和同一私有 Make；生产和消费目标为 arm64/15.0。

queue 与 lz4 的独立消费者各自 configure/build/CTest 通过 1/1。Mach-O 校验通过；下载后的 lz4 静态归档复核为 arm64，五个机器码成员 minos 均为 15.0。importer 为 arm64、minos 13.0，实际依赖仅 libSystem、libresolv、CoreFoundation 和 Security。成品 plan/result/run 的提交、run/attempt 一致，恰有以下三个归档及一个原样 canonical 入口；实际字节摘要核对通过：

| 资产 | SHA256 |
| --- | --- |
| `queue-45db9a8b4431-any-any.tar.zst` | `1a6ecf6e0abfc3232a2f71c9a70987b1f64c513c7f28795fc5f1671c60deef91` |
| `lz4-1.10.0-darwin-aarch64.tar.zst` | `ace6d4a07a5baf781275ba90a64d89cf7168813a8536aa5322fbcf6ffe71a39f` |
| `autolib-importer-darwin-aarch64.tar.xz` | `08966cc16cded69ecca87f15f7c8422500a944c8b20ac73635b8be9dda9a36bb` |

原代表[构建 #35](https://github.com/xxf1ow/xxautolib/actions/runs/42172071768)在 Make configure 失败；[诊断运行](https://github.com/xxf1ow/xxautolib/actions/runs/42173915819)确认直接调用 Xcode clang 时没有 SDKROOT，链接器报 `library 'System' not found`。准备脚本为 Make configure/build 显式导出 `xcrun --sdk macosx --show-sdk-path`，失败时保留 config.log；固定版本、Apple Clang、目标架构及部署基线不变。受控反例验证错误继承的 SDK 被替换，6 项准备及 5 项 workflow 测试通过，再由 #37 验证实际修复。Make 上游源码存在非原型声明及字符串比较警告，未修改上游。

工具专用运行自动触发的 [Gitee 同步 #32](https://github.com/xxf1ow/xxautolib/actions/runs/42171961532)使用默认 master `569acb0a012acb95ca07284b75c7d70516234711`，在 `Select original build` 因三平台白名单拒绝 Darwin，报 `invalid or duplicate planned platforms`；没有进入 Gitee Action 或写入。#37 后的[自动同步](https://github.com/xxf1ow/xxautolib/actions/runs/42174563173)以相同控制提交在同一处失败。任务分支的四平台发布校验已经通过受控测试，但不等于默认分支已更新或真实 Gitee 发布通过。本次没有合并、手动发布或替换附件。

证据保留在 `build/darwin-stage2-lf58aydo/`。工具专用证据在未受影响的路径复用，只复跑失败的代表输入；没有扩大至 Linux 实机、其他真实库、原样 example、全量 Go suite/vet 或最低 CMake，也不声称 macOS 15.0 原版系统实测通过。

## 影响

必填白名单使平台资格在每个版本配方中可见，配方、测试输入和文档共同维护该事实。白名单表达维护者支持决定，不自动证明上游在对应平台可用；新增真实支持仍需足够的正常构建及消费证据。

本机 Apple Clang 与 macOS 15 runner 的编译器及 SDK 不同。较新本机成功不能替代 CI 原生验收；deployment target 也不能降低预编译依赖或固定 Go SDK 自身的运行下限。若具体工具或依赖无法满足 15.0，不提高基线、换编译器或吞掉验证失败，先把实际阻塞及最小修订交回设计确认。

过滤后可能只交付工具与入口。汇总必须说明被过滤的请求库，避免把成功的工具交付理解为请求的所有库已经得到支持。扩展过滤理由时保持原计划、运输和发布集合一致，不能由发布阶段再次决定平台资格。

首轮没有共享库代表输入，也不适配全部包、相机功能或更早 macOS。后续包需要新的系统依赖、上游补丁或专用动态加载处理时，按该包实际需要另行确认，不扩建通用兼容、恢复或逐库工具链选择机制。

### 开发与测试指南预算

Darwin 的首次配置要求五项显式 Apple 参数和 GNU Make 路径，完整可复制命令归开发指南所有。工具版本的理由和验证细节已留在本设计；该命令无法在保持输入完整的情况下进一步压缩，拆入另一页会割裂平台配置入口。开发指南还须明确支持过滤、消费参数及 workflow 的平台范围，测试指南拥有定向命令和证据入口。详细结果已集中在本设计，继续删减指南会丢失操作条件；`scripts/doc-budgets.manifest.json` 的开发指南预算为 530、测试指南为 500，容纳必要命令并保留至少 5% 余量。
