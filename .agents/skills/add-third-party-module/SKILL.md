---
name: add-third-party-module
description: >-
  Add, migrate, integrate, standardize, or upgrade a third-party C++ module under
  src/dependencies/{name}/module.toml in xxautolib, including source builds and copy-only
  installations. Use even when an integration request only names the library. Do not use
  for runtime use of an installed library, pure environment or PATH troubleshooting,
  downstream CMake projects, shared build-engine or root orchestration changes, or
  packaging and release operations alone.
---

# 新增 / 标准化第三方模块

把库接入根 CMake 构建并安装到 `3rdparty/<name>/`。模块只声明数据；Python 生成原生 ExternalProject，CMake 执行获取、补丁、构建与安装。当前职责见[架构](../../../docs/architecture.md)，字段与执行规则见[流水线决定](../../../docs/agent-notes/implemented/architecture/2026-10-19-toml-externalproject-zlib.md)。

本技能提供模块接入与核对流程。新增模块同时需要扩展公共引擎时，模块部分仍按本技能执行，引擎扩展另按其设计处理；不能把参考仓库存在的接口直接写进当前 TOML。

## 选择已有流程

先查看与本库接近的真实定义，不复制一份完整模板另行维护：

- CMake 源码构建：参考 [zlib](../../../src/dependencies/zlib/module.toml) 的固定 Git 来源、库特定 cache 与文件补丁。
- 归档源码构建：参考 [FreeType](../../../src/dependencies/freetype/module.toml) 的多 URL、SHA256 与统一下载名。
- 仅复制安装：参考 [queue](../../../src/dependencies/queue/module.toml) 的独立附属资源与明确文件清单；头文件或已解压预编译输入使用 `copy`，不执行虚假的 configure/build。

每个模块目录只定义一个对外模块。已独立成模块的库不在使用方重复构建；当前尚无模块依赖声明与前缀注入，遇到真实依赖需求时先按[模块扩展决定](../../../docs/agent-notes/proposed/architecture/2026-10-20-module-expansion.md)确认该部分设计，不用资源伪装独立库依赖。附属输入仍归消费模块，不强制拆成公开模块。

## 接入流程

1. 在 `src/dependencies/<name>/module.toml` 填写 `name`、`type` 及适用的 `version`。名称与目录大小写完全一致；聚合多个独立上游时不编造统一版本号。文件开头用中文说明库用途和交付类型。
2. 为主来源或 `[resources.<资源名>]` 选择一种来源：Git 仓库与完整固定提交成对；归档使用有序非空 `URL` 数组和 `URL_HASH="SHA256=..."`。需要统一镜像文件名时填写 `DOWNLOAD_NAME`；升级同时核对版本、地址、摘要与安装布局。各资源独立固定版本。
3. 只保留实际需要的库特定 cache、patches、steps 或 copy 数据。非默认选项、固定版本、补丁与专用步骤写明中文维护约束；公共生成器、工具集、instance、CRT、Release 和路径由公共入口与引擎拥有，policy/PIC/C++ 标准兜底值由引擎的 CMake 子项目参数组装处提供，不逐库重复。模块 cache 优先于父级已定义值，父级值优先于引擎兜底值；具体规则见开发指南。
4. 补丁、专用 CMakeLists 和执行文件放在同一模块目录。文件补丁使用 `[[patches]]`；其他时机的修改使用原生 `[[steps]]`，按 DEPENDEES、DEPENDERS 排序，需要输入跟踪时声明 DEPENDS。显式 shell 命令才依赖 bash/sed，不假设 Windows 自带这些工具。
5. `copy` 使用相对来源的明确文件列表和相对安装前缀的 destination，核对同目录 include、库和运行文件是否完整。附属资源在 build 时才准备，不以 configure 时路径尚未存在判断失败。
6. 按[开发指南](../../../docs/development.md)通过普通根 CMake 入口选择模块并 configure/build，不要求 preset 或 IDE 设置；再按[测试指南](../../../docs/testing.md)完成实际构建安装与产物检查。configure 成功只证明发现和目标生成，不替代构建安装。

已有来源更换前，先保护本地修改并按开发指南重新准备受影响模块；不清空整个 build 或其他模块，不把重新 configure 或旧安装文件存在当成升级完成。

## 核对清单

按本次库的实际情况逐项核对，不添加用不到的字段或空占位：

- [ ] 一个目录对应一个对外模块，没有重复构建已独立成模块的依赖。
- [ ] 名称、目录及 BUILD_LIST 键大小写一致；版本和来源匹配，聚合资源各自固定版本。
- [ ] 中文简介与维护注释说明用途、交付类型及非默认设置理由；不含其他库的模板残留。
- [ ] 每个来源仅使用 Git 或归档之一，Git 字段成对，归档摘要与全部候选字节一致，镜像顺序和文件名正确。
- [ ] 只声明当前支持且实际使用的数据；无旧 helper、sentinel、版本覆盖、持久缓存、平台来源或发布 adapter 操作。
- [ ] 库特定 cache 已按需精简；未重复公共构建参数，也未为迁入库擅自增加参考没有的功能开关。
- [ ] 补丁和步骤属于本模块，顺序、输入依赖及工具前置条件明确；所需系统包不依赖其他模块碰巧安装。
- [ ] 复制清单完整且不越出来源/安装目录；资源准备先于实际使用。
- [ ] 真实根入口构建安装通过，代表头文件、实际库名、CMake 包文件及 `.pc` 路径与安装布局一致；检查实际外部依赖，不将静态库等同于依赖自包含。
- [ ] 沿用引擎能力时按风险检查产物，不强制每库新建测试工程；新增公共行为有公共回归。按约定平台验证，未验证项明确保留。

本地构建接入不附带下游导入、打包或发布要求。参考实现与后续方向的边界由模块扩展决定拥有；不迁入尚不可执行的旧模板。
