# 架构

本文记录版本化构建、安装、消费与打包的组件关系。[统一交付设计](agent-notes/proposed/architecture/2026-10-29-versioned-artifact-delivery.md)拥有版本与元数据契约，[自定位入口设计](agent-notes/implemented/architecture/2026-11-03-entry-delivery-and-tool-only-ci.md)拥有入口交付及来源；[开发指南](development.md)和[测试指南](testing.md)说明命令及验证范围。

## 包与版本

源码配方采用 `src/dependencies/<包名>/package.toml` 和 `<版本>/module.toml`、`<版本>/autolib-import.cmake`。各版本平等，安装位置为 `<安装根>/<包名>/<版本>/`，源码、构建和缓存身份也区分版本。`package.toml` 的 `latest` 是显式版本选择，不是版本排序结果或发布完成标记。

构建列表、配方依赖和下游请求统一使用 `包名@版本`；省略版本时读取对应来源的 latest，显式版本必须严格匹配。`BUILD_LIST` 留空发现全部配方版本，不增加 `BUILD_VERSIONS`。构建时解析依赖，安装包记录精确版本，消费时不重新选择依赖 latest。不做版本范围求解或同一消费工程的多版本隔离；全版本构建允许不同根包各自使用不同依赖版本，消费闭包的同名版本冲突直接报错。

版本采用统一的跨平台 ASCII 路径规则，拒绝保留名称、固定文件名冲突及仅大小写不同的版本；合法字符串不改写。具体规则由正式设计拥有。

本地和 CI 都只做原生构建，不交叉编译或共用跨平台安装树。三个 CI 平台各有独立安装根；不同平台仅在 Release 中按不同文件名平铺共存，平台无关包继续使用 any/any。

## 组件职责

根 [CMakeLists.txt](../CMakeLists.txt) 和[构建入口](../src/autolib-builder/index.cmake)调用[共享工具准备](../src/autolib-builder/prepare-tools.cmake)，准备固定 Go SDK、builder、宿主 importer 和原样安装入口，再由 CMake/ExternalProject 执行构建。CI 零库模式通过同一工具准备的脚本入口执行，不调用模块生成器。模块声明、资源、补丁、依赖接线和安装机制归 builder；它也拥有安装副本的路径修正及归档机制，不认识托管平台或发布凭据。

`autolib-importer` 负责包选择、精确依赖遍历、HTTPS 下载、安全解包、校验和缓存准备，不复制独立安装来源或执行路径修正。单文件 [autolib.cmake](../src/autolib.cmake) 负责宿主 importer 引导、按需 FetchContent Git 快照及仓库检查、结果身份与闭包校验及包就绪后的 target 加载。adapter 使用已准备的依赖公共目标，不自行推断 sibling 版本目录、重新选择依赖或下载编译。

`autolib-ci` 在选择库时通过 builder 取得精确根及闭包，保留请求、有效及过滤根，按依赖层构建、逐根消费验证并运输安装结果；两个 Eigen 版本各有独立消费者。零库选择仍保留各平台，验证 importer 与入口后运输，不执行库构建或消费。workflow 提供可信源码仓库、平台环境、触发、权限与同目标串行，不在构建矩阵内发布 Release。

## 构建与交付

普通根入口生成版本化安装结果，包含原样 `autolib.cmake`、宿主 importer、包内身份、精确依赖和包级 `package.toml`。CI 在安装验证及运输前将入口与当前构建提交的源码逐字节比对，结果记录入口 SHA256，汇总拒绝未验证或不同摘要的入口。本地编译结束即可消费，不要求打包、上传或源码树参与下游运行。实际安装失败以未完成消费描述阻止导入；复用目标仍执行消费准备，但不重新标记已冻结的依赖身份。

公开二进制沿用既有 CI 的单一平台 ABI 基准和发布前验证；本地产物结构一致不代表任意工具链二进制兼容。本地自定义配置及混用远端依赖时，由使用者保持兼容；包身份和摘要校验不替代 ABI 判断，不增加 ABI 变体。

本地打包读取安装中的精确身份和依赖，在副本修正生产路径并生成版本化 tar.zst、单程序 importer tar.xz 及 SHA256；不读取源码 latest 重选安装版本。只打包显式模块，调用者提供完整分发集合。原安装保持不变。

独立发布以 workflow 运行上下文为可信源码仓库，核对成功构建的精确 run/attempt、原计划平台、安装验证和包级候选，调用原提交 builder 从运输副本打包完整有效集合。全部运输入口必须与原构建 Git 提交字节一致；发布准备只在上传副本注入实际 Gitee 目标和编码 tag，每次交付各平台工具及一个入口，零库仍保留工具和入口。Gitee Action 只接收明确资产清单并核对可信来源；上传 worker 返回结果，一个协调器更新成功工具和库的摘要及 latest 并确认 Git push。入口计入上传成功判定但不写 Git 元数据。普通发布只初始化缺失指针，显式映射可以随资产或独立更新。新库私有，用户公开后才匿名消费；附件替换不提供事务回滚。交接及失败约束由[Gitee 交付设计](agent-notes/implemented/architecture/2026-11-02-gitee-delivery-reimplementation.md)拥有。

## 消费与缓存

入口的实际路径所在目录是唯一工具、包及下载缓存根。源码和本地安装入口的内置远端为空；发布副本带实际 Release 地址。用户变量不能覆盖来源，本地消费者直接 include 安装入口，远程消费者将发布入口放入专用可写、忽略版本控制的目录。

工具和包先查同根已有结果，只有必需输入不存在且内置远端非空时才下载。已有候选损坏、身份不符或校验失败直接报错，不覆盖或降级；远端包经暂存、安全解包和校验后提交到同根。完整精确闭包就绪后才加载 adapter 和公共 `autolib::<包名>` 目标，同次 configure 复用结果。

已有缓存及其 latest 不自动更新，补充新包只获取缺失部分；用户手动清理缓存后才重新选择。精确请求不改写缓存 latest，依赖不重选 latest。完整有效工具和闭包命中时零远端操作；严格禁止缺输入时联网须使用内置地址为空的入口。不回退系统包或源码编译，同根写入由调用方串行，不自动清理既有文件。

缓存 latest 仅在其指向的完整包仍存在时有效；删除目标版本目录后，该选择失效，重新按来源发现并在成功后保存新选择，不猜测其他缓存版本。包损坏仍报错，缺少依赖则只补齐已记录的精确依赖。

## 远端元数据与下载

需要远端时，CMake 浅克隆所选专用仓库默认分支，检查身份及整棵 Git 树，不执行仓库代码；importer 只读取已检查快照的 latest 和独立摘要，按精确 URL 下载并校验归档。空缓存工具引导与包依赖共用一份快照；同次 configure 只获取检查一次，下次实际需要新包时重新获取，完整缓存复用零远端操作。已准备快照丢失或损坏时失败，显式重新 configure，不自动重克隆或使用旧快照兜底。

[统一产物交付设计](agent-notes/proposed/architecture/2026-10-29-versioned-artifact-delivery.md#cmake-与-importer-的远端交接)拥有内部交接、仓库格式和失败约束。消费不访问 Release/contents API、Raw 或正文摘要索引；CI/独立发布及真实托管验收仍按[路线图](roadmap.md)推进，远端写入须明确授权。

## 文档职责

本页拥有整体流程，正式设计拥有协议细节与长期理由；[路线图](roadmap.md)记录实施与验收范围。既有构建引擎见[流水线设计](agent-notes/implemented/architecture/2026-10-19-toml-externalproject-zlib.md)，平台环境和原生并行约束见[CI 系统设计](agent-notes/implemented/architecture/2026-10-20-ci-system.md)。后续设计与实现不得重新引入与本基准冲突的单版本槽位、消费 API 摘要查询或 latest 发布门槛。
