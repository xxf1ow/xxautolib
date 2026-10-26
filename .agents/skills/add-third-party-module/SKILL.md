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

把库接入根 CMake 构建并安装到 `3rdparty/<name>/`。模块只声明数据；Go builder 生成原生 ExternalProject，CMake 执行获取、补丁、构建与安装。当前职责见[架构](../../../docs/architecture.md)，基础字段见[流水线决定](../../../docs/agent-notes/implemented/architecture/2026-10-19-toml-externalproject-zlib.md)，依赖与资源消费见[依赖接线决定](../../../docs/agent-notes/implemented/architecture/2026-10-20-flann-opencv-dependencies.md)。

本技能提供模块接入与核对流程。新增模块同时需要扩展公共引擎时，模块部分仍按本技能执行，引擎扩展另按其设计处理；不能把参考仓库存在的接口直接写进当前 TOML。

## 选择已有流程

先查看与本库接近的真实定义，不复制一份完整模板另行维护：

- CMake 源码构建：参考 [zlib](../../../src/dependencies/zlib/module.toml) 的固定 Git 来源、库特定 cache 与文件补丁。
- 源码子目录：参考 [lz4](../../../src/dependencies/lz4/module.toml) 的 `[cmake] SOURCE_SUBDIR`；路径相对来源根，不改变补丁根。
- 归档源码构建：参考 [FreeType](../../../src/dependencies/freetype/module.toml) 的多 URL、SHA256 与统一下载名。
- 仅复制安装：参考 [queue](../../../src/dependencies/queue/module.toml) 的独立附属资源与明确文件清单；头文件或已解压预编译输入使用 `copy`，不执行虚假的 configure/build。
- 平台预编译包：参考 [onnxruntime](../../../src/dependencies/onnxruntime/module.toml) 的平台/架构来源和完整目录复制。
- 独立库依赖：参考 [flann](../../../src/dependencies/flann/module.toml) 的 `depends` 与 lz4 安装查找元数据；只请求叶子即可构建依赖闭包。
- 复杂资源：参考 [opencv](../../../src/dependencies/opencv/module.toml) 的 `<RESOURCE_SOURCE_DIR:opencv_contrib>`、资源补丁与平台包布局；[harfbuzz](../../../src/dependencies/harfbuzz/module.toml) 展示 FreeType 依赖接线。当前功能已验证无需本仓库注入 Python；新库若实际需要解释器，先按证据确认要求，不自动恢复解释器引用。

每个模块目录只定义一个对外模块。独立库用 `depends` 声明直接依赖，不在使用方重复构建，也不用资源伪装独立库。附属输入仍归消费模块，不强制拆成公开模块；尚未实现的模块扩展按[总体约束](../../../docs/agent-notes/proposed/architecture/2026-10-20-module-expansion.md)另行确认。

为可供后续模块查找的实际安装目录填写 `config_dir`、`pkgconfig_dir`，包名不同时填写 `package_name`；这些字段及 sentinel 的平台覆盖以实际布局为准。引擎只向使用方注入直接依赖位置，仍保留系统可选依赖搜索；检查 cache、`.pc` 解析值和实际链接输入，不能只看构建成功。

## 接入流程

1. 在 `src/dependencies/<name>/module.toml` 填写 `name`、`type` 及适用的 `version`。名称与目录大小写完全一致；聚合多个独立上游时不编造统一版本号。文件开头用中文说明库用途和交付类型。
2. 为主来源或 `[resources.<资源名>]` 选择一种来源：Git 仓库与完整固定提交成对；归档使用有序非空 `URL` 数组和 `URL_HASH="SHA256=..."`。需要统一镜像文件名时填写 `DOWNLOAD_NAME`；升级同步核对版本、地址、摘要与布局。平台表可声明 source、resources；architectures 只支持 x86_64、aarch64 下的 source、resources。按公共→平台→架构整组替换来源，不混合 Git/URL 字段；无当前目标来源应报错。各资源独立固定版本。
3. 只保留实际需要的库特定 cache、patches、steps 或 copy 数据。非默认选项、固定版本、补丁与专用步骤写明中文维护约束；公共生成器、工具集、instance、CRT、Release 和路径由公共入口与引擎拥有，policy/PIC/C++ 标准兜底值由引擎的 CMake 子项目参数组装处提供，不逐库重复。模块 cache 优先于父级已定义值，父级值优先于引擎兜底值；具体规则见开发指南。
4. 补丁、专用 CMakeLists 和执行文件放在同一模块目录。文件补丁使用 `[[patches]]`，可用 `resource` 指向本模块资源；主库配置等待资源补丁完成。cache 用 `<RESOURCE_SOURCE_DIR:资源名>` 引用所选源码根，不硬编码 build 布局。其他时机的修改使用原生 `[[steps]]`，按 DEPENDEES、DEPENDERS 排序，需要输入跟踪时声明 DEPENDS。显式 shell 命令才依赖 bash/sed，不假设 Windows 自带这些工具。
5. `copy` 使用互斥的非空 files 或 directories 和相对安装前缀的 destination；`directories=["."]`、`destination="."` 复制完整归档内容。核对同目录 include、库和运行文件；资源在 build 时才准备，不以 configure 时路径尚未存在判断失败。可选 sentinel 登记参考安装文件的相对路径；存在时跳过资源和安装步骤，但保留直接模块依赖顺序、查找元数据及消费准备，不验证版本、ABI 或完整性。
6. 按[开发指南](../../../docs/development.md)通过普通根 CMake 入口选择模块并 configure/build，不要求 preset 或 IDE 设置；再按[测试指南](../../../docs/testing.md)完成实际构建安装与产物检查。configure 成功只证明发现和目标生成，不替代构建安装。

Git 与归档复用根 git/；本地 `.git` 存在即保留当前源码，不自动校准提交。已有来源更换前，先保护本地修改并按开发指南准备受影响持久来源、build 状态和安装树；不迁移或删除旧 build 来源，不清空其他模块，不把重新 configure 或 sentinel 存在当成升级完成。包输入按[取源规则](../../../docs/agent-notes/implemented/process/2026-10-19-repository-foundation.md#验证授权)先在 Windows 下载校验再传 ARM，包括上游 Git bundle 和原生内部源码缓存；不传宿主构建产物。

## 核对清单

按本次库的实际情况逐项核对，不添加用不到的字段或空占位：

- [ ] 一个目录对应一个对外模块，没有重复构建已独立成模块的依赖。
- [ ] 名称、目录及 BUILD_LIST 键大小写一致；版本和来源匹配，聚合资源各自固定版本。
- [ ] 中文简介与维护注释说明用途、交付类型及非默认设置理由；不含其他库的模板残留。
- [ ] 每个来源仅使用 Git 或归档之一，Git 字段成对，归档摘要与全部候选字节一致，镜像顺序和文件名正确。
- [ ] 只声明当前支持且实际使用的数据；无旧 helper、命令行版本覆盖、独立缓存数据库或发布 adapter 操作。
- [ ] 库特定 cache 已按需精简；未重复公共构建参数，也未为迁入库擅自增加参考没有的功能开关。
- [ ] 补丁和步骤属于本模块，顺序、输入依赖及工具前置条件明确；所需系统包不依赖其他模块碰巧安装。
- [ ] 复制清单完整且不越出来源/安装目录；资源准备先于实际使用。
- [ ] 真实根入口构建安装通过，代表头文件、实际库名、CMake 包文件及 `.pc` 路径与安装布局一致；检查实际外部依赖，不将静态库等同于依赖自包含。
- [ ] 沿用引擎能力时按风险检查产物，不强制每库新建测试工程；新增公共行为有公共回归。按约定平台验证，未验证项明确保留。

本地构建接入不附带下游导入、打包或发布要求。参考实现与后续方向的边界由模块扩展决定拥有；不迁入尚不可执行的旧模板。

## 可选的二进制交付资格

仅在任务明确包含打包或下游消费时提供同目录 `autolib-import.cmake`；本地构建无需 adapter。正文以包实际目录定位，使用目标存在性 guard，每包只建立一个 `autolib::<name>` 全局 INTERFACE IMPORTED 公共目标，统一封装所支持的上游库及声明依赖的公共目标，不保留大写兼容别名。参考 queue 的固定 include、lz4 的精确 Config、flann 的 C/C++ 封装及显式 sibling 依赖；实际使用的直接包依赖必须在模块声明中列出。不得聚合互斥静动态产物，不扫描任意 Config、不回退系统包、不在 adapter 下载或编译。

提供 adapter 的模块由 builder 在普通安装后生成带身份及依赖头的消费文件，sentinel/stamp 复用不跳过准备；所声明依赖也必须有 adapter。没有 adapter 的独立模块照常构建。打包及 CI 复用安装中的描述，不另行生成；仅在 staging 修正生产前缀。产物布局及时序由[架构](../../../docs/architecture.md#二进制消费)拥有，adapter 不回写解包元数据，也不为搬迁更改第三方编译方式。

按[测试指南](../../../docs/testing.md#本地二进制闭环)验证原安装直接消费、真实链接、动态运行及打包搬迁；OpenMP 等系统运行要求按实际产物说明。消费、打包及人工刷新由[开发指南](../../../docs/development.md#本地打包与消费)拥有，远端发布仍须独立授权。
