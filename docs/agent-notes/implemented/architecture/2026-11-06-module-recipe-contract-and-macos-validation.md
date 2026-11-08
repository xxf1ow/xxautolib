# Agent Note: 统一模块配方字段与 macOS 全包验收

Status: implemented

## 问题

模块配方的字段说明分散在多个设计记录与接入技能中，声明、解析校验、执行和消费对部分字段的解释不同。`supported_platforms` 与 `platform_independent` 分别控制平台选择和发布身份；`copy` 配方可以声明不会生效的 CMake cache；部分入口参数被静默删除；步骤接受多种命令形状，路径检查在生成和描述入口之间存在差异。仅补平台字段不能建立一致的配方规则。

Eigen 的 C++ 模板 API 是纯头文件，但上游还提供可选的 BLAS/LAPACK 二进制接口。本仓库交付头文件与 CMake/pkg-config 元数据，PCL 通过 Eigen config 导入。安装方法、查找方法和产物的平台属性需要分别定义。

接入前的 30 个包共有 31 份版本配方，包括两个 Eigen 版本。仅 lz4 和 queue 声明 Darwin 支持；其余声明不能证明上游不支持 macOS。需要真实获取、安装、消费和必要的编译运行证据，确定每个现有版本的支持范围、补丁和系统依赖。

## 决定

统一模块配方及其在 builder、安装、importer、CI 计划、运输和发布校验中的表示。配方只声明事实，builder 统一解析；CI 使用解析结果组织执行和发布，不再次解释 TOML。安装入口控制执行环境，adapter 表达用户消费要求。

第一阶段以 `05ad42949ec1ec1d6f9113cdc7e538263836215d` 为起点，其实现和验收修复保留到 `49df3fc45041861fdd251e30b10b4106e3f1a65d`。第二阶段从该收口提交分叉到普通任务分支 `task/darwin-aarch64-sync`，保留 GCC 10.5、HarfBuzz 修复及已确认的字段、通用包和执行基础。旧分支提交 `b0e30938c1830fcfa9ddc7015799330e28f8bf43` 作为调查和产物证据来源；不整体合并或批量移植其第二阶段实现，不 reset 或改写旧历史，不创建 worktree，也不把 master 合入任务分支。

第一阶段已覆盖全部现有配方的字段迁移、相关引擎和 JSON 消费者、安装及消费准备、配方接入技能和对应文档。第二阶段的产品目标是在既有构建、运输及发布链路中增加 macOS 产物，使 `all` 包含已确认的 Darwin 包；独立 workflow 用于验收，不另建发布系统。第二阶段只增加必要的 Darwin 来源、支持声明、系统依赖和构建交付适配，不全面重构独立 CI 工具配置或其他 workflow 输入，不顺带升级上游或调整原平台功能。新增版本包括下文已单独批准的 Asio 1.24.0，以及从独立提交迁入的 ViSP 3.7.0。

### 原平台基线与 macOS 同步范围

Windows x86_64、Linux x86_64 和 Linux aarch64 的功能基线固定为 `49df3fc`：保留源码版本、来源摘要、功能选项、静动态形态、仓库依赖、apt/choco 声明及已有平台覆盖。第一阶段已经批准的字段迁移、Eigen 通用身份和 Config 安装导入继续保留；这不授权第二阶段进一步调整其他平台的产物配方。共享引擎或 adapter 的修改仅限 Darwin 接入所必需的行为，必须核对原平台有效配置及消费语义不变；改变原平台契约的修复须单独讨论。

每个包先用固定版本的明确配置、已有构建日志和可获得的成品确定原平台实际启用的模块、可选后端、依赖及使用要求。源码或配方未设置某开关不证明各平台功能相同；没有公开产物也不证明某功能关闭。优先复用已有证据，只为影响判断的缺口补最小核验，不安排原平台全包重编。原平台已有不一致或证据不足且无法确定 macOS 目标时，报告具体差异并暂停受影响包，不自行取功能并集、交集或改造原平台。

macOS 只同步已确认的既有能力。既有能力需要额外依赖时，只补足 Darwin 所需的 brew 声明、仓库依赖接线、Framework 或交付补丁；原平台已经满足该能力，不要求包管理器名称和依赖文件完全相同。原平台确认未启用的可选能力，macOS 也不得因 runner 预装库或 brew 传递依赖而自动开启，可通过 Darwin 专属开关显式关闭这项额外能力。关闭额外能力不等同于裁掉既有功能；证据不明时不得以此名义关闭功能。

禁止为了“统一”而给 Windows/Linux 添加或关闭后端、补 apt/choco 包、改版本或依赖闭包，也禁止只在 macOS 增强功能。若确实需要跨平台新增能力，必须报告影响并另行取得明确决定，不能并入本阶段适配。当前任务不增加第二份产品能力 schema、通用功能开关管理器或自动分析全部上游选项的框架；基线对照和关键验收结论保存在本记录，执行细节放在忽略的计划及证据目录。

### 已单独批准的头文件包修复与兼容版本

WebSocket++ 的发布安装与复制头文件不依赖 Boost 或 Asio。恢复已批准的全平台独立头文件包修复：移除该包的 Boost `depends` 和 adapter 后端绑定，不改成 Asio 依赖；网络后端由消费者显式选择。检查其他现有纯头文件配方的发布依赖，保留真正的系统线程使用要求和 pybind11 消费者的 Python 开发环境，不增加泛化的“头文件类型禁止依赖”解析规则。两个 Eigen 继续上游 CMake 安装及 Config 导入。

新增独立 `asio@1.24.0`，固定来源提交 `147f7225a96d45a2807a64e443177f621844e51c`，以 copy 从 `asio/include` 安装头文件，附带 `asio/LICENSE_1_0.txt`，身份为 `any-any`，不声明包依赖。原 `asio@8806a6803cde` 和包级 latest 不变，消费者显式选择版本。基线 31 份版本配方加此项后为 32 份；不替换原版本或自动降级。WebSocket++ 单包消费与它和 Asio 1.24.0 的 standalone 组合分别验收，后者检查直接及搬迁后的网络 transport 初始化。

WebSocket++ 与 Asio 两项是用户已经单独批准的全平台例外，不构成修改其他原平台配方的授权。旧分支的其他全平台变更，例如 Taskflow 语言标准传播，先根据本分支既有消费契约核对必要性；若改变原平台要求，单独报告，不随 macOS 补丁一并带入。

SDL3 已补查原三平台均未启用 libusb 后端。用户批准在公共 cache 显式设置 `SDL_HIDAPI_LIBUSB=false`，覆盖原三平台并供 Darwin 同步使用；这是对已证实关闭状态的固定，不新增或裁掉既有能力。该设置是原平台配方冻结规则的具名例外，其余 SDL3 配置、系统依赖及上游提交不变。SDL3 和 ImGui 在 Darwin 完成构建、直接消费及打包搬迁消费验收后，才提交本轮正式修改并进入下一阻断包。

libzmq 已补查原三平台均因未找到 GnuTLS 而不启用 WSS。用户批准在公共 cache 显式设置 `WITH_TLS=false`，供原三平台与 Darwin 同步使用，保留普通 WS 及其他原配置；这是原平台配方冻结规则的另一具名例外。不新增 GnuTLS 系统依赖或修改静动态形态，完成 macOS 功能查询、直接及搬迁消费验收后提交。

字体依赖链允许全平台统一修复 FreeType/HarfBuzz 的集成方向及仓库 zlib 接线；具体能力取舍和理由由 [FreeType 配方注释](../../../../src/dependencies/freetype/2.13.2/module.toml)及 [HarfBuzz 配方注释](../../../../src/dependencies/harfbuzz/8.5.0/module.toml)拥有。OpenCV 保留字体模块并声明其 pkg-config 查找所需的仓库 zlib。FreeType 对 PNG、BZip2、Brotli 三项可选字体格式依赖在所有平台显式关闭，避免引入非各目标系统默认具备的库；不影响 OpenCV 的 PNG 图像编解码。OpenCV 其余后端基线继续沿用原规则。验收覆盖四个平台的字体链原生构建、实际中文字形绘制和打包搬迁消费；完成前不解除这三个包的 Darwin 阻断。

### ViSP 独立接入

ViSP 3.7.0 从 `fdde64b8bfc5e7d4ce057e3eb85ce84b9ac8b213` 迁入当前开发分支，保留该提交的源码摘要、七个公开模块、内置 LAPACK、OpenCV 精确依赖、OpenMP 和其他功能选项；不借迁入修改现有包的配方。新增 Config adapter 提供 `autolib::visp`，封装本包全部公开模块并链接仓库 OpenCV。Darwin 使用 Apple Clang 和已声明的 brew libomp；运行库、公开头文件及许可随 ViSP 交付，Config 和 Mach-O 引用使用包内位置。完整接入在 macos-15 完成构建、公共入口重复导入和跨目录使用、混合跟踪及视觉伺服、打包搬迁消费后，一次提交到正式开发分支。

### 字段规范所有者与实现职责

`docs/subsystems/module-recipes.md` 是当前字段参考的唯一所有者，完整列出类型、默认值、必填条件、适用类型、路径基准、覆盖方式、互斥条件、实际效果和校验时机。本记录保存设计、理由和验收要求；架构文档解释组件职责，开发及测试指南拥有命令，接入技能引用字段参考。

配方自有字段使用小写名称，明确沿用 CMake 原生语义的来源、步骤与 `SOURCE_SUBDIR` 保留原名，cache 中的上游变量也保留原名。不开放任意 ExternalProject 参数透传。未知字段、无效类型、没有执行效果的字段组合及违反参数所有权的声明必须报错。

解析需要区分原始声明、目标覆盖后的有效配方及执行上下文。同一套解析结果供生成和描述使用；这些职责可在现有 builder 内实现，不为分层复制多套同义模型，不引入通用 schema 引擎或插件系统。

### 名称、版本与依赖

| 字段或位置 | 规则 |
|---|---|
| `src/dependencies/<name>/<version>/module.toml` | 目录是名称和版本的唯一输入；删除配方的 `name`、`version` |
| 名称及版本 | 精确匹配、保留大小写；使用可安全组成路径与引用的标识，拒绝路径分隔符、末尾点、Windows 保留名和大小写冲突；版本不强制 SemVer |
| `package.toml.latest` | 必填非空字符串，指向已有版本；包级文件仅接受该字段，不通过排序推算版本 |
| `depends` | 可选字符串数组，默认空；每项必须是 `name@version`，只列直接依赖，拒绝重复及同名不同版本 |

用户入口仍接受裸包名并通过 `latest` 选择；显式版本始终精确选择，不因目标不支持而回退。已有配方中的裸名依赖已按迁移时的 `latest` 一次性改写为 `name@version`；新解析器始终拒绝裸名依赖，不在读取时展开，不保留旧格式兼容。之后修改某依赖的 `latest` 不改变既有配方的依赖。

builder 向 CI 与安装消费元数据传递确定版本的直接依赖。产物离开源码目录后仍需自描述，因此输出继续包含名称和版本。依赖关系服务于构建顺序和消费加载；实际链接目标由 adapter 表达，不把所有依赖机械加入链接命令。单个根包闭包内的版本冲突、缺失依赖和依赖环报错并给出依赖链；不同根包的独立版本不因此禁止分别构建。

### 包平台与发布身份

`package_platforms` 必填，是非空、无重复的字符串数组，允许 `windows-x86_64`、`linux-x86_64`、`linux-aarch64`、`darwin-aarch64`，或单独的 `any-any`。通用与具体平台禁止混用。删除 `supported_platforms` 和 `platform_independent`，不保留别名、兼容解析或另一个构建白名单。

请求平台 P 时，通用声明解析为 `any-any`；具体列表包含 P 时解析为 P，其余为不支持。通用资格限于引擎已识别的目标，不扩展新系统或架构。Native 执行仍使用当前目标平台，包身份由此字段推导，不由安装工具决定。具体平台包不能同时发布通用副本。

每个根及传递依赖独立判断平台。通用根仍受平台相关依赖限制；坏声明、缺失版本和依赖环是错误。沿用普通显式请求失败、自动发现及生产 CI 返回有效根与过滤原因的区别；过滤后零库可以保留工具和入口，但不能声称库已验证。平台资格在取源和要求目标专用来源前判断。

`any-any` 声明针对完整交付内容。头文件、生成文件、导出目标、依赖记录和查找元数据都必须可跨目标使用；没有动态库不足以证明通用性。`cmake` 和 `copy` 均可使用通用身份，取消限定 copy 及 include-only 的通用资格规则。copy 的映射和适用的源码字节核对继续使用，允许附带平台无关元数据；CMake 安装使用真实安装、身份、重定位和消费验证，不套用复制映射。

两种安装类型在消费准备及打包时共用通用载荷检查，按文件内容拒绝已识别的 ELF、PE、Mach-O、COFF 目标文件及目标代码归档，不仅检查扩展名或是否位于 include 下。copy 的集合及来源字节核对不能代替这一检查。不增加任意头文件的平台无关性分析器或第二份通用载荷清单；生成头文件、CMake/pkg-config 元数据及条件逻辑仍由配方审查与真实跨平台消费验证，已知二进制格式检查不证明所有内容都平台无关。

错误声明通过修正配方解决，不在失败后自动降级身份。不同生产平台必须保持同一版本的功能、公共 API、依赖要求和跨平台可用性，平台覆盖只适配生产过程；不要求归档时间戳、文件顺序等造成的字节差异消失。唯一发布副本的摘要定义公开归档和消费证据，不能仅凭唯一 publisher 或摘要推断交付语义一致。

### 主来源与附属资源

`source` 为主来源，`resources.<名称>` 为所属模块私有的附属输入，默认无主来源和空资源表。二者使用完全相同的格式。CMake 安装需要主来源；copy 映射或补丁选择主来源时也必须提供。资源不单独发布，其他包不能通过 `depends` 引用；独立交付的库使用包依赖。

| 来源字段 | 类型与规则 |
|---|---|
| `GIT_REPOSITORY` | Git 模式必填非空字符串，与 `GIT_TAG` 成对 |
| `GIT_TAG` | 完整固定提交哈希；当前配方使用 40 位十六进制提交，禁止分支、标签和短哈希 |
| `URL` | 归档模式必填，有序、非空的非空字符串数组；各地址提供同一份归档 |
| `URL_HASH` | 归档模式必填，格式为 `SHA256=<64位十六进制>` |
| `DOWNLOAD_NAME` | 归档模式可选；仅为下载文件名，禁止目录、绝对路径、`.` 和路径穿越 |

Git 和归档字段互斥，不另增来源类型字段。来源声明整组替换，不能把新 URL 与旧 hash 合并。下载和解压继续由原生 ExternalProject 执行；固定提交约束新准备的源码，不授权对已有源码目录自动 fetch、checkout、reset 或还原。已有 Git 工作副本和人工修改按既有规则保留。

### 安装类型、CMake 参数与复制映射

`type` 必填且只接受 `cmake`、`copy`，分别执行上游 CMake 安装或声明的复制映射。它不表示纯头文件、二进制、通用包或 config 导入方式。

| 字段 | 类型、默认及适用范围 |
|---|---|
| `cmake.SOURCE_SUBDIR` | cmake 专用的可选相对目录，缺省为主源码根；不得越界 |
| `cmake.cache` | cmake 专用表，默认空；键为有效 CMake 变量名，值为字符串、布尔、整数或字符串数组 |
| `copy` | copy 专用非空映射数组；cmake 配方禁止出现复制映射，copy 配方禁止出现 `cmake` 表 |
| `copy[].resource` | 可选非空资源名；缺省使用主源码，填写时必须存在当前有效资源 |
| `copy[].files` / `directories` | 二选一的非空相对路径数组，基于选定源码根，允许目录项 `.`，禁止越界 |
| `copy[].destination` | 必填相对目录，基于本包安装前缀，允许 `.`，禁止越界 |

布尔 cache 生成 BOOL，其他允许值生成 STRING，字符串数组转为 CMake 列表。库选项和允许覆盖的默认值按照引擎兜底、入口默认值、有效配方的顺序合成；目标平台覆盖最后生效。不得把不能解释的值静默当作空列表。

公共默认值与库构建形态按下表归属；copy 类型不应用这些 CMake 默认值。

| 变量 | 默认及所有权 |
|---|---|
| `CMAKE_POLICY_VERSION_MINIMUM` | 引擎兜底 `3.5`，允许入口默认值及配方覆盖；不降低上游更高的最低版本声明 |
| `CMAKE_POSITION_INDEPENDENT_CODE` | 引擎兜底 `ON`，允许入口默认值及配方覆盖；显式关闭时不再保证静态库可链入下游共享库，相关交付承诺须经实际消费验证 |
| `CMAKE_CXX_STANDARD` | 引擎兜底 `17`，允许入口默认值及配方覆盖；adapter 的语言要求由实际公共 API 和上游导出目标决定 |
| `CMAKE_INSTALL_LIBDIR` | 引擎兜底 `lib`，允许入口默认值及配方覆盖，查找元数据与 adapter 必须匹配最终安装布局 |
| `BUILD_SHARED_LIBS` | 库构建形态由配方及其平台覆盖决定，未声明时遵循上游默认；不新增全局强制静态或动态开关，也不把父项目同名值自动传入子项目；库专用开关及补丁仍由配方拥有 |

生成器及其 platform/toolset/instance、安装前缀、构建配置、运行库、入口控制的 Make、编译器与目标平台设置由执行入口拥有。配方禁止填写 `CMAKE_INSTALL_PREFIX`、`CMAKE_BUILD_TYPE`、`CMAKE_MSVC_RUNTIME_LIBRARY`、`CMAKE_GENERATOR`、`CMAKE_GENERATOR_PLATFORM`、`CMAKE_GENERATOR_TOOLSET`、`CMAKE_GENERATOR_INSTANCE`、`CMAKE_MAKE_PROGRAM`、`CMAKE_C_COMPILER`、`CMAKE_CXX_COMPILER`，Darwin 还禁止填写 `CMAKE_OSX_ARCHITECTURES`、`CMAKE_OSX_DEPLOYMENT_TARGET`、`CMAKE_OSX_SYSROOT`；与入口相同的重复声明也报错。配方不得通过 toolchain 或系统身份设置切换 Native 目标。

依赖接线单独处理。直接依赖前缀加入 `CMAKE_PREFIX_PATH` 并保留配方路径；项目 include 包装保留配方原有 include 的执行；这些列表型键仅接受字符串或字符串数组。依赖的 `config_dir` 和 `package_name` 描述该依赖自身的安装查找位置，builder 据此向使用方自动注入 `<Package>_DIR`。使用方有效 `cmake.cache` 只要声明同名键即报错，包括值相同的重复声明；这是按有效直接依赖确定的保留键，不是禁止所有以 `_DIR` 结尾的变量。使用方自己的顶层 `config_dir` 描述其安装输出，不参与这一冲突判断。`PKG_CONFIG_PATH` 加入直接依赖声明的目录。不存在的依赖资源或无法展开的已识别占位符报错。

复制按映射顺序执行，`files` 保留文件名，不保留上层源码目录；`directories` 复制目录内容到 destination，多个映射沿用原生复制的覆盖行为，不新增文件冲突分析器。资源在执行时准备，不因 configure 时尚未下载而判定内容缺失。

### 补丁与步骤

`patches` 为可选数组，默认空。每项 `file` 必填，表示配方目录内已有的普通补丁文件；可选 `resource` 指定当前有效资源，否则作用于主来源。禁止绝对路径及越界。针对同一源码目标按声明顺序应用补丁；主库配置和复制必须等待所用资源及补丁完成。

`steps` 为可选数组，默认空，适用于两种安装类型。它保留原生步骤语义，不增加 before/after 接口。

| 步骤字段 | 规则 |
|---|---|
| `name` | 必填有效标识；合并后唯一，不得与原生步骤重名 |
| `COMMAND` | 必填二维 argv 数组；至少一条命令，每条非空且首项非空；多条命令依次执行 |
| `DEPENDEES` | 可选步骤名数组，本步骤等待的原生或自定义步骤 |
| `DEPENDERS` | 可选步骤名数组，必须等待本步骤的原生或自定义步骤 |
| `DEPENDS` | 可选文件路径数组，默认空；用于输入变化引起的原生步骤失效，不表示包依赖 |
| `WORKING_DIRECTORY` | 可选路径，缺省 `<BINARY_DIR>`；普通相对路径基于配方目录 |

步骤至少有一侧先后关系；builder 检查名称及引用，原生步骤图负责调度和循环检查。步骤数组顺序不决定执行顺序。命令按 argv 执行，不隐式使用 shell；参数中的相对路径按工作目录解释，builder 不猜测参数是否为文件。

cache 和步骤支持 `<SOURCE_DIR>`、`<BINARY_DIR>`、`<INSTALL_DIR>`，以及 `<RESOURCE_SOURCE_DIR:资源名>`。资源引用由 builder 统一展开，其他列出的目录标签按原生步骤位置展开；未知目录占位符报错。普通 `<` 字符或 CMake 生成表达式不能仅凭包含尖括号跳过路径检查。`DEPENDS`、`WORKING_DIRECTORY` 的普通路径均限制在配方目录内；以明确目录标签为基准的路径不能通过 `..` 逃出该目录。生成文件的存在性在实际执行时检查，不能强制其在描述阶段已存在。

### 安装查找元数据、adapter 与复用

| 字段或文件 | 规则 |
|---|---|
| `config_dir` | 可选相对目录，基于安装前缀，允许 `.`；构建直接依赖该包的模块时注入 `<Package>_DIR` |
| `package_name` | 可选非空查找名称，缺省包名；仅与有效 `config_dir` 一起使用 |
| `pkgconfig_dir` | 可选相对目录，基于安装前缀，允许 `.`；加入使用方的 pkg-config 搜索路径 |
| `sentinel` | 可选相对路径，指向安装树的代表文件；存在时进入已有安装复用流程 |
| `autolib-import.cmake` | 配方同目录的消费适配器，表达公共目标及实际使用要求 |

元数据适用于两种安装类型，不强制 adapter 使用 find_package。安装完成和复用时检查声明目录存在；实际查找、目标使用和链接正确性由消费验收证明，不从目录存在推断。有效上游导出目标优先复用，避免重复维护包含路径和语言要求。缺少 adapter 的合法配方仍可本地构建，但其自身及包含它的依赖闭包不能作为完整可消费包发布，CI 报告排除原因。

普通源码安装与下载包使用相同 adapter。builder 继续在安装后生成身份和确定版本依赖头，打包使用已有安装，不生成另一份 adapter。`sentinel` 不是版本、ABI、完整性或通用性的证明；复用仍需通过已有身份与依赖检查，失败不自动清理或重标旧安装。

### 平台覆盖

覆盖只有公共声明到具体平台两层，使用 `platforms.<具体平台ID>`，取消 OS 与 architectures 嵌套。具体平台包的覆盖键必须属于 `package_platforms`；通用包可以针对仓库支持的平台调整生产过程，但不能交付绑定生产平台的内容。

第二阶段新增覆盖限于 Darwin 适配，不能用公共 `cmake.cache` 或原平台表改变 Windows/Linux 功能。字段格式允许覆盖不等于本任务授权改变功能；执行范围遵循“原平台基线与 macOS 同步范围”。

| 字段 | 覆盖方式 |
|---|---|
| `source` | 整组替换 |
| `resources` | 按资源名整项替换 |
| `cmake.cache` | 按键覆盖 |
| `cmake.SOURCE_SUBDIR`、四项安装元数据 | 替换；四项为 `sentinel`、`config_dir`、`pkgconfig_dir`、`package_name` |
| `steps`、`patches` | 追加在公共声明之后；步骤仍按依赖关系执行 |
| `copy` | 整个映射数组替换 |

其他字段不允许覆盖，包括 `type`、`package_platforms`、`depends` 和 `system_dependencies`。未声明表示继承，不增加删除标记、空字符串清除或取消继承语法。只适用于部分平台的内容放在相应平台表。所有分支检查字段及类型；当前目标合并后检查完整性、有效资源引用和跨字段条件。

`depends` 表达各平台共用的仓库包依赖，不表达系统 Framework 或包管理器条目。本设计不能表达平台专属的仓库包依赖；逐包调查若发现现有版本确实需要不同闭包，暂停该包并提出最小规则修订，不添加无效依赖凑集合，也不以这一限制直接判定上游不支持 macOS。系统 Framework 由库配置和 adapter 表达，系统包仍使用下节的声明。

### 系统依赖与 Homebrew

`system_dependencies` 为可选公共表，默认空，接受 `apt`、`choco`、`brew`，分别用于 Linux、Windows、Darwin 的构建环境。值为包名字符串数组，默认空，单配方内重复项报错；CI 汇总有效闭包后去重，builder 描述只输出当前平台有效条目。

包名按包管理器校验并拒绝空项、命令选项及命令片段，builder 与准备脚本使用一致规则。brew 仅安装声明的 formula，不引入 cask、自动加 tap 或无需求的全局升级。Darwin 脚本从计划读取 formula，安装后记录实际包版本；只在已声明依赖需要时补充相应查找路径，不全局强塞 Homebrew 的所有库。

仓库已有配方的依赖库使用仓库声明的版本并通过包依赖接线，不能以系统包替代或失败后回退；仓库没有配方的库可以使用已声明的系统包，不要求先为其新增配方。Apple SDK Framework 和原生运行时仍由平台提供。第二阶段只为已确认的既有能力补充 brew，不修改原 apt/choco 声明，也不让为一个依赖安装的其他库意外开启新功能。难解的循环或运行依赖问题具名报告，不扩展方案。

builder 不自行调用包管理器。本地和 CI 使用同一构建流程；本地维护者准备描述中列出的系统依赖，CI 在构建前安装。下载包消费端不自动执行 brew 或其他系统包安装命令。brew 安装成功不代表运行产物通过验收；Apple Clang、arm64、SDK 和 deployment target 仍由 Native 入口控制。

### builder 描述、CI 与唯一发布者

`describe` 保持只读、确定性输出，复用生成入口的解析与目标规则。保留请求根、有效根、过滤原因、有效模块及输入文件；模块输出名称、版本、构建目标、确定版本的直接依赖、已解析 `package_platform`、当前系统依赖及 adapter。删除手工拼接的完整 `configuration` 镜像和 CI 对其非空的检查，不增加另一种完整配方传输格式。

补丁、专用文件和实际存在的 adapter 继续计入输入；路径为源码根相对路径，不泄漏临时 checkout 的绝对位置。生成目录标签不是已经存在的源码文件，不能对其执行源码文件存在性检查。没有 adapter 不使通用描述失败；可发布资格由 CI 单独处理。

CI 按精确 `name@version` 为每个通用包确定一个 publisher，候选仅为本次有效闭包含有该包的平台。沿用 linux-x86_64、linux-aarch64、windows-x86_64、darwin-aarch64 的优先顺序，以 `publishers` 映射保存结果，无通用包时为空。publisher 固定后失败不接管、不降级或重新选择。其他平台继续安装及消费验证，但不重复交付该通用包。

publisher 失败使本次相应交付失败，不阻塞其他平台报告结果；允许在原平台、原代码及原选择下重新执行失败任务，结果仍绑定实际 run/attempt。已有效且输入未受影响的证据继续复用。自动换平台不是重试机制，不为偶发网络或 runner 故障新增自动接管。

运输、finalize 和发布准备按已解析身份及映射核对每个通用归档唯一且来源正确；具体包和宿主工具保持各自身份，canonical 入口仍唯一。发布端不重新解释配方、编译或重打包。包名、包内 OS/Arch、确定版本及摘要沿用[版本化交付设计](../../proposed/architecture/2026-10-29-versioned-artifact-delivery.md)。

### 校验时机与错误

| 时机 | 检查内容 |
|---|---|
| 读取声明 | 未知字段、类型、必填格式、路径规则、所有分支语法、Git 提交及归档摘要格式 |
| 目标解析 | 覆盖后的完整性、安装类型适用性、资源与步骤引用、参数所有权、平台及依赖闭包 |
| 实际执行 | 下载摘要、解压、补丁、命令、源码输入和安装是否成功 |
| 安装完成或复用 | 安装身份、确定版本依赖、声明的查找目录及适用的通用载荷检查 |
| 打包及消费 | 安全链接、生产路径修正、平台身份、真实导入、链接、运行及架构和最低系统要求 |

错误指出配方路径、字段和相关平台；依赖错误提供依赖链。无效声明不能当成平台过滤。生成和描述不能各自放宽或收紧同一字段规则。数组为空与字段省略按各字段定义处理，不把命令 argv、cache 列表等有顺序意义的内容自动去重。

### Eigen 通用交付与跨平台消费

两个 Eigen 版本使用 `type = "cmake"` 与 `package_platforms = ["any-any"]`，保留固定来源、头文件布局、Config/pkg-config 交付及现有 BLAS/LAPACK 关闭选择，不改 latest。它们交付 Eigen 的模板 API，不宣称包含上游全部可选二进制接口。adapter 通过本包 config 获取 `Eigen3::Eigen`，再提供 `autolib::eigen`；5.0.1 的 C++14 要求由上游目标传递。

PCL 保留明确的 Eigen 3.4.0 依赖及 config 查找，不为通用身份修改它的 Eigen 查找机制。两个 Eigen 版本分别在独立消费者中验证；验证使用精确安装根，不能回退系统 Eigen。

验收复用同一份 Linux 生产的 Eigen 归档，在 Windows 和 macOS 解包后完成导入、矩阵运算及路径搬迁消费，记录相同归档摘要。各平台各自重装 Eigen 不替代这项证据。PCL 已支持的平台使用相应平台的 PCL 包导入该 Eigen；macOS PCL 成功适配后必须包含这一组合。任何一方失败都分别记录，不由 Eigen 通用声明推断 PCL 已支持 macOS。

### macOS 全包验证与试验流水线

最终基线为 Apple Silicon `macos-15` runner、Apple Clang、CMake arm64 和 deployment target 15.0；不引入 Intel 包、Rosetta 或交叉编译。本机 `sw_vers` 报告 ProductVersion `27.2`、BuildVersion `26B123`；该执行环境可用于辅助定位，不能替代 15.x 验收或证明最低部署目标成立。

验证范围由版本配方发现，覆盖基线 31 份及追加的 Asio 1.24.0、ViSP 3.7.0，共 33 份，不仅验证 latest 或原有白名单。依赖先行，失败按包隔离；保留有效证据，仅重试受影响的包及下游。源码配方执行获取、补丁、编译、安装和消费；copy 配方执行实际安装与消费，不制造编译步骤。

Asio 1.24.0 和 ViSP 3.7.0 分别作为第 32、33 份配方完成追加验证，不扩大到其他新版本。新分支复用旧调查的方式见“旧分支调查与证据复用”；已有成功只证明对应源码、配置和场景，不自动证明本分支通过。

新增独立的 `validate-macos-recipes.yml` 验收 workflow，名称不匹配现有 Gitee 的 `Representative releases` 监听。复用同一 builder、环境准备和 CI prepare/layers/verify/transport 命令，只保留 Actions 产物和证据，不运行发布入口或写 Gitee。无需依赖密钥缺失、发布故障或默认分支尚未更新来阻止发布，也不全面改造生产 workflow。

逐包验证以仅匹配当前任务分支的 `push` 触发为完整可用入口，根包在该次提交的验收 workflow 中明确选择；调整候选或修复后推送相应诊断提交，偶发失败可重跑原任务，不要求创建空提交。GitHub 的 [push 规则](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#push)支持尚未合入默认分支的 workflow，不为注册验收入口提前合入主分支。

`workflow_dispatch` 仅在当前仓库实际验证可用后作为指定根包的便捷入口，逐包流程不依赖它。GitHub 的 [事件规则](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_dispatch)同时包含默认分支要求和已运行 workflow 的分支 API/CLI 描述；[手动运行指南](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow#configuring-a-workflow-to-run-manually)仍要求默认分支存在该 workflow，因此不将“push 登记后必然可 dispatch”作为执行前提。各触发方式使用同一隔离发布规则，推送时机仍受当前阶段授权限制。

候选包通过任务分支的诊断提交临时启用 Darwin 后试验，不增加第二个平台资格字段或绕过解析器。验收 workflow 要求请求根全部进入有效根；被平台或 adapter 过滤必须显式失败，不能以工具专用成功代替包验证。最终只有符合交付及已验功能要求的候选保留 Darwin 声明，失败候选撤回临时声明并保存具体阻断。通用包保留通用身份，其依赖限制由闭包表达。

每个版本的结果包含获取、构建或复制、安装、导入、链接和必要运行结论，使用的 brew formula、补丁、上游限制、提交及 run/attempt 证据；同时记录与原平台基线对照的关键功能开关、实际模块和依赖探测结果。简单消费复用现有 driver；现有 smoke 未实际调用到的包，补充最小有效 API 使用，不以空 main 或仅创建目标计为运行通过。只对实际修改或自动探测影响的能力增加定向检查，不穷举所有上游功能。设备、图形界面等无法在 runner 完成的功能明确列为未验，不将可构建等同于全部功能支持。

保留现有 Mach-O 的 arm64、部署版本、动态依赖和搬迁路径要求，不把 `/opt/homebrew` 整体加入系统路径豁免。先确认对应能力属于同步基线，再判断 Homebrew 运行库的实际链接及交付需求；不能仅因自动探测到某库便开始收集其依赖。需要随包交付的非系统动态库，按具体包完成传递依赖闭包、必要 dylib 收集、install name/rpath 重定位、许可证和最低系统要求核对。处理发生在隔离安装或 staging 中，不修改 Homebrew 原文件；同一实际运行依赖已由仓库包提供时复用该包，不重复私自附带另一版本。搬迁后实际导入、链接及运行不得依赖构建机 Homebrew 前缀。

按包收集与修正必要动态库属于可交付性适配，不要求预建通用动态库收集框架。不能合法交付或无法满足系统基线时记录具体阻断；涉及改变既有功能、版本、运行依赖承诺或既定产物要求时暂停受影响包，提出最小修订。不得通过关闭既有功能、增加额外后端、升级上游、提高最低系统版本或更换编译器取得绿灯。Darwin 显式关闭已确认不属于同步基线的额外探测能力，依照前述同步规则处理。

RealSense 和 ONNX Runtime 也列入调查与验收，不能仅因当前无 Darwin 声明跳过。RealSense 区分 SDK 构建消费与实际相机功能；ONNX Runtime 核对当前版本官方 Darwin ARM64 产物、来源摘要及运行能力，不混入 Intel 包，也不无依据改为另一套预编译来源。FLANN 通过后，现有 `example/` 必须使用本轮产物完成导入、编译、链接和运行；验证可预置本轮入口及包，避免改写示例的业务代码或依赖旧远端产物。

### 实施范围与数据保护

第一阶段统一字段、解析校验、全部配方格式、JSON 消费者、每包 publisher、通用包已知二进制载荷检查、Eigen adapter、brew 环境准备及独立验收接线，补齐字段参考和关联文档。保留无实际 Darwin 证据的具体包支持范围；候选在第二阶段逐个验证。第一阶段以同一规则可从本地和 CI 执行、真实 Eigen 安装及定向消费和原有轻量 Darwin 路径不受损作为独立验收结果，不声称全部库已适配。

第一阶段及其验收修复以 `49df3fc` 为已保留基线，不重复执行。第二阶段在 `task/darwin-aarch64-sync` 完成全部现有版本和 Asio 1.24.0 的 macos-15 调查、最小同步适配、逐包证据和最终支持结论，以及 Linux Eigen 同归档跨平台消费和 example 验证。范围内的发布接线复用既有 `build-platform.yml`、`all` 选择、运输和发布校验；独立验收通过不等于 Gitee 已发布。支持失败必须给出实际原因；可修复且在范围内的问题完成修复，需改变已确认约束的问题交回人工决定，未验项不能伪装为不支持或已支持。

最终逐版本结果表追加到本记录的验证结果章节，随阶段交付受 Git 跟踪，保存已验证、经确认的阻断及未验覆盖缺口，引用对应源码提交、run/attempt 和产物摘要。关键原因、影响及覆盖缺口写入正文，不能只链接可能过期的 CI 日志。影响未来支持判断的长期阻断及重新尝试条件由本记录或已有库接入 Agent Note 保存并交叉链接；撤回临时 Darwin 声明时同步保存结论。结果表是具名版本的验收快照，不作为平台选择输入，也不要求随每次 CI 更新，不增加第二份长期状态数据库。

验证按实际影响增量执行，复用已有效证据，不重复未变化构建或增加无关 Linux/Windows 实机、全量 Go suite/vet、最低 CMake 矩阵。全部目标包的首次 macOS 验证和同一 Linux Eigen 归档的必要跨平台验证属于本轮范围，不因增量原则省略。

已有源码、安装、缓存和远端资产保持原有数据保护规则。新身份使用隔离任务根；旧安装身份不符时拒绝，不自动重标、覆盖或清空。新规范面向未来声明及本轮迁移，不迁移历史 CI JSON、已发布归档或远端元数据，不增加旧字段兼容和协议协商。

逐包阶段使用独立验收，正式发布使用既有四平台生产及 Gitee 发布入口。用户追加的收口范围包含完整产物发布和本任务临时分叉清理；发布目标为 `xxf1ow/autolib-release` 的 `autolib-release` tag。发布只更新本轮明确清单中的资产，保留其他远端数据和已有 latest；不强推或把 master 合入开发分支。临时分支引用在删除前保存精确 SHA 和可恢复 Git bundle，正式开发分支及 master 保留。源码、安装、缓存和调查文件不随 Git 分支清理而删除。

### 关联决定与文档归属

本记录部分替代[基础配方设计](2026-10-19-toml-externalproject-zlib.md)、[依赖接线设计](2026-10-20-flann-opencv-dependencies.md)、[模块扩展设计](../../proposed/architecture/2026-10-20-module-expansion.md)和[分批迁移设计](../../proposed/architecture/2026-10-28-batch-module-migration.md)中的字段表达，替代 [CI 系统设计](2026-10-20-ci-system.md)的完整配置镜像与全局 publisher，并扩展[直接消费设计](2026-10-25-direct-install-consumption.md)的通用包资格和 [Darwin 原生设计](2026-11-05-darwin-aarch64-native-build-and-ci.md)的包覆盖范围。

这些记录仍拥有原生执行、依赖接线、直接消费、数据保护及既有验证理由，属于部分替代，保持 active 并交叉链接，不归档或改写为相反决定。维护时同步更新仍由它们拥有的实现事实，字段细目统一指向字段参考。

## 备选方案

**保留格式，只逐处补文档和校验。** 改动较少，但身份重复、平台多层语法、命令多形状和独立的配置镜像继续造成解释分散。选择统一职责并直接迁移现有配方。

**引入独立 schema 或插件系统。** 可以集中声明字段类型，但不能解决安装、消费及发布中的所有权，也增加维护实体。采用现有 builder 内共享解析和针对性校验。

**保留两个平台字段或条件必填布尔值。** 仍需要解释无效组合及同一通用包的不同资格；单一 `package_platforms` 直接表达可提供的产物身份，不新增共享归档但限制部分目标消费的机制。

**将 Eigen 改为 copy，或修改 PCL 以绕过 config。** 可以构造另一路径，但安装工具不决定平台属性，删除查找元数据会转移兼容维护责任。保留上游 CMake 安装，复用导出目标及 PCL 的既有查找。

**继续全局 publisher 或失败接管。** 各通用包有效平台集合可以不同；逐包在既有候选中选择即可。失败接管会改变同次交付和摘要来源，缺少需求，不增加。

**全部在本机验证，或直接运行现有发布 workflow。** 本机系统版本不能证明 macOS 15；现有成功运行会自动进入 Gitee 发布。选择独立 macos-15 验收入口，复用生产构建执行而隔离正式发布。

**从 `05ad429` 重做全部实现。** 可以避开第二阶段改动，但也丢弃已批准并完成验收的字段、通用包和执行基础，造成重复工作。选择从含第一阶段收口修复的 `49df3fc` 分叉，只重做受影响的第二阶段。

**整体沿用旧分支，或为了对齐而修改其他平台。** 旧分支包含 macOS 独有的增强和与其相关的交付处理，整体保留会继续携带范围偏离；调整 Windows/Linux 又改变了任务对象。选择冻结原平台配置，按同步基线筛选 Darwin 适配和旧证据。

## 验证约束

1. 全部现有版本配方使用同一字段契约，旧身份及平台字段、旧平台层级和一维 COMMAND 均拒绝；实际配方可完整解析、描述并生成，未选分支的坏语法不能隐藏。
2. 定向正负例覆盖依赖固定、平台及依赖过滤、有效覆盖、来源互斥及提交格式、无效类型字段、路径及占位符、保留参数、步骤关系和目录验证。两种安装类型均拒绝通用包中的已知目标二进制载荷；合法头文件和可重定位元数据仍可交付。断言保护真实输入效果，不只检查内部结构。
3. 本地生成、describe、CI、安装身份及发布准备使用一致的包身份和确定版本依赖；异构有效平台集合的通用包各有唯一正确 publisher，错误来源、重复、缺失和失败不接管均可观察。
4. brew 的选择、安装和记录由当前有效闭包及已确认同步能力决定；原 apt/choco 声明不变。仓库已有库使用仓库版本，其余必要库可使用声明的系统包。独立验收 CI 可通过限定任务分支的 push 完成逐包试验，不依赖未验证的 dispatch，不被 Gitee 正式发布监听；过滤候选不能形成包成功结果。需要随包交付的 Homebrew 动态依赖完成闭包、重定位及搬迁运行验证。
5. 两个 Eigen 版本保留头文件与查找元数据，通过 config 目标完成真实安装、直接及搬迁消费；同一 Linux 归档在 Windows/macOS 使用，有 PCL 组合的实际消费证据，不能回退系统 Eigen。
6. 基线 31 份配方及追加的 Asio 1.24.0、ViSP 3.7.0 均有可适用于本分支的 macOS 15 实际结果或经人工确认的具名阻断及覆盖缺口。原平台配置与 `49df3fc` 的差异须属于本记录已明确批准的具名例外。受影响的可选后端、模块和依赖结果与确认基线一致，不能只验证编译及搬迁。补丁、系统依赖、导入运行及功能限制有可追溯且受 Git 跟踪的结果记录，CI 日志过期后仍能理解关键结论、长期阻断及重试条件；支持列表与最终结论一致。example 完成实际消费，硬件未验不计为全功能成功。
7. 字段参考、架构、开发和测试文档、接入技能及关联记录同步，无相互冲突的现行字段标准；新增 Darwin 包在既有生产计划的 `all` 和运输、发布准备中保持正确资格、身份和唯一性，不修改原平台功能配置。完成受影响检查及文档验证，保留既有数据且无未授权远端发布。

## 影响

字段与内部 JSON 必须成套切换，部分迁移会导致错误身份、依赖漂移或发布缺失。旧安装不会因新配方自动有效，需要隔离任务根；不以兼容层或破坏性清理掩盖问题。

`any-any` 是对交付物的维护者声明，不保证所有编译器、语言标准或外部后端均可用。元数据可能携带生产路径和版本检查，只有实际同归档跨平台消费能证明所需场景；不能以各平台独立安装代替。

Homebrew 及其传递依赖可改变可选功能探测、动态链接及最低运行要求。显式对照受影响能力，阻止预装库导致的额外功能；没有原平台证据时先保留判断缺口，不以补齐所有依赖来消除不确定性。保持 Apple Clang 和部署基线，不以放宽产物路径检查替代可交付性。

第三方固定版本可能存在上游 macOS 限制，设备和图形功能也可能缺少执行条件。本任务要求完成调查及范围内最小修复，不预先保证所有包都能同步既有能力；具名阻断需要证据及人工确认，不能以过滤、关闭既有功能或换版本结束试验。


## 验证结果

### 第一阶段：统一配方与执行基础

本阶段实现以 `9428eb9` 的批准设计为依据，在 `task/darwin-aarch64-native-ci` 追加本地提交；身份与平台、严格字段、执行所有权、通用 CMake 包、逐版本 publisher 及 brew/隔离验收入口分别由 `8532611`、`1d1a090`/`33b6588`、`3210413`、`1df1964`、`f787aa1`、`193d207` 实现。第一阶段只交付执行基础；完整 macos-15 包验收、同归档跨平台消费和 example 结果由下文第二阶段证据说明。

全部 31 份配方通过 `TestRealModuleDeclarations` 的 Windows x86_64、Linux x86_64/aarch64 和 Darwin aarch64 局部读取、有效描述与生成检查，没有取源或全库编译。前三目标各有 31 个有效根，Darwin 有 9 个；websocketpp 虽为通用载荷，仍因 boost 的具体平台资格被过滤。PCL 的五个精确依赖保持不变，Eigen 固定为 3.4.0。`TestDarwinEntryCompiler` 通过真实根与子项目确认 AppleClang、arm64 和 15.0 参数。

字段、来源、依赖、覆盖、路径和参数所有权均有定向正负例；真实 argv、依赖接线与默认值夹具通过。两种安装类型的通用二进制内容拒绝、合法 config 目标及 staging 数据保护通过。每包 publisher 的异构平台集合、失败不接管和发布准备在 Go/Python 两端通过；真实轻量归档经运输及受控 HTTPS 入口消费通过。brew 选择、安装、版本记录及公式前缀传递由受控执行验证；独立 workflow 的过滤拒绝、执行接线与 Gitee 监听隔离通过。Chocolatey 名称规则完成静态核对，本机无 PowerShell，未执行该脚本。

真实生产与消费位于 `build/recipe-contract-stage1-WWVGJ7/`，使用本机 macOS 27.2、Apple Clang、CMake 3.31.12、GNU Make 4.4.1，目标固定 arm64/15.0。普通根一次选择 `eigen@3.4.0,eigen@5.0.1,queue@45db9a8b4431,lz4@1.10.0` 完成构建。复用的 Git 来源取源前只读核对 HEAD 与干净状态：lz4 为 `ebb370ca83af193212df4dcbadcc5d87bc0de2f0`，queue 两资源为 `6867b56452352acf077fccd5f6cc7e3a8cfde0fb` 和 `683b9e31ea15eb69f1b81cc1defc7850d5f20b71`；Eigen 归档按各配方 SHA256 校验。没有重编无关模块或清理旧安装。

两版 Eigen 的上游 pkg-config 模板将生产前缀写成绝对路径，真实检查先失败。版本目录内的局部补丁将 prefix 改为相对 `pcfiledir` 的本包前缀，保留上游 CMake 安装、Config/pkg-config 和全部功能选项；仅重建本任务 Eigen 状态，原结果留备份。补丁后 `.cmake/.pc` 无任务源码、build 或安装绝对路径，pkg-config 在原根及搬迁根均报告对应版本和正确 include。安装身份为 any/any，5.0.1 的语言要求来自上游目标。

`tests/autolib/run-installed.cmake` 对两个独立 Eigen 消费者及 queue/lz4 组合，在原安装和复制搬迁安装各执行一次 configure/build/CTest，六次均为 1/1 通过，Eigen 分别报告 3.4.0、5.0.1。搬迁组运行时隐藏本任务原安装根，结束后还原。生产、增量 Eigen 重建及消费日志分别为任务根下 `production.log`、`eigen-rebuild.log`、`consumption.log`，逐消费者证据位于 `consumers/install/`、`consumers/relocated/`。

独立总审查在 `05ad429..e54dbac` 发现一项平台过滤顺序缺陷：仅在 Windows 覆盖声明复制映射的 copy 包，会提前阻断 Darwin 自动发现。`TestPlatformSelectionCopyMappings` 先复现失败，再将非空映射完整性限制为有效目标检查；Windows 映射读取、支持目标缺映射拒绝及未选分支坏字段拒绝均通过。受影响的字段、过滤、describe 与全配方检查六项通过，未重跑未失效的真实库消费。审查无其他已证实 Critical/Minor；托管环境、跨平台同归档、PCL/example 和 PowerShell 执行仍为下节缺口。

第一阶段验收修复由 `977e2ca` 与 `49df3fc` 收口；后者检查全部本地安装的元数据并共享字段解析，不再有待提交的十文件修复。本分支直接保留该结果，不重做第一阶段。

### 旧分支调查与证据复用

旧分支的[完整第二阶段记录](https://github.com/xxf1ow/xxautolib/blob/b0e30938c1830fcfa9ddc7015799330e28f8bf43/docs/agent-notes/proposed/architecture/2026-11-06-module-recipe-contract-and-macos-validation.md#第二阶段macos-逐包验收)保存逐版本 run、归档摘要、成功场景和具名失败；本地原始证据位于 `build/stage2-runs/`，版本调查与发布包核对位于 `build/header-dependency-audit/`、`build/asio-124-validation/` 和 `build/libzmq-platform-audit/`。这些记录属于旧提交，未作为本分支新增支持声明或验收通过的证明。

复用前逐项比对实际源码、功能配置、补丁、依赖、工具链和消费者输入。输入等价且未受本次修订影响的结果可以复用，不因换分支重编；有变化时只使相应能力、包及必要下游证据失效。固定上游行为、失败根因和已确认的原平台配置可以直接作为调查依据；旧源码提交和旧归档摘要始终保留，不重标为新提交。不能确认等价性时列为待核验，不将全部旧结果一律通过或一律重跑。

| 对象 | 已有事实与证据 | 本分支的处理边界 |
|---|---|---|
| Boost 1.87.0 | [原三平台 run 42272676188](https://github.com/xxf1ow/xxautolib/actions/runs/42272676188) 的 Iostreams 均为 ZLIB ON、BZip2/LZMA/Zstd OFF；[Darwin run 42341486257](https://github.com/xxf1ow/xxautolib/actions/runs/42341486257) 四项均 ON | 这是已证实的功能差异。Darwin 对齐原平台只启用 ZLIB，不带入额外压缩后端的 brew zstd 和相关交付补丁；修改配置后的旧 Boost/PCL 成功结果不能直接证明新闭包通过 |
| FLANN 与 PCL | 同上两个 run 的 PCL 构建子系统清单一致，均为 19 项。Gitee 三个 FLANN 包中，两种 Linux 的 C 库含 GOMP 符号，Windows 的 C 库含 VCOMP 符号；[Darwin FLANN/example run 42335992231](https://github.com/xxf1ow/xxautolib/actions/runs/42335992231) 验过 OpenMP、搬迁和原样 example | OpenMP 是既有能力；仅筛选必要的 Darwin libomp、导出和搬迁补丁。PCL 的混合依赖选项重新判断，不能因旧 Boost 增强而默认带入；不修改原平台 OpenMP 或模块配置 |
| Taskflow | 旧分支 `9757ac1` 增加全平台 `cxx_std_20`，不是 Darwin 条件设置 | 不自动复用这项全平台要求变更；核对既有契约，需要改变原平台要求时单独报告 |
| libzmq 4.3.5 | [run 42260213354](https://github.com/xxf1ow/xxautolib/actions/runs/42260213354) 构建和进程内收发通过，交付检查拒绝 Homebrew GnuTLS 绝对路径；固定上游 Git 来源默认启用 draft/WS，并在找到 GnuTLS 时启用 WSS | 先确认原平台 WSS 状态，不先补 GnuTLS 闭包。已核对的 Gitee release 没有 libzmq 资产，不能据此推断 WSS 关闭；该版本能力查询参数为大写 `zmq_has("WSS")`。基线证据不足时保持待确认 |
| FreeType、HarfBuzz、OpenCV | 旧试编中 FreeType 探测 Homebrew HarfBuzz，与仓库 HarfBuzz→FreeType 依赖形成循环；相关链未完成验收 | 先核对原平台真正启用的能力，循环仅是调查线索，不把自动探测到的全部后端提升为交付要求。不为解循环变更原平台配方，不静默裁掉原能力 |
| WebSocket++ 与 Asio | [单包 run 42345772457](https://github.com/xxf1ow/xxautolib/actions/runs/42345772457) 验过独立安装；[组合 run 42348042330](https://github.com/xxf1ow/xxautolib/actions/runs/42348042330) 验过 Asio 1.24.0、standalone 客户端和搬迁，四次 CTest 通过 | 按已单独批准的头文件包修复及版本补充恢复；原 Asio 1.38.2 与旧 WebSocket++ 的 API 不兼容仍是具名限制，不升级或修改其 API |
| Eigen 同归档消费 | [Linux producer 42264057220](https://github.com/xxf1ow/xxautolib/actions/runs/42264057220) 生产两版原始归档，Windows/macOS 独立消费及搬迁通过；原三平台 PCL 组合见 run 42272676188 | 核对生产来源、原始摘要和输入等价后复用。Darwin PCL 因 Boost 闭包修订只补受影响组合，不重做原三平台 PCL 或重新生产未变化的 Eigen |
| 其他逐包调查 | 旧分支记录 28/32 份版本配方的基础交付成功；RealSense 包含 SDK/API 及搬迁，不含真实相机取流 | 逐包核对输入和覆盖后决定复用；包构建成功、设备能力与本分支支持声明分别判断，不整体移植旧白名单 |

Gitee FLANN 原包的 SHA256 分别为：Linux x86_64 `890781eff8f8d14df56630b6e3212359dee6fc15062bee40cf1c2f422461c358`、Linux aarch64 `4a081fb344d21452fd7b09c956461441c5a5f4a22b1eba1b8ee70df367afa53c`、Windows x86_64 `c71a1d25e1889500103d4f73b75f981594b3d90035ad32bceb208c5202a7ad42`。检查是归档和二进制符号核对，没有在 macOS 上执行 ELF/PE 产物；能力及原生消费的覆盖不能由字符串检查替代。详细附件列表、检查摘要及 Boost/PCL 日志路径保存在 `build/libzmq-platform-audit/audit.json`。

### 新分支第二阶段验证

`task/darwin-aarch64-sync` 从 `49df3fc` 保留第一阶段，在 `639eae4` 提交修订设计。原平台配方对照核验覆盖全部 31 份：移除 Darwin 专用声明后，源码、公共及原平台 cache、依赖和 apt/choco 均保持基线，仅 WebSocket++ 按批准删除 Boost 依赖。原 package.toml/latest 全部不变；Asio 1.24.0 独立追加。Taskflow adapter 与基线逐字节一致，C++20 由 smoke 应用显式选择，不传播为新的全平台包要求。

独立入口提交 `424ac651d39c367050c6df9c802181723a960981` 的 [run 42353199898，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42353199898) 通过 queue 真实生产、导入、API 消费、打包运输和汇总；runner 为 macOS 15.8.2/arm64、Apple Clang 17.0.0，CMake 3.31.12，部署目标 15.0。目录使用作业序号，不把精确根或逗号列表放入工作路径；实际 Gitee 监听和发布入口均未运行。

#### 输入等价的旧证据

下表 23 个版本逐一核对到原始结果中的源码提交，其完整版本目录（配方、adapter、补丁及专用脚本）与新分支逐字节一致。对应的构建工具、功能配置、依赖及消费 API 未因本次修订改变，因此复用原 macos-15 交付及搬迁证据，不重新编译；归档摘要和 run 始终指向原生产提交。SDL3 与 ImGui 的旧结果因未闭合的 libusb 后端不作为支持依据，另见阻断记录。两个 Eigen 的 Linux 原件跨平台证据另见下节，不能用此处 Darwin 归档替代。源码完整提交与消费记录可由具名 run 的结果身份解析，关键包限制保留在正文。

| 精确版本 | 原生产提交 / run（attempt 1） | 原始归档 SHA256 |
|---|---|---|
| `asio@1.24.0` | `22d38a4a751e35cecd9b1d81432410e20caacdc7` / [run 42348042330](https://github.com/xxf1ow/xxautolib/actions/runs/42348042330) | `faa545790c7debfdb2c415eef77270ad960ef507472b2984a43d3880109975bc` |
| `asio@8806a6803cde` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `331bb64fdb8973a4efba04a2bfbfaf005e6aaf06384c02f6eeaf2a965e05d827` |
| `clipper2@1.5.4` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `a295b1c3cba844db372056be276391010d43611bfa2dc4c25e64fe0fb0932f19` |
| `cpp-httplib@88956ccad838` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `6a132b0d77acc8f4e0336baebbfd0dcb90291bbe8096eef870647cbda361a056` |
| `eigen@3.4.0` | `c8eec1ec11a8464cd8af03a9dd953bcb35740eec` / [run 42260213354](https://github.com/xxf1ow/xxautolib/actions/runs/42260213354) | `d596afb529434130ff3c2e22ff5f406ca10e70b59d841e72987b548a9bc58170` |
| `eigen@5.0.1` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `b964eadcd30e8b241f02dfb8625d830acc29a2990c1f3bb092a34504654ae1b3` |
| `expected@1.3.1` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `71abad84a720aa862e0d91ebf4f098e89d37ac952716a6da60327933efcac7a7` |
| `flann@f9caaf609d8b` | `a8ea348a89e1ace7a6cc425b3603d8a629751ae4` / [run 42335992231](https://github.com/xxf1ow/xxautolib/actions/runs/42335992231) | `c817fbb1736a5f4e8d2bd0a1138d6d3a36d4e6987378f8af4c4d302a622ee9ca` |
| `gtest@1.17.0` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `86b965d2374257f1c96838b7cfbd332329074f8b01c70535f8af13f2bac851bd` |
| `librealsense@2.58.1` | `04842df170604bbd1f2d60725524bdd817334ea4` / [run 42263227882](https://github.com/xxf1ow/xxautolib/actions/runs/42263227882) | `ace2fae72212280fad3aaadfdb8cf8d85b1e0937b7961fc65c93fd659b63d310` |
| `libusb@1.0.27` | `d07d8c06dc930ac2cb897e22bdf4e6df87579749` / [run 42257137133](https://github.com/xxf1ow/xxautolib/actions/runs/42257137133) | `0b131d2c44727f966e1edacc6b209b05a2a5c8714cd8b115fe0db6dcf5e7998a` |
| `lz4@1.10.0` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `b32d744bf837353f66d2f9f580bf69edf091c73f70354910db4a21a8df8beeb2` |
| `nativefiledialog-extended@3cd252a8f7ca` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `efcadeb69ce4b804423ca60f6ef32e2420a9f43ae66f9d0977c38db8afeb439c` |
| `nlohmann@3.12.0` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `4f24dc13816ab35c303143a92e21ea171647de043f2929695cfc5578347752a8` |
| `oneTBB@628c90df6867` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `61abcbc72be90bd3486d5ed114291993c874cd1b83e6e604e3884a0b3ca272de` |
| `onnxruntime@1.24.4` | `c8eec1ec11a8464cd8af03a9dd953bcb35740eec` / [run 42260213354](https://github.com/xxf1ow/xxautolib/actions/runs/42260213354) | `1a328c640b48bb5005cfbeb25adfe332b2f0bb4cb0be750f7f968fa561165030` |
| `portaudio@v190700_20210406` | `c8eec1ec11a8464cd8af03a9dd953bcb35740eec` / [run 42260213354](https://github.com/xxf1ow/xxautolib/actions/runs/42260213354) | `ec0c7881e25626dd4711dff243307cd724c84484f7954f93478da33e1c782c95` |
| `pybind11@2.13.6` | `d07d8c06dc930ac2cb897e22bdf4e6df87579749` / [run 42257137133](https://github.com/xxf1ow/xxautolib/actions/runs/42257137133) | `eb4dbd606cdd9d4c88c48b593e544efd48c9fb0607d752676dfea82ee326859c` |
| `queue@45db9a8b4431` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `e7d91ecb0a6645dd99eea04b0a181d9d0930722c495dd216bd3c88ffd512a7d5` |
| `spdlog@1.17.0` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `edfe516678412688dc90415a7a1ba6ce14aeced1e51e87cebe0505d8c24b1153` |
| `websocketpp@4dfe1be74e68` | `13214746bfe092cc4ceb631847ca31272d1324f8` / [run 42345772457](https://github.com/xxf1ow/xxautolib/actions/runs/42345772457) | `354e608fa0127eb75b50099beff465e7975de61287aa89c89364f521cd4ab359` |
| `yaml-cpp@2decf96e915d` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `61674b68034ae2a917e4622841fd59954ab25a64f6dc35f93f4f3acabdb723c3` |
| `zlib@1.3.1` | `03d5f5afd5c080f5bd5106bc4083906b68a2318d` / [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) | `f5d86230d8abb59f97fff177dd351df89261ffaccef5957ead480e0c33bc05a1` |

FLANN 保留仓库 lz4 和已证实的 OpenMP，Darwin 仅声明 libomp 并安装其运行库、公开编译要求与许可证；不使用系统 lz4，也不改 Homebrew 原文件。原样 example 的源码和 driver 与 [run 42335992231](https://github.com/xxf1ow/xxautolib/actions/runs/42335992231) 的输入一致，复用其 queue/lz4/FLANN 导入、编译、链接及运行和搬迁结果；未修改用户 example/3rdparty。WebSocket++ 的单包 iostream 与 Asio 1.24.0 standalone 组合仍分别引用 run 42345772457 和 42348042330，旧 Asio 1.38.2 API 兼容限制不因此消失。

#### 受影响的补验与既有发布准备

`7da790a` 的 Darwin 适配不包含旧 Boost 的 zstd 补丁、brew 增强或 Taskflow adapter C++20 变更。补验提交 `0a3cd6c1a0e443358a136e4b7a2b08bd12826e35` 的 [run 42354393503，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42354393503) 全部通过。实际 Boost 摘要为 ZLIB ON、BZip2/LZMA/Zstd OFF；PCL 构建子系统清单与原平台的 19 项逐项一致，保留五个精确依赖及 Eigen 3.4.0 Config。没有加入混合静动态依赖开关。直接、搬迁及同归档组合共七次 API 消费均通过。

该 run 的 Taskflow 原始归档为 `taskflow-83f90a279aaa-any-any.tar.zst`，SHA256 为 `5bbcd7c7cae5ea6787ff402d71f84fc1b23d9736e03353b9cc5af709bb6b941e`；已下载运输包逐项核对摘要，直接及搬迁 API 测试各为 1/1 通过。

同次 Boost 原始归档 `boost-1.87.0-darwin-aarch64.tar.zst` 的 SHA256 为 `8c7f6632b806bc662a91eb57ca0757c94d94434a711a03a998355a3416c0f598`；PCL 原始归档 `pcl-pcl-1.15.1-darwin-aarch64.tar.zst` 为 `991ed0d16c4c3ebedc6783e1a64530ed3743945fd5576d59abdaf25e5fef50dc`。两者均已从该 run 的运输包下载并核对摘要，完整结果保存在 `build/darwin-sync-runs/42354393503-attempt1/`。

阶段审查时，真实 `autolib-ci plan --platforms all --modules all` 输出原三平台各 32 根、Darwin 26 根；当时 Darwin 过滤项恰为下节六个阻断版本。26 个有效版本由上述 23 份可复用结果及 Boost、PCL、Taskflow 的新结果覆盖。10 个通用精确版本均仅由 Linux x86_64 发布，Eigen 与新增 Asio 的身份保持 any-any。既有构建、运输、唯一 publisher 和发布准备协议没有变化；独立验收不表示已正式发布到 Gitee。

新增及受影响检查已有 RED→GREEN：缺 smoke 拒绝、精确根及证据隔离、Boost 能力摘要匹配、库链接输入排除 imported executable、example 缺输入保护、同归档摘要及身份预检。当前定向结果为 workflow 9/9、消费入口 5/5、Eigen 预检 5/5，以及实际配方读取/生成、版本/latest 与 RealSense 依赖接线检查通过；没有全量 Go test/vet 或原平台全包重编。

#### 阶段审查时的六个阻断版本

阶段审查时，用户确认以下六个版本暂列阻断，当时保留原平台声明并撤回 SDL3、ImGui 的新增 Darwin 声明。下表保留该次阻断原因和重试条件；六个版本均已在下文专项验收中解除阻断。当时的阻断属于仓库交付缺口，不表示上游全面不支持 macOS。

| 精确版本 | 原因与证据 | 重新尝试条件 |
|---|---|---|
| `freetype@2.13.2` | [原平台验证记录](2026-10-20-flann-opencv-dependencies.md#验证结果)中，Windows 无额外 private 依赖，Linux ARM 启用系统 zlib/PNG，原平台实际能力已有差异；旧 Darwin 试编还探测系统 HarfBuzz | 按配方确定的单向集成、仓库 zlib 接线及三个可选字体格式依赖统一关闭的规则验收 |
| `harfbuzz@8.5.0` | 固定依赖仓库 FreeType，后者 Darwin 基线尚未确定 | FreeType 阻断解除后按既定 HB_HAVE_FREETYPE=ON 验收 |
| `opencv@4.14.0` | 既有 freetype 模块依赖上述 FreeType/HarfBuzz；不能关闭模块取得成功 | 两个字体库完成同步后，保留既有模块与后端要求验证 |
| `libzmq@4.3.5` | 固定 Git 来源的 WSS 由 GnuTLS 自动探测；阶段审查时缺少原三平台 WSS 状态证据，Gitee 也没有该包资产。旧 Darwin 构建/进程内消息通过，但 GnuTLS 绝对路径未满足交付要求 | 取得原平台配置或成品能力证据（查询使用 `zmq_has("WSS")`），或用户明确决定 Darwin WSS 目标后再处理必要交付 |
| `sdl3@3.2.30` | [run 42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424) 探测 Homebrew libusb 1.0.30，启用 SDL_HIDAPI_LIBUSB/SHARED；静态归档含 libusb 后端及 `libusb-1.0.dylib` 加载名，却未交付运行库或声明仓库 libusb 1.0.27。surface smoke 不覆盖这条设备路径；原三平台补查均未启用 libusb 后端，详见下节 | 关闭基线以外的 libusb 后端，保留原生 HID 能力，再完成构建、消费和搬迁验收；结果见下文专项验收 |
| `imgui@065f9e02695c` | 固定依赖仓库 SDL3；旧基础消费通过不能解除 SDL3 的依赖交付缺口 | SDL3 完成能力同步与交付后，保留既有 SDL3/OpenGL3 后端验证 |

SDL3 调查归档来自源码 `03d5f5afd5c080f5bd5106bc4083906b68a2318d`、run 42251612424/attempt 1，SHA256 为 `116658d320c223d459df7ae2525933c8c6c8cf13a02418c57239def33d226097`；ImGui 来自 `c8eec1ec11a8464cd8af03a9dd953bcb35740eec`、[run 42260213354/attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42260213354)，SHA256 为 `af8f3931f11087e7d357db90b295ab0351db7c50e1ccdde1d18c56357a79f453`。两份归档保留为具名调查证据，不计入本分支支持结果。

#### SDL3 原平台 libusb 后端补查

[run 42041533183，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42041533183) 的源码为 `569acb0a012acb95ca07284b75c7d70516234711`。逐项解析 TOML、归一名称/版本字段迁移及 Windows 平台键后，该提交、冻结基线 `49df3fc` 与显式固定开关前的 `44df867` 的 SDL3 配方语义相同：来源提交、静态形态、cache、依赖及 apt 声明一致。该运行的三平台构建日志均显示未找到 LibUSB，SDL3 编译成功；结果 JSON 均记录该包 `built=true`。

| 平台 | `SDL_HIDAPI` / `SDL_HIDAPI_JOYSTICK` | `SDL_HIDAPI_LIBUSB` / `SDL_HIDAPI_LIBUSB_SHARED` | 构建日志行号 |
|---|---|---|---|
| Windows x86_64 | ON / ON | OFF / OFF | 100、151–154 |
| Linux x86_64 | ON / ON | OFF / OFF | 495、564–567 |
| Linux aarch64 | ON / ON | OFF / OFF | 479、548–551 |

各平台日志位于对应 `ci-evidence-42041533183-1-<平台>` artifact 的 `logs/layer0/19/build.log`。该运行整体失败：Linux x86_64 完成 SDL3 消费验证，Windows 因 HarfBuzz 消费失败、Linux ARM64 因后续构建失败而未完成 SDL3 消费验证。历史 Linux 使用 GCC 9.4，当前冻结基线为 GCC 10.5；这些日志用于确认既有后端状态，不代替当前工具链的完整验收或真实 USB 设备测试。

补查时 Gitee 没有 SDL3 发布资产；已有日志足以确定原三平台的 libusb 后端均未启用，因此未新触发 CI。原始计划、结果、诊断及日志摘要保存在 `build/stage2-sdl-audit/`，`audit.json` 记录各平台日志 SHA256 和具体行号。此证据确定了关闭额外 libusb 后端、保留 HIDAPI 及 joystick 的同步目标，不需要为 SDL3 新增仓库 libusb 依赖。随后按用户决定，在公共 cache 固定关闭该后端，原三平台其他配置不变；Darwin 专项结果见下节。

#### SDL3 与 ImGui 专项验收

公共 cache 显式设置 `SDL_HIDAPI_LIBUSB=false`，覆盖 Windows x86_64、Linux x86_64/aarch64 和 Darwin ARM64。两个包恢复 `darwin-aarch64` 支持声明，其余源码、cache、依赖、apt 和 adapter 均不变。真实 builder 四平台生成结果均包含 `-DSDL_HIDAPI_LIBUSB:BOOL=OFF`；原三平台复用上述已关闭后端的编译证据，不重复编译。

独立测试快照 `93b9b2229639a3b194ecbbf33963f35d3bb78a94` 的 [run 42360397659，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42360397659) 全部通过。测试入口只选择 SDL3 和 ImGui，使用相同的仓库生产及消费流程；未触发 Gitee 发布。正式分支的两份配方与该快照逐字节一致，测试快照身份与成品摘要保留原值。

runner 为 macOS 15.8.2/arm64、Apple Clang 17.0.0，CMake 3.31.12，部署目标 15.0。SDL3 实际摘要为 HIDAPI/joystick ON、LIBUSB/SHARED OFF；原始归档解出后的静态库没有 `LIBUSB_hid_*` 符号或 `libusb-1.0.dylib` 加载名。ImGui 静态库包含 `ImGui_ImplSDL3_InitForOpenGL` 与 `ImGui_ImplOpenGL3_Init` 的定义，SDL3/OpenGL3 后端仍被编译。两个独立根的直接 API 消费各 1/1 通过，同归档搬迁后的组合消费 1/1 通过；原安装在搬迁消费期间被移走。覆盖限于 SDL surface 和 ImGui context/颜色转换，未验证交互窗口、OpenGL 上下文或真实 HID/USB 设备。

| 原始归档 | SHA256 |
|---|---|
| `sdl3-3.2.30-darwin-aarch64.tar.zst` | `742d9835ffa4d4effb147e03d0def07a3762fb995df4741a320988ff21eb9143` |
| `imgui-065f9e02695c-darwin-aarch64.tar.zst` | `106c55764e54c49647bbdcc214c7add39d646fb5b70bebee4768b18ff9f6d9f8` |

已下载运输包并逐项核对归档摘要、run/attempt、源码身份、两根 `verified=true` 及完整结果。原始日志、结果和成品保存在 `build/sdl3-imgui-acceptance/42360397659-attempt1/`。该次真实 `all` 计划保留原三平台各 32 根，Darwin 为 28 根；当时只过滤 FreeType、HarfBuzz、OpenCV 和 libzmq 四个版本。SDL3、ImGui 的后端交付阻断解除，原三平台的唯一显式配置调整是已批准的 libusb 后端关闭值。

#### libzmq 原平台补查与专项验收

[run 42041533183，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42041533183) 的固定源码、libzmq 功能配方及依赖声明与 `49df3fc` 和显式固定开关前的 `039e800` 一致。三平台 `ci-evidence-42041533183-1-<平台>` artifact 的 `logs/layer0/10/build.log` 均报告未找到 GnuTLS、`No WSS support`：Windows 位于 98、100 行，Linux x86_64/aarch64 均位于 82、84 行。日志确认原平台未启用 WSS，不用缺少发布包推断功能。历史 Linux 工具链的 GCC 9.4 与当前 10.5 差异仍按上述限制保留。

公共 cache 显式设置 `WITH_TLS=false`，四平台生成结果均包含 `-DWITH_TLS:BOOL=OFF`；只新增 Darwin 支持声明，其他来源、功能 cache、静动态形态、系统依赖及 adapter 不变。固定上游的 `WITH_TLS` 仅控制 WS 下的 GnuTLS/WSS 接线，保留 `ENABLE_WS` 和其他传输配置；不增加 brew 依赖。原三平台复用已关闭 WSS 的历史证据，不重新编译。

独立测试快照 `5b0c1504bcaacc9537ebf6cffacd2b256c58cc3b` 的 [run 42361601058，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42361601058) 全部通过。实际环境为 macOS 15.8.2/arm64、Apple Clang 17.0.0、CMake 3.31.12，部署目标 15.0。直接安装与同归档搬迁后的动态库分别实际查询到 `zmq_has("WS")==1`、`zmq_has("WSS")==0`；两份 `otool -L` 仅包含自身 `@rpath` ID、系统 libc++ 和 libSystem，没有 GnuTLS 或 Homebrew 依赖。直接及搬迁后的 inproc 消息收发测试各 1/1 通过，搬迁测试期间原安装已移走。WS 查询证明该传输被编入，未验证网络 WS 握手。

原始归档 `libzmq-4.3.5-darwin-aarch64.tar.zst` 的 SHA256 为 `0680d4772d7a910914281485907b4e12f7d8beefc1bc5dd5ed86d81ac07db8d0`。已下载运输包并逐项核对摘要、生产身份、`complete=true` 和独立根 `verified=true`，从下载归档再次检查动态库依赖及 WS/WSS 查询。原始结果、日志、`ws-direct.json`、`ws-relocated.json` 和成品位于 `build/libzmq-acceptance/42361601058-attempt1/`。正式分支配方与测试快照逐字节相同，未重标旧产物身份，也未触发 Gitee 发布。

libzmq 的 WSS 基线与交付阻断解除。该次真实 `all` 计划保留原三平台各 32 根，Darwin 为 29 根；当时只过滤 FreeType、HarfBuzz 和 OpenCV 三个字体依赖链版本。

#### 字体依赖链专项验收

FreeType 的单向集成和可选能力规则由配方注释拥有。OpenCV 显式设置 `BUILD_ZLIB=false`，实际图像编解码与字体链都查找仓库 zlib 1.3.1，避免 Windows/macOS 默认自建内置 1.3.2。独立字体链在 Windows 需要 HarfBuzz 配方声明 `pkgconfiglite`；zlib 的静态 pkg-config 补丁使 Windows 的 `-lzlibstatic` 与实际 `zlibstatic.lib` 一致，Unix 仍为 `-lz`。这些修正保留源码版本及库的静动态形态。

原生快照 `9053976d67a9084f0ced7e7b1eccc0efbbc08514` 的 [run 42381747989，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42381747989) 中，Linux x86_64/aarch64 两个作业均成功：四库闭包完成构建、三个独立根消费、运输及搬迁。该 run 整体取消，仅停止了尚未完成、仍使用错误 Windows 静态库元数据的 Windows 作业；不把整个 run 记为成功。两个 Linux 平台的 FreeType 均只发现仓库 zlib，其余 PNG/BZip2/Brotli 未启用。

同次 Darwin 四库均构建成功，FreeType 与 OpenCV 的实际 zlib 路径均位于仓库安装树；FreeType 自动启用 PNG、BZip2、Brotli。独立消费的 Mach-O 检查拒绝 `/opt/homebrew/opt/libpng/lib/libpng16.16.dylib`，结果为 `complete=false`。链接输入还包含 Homebrew Brotli；该次快照保留自动检测，没有豁免 Homebrew 路径。macOS 的 BZip2 来自系统 SDK，PNG 与 Brotli 来自 Homebrew；三者并非所有目标系统默认具备。公共配方统一关闭这三项可选字体格式依赖，最终结果见下文补验。Darwin 未生成合格 transport，搬迁验收未执行。

修正静态库元数据后的快照 `82a16079eab25b83aa0f43c0d67309f48de9b78c` 的 [run 42382843480，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42382843480) 仅复验 Windows。四库均构建成功，FreeType/HarfBuzz 独立根消费通过；OpenCV 的 API 检查完成后，运行库来源检查将 `cv::getTickCount` 的地址定位到消费者 EXE，未落在包目录内，导致验收失败。OpenCV 配置摘要为动态库开启；故障原因和来源探针修复见下文最小原生诊断。该平台未打包，搬迁验收未执行。

Windows 最小原生诊断的 [run 42398648019，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42398648019) 复用来源检查函数，实际构建和调用 DLL：不带 `dllimport` 的函数地址定位到消费者 EXE，已加载 DLL 的导出地址定位到 DLL，错误包目录仍被拒绝。OpenCV 4.14.0 的 Windows 消费声明不带 `dllimport`，与该机制一致。Windows OpenCV smoke 从已加载的 `opencv_core` 目标 DLL 查询 `cvGetTickCount` 导出，再保留原包目录校验；不额外加载 DLL，不修改库的静动态形态。实际 OpenCV 产物及搬迁结果见下文 Windows 验收。

初次[中文绘制测试](../../../../tests/opencv/chinese/CMakeLists.txt)的 100 像素高画布裁切了文字，278 个非零像素不构成完整中文字形验收。测试改为 180 像素高，并拒绝非零像素接触画布边缘。快照 `c1ace7addc53210ff41bb724b39929f42bd7465b` 的 [run 42384398252，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42384398252) 原样使用上述两个 Linux transport，仅编译消费者，未重新编译库。两平台均实际完成 HarfBuzz 8.5.0 整形和 OpenCV 完整“中文”绘制，得到两个非零字形、1811 个非零像素，边界检查及 CTest 各 1/1 通过。归档解压前逐项核对 SHA256，新 runner 没有生产安装可回退。Windows 作业因其原生生产验收失败而停止，未取得合格归档；截至该快照，Windows 和 Darwin 的完整画布测试未验收。

快照 `812a56aabb237d156bb73d7ae124cba41560cd09` 的 [run 42398984550，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42398984550) 中，Darwin 四库构建和完整中文绘制通过（两个字形、1811 个非零像素、边界检查通过），PNG/BZip2/Brotli 均为关闭，FreeType `.pc` 仅保留仓库 zlib。FreeType 独立根消费通过，HarfBuzz 独立根在链接时缺少 CoreText/CoreFoundation 符号：上游 `.pc` 的静态参数包含 `-framework ApplicationServices`，普通 pkg-config 导入目标未传递该项。adapter 补齐 macOS 系统 framework，不关闭 CoreText；该次未产生合格 transport，未执行搬迁。

同一 run 42398984550 的 Windows 作业成功，四库均 `built=true/verified=true`，三个独立根、运输及搬迁均通过，平台结果 `complete=true`。修正后的运行库来源为仓库包内 `x64/vc17/bin/opencv_world4140.dll`；FreeType 和 OpenCV 的 zlib 均为仓库 `zlibstatic.lib`，三个可选能力关闭，FreeType `.pc` 只要求 zlib。直接和搬迁的中文绘制均为两个字形、1811 个非零像素，绘图 SHA256 与两份 Linux 原归档消费结果一致：`b313a5267bdce636b43ebe1a955dc6afb0ead9716cbb69bfc56477200d786939`。Windows transport SHA256 为 `f6008f7a216f46922590fd8d4525a07e673ab1d002c502e33a14b903aaa0fda9`，内含五个资产的摘要均已逐项核对。该 run 整体因 Darwin 作业失败而失败，不记为全平台通过。

快照 `a019657fe45c33b875686093e0b96b6e1a856fbb` 的 [run 42400038170，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42400038170) 仅复验 Darwin，整体成功。四库均完成构建、三个独立根消费、Mach-O 检查、运输及搬迁，平台结果 `complete=true`。保留 HarfBuzz 的 CoreText 能力并传递系统 ApplicationServices；FreeType 的 PNG/BZip2/Brotli 均关闭，FreeType/OpenCV 均使用仓库 `libz.a`。直接和搬迁的完整中文字形检查各 1/1 通过，绘图与上述 Windows/Linux 文件字节相同。Darwin transport SHA256 为 `25bc5c02c90024bb9b9299e33b9ec1ca85f6f29210ae6538806e7065180754b2`，五个资产摘要均已核对。CI 快照与正式配方、adapter、消费者内容一致；两份 Linux 既有产物的三项可选能力均未启用，复用已通过的原生构建、归档和完整中文消费证据，不重复编译。

原始结果、能力 JSON、完整步骤日志、逐资产摘要和实际绘图保存在 `build/font-integration-review/`。FreeType、HarfBuzz、OpenCV 的 Darwin 阻断解除，正式配方加入 `darwin-aarch64`；四个平台的全部 32 个版本均可选择，原有精确版本和通用包 publisher 规则不变。独立验收没有触发正式发布。

#### ViSP 专项验收

独立快照 `8efe406df43fdc8e34936954543d51fc53ec53cd` 的 [run 42449899414，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42449899414) 整体成功。环境为 macOS 15.8.2/arm64、Apple Clang 17.0.0、CMake 3.31.12，部署目标 15.0。五包闭包从当前配方完成构建，ViSP 独立根、Mach-O 检查、运输和汇总通过，结果为 `complete=true`；本地轻量夹具另验证了 `CMP0082` 安装顺序及安装后运行库修补，不能代替 macOS 15 验收。

公共入口重复导入、兄弟目录消费及完整混合跟踪/视觉伺服在原安装和搬迁归档上各 1/1 通过；两次均取得 edge=156、klt=334、dense=361、normal=3。标准 API smoke 的直接与搬迁检查也各 1/1 通过，搬迁消费期间原安装被移走。成品包含七个公开模块、libomp、omp.h 及许可证；实际 ViSP 动态库以 `@loader_path/libomp.dylib` 加载包内运行库，后者最低系统版本为 15.0。覆盖不包含真实相机、设备控制或应用现场跟踪。

ViSP 原始归档 `visp-3.7.0-darwin-aarch64.tar.zst` 的 SHA256 为 `64d02250edef76929e9abea03b8999ed8282d6b08a164dbcc6c92d8f24cbaab5`，transport SHA256 为 `d28c4446414e64433eedd52e814c772be1330a8cd4a9189789e1e21d9e395746`。已下载并逐项核对六个资产的大小、摘要及生产身份；正式产品文件与测试快照逐字节一致。证据和成品保存在 `build/visp-acceptance/42449899414-attempt1/`。真实 `all` 计划四平台各 33 根、0 过滤；原 Windows/Linux 三平台配方语义与迁入提交一致，本次未重编这些平台。独立验收未触发正式发布。

#### 同归档与功能覆盖缺口

两份 Linux Eigen 原件及 `producer.json` 已核对为 run 42264057220/attempt 1、源码 `d8ffda6a51e0a427a8ddec6fd508ff61c564625c`：3.4.0 SHA256 为 `be0dcfb8cd69556b00edeb541fbacff0ac4f9d9167ac75f47546bdf00923a2c4`，5.0.1 为 `9d71707429eeff4186b5993e3caff52fe76c33f6f8afb66f2801dcbb0831d578`。复用 Windows/macOS 两版直接及搬迁共八次消费，以及 run 42272676188 的原三平台 PCL 组合；没有重新生产或改写旧身份。同归档消费始终使用这两份原件，不使用本轮 transport 中重新打包的 Eigen 替代。Darwin PCL 在上述新运行中完成同一 Linux 3.4.0 Config 组合，直接及搬迁各 1/1 通过；已下载核对原摘要、生产身份及实际 Eigen3_DIR，均指向对应隔离安装的 3.4.0 Config。

实际覆盖限于有效 API smoke。RealSense 验证 SDK/API 和搬迁，未验相机取流；固定上游另有 Viewer/IMU/USB 权限限制。PortAudio 未验音频设备；SDL3 验证内存 surface，未验交互图形或 HID/USB，ImGui 验证 context 与颜色转换，未验窗口；Native File Dialog 未调用交互对话框；libusb 未验设备；ONNX Runtime 创建并释放 CPU 环境，未验模型推理。pybind11 包含编译扩展并由匹配 Python 实际调用。构建消费通过不扩大这些覆盖。

第二阶段首轮独立审查覆盖 `49df3fc..0a3cd6c` 及对应文档，发现 SDL3/libusb 一项 Important，ImGui 随依赖受到影响；用户确认暂列两包阻断后撤回新增 Darwin 声明。真实 plan 检查先复现两包仍被选中，修正声明后验证过滤，原三平台配置保持不变。没有其他已证实的 Critical/Important 或待处理 Minor。后续 SDL3、libzmq、字体链和 ViSP 的补验结果见各专项章节；正式发布与分支清理独立记录，不由包验收成功推定。
