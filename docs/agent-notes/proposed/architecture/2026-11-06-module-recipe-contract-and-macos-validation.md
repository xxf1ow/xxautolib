# Agent Note: 统一模块配方字段与 macOS 全包验收

Status: proposed

## 问题

模块配方的字段说明分散在多个设计记录与接入技能中，声明、解析校验、执行和消费对部分字段的解释不同。`supported_platforms` 与 `platform_independent` 分别控制平台选择和发布身份；`copy` 配方可以声明不会生效的 CMake cache；部分入口参数被静默删除；步骤接受多种命令形状，路径检查在生成和描述入口之间存在差异。仅补平台字段不能建立一致的配方规则。

Eigen 的 C++ 模板 API 是纯头文件，但上游还提供可选的 BLAS/LAPACK 二进制接口。本仓库交付头文件与 CMake/pkg-config 元数据，PCL 通过 Eigen config 导入。安装方法、查找方法和产物的平台属性需要分别定义。

现有 30 个包共有 31 份版本配方，包括两个 Eigen 版本。仅 lz4 和 queue 声明 Darwin 支持；其余声明不能证明上游不支持 macOS。需要真实获取、安装、消费和必要的编译运行证据，确定每个现有版本的支持范围、补丁和系统依赖。

## 方案

统一模块配方及其在 builder、安装、importer、CI 计划、运输和发布校验中的表示。配方只声明事实，builder 统一解析；CI 使用解析结果组织执行和发布，不再次解释 TOML。安装入口控制执行环境，adapter 表达用户消费要求。

工作从 `05ad42949ec1ec1d6f9113cdc7e538263836215d` 继续，保留该提交包含的 GCC 10.5 与 HarfBuzz 修复。沿用当前普通任务分支，不创建 worktree，不把 master 再合入开发分支，也不重建或改写已有提交。实现中的设计修订追加在原分支。

范围包括全部现有配方、相关引擎和 JSON 消费者、安装及消费准备、配方接入技能和对应文档，以及 macOS 系统包准备与逐包验收所需的 CI 接线。不全面重构独立的 CI 工具配置或其他 workflow 输入，不新增包版本，不顺带升级上游或切换功能范围。

### 字段规范所有者与实现职责

实现时以 `docs/subsystems/module-recipes.md` 作为当前字段参考的唯一所有者，完整列出类型、默认值、必填条件、适用类型、路径基准、覆盖方式、互斥条件、实际效果和校验时机。本记录保存设计、理由和验收要求；架构文档解释组件职责，开发及测试指南拥有命令，接入技能引用字段参考。

配方自有字段使用小写名称，明确沿用 CMake 原生语义的来源、步骤与 `SOURCE_SUBDIR` 保留原名，cache 中的上游变量也保留原名。不开放任意 ExternalProject 参数透传。未知字段、无效类型、没有执行效果的字段组合及违反参数所有权的声明必须报错。

解析需要区分原始声明、目标覆盖后的有效配方及执行上下文。同一套解析结果供生成和描述使用；这些职责可在现有 builder 内实现，不为分层复制多套同义模型，不引入通用 schema 引擎或插件系统。

### 名称、版本与依赖

| 字段或位置 | 规则 |
|---|---|
| `src/dependencies/<name>/<version>/module.toml` | 目录是名称和版本的唯一输入；删除配方的 `name`、`version` |
| 名称及版本 | 精确匹配、保留大小写；使用可安全组成路径与引用的标识，拒绝路径分隔符、末尾点、Windows 保留名和大小写冲突；版本不强制 SemVer |
| `package.toml.latest` | 必填非空字符串，指向已有版本；包级文件仅接受该字段，不通过排序推算版本 |
| `depends` | 可选字符串数组，默认空；每项必须是 `name@version`，只列直接依赖，拒绝重复及同名不同版本 |

用户入口仍接受裸包名并通过 `latest` 选择；显式版本始终精确选择，不因目标不支持而回退。本轮迁移由维护者将已有配方中的裸名依赖按迁移时的 `latest` 一次性改写为 `name@version` 并提交；新解析器始终拒绝裸名依赖，不在读取时展开，不保留旧格式兼容。之后修改某依赖的 `latest` 不改变既有配方的依赖。

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

builder 不自行调用包管理器。本地和 CI 使用同一构建流程；本地维护者准备描述中列出的系统依赖，CI 在构建前安装。下载包消费端不自动执行 brew 或其他系统包安装命令。brew 安装成功不代表运行产物通过验收；Apple Clang、arm64、SDK 和 deployment target 仍由 Native 入口控制。

### builder 描述、CI 与唯一发布者

`describe` 保持只读、确定性输出，复用生成入口的解析与目标规则。保留请求根、有效根、过滤原因、有效模块及输入文件；模块输出名称、版本、构建目标、确定版本的直接依赖、已解析 `package_platform`、当前系统依赖及 adapter。删除手工拼接的完整 `configuration` 镜像和 CI 对其非空的检查，不增加另一种完整配方传输格式。

补丁、专用文件和实际存在的 adapter 继续计入输入；路径为源码根相对路径，不泄漏临时 checkout 的绝对位置。生成目录标签不是已经存在的源码文件，不能对其执行源码文件存在性检查。没有 adapter 不使通用描述失败；可发布资格由 CI 单独处理。

CI 按精确 `name@version` 为每个通用包确定一个 publisher，候选仅为本次有效闭包含有该包的平台。沿用 linux-x86_64、linux-aarch64、windows-x86_64、darwin-aarch64 的优先顺序，以 `publishers` 映射保存结果，无通用包时为空。publisher 固定后失败不接管、不降级或重新选择。其他平台继续安装及消费验证，但不重复交付该通用包。

publisher 失败使本次相应交付失败，不阻塞其他平台报告结果；允许在原平台、原代码及原选择下重新执行失败任务，结果仍绑定实际 run/attempt。已有效且输入未受影响的证据继续复用。自动换平台不是重试机制，不为偶发网络或 runner 故障新增自动接管。

运输、finalize 和发布准备按已解析身份及映射核对每个通用归档唯一且来源正确；具体包和宿主工具保持各自身份，canonical 入口仍唯一。发布端不重新解释配方、编译或重打包。包名、包内 OS/Arch、确定版本及摘要沿用[版本化交付设计](2026-10-29-versioned-artifact-delivery.md)。

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

验证范围由现有版本配方发现，覆盖全部 31 份，不仅验证 latest 或原有白名单。依赖先行，失败按包隔离；保留有效证据，仅重试受影响的包及下游。源码配方执行获取、补丁、编译、安装和消费；copy 配方执行实际安装与消费，不制造编译步骤。

新增独立的 `validate-macos-recipes.yml` 验收 workflow，名称不匹配现有 Gitee 的 `Representative releases` 监听。复用同一 builder、环境准备和 CI prepare/layers/verify/transport 命令，只保留 Actions 产物和证据，不运行发布入口或写 Gitee。无需依赖密钥缺失、发布故障或默认分支尚未更新来阻止发布，也不全面改造生产 workflow。

逐包验证以仅匹配当前任务分支的 `push` 触发为完整可用入口，根包在该次提交的验收 workflow 中明确选择；调整候选或修复后推送相应诊断提交，偶发失败可重跑原任务，不要求创建空提交。GitHub 的 [push 规则](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#push)支持尚未合入默认分支的 workflow，不为注册验收入口提前合入主分支。

`workflow_dispatch` 仅在当前仓库实际验证可用后作为指定根包的便捷入口，逐包流程不依赖它。GitHub 的 [事件规则](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_dispatch)同时包含默认分支要求和已运行 workflow 的分支 API/CLI 描述；[手动运行指南](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow#configuring-a-workflow-to-run-manually)仍要求默认分支存在该 workflow，因此不将“push 登记后必然可 dispatch”作为执行前提。各触发方式使用同一隔离发布规则，推送时机仍受当前阶段授权限制。

候选包通过任务分支的诊断提交临时启用 Darwin 后试验，不增加第二个平台资格字段或绕过解析器。验收 workflow 要求请求根全部进入有效根；被平台或 adapter 过滤必须显式失败，不能以工具专用成功代替包验证。最终只有符合交付及已验功能要求的候选保留 Darwin 声明，失败候选撤回临时声明并保存具体阻断。通用包保留通用身份，其依赖限制由闭包表达。

每个版本的结果包含获取、构建或复制、安装、导入、链接和必要运行结论，使用的 brew formula、补丁、上游限制、提交及 run/attempt 证据。简单消费复用现有 driver；现有 smoke 未实际调用到的包，补充最小有效 API 使用，不以空 main 或仅创建目标计为运行通过。设备、图形界面等无法在 runner 完成的功能明确列为未验，不将可构建等同于全部功能支持。

保留现有 Mach-O 的 arm64、部署版本、动态依赖和搬迁路径要求，不把 `/opt/homebrew` 整体加入系统路径豁免。macOS 包的第三方库依赖必须使用本仓库固定并编译的版本，禁止链接或捆绑 Homebrew、runner 及其他系统预装的第三方库；Apple SDK 的系统 Framework 和原生运行时继续由平台提供。brew 可用于已声明的构建工具，不能补齐运行库闭包。需要随包交付的仓库动态库按具体包核对传递依赖、install name/rpath、许可证和最低系统要求；修正只发生在隔离安装或 staging 中。搬迁后实际导入、链接及运行不得依赖构建机 Homebrew 前缀。

按包修正仓库动态库属于可交付性适配，不预建通用动态库收集框架。缺少仓库依赖、存在依赖循环或无法满足系统基线时先报告具名阻断，保留后续处理条件，不为当前试验扩展方案；涉及改变功能、版本、运行依赖承诺或既定产物要求时暂停受影响包，提出最小修订。不得通过静默关闭原功能、升级上游、提高最低系统版本或更换编译器取得绿灯。

RealSense 和 ONNX Runtime 也列入调查与验收，不能仅因当前无 Darwin 声明跳过。RealSense 区分 SDK 构建消费与实际相机功能；ONNX Runtime 核对当前版本官方 Darwin ARM64 产物、来源摘要及运行能力，不混入 Intel 包，也不无依据改为另一套预编译来源。FLANN 通过后，现有 `example/` 必须使用本轮产物完成导入、编译、链接和运行；验证可预置本轮入口及包，避免改写示例的业务代码或依赖旧远端产物。

### 实施阶段与数据保护

第一阶段统一字段、解析校验、全部配方格式、JSON 消费者、每包 publisher、通用包已知二进制载荷检查、Eigen adapter、brew 环境准备及独立验收接线，补齐字段参考和关联文档。保留无实际 Darwin 证据的具体包支持范围；候选在第二阶段逐个验证。第一阶段以同一规则可从本地和 CI 执行、真实 Eigen 安装及定向消费和原有轻量 Darwin 路径不受损作为独立验收结果，不声称全部库已适配。

第二阶段在第一阶段验收后规划，完成全部现有版本的 macos-15 调查、最小适配、逐包证据和最终支持结论，以及 Linux Eigen 同归档跨平台消费和 example 验证。支持失败必须给出实际原因；可修复且在范围内的问题完成修复，需改变已确认约束的问题交回人工决定，未验项不能伪装为不支持或已支持。

最终逐版本结果表追加到本记录的验证结果章节，随阶段交付受 Git 跟踪，保存已验证、经确认的阻断及未验覆盖缺口，引用对应源码提交、run/attempt 和产物摘要。关键原因、影响及覆盖缺口写入正文，不能只链接可能过期的 CI 日志。影响未来支持判断的长期阻断及重新尝试条件由本记录或已有库接入 Agent Note 保存并交叉链接；撤回临时 Darwin 声明时同步保存结论。结果表是具名版本的验收快照，不作为平台选择输入，也不要求随每次 CI 更新，不增加第二份长期状态数据库。

每次只详细规划即将执行的阶段；阶段内按实际影响增量验证，阶段结束复用已有效证据，不重复未变化构建或增加无关 Linux/Windows 实机、全量 Go suite/vet、最低 CMake 矩阵。全部目标包的首次 macOS 验证和同一 Linux Eigen 归档的必要跨平台验证属于本轮范围，不因增量原则省略。

已有源码、安装、缓存和远端资产保持原有数据保护规则。新身份使用隔离任务根；旧安装身份不符时拒绝，不自动重标、覆盖或清空。新规范面向未来声明及本轮迁移，不迁移历史 CI JSON、已发布归档或远端元数据，不增加旧字段兼容和协议协商。

书面设计及当前阶段计划确认后进行相应本地提交与执行。阶段二逐包试验可按本轮授权推送任务分支的诊断及修复提交并触发独立验收 CI，保留原始 run 证据；不强推，不修改远端主分支，不合并，不触发 Gitee 正式发布。需要上述范围外的远端写入时另行授权。

### 关联决定与文档归属

本提案部分替代[基础配方设计](../../implemented/architecture/2026-10-19-toml-externalproject-zlib.md)、[依赖接线设计](../../implemented/architecture/2026-10-20-flann-opencv-dependencies.md)、[模块扩展设计](2026-10-20-module-expansion.md)和[分批迁移设计](2026-10-28-batch-module-migration.md)中的字段表达，替代 [CI 系统设计](../../implemented/architecture/2026-10-20-ci-system.md)的完整配置镜像与全局 publisher，并扩展[直接消费设计](../../implemented/architecture/2026-10-25-direct-install-consumption.md)的通用包资格和 [Darwin 原生设计](../../implemented/architecture/2026-11-05-darwin-aarch64-native-build-and-ci.md)的包覆盖范围。

这些记录仍拥有原生执行、依赖接线、直接消费、数据保护及既有验证理由，属于部分替代，保持 active 并交叉链接，不归档或改写为相反决定。实施时同步更新仍由它们拥有的实现事实，字段细目统一指向新参考；本提案未实施期间不把普通当前架构文档改写为已实现状态。

## 备选方案

**保留格式，只逐处补文档和校验。** 改动较少，但身份重复、平台多层语法、命令多形状和独立的配置镜像继续造成解释分散。选择统一职责并直接迁移现有配方。

**引入独立 schema 或插件系统。** 可以集中声明字段类型，但不能解决安装、消费及发布中的所有权，也增加维护实体。采用现有 builder 内共享解析和针对性校验。

**保留两个平台字段或条件必填布尔值。** 仍需要解释无效组合及同一通用包的不同资格；单一 `package_platforms` 直接表达可提供的产物身份，不新增共享归档但限制部分目标消费的机制。

**将 Eigen 改为 copy，或修改 PCL 以绕过 config。** 可以构造另一路径，但安装工具不决定平台属性，删除查找元数据会转移兼容维护责任。保留上游 CMake 安装，复用导出目标及 PCL 的既有查找。

**继续全局 publisher 或失败接管。** 各通用包有效平台集合可以不同；逐包在既有候选中选择即可。失败接管会改变同次交付和摘要来源，缺少需求，不增加。

**全部在本机验证，或直接运行现有发布 workflow。** 本机系统版本不能证明 macOS 15；现有成功运行会自动进入 Gitee 发布。选择独立 macos-15 验收入口，复用生产构建执行而隔离正式发布。

## 验收标准

1. 全部现有版本配方使用同一字段契约，旧身份及平台字段、旧平台层级和一维 COMMAND 均拒绝；实际配方可完整解析、描述并生成，未选分支的坏语法不能隐藏。
2. 定向正负例覆盖依赖固定、平台及依赖过滤、有效覆盖、来源互斥及提交格式、无效类型字段、路径及占位符、保留参数、步骤关系和目录验证。两种安装类型均拒绝通用包中的已知目标二进制载荷；合法头文件和可重定位元数据仍可交付。断言保护真实输入效果，不只检查内部结构。
3. 本地生成、describe、CI、安装身份及发布准备使用一致的包身份和确定版本依赖；异构有效平台集合的通用包各有唯一正确 publisher，错误来源、重复、缺失和失败不接管均可观察。
4. brew 的选择、安装和记录由当前有效闭包决定；与 apt/choco 的声明及执行校验一致。独立验收 CI 可通过限定任务分支的 push 完成逐包试验，不依赖未验证的 dispatch，不被 Gitee 正式发布监听；过滤候选不能形成包成功结果。macOS 第三方运行依赖来自本仓库，完成必要的闭包、重定位及搬迁运行验证，不使用或附带系统预装的第三方库。
5. 两个 Eigen 版本保留头文件与查找元数据，通过 config 目标完成真实安装、直接及搬迁消费；同一 Linux 归档在 Windows/macOS 使用，有 PCL 组合的实际消费证据，不能回退系统 Eigen。
6. 31 份配方均有 macOS 15 实际结果或经人工确认的具名阻断及覆盖缺口。补丁、系统依赖、导入运行及功能限制有可追溯且受 Git 跟踪的结果记录，CI 日志过期后仍能理解关键结论、长期阻断及重试条件；支持列表与最终结论一致。example 完成实际消费，硬件未验不计为全功能成功。
7. 字段参考、架构、开发和测试文档、接入技能及关联记录同步，无相互冲突的现行字段标准；完成受影响检查及文档验证，保留既有数据且无未授权远端发布。

## 风险

字段与内部 JSON 必须成套切换，部分迁移会导致错误身份、依赖漂移或发布缺失。旧安装不会因新配方自动有效，需要隔离任务根；不以兼容层或破坏性清理掩盖问题。

`any-any` 是对交付物的维护者声明，不保证所有编译器、语言标准或外部后端均可用。元数据可能携带生产路径和版本检查，只有实际同归档跨平台消费能证明所需场景；不能以各平台独立安装代替。

runner 预装的 Homebrew 库可能改变可选功能探测、动态链接及最低运行要求。逐包检查实际链接，拒绝用系统预装的第三方库替代仓库闭包；保持 Apple Clang 和部署基线，不以放宽产物路径检查替代可交付性。

第三方固定版本可能存在上游 macOS 限制，设备和图形功能也可能缺少执行条件。本任务要求完成调查及范围内最小修复，不预先保证所有包都能保留全部功能；具名阻断需要证据及人工确认，不能以过滤、关闭功能或换版本结束试验。


## 验证结果

### 第一阶段：统一配方与执行基础

本阶段实现以 `9428eb9` 的批准设计为依据，在 `task/darwin-aarch64-native-ci` 追加本地提交；身份与平台、严格字段、执行所有权、通用 CMake 包、逐版本 publisher 及 brew/隔离验收入口分别由 `8532611`、`1d1a090`/`33b6588`、`3210413`、`1df1964`、`f787aa1`、`193d207` 实现。本文保持 proposed：后续 macos-15 全包与跨平台消费尚未完成，不将执行基础交付等同于整体验收。

全部 31 份配方通过 `TestRealModuleDeclarations` 的 Windows x86_64、Linux x86_64/aarch64 和 Darwin aarch64 局部读取、有效描述与生成检查，没有取源或全库编译。前三目标各有 31 个有效根，Darwin 有 9 个；websocketpp 虽为通用载荷，仍因 boost 的具体平台资格被过滤。PCL 的五个精确依赖保持不变，Eigen 固定为 3.4.0。`TestDarwinEntryCompiler` 通过真实根与子项目确认 AppleClang、arm64 和 15.0 参数。

字段、来源、依赖、覆盖、路径和参数所有权均有定向正负例；真实 argv、依赖接线与默认值夹具通过。两种安装类型的通用二进制内容拒绝、合法 config 目标及 staging 数据保护通过。每包 publisher 的异构平台集合、失败不接管和发布准备在 Go/Python 两端通过；真实轻量归档经运输及受控 HTTPS 入口消费通过。brew 选择、安装、版本记录及公式前缀传递由受控执行验证；独立 workflow 的过滤拒绝、执行接线与 Gitee 监听隔离通过。Chocolatey 名称规则完成静态核对，本机无 PowerShell，未执行该脚本。

真实生产与消费位于 `build/recipe-contract-stage1-WWVGJ7/`，使用本机 macOS 27.2、Apple Clang、CMake 3.31.12、GNU Make 4.4.1，目标固定 arm64/15.0。普通根一次选择 `eigen@3.4.0,eigen@5.0.1,queue@45db9a8b4431,lz4@1.10.0` 完成构建。复用的 Git 来源取源前只读核对 HEAD 与干净状态：lz4 为 `ebb370ca83af193212df4dcbadcc5d87bc0de2f0`，queue 两资源为 `6867b56452352acf077fccd5f6cc7e3a8cfde0fb` 和 `683b9e31ea15eb69f1b81cc1defc7850d5f20b71`；Eigen 归档按各配方 SHA256 校验。没有重编无关模块或清理旧安装。

两版 Eigen 的上游 pkg-config 模板将生产前缀写成绝对路径，真实检查先失败。版本目录内的局部补丁将 prefix 改为相对 `pcfiledir` 的本包前缀，保留上游 CMake 安装、Config/pkg-config 和全部功能选项；仅重建本任务 Eigen 状态，原结果留备份。补丁后 `.cmake/.pc` 无任务源码、build 或安装绝对路径，pkg-config 在原根及搬迁根均报告对应版本和正确 include。安装身份为 any/any，5.0.1 的语言要求来自上游目标。

`tests/autolib/run-installed.cmake` 对两个独立 Eigen 消费者及 queue/lz4 组合，在原安装和复制搬迁安装各执行一次 configure/build/CTest，六次均为 1/1 通过，Eigen 分别报告 3.4.0、5.0.1。搬迁组运行时隐藏本任务原安装根，结束后还原。生产、增量 Eigen 重建及消费日志分别为任务根下 `production.log`、`eigen-rebuild.log`、`consumption.log`，逐消费者证据位于 `consumers/install/`、`consumers/relocated/`。

独立总审查在 `05ad429..e54dbac` 发现一项平台过滤顺序缺陷：仅在 Windows 覆盖声明复制映射的 copy 包，会提前阻断 Darwin 自动发现。`TestPlatformSelectionCopyMappings` 先复现失败，再将非空映射完整性限制为有效目标检查；Windows 映射读取、支持目标缺映射拒绝及未选分支坏字段拒绝均通过。受影响的字段、过滤、describe 与全配方检查六项通过，未重跑未失效的真实库消费。审查无其他已证实 Critical/Minor；托管环境、跨平台同归档、PCL/example 和 PowerShell 执行仍为下节缺口。

### 第二阶段：macOS 逐包验收（进行中）

用户在执行中收紧第三方库来源：macOS 依赖只使用本仓库固定并编译的版本，不使用或捆绑 Homebrew、runner 及其他系统预装的第三方库。困难依赖先报告并留待后续处理，不为当前试验改变版本、关闭功能或扩展依赖方案。Apple SDK Framework 和原生运行时仍按 Native 要求使用。

`FreeType@2.13.2` 在源码 `03d5f5afd5c080f5bd5106bc4083906b68a2318d` 的 [macOS 15 验收 run 42251612424，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424/job/128007885621) 完成静态构建和 API 调用，但 Mach-O 检查拒绝 `/opt/homebrew/opt/libpng/lib/libpng16.16.dylib`。实际导入元数据同时包含 Homebrew HarfBuzz 15.0.0_1、Brotli 1.2.1，以及 SDK 的 zlib/bzip2。仓库 HarfBuzz 8.5.0 已依赖 FreeType，补反向依赖会形成循环；当前仓库没有 PNG、Brotli、bzip2 配方，FreeType 也未声明仓库 zlib。关闭可选功能未获批准，系统或 Homebrew 版本不可替代仓库闭包。因此撤回临时 Darwin 声明，保留其他平台现状；这表示当前配方的 macOS 交付暂缓，不表示上游不支持 macOS。

HarfBuzz 8.5.0、OpenCV 4.14.0 的 Darwin 验收随此依赖链暂缓，尚无独立构建结论。重试需要先明确 FreeType/HarfBuzz 的无环构建方式、其余第三方依赖的仓库配方和功能要求，并批准相应依赖声明；本轮不实施这些后续方案。

`boost@1.87.0` 在源码 `d07d8c06dc930ac2cb897e22bdf4e6df87579749` 的 [macOS 15 验收 run 42257137133，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42257137133/job/128027205076) 完成静态构建及消费 API 检查，但 Mach-O 拒绝 Homebrew zstd 1.5.7 的 `libzstd.1.dylib`。实际 Boost::iostreams 元数据同时依赖 SDK bzip2、liblzma，zlib 则正确使用仓库 1.3.1。仓库缺少 zstd、bzip2 和 xz/liblzma 配方；不关闭压缩功能、不使用系统版本，暂缓该版本 Darwin 交付并撤回临时声明。重新试验需要先批准并建立这些仓库依赖及接线。

WebSocket++ 4dfe1be74e68、PCL pcl-1.15.1 的 Darwin 验收因 Boost 闭包缺失暂缓，尚无这两个根的独立构建结论；WebSocket++ 保持 any-any 载荷身份，其目标资格继续由 Boost 依赖过滤。PCL 在原有三个平台的 Linux Eigen 归档组合验收仍属于本阶段范围。

`libzmq@4.3.5` 在源码 `c8eec1ec11a8464cd8af03a9dd953bcb35740eec` 的 [macOS 15 验收 run 42260213354，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42260213354/job/128037859553) 完成构建及进程内消息收发，但 Mach-O 检查拒绝 `/opt/homebrew/opt/gnutls/lib/libgnutls.30.dylib`。导出的静态、动态目标都依赖 Homebrew GnuTLS，仓库没有对应配方。保留功能并撤回临时 Darwin 声明；重试需要先批准并建立仓库 GnuTLS 及其必要依赖。

`flann@f9caaf609d8b` 在同一源码的 [macOS 15 验收 run 42260213354，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42260213354/job/128037859606) 使用仓库 lz4 完成构建安装，但配置明确报告 `OpenMP NOT found`，未满足配方 `USE_OPENMP=true` 的既定要求；实际消费还因裸 install name `libflann_cpp.1.9.dylib` 无法加载而失败。仓库没有 Apple Clang 所需的 OpenMP 运行库，不能用 Homebrew libomp、关闭 OpenMP 或替换编译器绕过。因此撤回临时 Darwin 声明；重试需要先解决仓库 OpenMP 依赖及 Mach-O install name/rpath，再验证实际运行和搬迁。PCL 的 Darwin 闭包同时受此影响。

原样 `example/` 的隔离 driver 已有缺输入及数据保护检查，但本轮 macos-15 未完成真实导入、编译和运行：其 FLANN 依赖尚未合格。这是阶段验收缺口，不能以其他库的 smoke 通过代替。

`librealsense@2.58.1` 在 [macOS 15 验收 run 42260213354，attempt 1](https://github.com/xxf1ow/xxautolib/actions/runs/42260213354/job/128037859866) 编译到最终共享库链接，因静态 libusb 的 `SecTaskCreateFromSelf`、`SecTaskCopyValueForEntitlement` 未解析失败。包级本地依赖补丁补齐与 libusb 固定上游相同的 Foundation、IOKit、Security Framework；本机使用实际仓库 libusb 的链接及 API probe 先失败后通过。托管重试尚未完成，不计为成功；无相机环境不能验证取流。

### macOS 逐版本快照

以下成功表示该版本完成 macos-15 构建或复制安装、Config/adapter 导入、真实 API smoke、打包摘要核对和归档搬迁消费。归档摘要指 transport 中的原始 `.tar.zst`；直接与搬迁的 CTest 均通过。三个 run 的 attempt 均为 1，源码分别为 `03d5f5afd5c080f5bd5106bc4083906b68a2318d`（[42251612424](https://github.com/xxf1ow/xxautolib/actions/runs/42251612424)）、`d07d8c06dc930ac2cb897e22bdf4e6df87579749`（[42257137133](https://github.com/xxf1ow/xxautolib/actions/runs/42257137133)）和 `c8eec1ec11a8464cd8af03a9dd953bcb35740eec`（[42260213354](https://github.com/xxf1ow/xxautolib/actions/runs/42260213354)）。

| 精确版本 | 结论与证据 run | 原始归档 SHA256 |
|---|---|---|
| `asio@8806a6803cde` | 通过 / 42251612424 | `331bb64fdb8973a4efba04a2bfbfaf005e6aaf06384c02f6eeaf2a965e05d827` |
| `boost@1.87.0` | 暂缓 / 42257137133 / 压缩依赖缺失 | — |
| `clipper2@1.5.4` | 通过 / 42251612424 | `a295b1c3cba844db372056be276391010d43611bfa2dc4c25e64fe0fb0932f19` |
| `cpp-httplib@88956ccad838` | 通过 / 42251612424 | `6a132b0d77acc8f4e0336baebbfd0dcb90291bbe8096eef870647cbda361a056` |
| `eigen@3.4.0` | 通过 / 42260213354 | `d596afb529434130ff3c2e22ff5f406ca10e70b59d841e72987b548a9bc58170` |
| `eigen@5.0.1` | 通过 / 42251612424 | `b964eadcd30e8b241f02dfb8625d830acc29a2990c1f3bb092a34504654ae1b3` |
| `expected@1.3.1` | 通过 / 42251612424 | `71abad84a720aa862e0d91ebf4f098e89d37ac952716a6da60327933efcac7a7` |
| `flann@f9caaf609d8b` | 暂缓 / 42260213354 / OpenMP 缺失、动态库加载失败 | — |
| `freetype@2.13.2` | 暂缓 / 42251612424 / 字体依赖闭包及循环 | — |
| `gtest@1.17.0` | 通过 / 42251612424 | `86b965d2374257f1c96838b7cfbd332329074f8b01c70535f8af13f2bac851bd` |
| `harfbuzz@8.5.0` | 依赖闭包暂缓 / FreeType | — |
| `imgui@065f9e02695c` | 通过 / 42260213354 | `af8f3931f11087e7d357db90b295ab0351db7c50e1ccdde1d18c56357a79f453` |
| `librealsense@2.58.1` | 待修复重试 / 42260213354 | — |
| `libusb@1.0.27` | 通过 / 42257137133 | `0b131d2c44727f966e1edacc6b209b05a2a5c8714cd8b115fe0db6dcf5e7998a` |
| `libzmq@4.3.5` | 暂缓 / 42260213354 / GnuTLS 缺失 | — |
| `lz4@1.10.0` | 通过 / 42251612424 | `b32d744bf837353f66d2f9f580bf69edf091c73f70354910db4a21a8df8beeb2` |
| `nativefiledialog-extended@3cd252a8f7ca` | 通过 / 42251612424 | `efcadeb69ce4b804423ca60f6ef32e2420a9f43ae66f9d0977c38db8afeb439c` |
| `nlohmann@3.12.0` | 通过 / 42251612424 | `4f24dc13816ab35c303143a92e21ea171647de043f2929695cfc5578347752a8` |
| `oneTBB@628c90df6867` | 通过 / 42251612424 | `61abcbc72be90bd3486d5ed114291993c874cd1b83e6e604e3884a0b3ca272de` |
| `onnxruntime@1.24.4` | 通过 / 42260213354 | `1a328c640b48bb5005cfbeb25adfe332b2f0bb4cb0be750f7f968fa561165030` |
| `opencv@4.14.0` | 依赖闭包暂缓 / FreeType/HarfBuzz | — |
| `pcl@pcl-1.15.1` | 依赖闭包暂缓 / Boost/FLANN | — |
| `portaudio@v190700_20210406` | 通过 / 42260213354 | `ec0c7881e25626dd4711dff243307cd724c84484f7954f93478da33e1c782c95` |
| `pybind11@2.13.6` | 通过 / 42257137133 | `eb4dbd606cdd9d4c88c48b593e544efd48c9fb0607d752676dfea82ee326859c` |
| `queue@45db9a8b4431` | 通过 / 42251612424 | `e7d91ecb0a6645dd99eea04b0a181d9d0930722c495dd216bd3c88ffd512a7d5` |
| `sdl3@3.2.30` | 通过 / 42251612424 | `116658d320c223d459df7ae2525933c8c6c8cf13a02418c57239def33d226097` |
| `spdlog@1.17.0` | 通过 / 42251612424 | `edfe516678412688dc90415a7a1ba6ce14aeced1e51e87cebe0505d8c24b1153` |
| `taskflow@83f90a279aaa` | 通过 / 42257137133 | `370cb340dc92a5d1b8c50d3f86fb49f2a3f1292c1d08e0ebf2b94dc3b7d1529d` |
| `websocketpp@4dfe1be74e68` | 依赖闭包暂缓 / Boost | — |
| `yaml-cpp@2decf96e915d` | 通过 / 42251612424 | `61674b68034ae2a917e4622841fd59954ab25a64f6dc35f93f4f3acabdb723c3` |
| `zlib@1.3.1` | 通过 / 42251612424 | `f5d86230d8abb59f97fff177dd351df89261ffaccef5957ead480e0c33bc05a1` |

功能覆盖限于 smoke 实际调用：PortAudio 查询运行环境，未验音频设备；SDL3 验证内存 surface，未验图形会话、HID/USB 和按名加载 `libusb-1.0.dylib` 的可选路径；ImGui 创建上下文并生成帧数据，未验窗口显示；Native File Dialog 未调用交互对话框；libusb 未验真实 USB 设备；ONNX Runtime 创建并释放 CPU 环境，未验模型推理。上述设备及后端路径不计入通过范围，SDL 可选动态后端的依赖来源仍需后续实机核对。pybind11 的通过包含编译绑定扩展并由匹配 Python 实际调用。

### 第一阶段未覆盖的范围

第一阶段没有推送、远端 Actions、发布或合并。上述本机证据不证明 macOS 15 实际运行，也不证明全部 31 配方已支持 Darwin。完整逐版本 macos-15 结果、必要系统库和补丁、同一 Linux Eigen 归档跨 Windows/macOS 的摘要与消费、PCL 组合及 example 实机导入留待第二阶段；硬件和图形功能按实际条件单列。失败候选的具名阻断与重新尝试条件届时写入本节，不能以平台过滤或 CI 成功代替。
