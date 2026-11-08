# 模块配方字段

本页定义 `src/dependencies/<name>/<version>/module.toml` 的字段及解析规则。构建、描述和 CI 共用 builder 的有效配方；安装方法与产物平台身份分别声明。

## 身份与依赖

目录是名称和版本的唯一输入，配方不接受 `name`、`version`。名称和版本精确匹配并保留大小写，版本不强制 SemVer；禁止路径分隔符、末尾点、Windows 保留名和大小写冲突。每个包的 `package.toml` 只接受必填非空字符串 `latest`，且必须指向已有版本。

`depends` 为可选字符串数组，默认空，每项必须为 `name@version`，只列直接依赖；重复包名及同名不同版本均报错。用户入口可用裸名选择 latest；配方依赖始终固定，不随 latest 改变。根闭包内缺失依赖、环及版本冲突报错并保留依赖链。

## 平台与覆盖

`package_platforms` 是必填、非空、无重复的字符串数组，接受 `windows-x86_64`、`linux-x86_64`、`linux-aarch64`、`darwin-aarch64`，或单独的 `any-any`。通用与具体平台不能混用。未知值及旧 `supported_platforms`、`platform_independent` 字段均报错。

请求已知目标 P 时，通用包身份为 `any-any`，具体声明包含 P 时身份为 P，否则不支持。每项依赖独立判断平台；通用根不能绕过依赖的平台限制。显式请求不支持的平台报错；自动发现返回有效根及过滤原因。平台资格先于目标来源完整性判断。describe 的 `package_platform` 输出当前有效身份。

覆盖只有公共字段和 `platforms.<完整平台ID>` 两层。具体包只能覆盖自身声明的平台；通用包可覆盖所有已知平台。覆盖不允许改变 `type`、`depends`、`package_platforms` 或 `system_dependencies`。所有分支都检查字段和结构，当前目标只合并匹配分支。

| 字段 | 目标覆盖方式 |
|---|---|
| `source` | 整组替换 |
| `resources` | 按名称整项替换 |
| `cmake.cache` | 按键覆盖 |
| `cmake.SOURCE_SUBDIR`、`config_dir`、`pkgconfig_dir`、`package_name`、`sentinel` | 替换 |
| `steps`、`patches` | 追加 |
| `copy` | 整数组替换 |

旧 OS 表和 `architectures` 层级不接受。通用身份约束完整交付内容；安装类型本身不证明内容可跨平台使用。cmake 和 copy 均可交付 any-any；消费准备、复用和 staging 按内容拒绝已知 ELF、PE、Mach-O、COFF 及目标代码归档，不依赖扩展名。copy 另核对映射集合及来源字节，允许平台无关的查找元数据；生成头文件和元数据的跨平台语义仍需审查与真实消费验证。字段设计与验收要求见[统一配方设计](../agent-notes/implemented/architecture/2026-11-06-module-recipe-contract-and-macos-validation.md)。

## 来源与安装字段

`type` 必填且只能为 `cmake` 或 `copy`，分别使用上游 CMake 安装或复制映射。cmake 必须有有效主来源且禁止 `copy` 字段；copy 必须有非空映射且禁止整个 `cmake` 表，包括空表。安装方法不决定包平台身份。

`source` 为可选主来源表，`resources.<名称>` 为可选私有附属来源表，默认空资源表；二者共用以下格式。CMake 安装以及选择主源码的复制映射需要主来源，resource-only copy 可省略主来源。

| 字段 | 类型与规则 |
|---|---|
| `GIT_REPOSITORY`、`GIT_TAG` | 成对非空字符串；tag 必须为完整 40 位十六进制提交 |
| `URL`、`URL_HASH` | 成对；URL 为有序非空字符串数组且元素非空，hash 为 `SHA256=<64位十六进制>` |
| `DOWNLOAD_NAME` | 仅归档可用，单个文件名，不接受目录、绝对路径、`.` 或穿越 |

Git 与归档字段互斥。主来源和资源在目标覆盖时整项替换，不拼接不同来源的字段。准备已有 Git 工作副本不自动 fetch、checkout 或 reset。

`cmake.SOURCE_SUBDIR` 是相对主源码根的可选目录，默认源码根。`cmake.cache` 默认空表，变量名须合法，值只接受字符串、布尔、整数或字符串数组；列表型路径键仅接受字符串或字符串数组。

`copy` 为表数组，每项可用非空字符串 `resource` 指定有效资源，缺省主来源。`files` 或 `directories` 二选一，均为非空相对路径数组；`destination` 必填并相对本包安装前缀。路径禁止绝对值及越界，目录项允许 `.`。files 保留文件名，directories 复制目录内容；按映射顺序执行并沿用原生覆盖行为，下载前不检查输入文件是否已存在。

`config_dir`、`pkgconfig_dir` 为可选非空字符串，相对本包安装前缀的目录，允许 `.`；`sentinel` 为可选非空字符串，指向该前缀内的代表文件。三者缺省未声明，禁止绝对路径与越界。sentinel 存在时跳过本包获取及安装，但保留依赖顺序和消费准备；它不证明版本、ABI 或安装完整性。`package_name` 为可选非空 CMake 包名，仅在有 config_dir 时使用，缺省使用包名；显式声明时没有有效 config_dir 即报错。元数据路径与类型在解析时检查，来源需求、资源引用及跨字段完整性在目标合并后检查。所有分支中的未知字段、表结构和局部值错误均报错。

安装完成与复用检查声明的 config_dir/pkgconfig_dir 为已有目录，包括没有 adapter 的本地配方。有效上游 Config 导出目标优先复用；纯头文件身份不要求手写包含路径或语言要求。两个 Eigen 版本通过上游 CMake 安装，以 any-any 交付头文件与查找元数据，adapter 链接本包的 Eigen3::Eigen。


## 系统依赖

`system_dependencies` 是可选公共表，默认空，接受 apt、choco、brew 的字符串数组。单配方内重复项报错，闭包去重由 CI 执行；不接受选项或 shell 片段。

| 管理器 | 包名规则 |
|---|---|
| apt | `^[a-z0-9][a-z0-9+.-]*$` |
| choco | `^[A-Za-z0-9.][A-Za-z0-9.-]*$` |
| brew | `^[a-z0-9][a-z0-9+_.@-]*$`，仅 formula，不接受 tap/cask |

## CMake 参数归属

布尔 cache 生成 BOOL，其余允许值生成 STRING，字符串数组转 CMake 列表。cmake 子项目的 policy 下限、PIC、C++ 标准、libdir 兜底分别为 `3.5`、`ON`、`17`、`lib`，按引擎→入口默认→有效配方合成。copy 不应用这些默认值。policy 不降低上游更高声明；PIC 显式关闭后，静态库链入下游共享库须经实际消费验证。

`BUILD_SHARED_LIBS` 及库专用构建形态开关由配方和平台覆盖决定；未声明时采用上游默认。根入口不自动传递父项目的 BUILD_SHARED_LIBS。

入口拥有安装前缀、Release 配置、运行库、生成器及 platform/toolset/instance、Make、C/C++ 编译器和 Native 目标。配方禁止填写 `CMAKE_INSTALL_PREFIX`、`CMAKE_BUILD_TYPE`、`CMAKE_MSVC_RUNTIME_LIBRARY`、`CMAKE_GENERATOR` 及其 PLATFORM/TOOLSET/INSTANCE、`CMAKE_MAKE_PROGRAM`、`CMAKE_C_COMPILER`、`CMAKE_CXX_COMPILER`，也不能通过 toolchain、sysroot、系统身份或编译器目标切换目标。Darwin 还禁止配方填写三个 `CMAKE_OSX_ARCHITECTURES`、`CMAKE_OSX_DEPLOYMENT_TARGET`、`CMAKE_OSX_SYSROOT`。同值重复也报错。

直接依赖前缀加入 CMAKE_PREFIX_PATH，并保留配方路径。依赖自身的 config_dir 和 package_name 决定注入的 `<Package>_DIR`；使用方有效 cache 声明同名键即报错，不禁止其他 `_DIR`。使用方自身的 config_dir 描述安装输出，与此检查无冲突。依赖 pkgconfig_dir 加入子进程 PKG_CONFIG_PATH，项目 include 包装保留配方的 include 执行。

## 补丁、步骤与路径

`patches` 为可选表数组，默认空；每项 file 必填非空字符串，指向配方目录内已有普通补丁文件，可选非空字符串 resource 选择有效资源，否则需要主来源。同一来源按声明顺序应用，配置或复制等待所需资源及补丁完成。

`steps` 为可选表数组，默认空，适用于两种安装方法。每项 name 必填非空名称字符串，合并后唯一，不能与原生步骤重名。COMMAND 必须为非空二维 argv 数组，每条命令非空且首项非空；多条命令依次执行，不隐式使用 shell。

DEPENDEES、DEPENDERS 为可选字符串数组，默认空，表示步骤名，至少一侧非空，引用必须为原生或有效自定义步骤；数组顺序不决定调度，循环交给原生步骤图处理。DEPENDS 为可选字符串数组，默认空，表示文件路径，表示原生步骤输入变化，不是包依赖。WORKING_DIRECTORY 为可选非空字符串，缺省 `<BINARY_DIR>`；普通相对路径基于配方目录。命令参数中的相对路径按工作目录解释，builder 不推断参数是否为文件。

cache 和步骤支持 `<SOURCE_DIR>`、`<BINARY_DIR>`、`<INSTALL_DIR>`、`<RESOURCE_SOURCE_DIR:名称>`。builder 展开资源目录，其余按原生步骤位置展开，未知占位符报错。DEPENDS 和 WORKING_DIRECTORY 的普通路径须在配方目录内；标签路径须以明确目录标签开头，不能通过 `..` 越界。普通 `<` 不跳过检查，路径字段不接受生成表达式。普通 DEPENDS 输入须为已有文件；标签指向的生成文件在执行时检查，不要求 describe 时存在。

## 只读描述

describe 输出有效包身份、精确直接依赖、当前平台的系统包管理器条目、adapter 路径和相对 input_files，不输出完整配置镜像。输入包括配方、package.toml、实际 adapter、补丁及普通 DEPENDS 文件；生成文件不列为 checkout 输入。没有 adapter 的合法配方仍可描述和本地构建，CI 排除该包及依赖它的根并返回原因；坏声明直接报错。

## CI 与发布

CI 按每个精确通用包的有效平台集合选择唯一 publisher；计划以 publishers 映射传递 name@version 到具体平台。运输与发布准备按映射核对来源，失败可在原选择重试，不切换生产平台；具体交付规则见[CI 设计](../agent-notes/implemented/architecture/2026-10-20-ci-system.md)。

Darwin 准备脚本从计划的当前平台读取 brew formula，校验并去重后直接安装、记录版本，只将这些 formula 前缀追加到 CMake/pkg-config 查找环境；空列表不调用 brew。验收 workflow 使用任务分支 push 选择明确根包，并拒绝任何平台或 adapter 过滤；与正式发布入口独立。
