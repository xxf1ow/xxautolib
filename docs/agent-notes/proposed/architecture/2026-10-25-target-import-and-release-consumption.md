# Agent Note: 按目标导入与分阶段 Release 消费

Status: proposed

## 问题

消费者在请求包之后还需再次列出链接目标，容易遗漏传递依赖或额外的上游库。现有 FLANN 包装目标仅链接 C++ 库，使用 C API 时仍需消费者自行补链，不符合按包名消费的目标。来源、目标平台和工作目录混在每次导入调用中，也增加了调用方需要了解的准备细节。

本地包消费与 Release 生产已有基础，但消费端尚未启用联网获取。接口变更、GitHub 下载和 Gitee 镜像各有独立可观察结果，需要分阶段验收，不能把本地接口调整与全部远端接入合并实施。

## 方案

以 `autolib_import(TARGET app PRIVATE queue flann)` 一次表达消费 target、链接可见性和根包需求。来源在单文件 `autolib.cmake` 内设默认值并允许外部一次性覆盖；每包只提供一个 `autolib::` 公共目标，由 adapter 封装上游库和传递依赖。

本设计拥有 CMake 公共接口、来源默认值、公共目标封装及后续交付阶段，部分替代[无全局清单的双来源包消费](2026-10-24-manifestless-package-consumption.md)中的 CMake 调用形式、无远端默认值及后续阶段安排。包内四行头、固定资产名、本地布局、依赖遍历、来源优先级、失败保护、损坏检测及 GitHub 资产发现仍由该记录拥有，不复制另一套协议。[固定槽交付设计](2026-10-20-name-only-artifact-delivery.md)和[Go 工具层设计](2026-10-21-go-tooling-and-offline-import.md)的可搬迁打包、adapter 与纯 Go 运行约束继续有效，公共目标命名以本文为准。

CI 仍是独立系统，其构建编排、发布权限、并发和运行汇总由[CI 系统设计](../../implemented/architecture/2026-10-20-ci-system.md)拥有。Gitee 阶段实施前修订该独立设计，不把发布实现写入 builder 或 importer。本地接口与 GitHub 下载阶段已通过用户验收，Gitee 阶段尚未实施，因此本整体设计保留 proposed。架构及开发、测试指南记录当前行为与验收范围。

### 已验收范围

[三平台 CI 运行 40208173445](https://github.com/xxf1ow/xxautolib/actions/runs/40208173445)完成 queue、lz4、flann 和宿主 importer 的构建、消费验证及同名发布。十个实际 Release 资产的 SHA256 与 API 摘要逐一匹配；包使用小写公共目标，FLANN 聚合 C/C++ API 并传递 lz4。

Windows x86_64 的真实 GitHub 验证从空消费根引导工具、下载依赖、编译并运行，CTest 1/1 及再次 configure 通过。用户确认远程方式可用，并确认 Linux 验收通过；结合前述 Windows 证据，GitHub 阶段按三平台验收完成。Linux 结论采用用户验收，不附未提供的命令、日志或运行指标。

这里验收的是本地发行包与 GitHub Release 消费，不包含直接导入 builder 原始安装树。后者需要单独设计；Gitee 镜像与默认来源消费继续后置，不由 GitHub 验收推定可用。

### CMake 公共接口

消费者携带单文件入口，不需要旁置 CMake helper：

```cmake
include("${PROJECT_SOURCE_DIR}/3rdparty/autolib.cmake")
add_executable(app src/main.cpp)
autolib_import(TARGET app PRIVATE queue flann)
```

接口为 `autolib_import(TARGET <已有target> <PRIVATE|PUBLIC|INTERFACE> <根包...>)`。消费 target 必须已经存在，可见性沿用 CMake 链接语义。缺 target、缺可见性、缺根包、空包名及非法参数均失败；重复根包保留首次出现。函数先准备全部根包的依赖闭包，核对完整结果后加载 adapter，最后将根包的 `autolib::<name>` 按指定可见性链接到消费 target。依赖通过包目标传递，不返回要求用户另行链接的目标列表，不要求再次调用 `target_link_libraries()`。

旧的 `LOCAL_ROOT`、`REMOTE_ROOT`、`PACKAGES`、`WORK_ROOT`、`TARGET_OS`、`TARGET_ARCH` 导入参数及 `URI` 均拒绝，不保留两套调用接口。目标 OS/架构从当前 CMake 配置取得，沿用已有平台归一规则，VS 显式生成器平台优先；工作根内部使用 `${CMAKE_BINARY_DIR}/autolib`。宿主身份仅用于选择 importer，不用于选择库包。

重复 include 入口不创建库 target；同一次 configure 的多次导入固定使用同一来源对、目标平台和工作根，可以增加根包或链接其他消费 target。每次按本次根包准备依赖闭包，已有完整包按原协议复用；共享依赖的公共目标通过 adapter 的 target guard 复用，未加载的目标按依赖先序创建，再链接本次根包。首次导入拒绝已有上游同名目标，不协调不同来源或版本；同次 configure 不支持在外部替换已导入包的内容。

来源使用首次 include 捕获的值；目标平台和工作根在首次导入时固定，后续不允许混用。切换这些输入须重新启动 configure。当前 prepare 失败或完整结果核对失败时，不加载本次 adapter、不链接消费 target、不读取旧结果补齐；adapter 失败以 FATAL_ERROR 终止 configure，允许终止前已经创建部分 target，不承诺回滚，也不在失败进程中继续后续导入。重新 configure 重新建立 CMake target。

### 一次性来源配置

`autolib.cmake` 仅在外部变量未定义时填入默认值：

```cmake
if(NOT DEFINED AUTOLIB_LOCAL_ROOT)
    set(AUTOLIB_LOCAL_ROOT "${PROJECT_SOURCE_DIR}/3rdparty")
endif()
if(NOT DEFINED AUTOLIB_REMOTE_ROOT)
    set(AUTOLIB_REMOTE_ROOT "https://gitee.com/xxf1ow/autolib-release/releases/download/autolib-release")
endif()
```

用户可在 include 前通过 `set()` 或通过 `-D` 覆盖。首次 include 填入默认值后，将最终来源对保存为当前 CMake 进程的内部 GLOBAL property；相对本地路径按顶层源码目录归一为绝对路径。所有目录和函数作用域内的导入都读取该固定值，不再次从调用方作用域读取来源；后续修改同名外部变量或重复 include 不改变已捕获值。来源不放入 `autolib_import` 参数。显式空远端值禁用联网，不能按未定义处理而恢复默认值；显式空本地值跳过本地来源，两来源均为空时失败。

查找沿用本地已解包目录、本地归档、配置远端的顺序。只有候选不存在才继续，坏目录、坏归档、身份错误和读取失败立即报错。全部依赖和宿主工具已在本地就绪时，不查询远端或检查更新；不增加隐式 GitHub 回退、其他仓库搜索或离线探测。未启用对应远端能力的阶段，只有实际缺工具或包才报告尚未启用，不因设置了默认远端而阻断完整本地消费。

CMake 接口收缩不删除 importer CLI 所需输入。CLI 继续使用 `prepare`、可选 `--local-root`、`--remote-root`，必填 `--work-root`、`--os`、`--arch`、`--result` 及可重复 `--package`，一次 prepare 只处理一个目标平台。CMake 不向 CLI 传递空来源选项；直接 CLI 调用的显式空选项或两来源均缺失仍失败。CLI 不自行设置 CMake 的默认来源，普通脚本和 CI 可继续显式调用本地准备能力。

### 每包一个公共目标

所有现有及未来可消费包，每包只提供一个公共目标 `autolib::<name>`，命名空间统一使用小写 `autolib::`。例如 queue、lz4、flann 分别提供 `autolib::queue`、`autolib::lz4`、`autolib::flann`，不是把所有包合成一个目标。不保留 `AutoLib::` 大写兼容别名。

上游导出目标可以作为 adapter 的实现细节存在，消费者不需额外链接它们。`autolib::flann` 同时封装 `flann::flann_cpp` 和 `flann::flann`，支持 C++ 和 C API，并通过 lz4 包目标传递完整的头文件及链接信息；只请求 queue、flann 的消费者可以直接包含 `<lz4.h>` 并调用 LZ4 API。聚合不等于同时链接静态、动态等互斥产物，adapter 仍选择本包实际交付的可消费组合。支持范围为现有发布的 Release 产物，不增加静动态或构建配置选择接口，也不承诺任意组合可用。

adapter 不联网、不编译源码、不回退系统同名包，不在消费时改写上游元数据。受影响的现有 adapter、消费测试及新增模块技能的 adapter 约定同步迁移，防止后续模块继续引入大写公共目标。测试使用新打包产物和新消费根，不修改或覆盖用户已有缓存中的旧 adapter，不增加旧包自动迁移。

模块作者负责在模块声明中列出 adapter 实际需要的直接包依赖，由 builder 生成包头；adapter 使用已准备依赖的公共目标传递消费要求。真实消费测试验证声明闭包足以完成链接和运行，不增加 CMake 正文依赖分析器。编译器及系统运行库不因此变成包依赖。

### 职责边界

builder 负责声明解析、包内依赖生成、暂存修正和本地打包，不认识 Release 地址、CI 凭据或远端发布策略。importer 负责包查找、依赖遍历、下载、校验和安全准备，不执行 CMake adapter，也不发布产物。CMake 入口负责一次性来源配置、最小工具引导、结果核对、adapter 加载及消费 target 链接，不重写 importer 的包获取和解包能力。

首次工具引导使用 FetchContent，只下载宿主 importer，不编译源码；显式开启证书校验并通过 URL_HASH 核对 GitHub API 摘要，成功后才执行。CMake 请求接受原生重定向行为，不增加降级检测、自定义重试、外部下载器或自动恢复；已识别的错误直接终止 configure。包下载仍由 importer 执行，保留其拒绝非 HTTPS 重定向的规则。

架构、开发和测试指南分别保留远端消费职责、来源配置与安全限制、验证命令及供给前提；它们不再只描述本地导入。对应文档预算扩展以容纳这些必要说明及余量，协议细节仍集中在本设计和包消费设计，不重复维护资产规则。

`autolib-ci` 通过独立 CLI 调用 builder、importer 和 CMake，负责生产编排和 GitHub 发布；workflow 负责机器、环境、权限及运行并发。三个可执行目标继续独立源码和入口，不互相混编业务实现。Gitee 镜像由 CI 专用本地 Action 完成，不加入 builder 或 importer；普通本地构建与消费不依赖 CI。

### 交付阶段

1. 本地消费接口。实现 target 接口、一次性来源默认值、每包一个 `autolib::` 目标及 FLANN C/C++ 封装，迁移受影响的生产与测试调用点、当前架构与开发测试指南，以及新增模块技能的 adapter 约定。不启用远端枚举、下载或 importer 引导；本地齐全时直接消费，缺工具或包时明确失败且不联网。
2. GitHub 下载消费。在本地接口验收后，按既有无清单协议实现公开 Release 资产枚举、SHA256 下载校验及宿主 importer 引导。保留最终 Gitee 默认值，验证时外部覆盖 `AUTOLIB_REMOTE_ROOT` 为 `https://github.com/xxf1ow/xxautolib/releases/tag/autolib-release`，不改成 GitHub 默认值，也不隐式回退。使用现有消费测试验证真实下载后的准备、链接和运行。
3. Gitee 镜像与消费。GitHub 阶段验收后，核实 Gitee API、资产摘要及工具引导的实际能力，补充独立 CI 设计并确认消费协议，再规划和实施同步与默认来源消费。不能从 GitHub 字段推定 Gitee 能力，也不以放宽校验或自动切换来源解决未核实问题。

每阶段验收后再规划下一阶段，不提前编写后续全部实施计划。各阶段复用固定资产名、四行包头和平台身份，不引入全局清单、版本管理、锁文件、build ID、跨运行生产缓存或自动更新。GitHub 下载消费不自动授权远端上传、触发 CI 或 Gitee 写入。

用户已有 example 修改代表预期用法，随本地 target 接口一并验证和提交，不另行改造示例结构。不增加 example 专用 cmake 目录、重复跟踪单文件入口、自动 DLL 复制或 CTest 接线。真实消费验证由现有测试承担，构建成功不能代替运行成功；Windows 运行仍需通过 PATH 等满足实际 DLL 查找要求。

README 按完整交付形态编写，不随阶段反复改写，并包含“使用本仓库前后对比”。对比采用相同消费需求：不使用本仓库时自行准备第三方库、处理依赖和链接目标；使用本仓库时设置来源并通过 `autolib_import(TARGET app PRIVATE queue flann)` 完成依赖准备与链接。不得虚构性能或耗时收益，不省略消费环境仍须满足的工具链和系统运行库要求；阶段性来源限制与供给前提由开发、测试指南记录。

### Gitee 镜像选型与范围

默认 Gitee 仓库 `xxf1ow/autolib-release` 为专用 Release 镜像仓库，没有其他用途；参考项目已废弃，不调查旧项目共同写入、接口兼容或迁移需求。Gitee 使用固定 tag `autolib-release`、相同资产文件名及同名替换策略，不据此假设其 API、资产状态或摘要字段与 GitHub 相同。

镜像选用 [action-gitee-release](https://github.com/nICEnnnnnnnLee/action-gitee-release) 作为参考本体。实施时固定实际上游提交，将源码原样引入 `.github/actions/gitee-release/`，核对上游及其依赖许可，保留许可证、来源及提交记录，先验证基线，再最小适配；本地适配差异由 Git 记录。仅补固定 Release 不存在则创建、存在则复用，新增附件上传、同名附件替换，其他附件不动。上游 `update_asset` 要求附件已存在，不能直接当作新增与替换统一入口。

不删除整个 Release、不扫描历史、不同步 Git 源码，不重写成 Go 或扩为通用镜像工具。上游 Python 和依赖仅在 CI 运行，不给消费者增加 Python。Gitee token 只供镜像写入步骤，不进入包、消费入口或日志；默认分支、最小权限、写入并发及失败汇总承接独立 CI 设计，并在该阶段明确具体接线。

第三阶段的独立 CI 设计须明确镜像失败与重跑：失败使本次镜像阶段失败，保留已成功附件，失败槽可能缺失；重跑根据远端实际附件补齐或同名替换，不自动回滚，不以另一份持久化清单判断完成状态。该规则在第三阶段核实、确认及验收，不扩大前两阶段实现范围。

## 备选方案

**导入函数输出目标列表，再由用户链接。** 减少重复枚举目标，但仍需维护两次调用；一步式 target 接口直接表达消费者、可见性与包需求，不另设输出列表接口。

**继续在每次导入时传来源、平台及工作目录。** 调用者可以调整更多细节，但同次 configure 不能混用这些输入；一次性来源配置和现有 CMake 平台信息已能满足当前消费需求，不额外暴露准备参数。

**直接调用外部 Gitee Action 的原始接口。** 不需维护本地源码，但新增与更新接口不能直接完成固定槽批量替换，把判断散在 workflow 会重复资产规则；原样引入后最小适配保留可核对的参考基线。

**使用 hub-mirror-action 或 sync-release-to-gitee。** 前者用于 Git 源码镜像，不承担所需 Release 附件同步；所比较的后者对同名附件跳过，且下载关闭证书验证，不符合固定槽替换和 HTTPS 校验要求。

## 验收标准

### 本地消费接口阶段

- 真实 CMake fixture 验证 target 必须存在，`PRIVATE/PUBLIC/INTERFACE` 按标准语义传播；非法消费 target 类型或可见性组合按原生 `target_link_libraries()` 规则失败，不维护另一套类型矩阵。无效参数失败，不接受旧导入形式，不要求用户提供输出变量或再次列出链接目标。
- 默认值只填未定义变量，include 前赋值及 `-D` 覆盖生效；显式空远端禁网、空本地跳过本地来源、两源均空失败。首次 include 在函数或子目录作用域内时也能固定来源；后续修改同名变量及重复 include 不改变实际来源。本地闭包及工具齐全时，即使配置远端也不联网；缺包或缺工具时报告尚未启用，不尝试下载。
- 使用新 builder 包的真实消费者只请求 queue、flann 即编译运行，验证 queue 入出队、LZ4 压缩解压及 FLANN C/C++ API；不靠额外链接上游目标补齐。所有现有包只提供 `autolib::<name>`，新增模块技能使用相同约定。
- 保留依赖闭包、来源优先级、安全解包、重复 include、多次导入、失败不加载旧结果和搬迁运行的受影响验证；不改写已有用户包，不要求 example 或 README 改造作为阶段验收条件。
- 两个消费 target 共享依赖、后续调用新增根包及不同链接可见性均直接验证；复用已有公共目标，不重复创建，不要求第二次枚举链接目标。后一次 prepare 失败不加载其 adapter 或执行后续语句；adapter 失败直接终止 configure，不验收失败后继续使用部分 target。
- 平台定向 fixture 验证 VS 显式平台优先、已有架构别名归一，以及工具链设置的库目标与宿主工具选择相互独立；未知目标明确失败。不新增交叉编译系统或穷举平台组合。

### GitHub 下载消费阶段

- 先核对代表集三平台供给的固定名、状态、API digest、实际字节和本地消费；复用未受改动影响的有效证据，不以上传命令成功代替供给验收。
- 外部覆盖 GitHub 来源，以空消费根完成宿主 importer 引导、依赖闭包下载、校验、解包、链接和实际运行，不依赖 Go/Python；入口仍是单文件。本地齐全再次 configure 为零远端请求，同次 configure 多次导入至多一轮完整资产枚举。
- 真实 Release 验证资产信息、摘要和 HTTPS 重定向下载；分页、重名候选、非 uploaded 状态、非法或缺失摘要、枚举后删除及内容不匹配使用定向 fixture，不为制造异常污染正式 Release。CMake 引导验证证书和执行前摘要校验；拒绝 HTTPS 降级只验收 importer，不为 CMake 增加原生能力之外的检测。失败不自动重枚举、不加载旧结果。
- 保留 Gitee 默认值，未启用 Gitee 时实际缺输入明确失败，不隐式回退 GitHub；对应使用说明明确该阶段需外部覆盖地址。
- 本地阶段的显式空远端禁网、本地完整闭包零请求和坏本地候选不回退继续成立；配置 GitHub 且缺工具或包时，改为验证引导或补齐，远端失败终止本次调用，不保留本地阶段“远端尚未启用”的阶段性断言。

### Gitee 镜像与消费阶段

- 上游原样基线与最小适配均有验证，固定 Release 首次创建、后续复用、新附件上传、同名替换及无关附件保留符合约定；镜像失败必须可见，不能以 GitHub 已发布代替成功。
- 默认 Gitee 地址无需 GitHub 覆盖即可完成工具引导、下载、准备、链接和运行；摘要与工具执行前校验依据按该阶段已核实并确认的协议验证，不用 GitHub 成功推定 Gitee 可用。

README 的前后对比使用同一需求和可验证的用法，不加入虚构收益。局部和阶段末只验证受影响增量，全部受影响阶段完成后执行一次全量回归。真实远端验证及写入仍按授权范围进行，不以 fixture 代替实机证据。

## 风险

新接口和公共目标命名不兼容旧调用方式，活动调用点须同步迁移；不承诺兼容旧缓存里的 adapter。验证使用新包，不自动重写已有用户输入；需要使用新目标的用户自行准备新包或显式刷新对应缓存。

GitHub 阶段保留尚未接通的 Gitee 默认来源，缺包联网必须显式覆盖地址。Gitee 的 API 摘要及工具引导能力仍待其阶段核实，可能要求调整该阶段消费协议；在核实确认前不实施，不阻塞本地接口或 GitHub 阶段。

传输摘要不认证发布者，包内校验不防恶意替换；本地工具和 adapter 仍是用户信任的可执行输入。固定槽替换存在短暂不可下载窗口，不提供整套发行快照、跨批次 ABI 兼容或自动回滚，依照既有包消费协议失败并允许用户重跑。

FetchContent 的原生下载可能跟随 HTTPS→HTTP 重定向。仅工具字节被篡改时，来自可信 HTTPS API 的预期 SHA256 会阻止执行；若摘要请求也降级并被篡改，则不能依靠同源摘要证明工具可信。首次引导接受这一限制，以保留单文件入口和最小下载流程，不宣称具备全程 HTTPS 强保证。
