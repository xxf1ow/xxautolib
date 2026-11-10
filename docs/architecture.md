# 架构

本文记录版本化构建、安装、消费与打包的组件关系。[统一交付设计](agent-notes/proposed/architecture/2026-10-29-versioned-artifact-delivery.md)拥有版本与元数据契约，[自定位入口设计](agent-notes/implemented/architecture/2026-11-03-entry-delivery-and-tool-only-ci.md)拥有入口交付及来源；[开发指南](development.md)和[测试指南](testing.md)说明命令及验证范围。

## 包与版本

源码配方采用 `src/dependencies/<包名>/package.toml` 和 `<版本>/module.toml`、`<版本>/autolib-import.cmake`。各版本平等，安装位置为 `<安装根>/<包名>/<版本>/`，源码、构建和缓存身份也区分版本。`package.toml` 的 `latest` 是显式版本选择，不是版本排序结果或发布完成标记。

名称和版本仅取自目录，版本配方通过必填 `package_platforms` 声明平台资格和发布身份，规则见[字段参考](subsystems/module-recipes.md)。builder 校验完整依赖图，再检查有效闭包的目标来源；普通显式请求不支持时失败，自动发现和只读描述返回有效闭包及过滤原因，CI 继续叠加 adapter 资格，零有效库保留工具与入口。规则见[平台选择设计](agent-notes/implemented/architecture/2026-11-05-darwin-aarch64-native-build-and-ci.md#选择依赖与过滤结果)。

配方依赖必须使用 `包名@版本`；构建列表和下游请求可省略版本并读取对应来源的 latest，显式版本必须严格匹配。`BUILD_LIST` 留空发现全部配方版本，不增加 `BUILD_VERSIONS`。构建时解析依赖，安装包记录精确版本，消费时不重新选择依赖 latest。不做版本范围求解或同一消费工程的多版本隔离；全版本构建允许不同根包各自使用不同依赖版本，消费闭包的同名版本冲突直接报错。

哈希型包版本在配方中声明为前 12 位十六进制字符，不带省略号；目录、依赖、元数据、变量和归档名使用同一短版本。源码 `GIT_TAG`、实际完整性校验摘要及构建提交身份保留完整值。版本采用统一的跨平台 ASCII 路径规则，拒绝保留名称、固定文件名冲突及仅大小写不同的版本；输入按声明精确匹配，不自动截断或映射。

本地和 CI 都只做原生构建，不交叉编译或共用跨平台安装树。CI 默认/all 包含 Windows x86_64、Linux x86_64/aarch64 和 Darwin ARM64，各有独立安装根；Darwin 使用 macos-15。Darwin 根、子项目及消费者固定 Apple Clang、arm64 和部署目标 15.0。不同平台仅在 Release 中按不同文件名平铺共存，平台无关包继续使用 any/any。

## 组件职责

根 [CMakeLists.txt](../CMakeLists.txt) 和[构建入口](../src/autolib-builder/index.cmake)调用[共享工具准备](../src/autolib-builder/prepare-tools.cmake)，准备固定 Go SDK、builder、宿主 importer 和原样安装入口，再由 CMake/ExternalProject 执行构建。CI 零库模式通过同一工具准备的脚本入口执行，不调用模块生成器。模块声明、资源、补丁、依赖接线和安装机制归 builder；它也拥有安装副本的路径修正及归档机制，不认识托管平台或发布凭据。

`autolib-importer` 负责包选择、精确依赖遍历、HTTPS 下载、安全解包、校验和缓存准备，不复制独立安装来源或执行路径修正。单文件 [autolib.cmake](../src/autolib.cmake) 负责宿主 importer 引导、按需 FetchContent Git 快照及仓库检查、结果身份与闭包校验及包就绪后的 target 加载。adapter 使用已准备的依赖公共目标，不自行推断 sibling 版本目录、重新选择依赖或下载编译。

`autolib-ci` 通过 builder 取得精确根及闭包，分别保留有效、资格过滤和配方失败的根。每项按依赖顺序恢复或构建，经本平台独立消费验证后立即打包入库；两个 Eigen 版本使用独立消费者。某项失败只阻断其依赖者，无关项继续。结果区分入库、复用、非 publisher 本地就绪和失败；全部处理完毕与全部成功分别记录，最终汇总保留整体失败。

跨运行成品归档保存在当前源码仓库的固定 Release `autolib-cache`，完整记录保存在现有 Gitee 仓库的独立同名分支，绑定身份、来源、输入及依赖指纹、验证和最终字节。Gitee 是缓存读写的必要依赖，分支身份与并发规则见[存储设计](agent-notes/proposed/architecture/2026-11-10-gitee-build-cache-records.md)。源码决定身份与有效性；相关输入变化使缓存沿依赖链失效，单独修改 latest 不重建库。命中项恢复到新安装根并消费，不重新打包或改写来源；通用包由唯一 publisher 写入。Artifacts 保存诊断。运行级互斥保护缓存，平台并发写不同身份，共享准备负责初始化和容量预检。缓存约束由[Release 成品缓存设计](agent-notes/implemented/architecture/2026-11-09-release-cache-and-independent-gitee-sync.md)拥有。

## 构建与交付

普通根入口生成版本化安装结果，包含原样 `autolib.cmake`、宿主 importer、包内身份、精确依赖和包级 `package.toml`。CI 在安装验证及入库前将入口与当前构建提交的源码逐字节比对，结果记录入口 SHA256，汇总拒绝未验证或不同摘要的入口。本地编译结束即可消费，不要求打包、上传或源码树参与下游运行。实际安装失败以未完成消费描述阻止导入；复用目标仍执行消费准备，但不重新标记已冻结的依赖身份。

公开二进制沿用既有 CI 的单一平台 ABI 基准和发布前验证；本地产物结构一致不代表任意工具链二进制兼容。本地自定义配置及混用远端依赖时，由使用者保持兼容；包身份和摘要校验不替代 ABI 判断，不增加 ABI 变体。

本地打包读取安装中的精确身份和依赖，在副本修正生产路径并生成版本化 tar.zst、单程序 importer tar.xz 及 SHA256；不读取源码 latest 重选安装版本。只打包显式模块，调用者提供完整分发集合。原安装保持不变。

独立同步 workflow 在执行开始时固定触发分支的最新快照，再读取全部有效缓存。手动同步无需来源 run/attempt；整体成功构建自动触发同步，失败运行中已入库的成品也可供后续同步使用。两个 workflow 共用运行级互斥队列；同步只编译宿主只读工具，不编译库或 importer，不重新打包。单 Release 最多 1000 个附件，预检只为新归档预留槽位，远端已有附件全部计数；单文件须小于 2 GiB，容量不足明确失败，不驱逐有效成品或自动分片。

Gitee 同步只信任 Git JSON，sha256、size 和有序 urls 都相同才跳过，不回读 Gitee 归档。普通项按需下载缓存并校验字节，在有总时限的子进程中删除同名附件后上传，成功即由协调器在共用 Git 锁中提交并确认 JSON。超过 100000000 字节的库只登记 GitHub 地址，确认后清理 Gitee 同名旧副本；删除失败单独报告，下轮 JSON 相同也继续清理。依赖者等待递归精确依赖确认，无关项并发；缺缓存或失效单列待构建。主进程仅在 importer 实际上传后交付一次入口，再确认工具 JSON；入口无摘要。

源码明确删除的版本、包或平台身份触发两端精确清理，同时修复或删除悬空 latest。声明未知、缓存失效及列表读取失败都不能作为源码删除证据，未知附件受保护。latest 保持合法已有选择，不增加全平台齐备门槛；同版本替换及逐项 Git 提交不保证整组 ABI 原子切换。当前决策及远端验收缺口见[缓存与独立同步设计](agent-notes/implemented/architecture/2026-11-09-release-cache-and-independent-gitee-sync.md)。新建 Gitee 库默认私有，附件替换不提供回滚。

## 消费与缓存

入口的实际路径所在目录是唯一工具、包及下载缓存根。源码和本地安装入口的内置远端为空；发布副本带实际 Release 地址。用户变量不能覆盖来源，本地消费者直接 include 安装入口，远程消费者将发布入口放入专用可写、忽略版本控制的目录。

工具和包先查同根已有结果，只有必需输入不存在且内置远端非空时才下载。已有候选损坏、身份不符或校验失败直接报错，不覆盖或降级；远端包经暂存、安全解包和校验后提交到同根。完整精确闭包就绪后才加载 adapter 和公共 `autolib::<包名>` 目标，同次 configure 复用结果。

Go importer 只为实际包下载和解压打印过程，按 JSON 大小显示来源和进度，换源时清零；入口和工具成功获取安静，工具换源保留脱敏诊断。CMake 为每次成功导入打印消费闭包的库名和版本，包括缓存包及重复调用，单次调用内去重；格式见[开发指南](development.md#本地打包与消费)。

已有缓存及其 latest 不自动更新，补充新包只获取缺失部分；用户手动清理缓存后才重新选择。精确请求不改写缓存 latest，依赖不重选 latest。完整有效工具和闭包命中时零远端操作；严格禁止缺输入时联网须使用内置地址为空的入口。不回退系统包或源码编译，同根写入由调用方串行，不自动清理既有文件。

缓存 latest 仅在其指向的完整包仍存在时有效；删除目标版本目录后，该选择失效，重新按来源发现并在成功后保存新选择，不猜测其他缓存版本。包损坏仍报错，缺少依赖则只补齐已记录的精确依赖。

## 远端元数据与下载

需要远端时，CMake 浅克隆 Gitee 专用仓库默认分支，检查 schema 2 及整棵 Git 树，不执行仓库代码。工具和库记录包含 sha256、size 和完整有序 HTTPS 地址，消费者不拼归档 URL；普通项依次尝试 Gitee、GitHub，超限项只有 GitHub。空缓存工具引导与包依赖共用一份已检查快照，同次 configure 只获取一次；完整缓存零远端操作，快照损坏需重新 configure。

Go 区分来源错误与本地 I/O/父取消：来源失败可换源，受信字节的解包或身份错误直接失败。CMake 保持原生下载、大小与摘要校验循环，安全解包在循环外；入口和元数据仍依赖 Gitee。计时、记录与已接受更新窗口见[双来源设计](agent-notes/implemented/architecture/2026-11-09-dual-source-artifact-downloads.md)，消费不调用托管 API。

## 文档职责

本页拥有整体流程，正式设计拥有协议细节与长期理由；[路线图](roadmap.md)记录实施与验收范围。既有构建引擎见[流水线设计](agent-notes/implemented/architecture/2026-10-19-toml-externalproject-zlib.md)，平台环境和原生并行约束见[CI 系统设计](agent-notes/implemented/architecture/2026-10-20-ci-system.md)。后续设计与实现不得重新引入与本基准冲突的单版本槽位、消费 API 摘要查询或 latest 发布门槛。
