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
