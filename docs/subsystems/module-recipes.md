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

旧 OS 表和 `architectures` 层级不接受。通用身份约束完整交付内容；安装类型本身不证明内容可跨平台使用。字段设计与验收要求见[统一配方设计](../agent-notes/proposed/architecture/2026-11-06-module-recipe-contract-and-macos-validation.md)。

## 来源与安装字段

`type` 必填且只能为 `cmake` 或 `copy`，分别使用上游 CMake 安装或复制映射。cmake 必须有有效主来源且禁止 `copy` 字段；copy 必须有非空映射且禁止整个 `cmake` 表，包括空表。安装方法不决定包平台身份。

`source` 为可选主来源，`resources.<名称>` 为可选私有附属来源，默认空资源表；二者共用以下格式。CMake 安装以及选择主源码的复制映射需要主来源，resource-only copy 可省略主来源。

| 字段 | 类型与规则 |
|---|---|
| `GIT_REPOSITORY`、`GIT_TAG` | 成对非空字符串；tag 必须为完整 40 位十六进制提交 |
| `URL`、`URL_HASH` | 成对；URL 为有序非空字符串数组且元素非空，hash 为 `SHA256=<64位十六进制>` |
| `DOWNLOAD_NAME` | 仅归档可用，单个文件名，不接受目录、绝对路径、`.` 或穿越 |

Git 与归档字段互斥。主来源和资源在目标覆盖时整项替换，不拼接不同来源的字段。准备已有 Git 工作副本不自动 fetch、checkout 或 reset。

`cmake.SOURCE_SUBDIR` 是相对主源码根的可选目录，默认源码根。`cmake.cache` 默认空表，变量名须合法，值只接受字符串、布尔、整数或字符串数组；列表型路径键仅接受字符串或字符串数组。

`copy` 每项可用 `resource` 指定有效资源，缺省主来源。`files` 或 `directories` 二选一，均为非空相对路径数组；`destination` 必填并相对本包安装前缀。路径禁止绝对值及越界，目录项允许 `.`。files 保留文件名，directories 复制目录内容；按映射顺序执行并沿用原生覆盖行为，下载前不检查输入文件是否已存在。

`config_dir`、`pkgconfig_dir`、`sentinel` 为可选安装前缀内相对路径，缺省未声明；`package_name` 为可选 CMake 包名，声明时必须有有效 config_dir。元数据路径与类型在解析时检查，来源需求、资源引用及跨字段完整性在目标合并后检查。所有分支中的未知字段、表结构和局部值错误均报错。

## 系统依赖

`system_dependencies` 是可选公共表，默认空，接受 apt、choco、brew 的字符串数组。单配方内重复项报错，闭包去重由 CI 执行；不接受选项或 shell 片段。

| 管理器 | 包名规则 |
|---|---|
| apt | `^[a-z0-9][a-z0-9+.-]*$` |
| choco | `^[A-Za-z0-9.-]+$` 且首字符不能为 `-` |
| brew | `^[a-z0-9][a-z0-9+_.@-]*$`，仅 formula，不接受 tap/cask |
