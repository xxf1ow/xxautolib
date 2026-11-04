# Agent Note: 版本化包与统一产物交付

Status: proposed

## 问题

同一个三方库的不同消费者可能需要不同版本，例如 PCL 与 sherpa 的独立项目分别需要不同 Eigen 版本。单个当前包槽位无法表达这种选择；全局替换一个库会让另一个消费者失去所需输入。

包既要在本地编译安装后直接被消费，也要在发布后被远程消费。消费选择、依赖身份和完整性校验不能依赖某个平台的 Release API 或正文聚合索引，否则只发布一个包仍需维护其他包记录，首次工具获取还会受到匿名 API 限流影响。

## 方案

采用精确版本包、独立 latest 指针、统一安装结果和专用 Git 元数据仓库。实际归档放在同仓库的固定 Release 中，按完整文件名定位；消费端只克隆元数据并下载归档，不编译源码。[架构](../../../architecture.md)是开发入口，本文保留版本、缓存、Git 元数据及前三阶段已验收的契约和证据。第四阶段由已验收的[Gitee 交付重做](../../implemented/architecture/2026-11-02-gitee-delivery-reimplementation.md)独立拥有；本文的第四阶段方案仅供参考，不要求兼容或并存两套实现。

[自定位消费入口与工具专用 CI](../../implemented/architecture/2026-11-03-entry-delivery-and-tool-only-ci.md)拥有已实现的入口安装、来源不可配置及缓存自定位，部分替代本文独立本地来源及四步选择；本文的版本、依赖、Git 快照、完整性校验和历史验证依据继续适用，历史复制或本地归档用例不表示这些路径仍可调用。

### 版本与依赖

包名加精确版本构成包身份。数字、SHA 等版本都按原字符串匹配，不排序、不补全、不去除 `v`，也不接受 SHA 前缀或版本范围。版本必须能安全用于版本目录和引用，不允许路径穿越；不通过改写字符串猜测用户意图。

版本统一使用 ASCII 字符，满足 `^[A-Za-z0-9][A-Za-z0-9_.+\-]*$`，并拒绝尾随点、Windows 保留设备名和固定文件名 `package.toml`。保留设备名按第一个点之前的部分、不区分大小写判断，包括 CON、PRN、AUX、NUL、COM1–COM9、LPT1–LPT9；固定文件名冲突同样不区分大小写判断。该规则排除空版本、路径分隔符、空白、`@` 和路径穿越。一个包的不同版本不得仅大小写不同；源码发现及读取外部安装、归档或元数据时，拒绝所处理包的此类路径碰撞，即使当前文件系统允许共存。合法版本仍按原字符串匹配，不把大小写折叠用于版本选择，也不自动修正非法声明。

构建选择、模块依赖和下游导入统一使用 `包名@版本`，省略版本时从对应来源的 `package.toml` 读取 latest。版本化引用格式如下：

```text
BUILD_LIST=eigen@3.4.0,pcl
depends = ["eigen@3.4.0", "boost", "zlib"]
autolib_import(TARGET app PRIVATE eigen@3.4.0 zlib)
```

`BUILD_LIST` 留空时发现全部配方版本；显式包名不带版本时只选择 latest，所选包自动带入依赖。不增加 `BUILD_VERSIONS`、版本范围求解或另一套全版本/latest 开关。没有版本号的配方补充明确版本，不以兼容现有目录和旧消费协议为约束。

构建阶段将无版本依赖解析为精确版本，安装包记录精确直接依赖。下游只遍历包内依赖，不能在消费时重新选择这些依赖的 latest。包内身份包含包名、版本、平台和精确依赖，必须与实际请求及所选产物一致，不能只相信文件名。

不同版本可分别构建、发布和导入，不做同一消费工程中的多版本隔离。同一消费闭包或同次配置已经加载的同名包出现不同版本时直接报错，不重命名上游 target 或自动覆盖。全版本构建把各配方视为独立构建根，允许两个根分别依赖不同版本，不对整份构建列表施加消费闭包的单版本限制。

### 源码与安装布局

各版本处于相同层级，根目录不隐含 latest。包级 `package.toml` 只维护独立选择，例如 `latest = "5.0.1"`：

```text
src/dependencies/eigen/
├─ package.toml
├─ 3.4.0/
│  ├─ module.toml
│  └─ autolib-import.cmake
└─ 5.0.1/
   ├─ module.toml
   └─ autolib-import.cmake

3rdparty/
├─ autolib.cmake
├─ autolib-importer-…
├─ eigen/
│  ├─ package.toml
│  ├─ 3.4.0/
│  │  ├─ …
│  │  └─ autolib-import.cmake
│  └─ 5.0.1/
│     ├─ …
│     └─ autolib-import.cmake
└─ 其它包/
```

持久源码、资源、安装、生成目标及构建中间目录的身份都区分版本，不能让 sentinel 或同名源码缓存复用另一个版本。包内 adapter 继续声明精确的上游导入机制，不通过扫描任意 Config 或系统同名库猜测入口。

本地和 CI 都只进行原生构建，不存在交叉编译。Windows x86_64、Linux x86_64、Linux aarch64 各自使用独立工作目录与安装根，不跨平台复制整个工作副本或共用安装目录。上述安装布局不增加平台目录；远端不同平台归档才在 Release 中以不同文件名平铺共存。平台无关 copy 包沿用 any/any 身份与唯一 publisher，不把它当作任意机器码包的回退规则。

### ABI 支持范围

公开二进制沿用[CI 环境与 ABI 基准](../../implemented/architecture/2026-10-20-ci-system.md#环境与-abi)：Linux 使用 Ubuntu 20.04、GCC 9.4、glibc 2.31 及官方配套的动态 C++ 运行库，不切换旧字符串 ABI；Windows 使用 VS 2022 x64/v143、Release、`MultiThreadedDLL`（`/MD`）。生产者、依赖和验证消费者使用相应基准，继续执行既有适用的符号需求检查及真实链接运行验证，不因版本化交付取消这些发布前检查，也不承诺工具集或 SDK 补丁版本完全固定。

本地与 CI 保证相同的产物结构和消费协议，不保证任意本地工具链、CRT 或编译选项生成的二进制都与公开产物兼容。本地允许沿用父构建的显式配置；使用者负责让消费者和依赖闭包保持兼容，混用本地与远端产物时也适用。包身份与 SHA256 校验不证明 ABI 兼容，不新增 ABI 变体、通用 ABI 自动判定或强制本地配置等同 CI 的限制。

### 统一构建结果

普通根构建与 CI 构建使用相同配方和引擎，得到同样结构的安装结果：原样 `autolib.cmake`、宿主 importer、包文件、已完成的消费描述、精确依赖及包级 `package.toml`。路径修正属于 builder 安装准备和副本打包，importer 不嵌入或执行搬迁脚本。不要求下游拥有 builder、生产源码树、Go SDK、Python 或独立的 prepare 操作。

只有已完成安装及消费准备的包可消费；失败安装不能因残留目录或 sentinel 被当作有效结果。平台、精确依赖和导入文件在复制、打包、解包及消费时保持一致。归档生成后才计算 SHA256，不要求编译前生成归档摘要。

CI 编译结束后自动触发独立发布流程。跨 job/workflow 的运输封装保留 Linux 软链接与权限，不改变统一安装结果；发布流程调用既有暂存修正和归档机制，再上传和提交元数据。本地需要归档分发时使用同一机制，但直接消费安装结果不要求先打包。

### 消费来源与缓存

`autolib.cmake` 的实际路径所在目录是唯一工具、包及下载缓存根。本地消费者直接 include 安装入口；远程消费者把发布入口放在专用可写目录，例如不纳入版本控制的 `3rdparty/`。源码及本地入口内置远端为空，发布副本带实际目标；用户来源变量不能覆盖它。

工具和库先检查同根已有结果，必需输入不存在且入口内置地址非空时才获取远端。精确版本只匹配该版本；裸名先使用有效缓存 latest，再使用远端 latest，没有 latest 时不从现有版本号猜测。远端选择后缺少对应产物即失败，不换版本。

仅不存在才继续下一来源。候选损坏、身份不符、已完成标识不成立、归档或摘要校验失败均直接报错，不隐式降级来源、系统库或源码构建。依赖按包内精确版本分别走相同优先级，完整依赖闭包准备、校验通过后才返回 CMake 加载 adapter。

远端归档在暂存区安全解包，验证成功后才成为完整缓存，失败不留下正式包。Linux 保留包内合法相对软链接，绝对链接及越界链接拒绝；Windows 不强求软链接等价支持。builder 仍在打包副本修正生产路径，原安装和运输解包输入保持不变。

远端取得的包最终留在入口目录。无版本请求成功后保留其 latest 选择；已有缓存的包和 latest 不自动更新，新增请求只补缺失的包及依赖。显式精确版本请求不会自动改写 latest。用户手动清理对应缓存后才重新获取，不增加后台更新或自动升级。

缓存 latest 是已成功获取包的版本选择记录，仅在所指向的完整包仍存在时有效。用户删除该版本包目录但保留 `package.toml` 时，该选择失效；无版本请求从内置远端重新发现，成功准备后写入新的缓存选择，失败不登记。包目录存在但损坏或未完成时仍直接报错，不能当成删除；根包完整但缺少依赖时，只补齐包内记录的精确依赖，不重新选择根包版本。没有有效 latest 时不从其他已缓存版本猜测默认值，重新选择也不刷新无关包或其他仍有效的 latest。

完整有效工具和闭包命中时不访问远端，即使入口带发布地址。内置地址为空时缺输入直接失败；远程入口不提供强制离线开关。同次 configure 复用准备结果。入口目录在补包时必须可写，同根写入由调用方串行，不增加并发写锁或恢复数据库。

### CMake 与 importer 的远端交接

消费端仅由 CMake 使用 FetchContent 负责 Git 克隆，不引入 Go Git 库，也不在 importer 中保留系统 Git clone、pull 或隐式重克隆逻辑。远端消费默认已有系统 Git；这不表示安装 CMake 会自动安装 Git。本地工具及完整包闭包可用时不获取远端快照。

importer 先检查入口同根已有包，尚需远端但没有快照时向 CMake 返回明确的“需要元数据快照”结果，而不是自行 clone 或把网络缺失当作包损坏。CMake 获取并检查快照后，将同一路径交给 importer 继续准备。该交接是内部工具接口，不新增用户必须配置的路径或命令。

内部 `prepare` 以退出码 `2` 表示需要快照，CMake 经 `--metadata-root` 传入已检查路径后继续；其余失败不得作为获取请求重试。该路径不对用户公开，也不是 importer 的 Git 获取开关；消费 CLI 不接受 `--source-response`。

首次缺少 importer 时，CMake 不依赖尚未取得的工具：先准备同一份元数据快照，读取宿主工具摘要，下载校验后使用 CMake 原生能力解包到入口缓存，再运行 importer。后续包和依赖使用已准备快照，不再次获取仓库。消费端不执行元数据仓库中的构建代码。

每次需要远端的新 configure 都重新浅克隆默认分支，不让 FetchContent 的跨配置 stamp 代替新快照。同次 configure 只获取和检查一次，并让快照存活至本次配置结束。尚未准备快照时由 CMake 获取；已经准备的快照意外丢失或损坏时本次配置报错，显式重新 configure 再取，不增加同配置的自动恢复流程。

快照仅包含小型元数据，不包含实际归档或源码；浅克隆不获取完整提交历史。本地工具及完整包闭包命中时不依赖 Git 服务；需要新远端输入时接受重新获取元数据的网络成本，不增加跨 configure 的旧快照回退或另一套新鲜度策略。

### Git 元数据与 Release 布局

GitHub、Gitee 都使用专用公开产物仓库，不在源码仓库中混放元数据。仓库工作树只保存 README、仓库身份、包级选择和独立摘要，实际归档位于同仓库的固定 Release：

```text
产物仓库/
├─ README.md
├─ LICENSE
├─ .autolib-repository.json
├─ autolib-importer/
│  └─ autolib-importer-….tar.xz.sha256
└─ eigen/
   ├─ package.toml
   ├─ 3.4.0/
   │  ├─ eigen-3.4.0-linux-x86_64.tar.zst.sha256
   │  └─ eigen-3.4.0-windows-x86_64.tar.zst.sha256
   └─ 5.0.1/
      └─ …
```

latest 在源码、安装结果和远端元数据中使用相同的包级 `package.toml` 格式，区别只在来源。每个 `.sha256` 文件仅保存对应归档的 SHA256；去掉该后缀就是完整资产文件名。库包使用版本化 tar.zst，工具使用现有宿主命名的单程序 tar.xz。不再用 JSON 重复存储包名、版本、OS、架构、附件 ID 或 URL，也不在 Release 正文聚合摘要。

已落地身份文件为 `{"type":"xxautolib-artifacts","schema":1,"upstream":"https://github.com/xxf1ow/xxautolib"}`，消费入口核对这三个字段的类型及精确值。包级选择只有 TOML `latest` 字符串；独立摘要为 64 位小写十六进制 SHA256 加 LF，分别位于 `autolib-importer/<工具归档名>.sha256` 与 `<name>/<version>/<完整库归档名>.sha256`，Windows 工具保留 `.exe.tar.xz` 命名。消费端统一忽略摘要首尾的空格、Tab、CR、LF，再严格校验，不删除内部字符；工具目录不接受包级 latest，也不回退 tools 路径。未登记摘要视为无该资产，摘要非法或已登记资产下载 404 直接失败，不换版本或来源。LICENSE 是合法根文件；初始化及发布端规则由[Gitee 交付记录](../../implemented/architecture/2026-11-02-gitee-delivery-reimplementation.md)拥有。

发布和消费根据精确包版本、目标平台及既有平台无关规则定位归档文件名，包内身份是第二层校验。文件名必须安全、可确定且在 Release 中无歧义，不通过包含匹配或任意子串选择资产。固定 Release 不按每个版本新建 tag；版本由归档名与 Git 元数据目录表达。

两托管的远端 base URL 统一为 `https://<github.com|gitee.com>/<owner>/<repo>/releases/download/<encoded-tag>`，据此推导仓库 `.git` 地址与精确资产 URL。拒绝凭据、query/fragment、额外路径及 `/releases/tag/`，不增加兼容转换；仅实际需要远端时解析，未使用的远端配置不阻止完整本地路径。消费不调用 GitHub contents、Release 或附件 API，不使用 Gitee Raw、发行版正文、静态站点或 Git 仓库中的源码发现摘要；也不自动跨来源回退。GitHub 和 Gitee 的区别限于发布端的建库、Release 和上传 API 以及写入凭据。

### 专用仓库初始化与检查

Gitee 目标继续读取 `GITEE_REPOSITORY`，写入凭据读取 `GITEE_TOKEN`。该凭据需要创建仓库、推送提交和管理 Release 的权限。独立 GitHub 目标使用同一元数据协议，另配目标仓库的写入凭据，不假设源码仓库的默认 `GITHUB_TOKEN` 具备跨仓库权限。

发布任务在启动时获取并固定目标。目标仓库确实不存在时创建公开仓库，`auto_init=false`，由项目提供自定义 README 和身份文件作为初始化提交；仓库介绍和 homepage 指向 `https://github.com/xxf1ow/xxautolib`。首次上传时自动创建所需 Release tag，不要求用户预先建库或创建 tag。权限、认证、网络、限流等错误不能被当作仓库不存在。

README 面向使用者说明产物用途、源码地址和使用方式，不写内部“禁止混入源码”的校验规则。身份文件声明仓库类型、schema 和上游源码地址；其预期身份由消费入口及发布端共同核对，不能仅凭仓库名或 README 判断专用性。

每份仓库快照只执行一次身份和内容检查。读取身份文件，并以 `git ls-tree -r -z HEAD` 核对允许的路径及对象类型，不遍读所有文件、不扫描历史、不为每个包再 clone 或调用 API。已有仓库缺少身份或混入其他内容就拒绝，不能自动认领、覆盖 README 或清理用户内容。发布端在任何 Release 写入前完成检查。

每个发布任务只在开始时 clone 一次，后续上传和元数据提交复用本地 Git 工作副本；新建仓库复用初始化使用的本地仓库，不再 clone。只有发布工作流操作目标，且同一目标不并发两个发布任务；latest-only 更新也通过同一写入通道，不另设并发写者。不引入分布式锁、rebase 或多写者合并协议。

### 产物发布与 latest 更新

源码仓库停止后续二进制 Release 上传，以构建 artifacts 交接安装结果到独立发布任务；不要求保留 GitHub 源码仓库 Release 作为 Gitee 同步源。自动发布使用触发它的那次构建结果，手动重跑不必重编三方库，不误取另一次运行的产物。历史 Release 和现有本地数据不自动迁移或删除。

发布任务使用构建结果携带的消费信息及包级元数据，调用统一打包机制生成归档和摘要。归档上传成功后，只更新其独立 `.sha256` 文件；并行上传结束后由协调器一次 commit/push。未选择的文件保持原样，不整树覆盖，不聚合重写其他包记录。部分上传失败也提交成功项，任务整体报告失败。

latest 是独立选择，不是产物发布完成标志。可以没有编译或上传输入，仅提交选中包的 `package.toml`；不要求对应版本产物存在或所有平台齐备。产物上传成功不自动推进 latest，上传失败也不构成阻止独立 latest 变更的条件。两类变更可以同一次提交，但各自根据本次明确选择决定内容；只发布旧版本或补平台产物不隐式改变 latest。

latest 指向的版本没有当前平台产物时，消费按缺包失败，不换版本。远端 latest 改变不刷新已有消费缓存；消费者要获得新选择，由用户手动清理缓存。

同包名、版本和平台继续允许同名替换。先更新附件、后提交摘要可能短暂不一致；摘要不符直接失败，不导入不匹配字节。不增加不可变修订、备份回滚、跨附件事务或内容寻址。元数据 push 失败使任务失败，已经上传的附件不回滚；允许有限重试，仍未确认时显式重跑。重跑不能只因附件存在就跳过摘要登记，必须补齐有效的提交。

重跑以明确选定的原构建结果为输入，重新生成待上传归档并计算实际上传字节的 SHA256；不要求重新打包与上次产生完全相同的归档字节。同名附件即使已经存在，也重新完成本次上传，成功后才登记与本次字节对应的摘要；不能仅凭附件存在、名称或大小认定完成，也不能把旧附件配上重新打包后的摘要。重跑从本次启动时获取的仓库状态更新成功项，保留无关记录；确认元数据 push 成功才报告本次相应发布成功。若没有可用的原构建结果则明确失败，不从另一次构建猜测输入；不另存发布进度数据库，也不恢复旧附件。

### 设计归属与实施范围

本文对后续开发部分替代[按名称固定槽交付](2026-10-20-name-only-artifact-delivery.md)、[双来源包消费](2026-10-24-manifestless-package-consumption.md)、[按目标导入](2026-10-25-target-import-and-release-consumption.md)、[工具压缩分发](2026-10-27-importer-distribution-and-branch-release.md)、[独立 Gitee 正文索引同步](2026-10-27-gitee-release-sync-workflow.md)、[双平台 API 消费](../../implemented/architecture/2026-10-27-ci-discovery-and-release-sources.md)及[CI 系统](../../implemented/architecture/2026-10-20-ci-system.md)中的单版本布局、消费获取和发布交接规则。相关记录仍保留独立的构建、adapter、平台环境、权限和验证依据，不把目标设计写成已实施事实，也不归档仍约束当前代码的决定。

只按本设计调整相关声明、构建身份、消费协议、打包与发布交接；不顺带重写构建引擎或上游参考 Action，不新增版本范围、同工程多版本隔离、ABI 变体、交叉编译、生产缓存或通用恢复服务。后续新包接入遵守本基准，sherpa 的接入仍须另行完成与验收。

本文不授权 Git push、远端合并、触发 CI、上传附件、创建实际远端仓库或覆写已有数据。书面设计确认后，按任务分支及首提交规则保存设计，再交接实施计划和执行方式，不在文档更新阶段开始产品实现。

### 实施阶段

本次改动贯穿 builder、importer、CMake 入口、CI 计划、发布工作流及现有配方布局，按以下四个可独立观察的闭环分阶段，不把目录迁移单独作为完成阶段。整体设计在各阶段共用，不增加阶段状态协议或过渡兼容层。

1. **版本化构建与安装。** 完成版本声明和路径校验、配方布局迁移、全版本及 latest 构建选择、版本化源码与构建目录、精确依赖和统一安装身份。以两个 Eigen 版本和代表性依赖闭包证明独立构建、安装互不覆盖、依赖固定及单闭包冲突拒绝；全版本构建不误判独立根之间的版本差异。此阶段不依赖远端发布或新消费者。
2. **本地消费闭环。** 下游不持有生产源码，直接 include 安装入口并消费同根精确闭包，完成链接及适用运行检查；确认安装只读、Linux 相对软链接保留、完整结果离线可用。独立本地来源复制及本地归档填充由自定位入口决定替代，历史证据见后文，不要求恢复已退役路径。
3. **Git 元数据远端消费。** 专用仓库检查、FetchContent 单份浅克隆、首次 importer 获取及 Release 归档消费共用快照，消费不使用 API 摘要发现。受控夹具验证同根已有结果优先、精确身份及摘要、配置内复用及下一轮按需获取；真实平台网络及发布消费独立验收，不以夹具替代。
4. **CI 与独立仓库发布。** CI 发现和计划适配版本化身份，构建结果改用保留权限及链接的运输封装，发布端从该次构建结果打包，完成 GitHub/Gitee 专用仓库发布、Gitee 从零建库、成功项批量提交、latest-only 更新和同名重跑。停止源码仓库的新二进制上传；按另获授权的真实远端操作验证三平台及两种托管的端到端交付、部分失败和元数据提交失败后的重跑，并执行整体回归。

每阶段开始前只编写该阶段实施计划；结束后由用户验收成果，检查后续设计是否受实际发现影响，再确认下一阶段。定向验证覆盖本阶段及受影响接口；没有影响结论的变化时复用既有证据，不在每阶段重复全量回归或重编全部第三方库。sherpa 接入仍是本架构交付后的独立任务，不混入这些阶段。

## 备选方案

**通过数字或 SHA 推断最新版本。** 数字、tag 和 SHA 不共享可靠的排序规则；选择显式 latest，保留精确字符串匹配。

**根目录版本隐含 latest。** 可减少一次目录迁移，但把选择与存储位置绑定，版本不再平等，也不便独立更改指针；选择包级 `package.toml`。

**Release API、正文聚合索引、Raw 或静态页面分发元数据。** API 会受到匿名限流，正文更新要求保留重写其他包记录，Raw 与静态服务也不是已确认的可靠获取基础；选择专用 Git 仓库及独立文件。

**在 importer 中再维护 Git 获取和自动重克隆。** 会与 CMake 首次工具引导重复获取职责，并可能让同配置使用不同快照；选择 FetchContent 单一负责者，快照丢失时显式重配。

**不可变产物修订或发布事务。** 可以缩小覆盖期间的失败窗口，但当前接受缺包或摘要不符时报错及显式重跑，不增加修订、回滚或额外发布状态。

## 验收标准

- 两个 Eigen 版本可分别构建、打包、发布和由独立消费者导入；默认全版本构建与无版本 latest 选择均有效，显式版本严格匹配，包内依赖不被消费端 latest 改写。同一消费闭包版本冲突明确失败。
- 版本非法字符、保留名称、固定文件冲突及仅大小写不同的版本在三个平台采用相同拒绝规则；合法版本保持原字符串精确匹配。公开产物沿用既有 ABI 基准及适用验证，不把本地与 CI 的结构一致当作任意工具链兼容保证。
- 本地及 CI 产物结构一致。下游直接 include 安装入口，不打包、不联网、不读取生产源码即可链接运行，安装树不变；Linux 链接和软链接有正样本验证，路径修正只属于 builder。
- 同根已有结果优先，完整工具和闭包零远端、坏候选拒绝、缓存命中不更新及补包不覆盖均有定向验证；内置地址为空时缺输入失败，不增加来源覆盖开关。
- 删除缓存 latest 指向的版本目录后，无版本请求重新发现并在成功后保存新选择；损坏包不触发重选，缺依赖只补精确依赖，显式版本请求不改 latest，无有效 latest 时不猜测其他缓存版本。
- 空缓存首次获取 importer 与库包共享一份已检查快照，同配置多次请求和依赖复用不重复 clone，新配置确实重新浅克隆；本地完整路径零远端请求，快照意外缺失不在 importer 中重克隆。
- GitHub、Gitee 使用同一元数据夹具与实际归档通过消费，消费者不访问 Release/contents API；归档摘要、包内身份、安全解包及失败清理有效，工具必须在校验后执行。
- 发布支持从零创建专用仓库、自定义初始化内容、已有专用仓库复用与非专用仓库拒绝；每任务仅一份 Git 工作副本，批量成功摘要一次提交，单包操作不覆盖无关记录，部分失败与 push 失败可显式重跑补齐。
- 同名附件存在不短路重跑；重跑以选定构建结果重新完成上传，摘要对应本次上传字节，确认 push 后才报告成功，不能将旧附件与新摘要组合，缺失原构建输入时明确失败。
- latest-only 更新不要求任何产物操作或平台齐备；单独发布产物不隐式改变 latest，latest 与产物部分失败互不构成发布前置条件。指针无对应平台产物时按缺包失败，已有缓存不自动刷新。
- 只读检查、fixture 或上传响应不替代真实建库、上传、下载字节及消费验收；真实远端写入按单独授权执行，各平台和最低 CMake 的结果分别记录。

### 已有机制验证

Windows、本机 CMake 4.2.3 和 Git 2.37.1 下，临时本地仓库的 FetchContent 实验连续执行两轮 configure：同次两次获取复用同目录与提交，即使上游中途产生新提交也不切换快照；下一次配置使用新目录并取得新提交，两轮提交深度都为 1。该证据只覆盖重新浅克隆和配置内复用机制，不证明真实网络、最低 CMake、Linux、本方案的端到端消费或发布已通过。

### 版本化构建与安装验证

2026-10-30，Linux x86_64、GNU 9.4.0、CMake 3.31.12、Go 1.27.3 上，`cmake -P src/autolib-builder/go-sdk.cmake -- test ./src/autolib-builder -run '^TestVersion' -count=1 -v` 通过 17 个顶层测试、61 个节点；`vet ./src/autolib-builder` 通过。声明发现覆盖 30 包、31 配方，Eigen latest 为 3.4.0，PCL 固定 `eigen@3.4.0`。

[版本测试](../../../../src/autolib-builder/versions_test.go)验证引用、跨平台保留名称及目录身份；[根入口测试](../../../../src/autolib-builder/entry_test.go)验证全部版本、裸名 latest、精确选择及重配置；[原生安装测试](../../../../src/autolib-builder/versioned_build_test.go)验证独立根分别使用两个 Eigen 版本、精确共享依赖仅安装一次、其他版本 sentinel 不被复用及 latest 未安装时显式旧版本仍成功。安装 marker 由实际编译运行的程序读取所选依赖头文件生成；将 5.0.1 的编译 include 错接到 3.4.0 时，右根 marker 断言失败，恢复接线后通过。闭包冲突、冻结依赖、reuse 拒绝重标依赖和生成文件无碰撞也有正负样本。

真实 Eigen 使用普通根入口，从空的专用 build/install 根完成配置与安装：

```sh
env AUTOLIB_INSTALL_ROOT=/home/lxx/xxautolib/build/versioned-stage1-install cmake -S . -B build/versioned-stage1 -G "Unix Makefiles" "-DBUILD_LIST=eigen@3.4.0,eigen@5.0.1"
cmake --build build/versioned-stage1 --config Release
```

两个版本前缀分别包含真实头文件、`share/eigen3/cmake/Eigen3Config[Version].cmake` 和 `share/pkgconfig/eigen3.pc`；版本头、Config/pkg-config 均为 3.4.0、正式 5.0.1，五行安装身份分别记录 eigen、精确版本、linux、x86_64 和空依赖。包级 latest 保持 3.4.0。旧 `3rdparty/eigen` 在构建前后使用同一排序 tar 流检查，SHA256 均为 `b4a2852da34d3dba5cdc71ba934019db44a0400bc3e9e5664df2ce5b3c245587`；没有迁移或覆盖旧安装。

命名规则在该 Linux 主机上验证，不代替 Windows 文件系统、Visual Studio 或 Linux aarch64 实机验收；最低 CMake 3.20 也未实测。真实 PCL、Boost 全库、其他库的版本化重建及 CI 未运行。当次既有消费/打包测试有九组旧协议缺口，不表示 builder 全集通过；本地消费证据见下一节，Git 元数据快照及 CI/发布接口仍属于后续阶段。

### 版本化本地消费验证

2026-10-30，Linux x86_64、GNU 9.4.0、CMake 3.31.12、Go 1.27.3 上，以下阶段增量均退出 0：

```sh
cmake -P src/autolib-builder/go-sdk.cmake -- test ./src/autolib-builder -run '^TestVersioned(StageFixup|LocalRelease|ReleaseUsesInstalledIdentity)$' -count=1 -v
cmake -P src/autolib-builder/go-sdk.cmake -- test ./src/autolib-importer -run '^TestVersioned' -count=1 -v
cmake -P src/autolib-builder/go-sdk.cmake -- vet ./src/autolib-builder ./src/autolib-importer
```

builder 为 3 个顶层测试、17 个节点，importer 为 15 个顶层测试、130 个节点。另以 `-run '^(TestRepresentativeAdapters|TestAdapterExistingTargets|TestAdapterDependencyOrder|TestInstalledValidationDriver|TestVersionedLocalLoop)$' -count=1 -v` 通过 5 个顶层测试、15 个节点；代表集 fixture 原样使用生产 adapter，不改写其依赖路径。`TestInstalledValidationDriver` 检查精确 roots、完整列表参数、独立入口缓存及来源不变，缺工具或描述时通过公共入口失败。

[原生闭环](../../../../src/autolib-importer/versioned_local_loop_test.go)由普通根入口安装带版本标识的轻量依赖和共享库，再由真实 builder 打包。安装复制及归档两条路径各自真实链接运行，头版本与机器码版本同时为 1；将缓存头改为版本 2 时，编译仍成功而运行明确报告 `header_dep=2 linked_dep=1` 并失败，恢复后通过。隐藏生产树并搬迁完整入口缓存后，两源均空仍能重新 configure/build/CTest；dladdr 实际加载位置属于当前缓存，来源工具/归档和无关缓存的内容、权限及 mtime 不变。

真实代表集使用新的生产及安装根，不写第一阶段或旧 `3rdparty/`：

```sh
env AUTOLIB_INSTALL_ROOT=/home/lxx/xxautolib/build/versioned-stage2-install cmake -S . -B build/versioned-stage2-producer -G "Unix Makefiles" "-DBUILD_LIST=eigen@3.4.0,eigen@5.0.1,queue,flann"
cmake --build build/versioned-stage2-producer --config Release
```

Eigen 3.4.0 和 5.0.1 在独立消费者各通过头版本、简单矩阵及 CTest 1/1。queue、FLANN C/C++ 与 LZ4 的真实 API 经安装复制、归档导入、缓存搬迁离线和 `RELEASE_READY=ON` 消费验证；各次 CTest 均 1/1。只请求 flann、queue，公共入口准备 `lz4;flann;queue`，安装描述固定 `lz4@1.10.0`；导入属性和实际链接输入指向当前缓存的 lz4/1.10.0、FLANN 精确版本目录，不含生产前缀，dladdr 报告当前缓存的 `libflann.so.1.9.2`。

Linux 的 `libflann.so` 和 `libflann_cpp.so` 各经 `.so.1.9` 指向 `.so.1.9.2`，共四条相对软链接，在安装、复制和搬迁后的归档缓存中保持。仅删除测试缓存 latest 指向的 FLANN 版本目录后，裸名请求成功重发现；共享 lz4 和无关 queue 的内容、类型、权限、mtime、软链接及 latest 不变。安装消费前后完整 metadata 清单 SHA256 均为 `149b9c54df4d4be60ee0dcb5eb0d40d30b2daeba9301757cf499ad765dec9346`，逐文件字节清单 SHA256 均为 `45e2ea0ee38edd87695d05e1b1eb40ec8ded03b34cbb858cc58b36d073033321`。该基线取自获准 adapter 修复并经普通复用目标刷新消费描述之后，区分生产准备写入与下游只读消费。

真实消费曾复现 FLANN adapter 的无版本 sibling 路径错误；adapter 依赖公共入口已校验并按顺序加载的精确 lz4 结果和公共 target，不自行 include 依赖描述。固定上游来源在 109 遇到 TLS 中断，经授权在本机预取精确 commit、核对干净逐文件字节，再仅传上游 bundle 到新/空目标后增量续跑；没有改配方、来源协议或上游代码。

Windows 文件系统/Visual Studio、Linux aarch64 和最低 CMake 3.20 未验证；Linux 原生运行不算作这些平台证据。未重编 PCL、Boost、OpenCV 或全库，未运行旧远端/CI 全集，未执行 Git 元数据获取、发布、push 或远端 CI。上述结果只覆盖本地消费阶段，整体设计仍 proposed，阶段成果交由用户验收。

### 版本化远端消费验证

Linux x86_64、GNU 9.4.0、CMake 3.31.12、Go 1.27.3 上，第三阶段定向命令均退出 0：

```text
cmake -P src/autolib-builder/go-sdk.cmake -- test ./src/autolib-importer -run '^(TestVersionedRemoteLoop|TestGitHubValidationDriver)$' -count=1 -v
cmake -P src/autolib-builder/go-sdk.cmake -- test ./src/autolib-importer -run '^TestVersioned(Metadata|Snapshot|Remote|NoConsumerAPI)' -count=1 -v
cmake -P src/autolib-builder/go-sdk.cmake -- test ./src/autolib-importer -run '^(TestVersionedLocalLoop|TestVersionedRejectSourceResponse|TestCMakeRemoteBootstrapMigration|TestCMakeGiteeSnapshotReuse|TestGitHubValidationDriver)$' -count=1 -v
cmake -P src/autolib-builder/go-sdk.cmake -- vet ./src/autolib-importer
```

三组分别通过 2 个顶层测试/7 个节点、12 个顶层测试/168 个节点、5 个顶层测试/27 个节点；组间包含重叠用例，不合计为独立测试数量。元数据与快照组覆盖来源解析一致性、独立摘要/身份检查、gitlink 与非白名单内容拒绝、不执行仓库代码、单份浅克隆及配置内复用、快照丢失失败和下一配置重新获取。远端组覆盖四步选择、精确依赖、损坏候选立即失败、TLS/下载/工具引导及零 API/Raw 请求；CLI 删除另由显式回归覆盖，消费源码的旧 API/正文发现缺失以定向静态检索及审查确认，不以读取源码文本的自动测试代替行为。

[远端原生闭环](../../../../src/autolib-importer/versioned_remote_loop_test.go)复用本地轻量库和版本断言，通过普通根入口及真实 builder 生成 probe@1、dep@1/2 和 importer tar.xz；元数据保存与 builder 相同的实际归档摘要。生产树在消费前隐藏，两托管各从空入口缓存获取工具和 `dep;probe` 精确闭包，dep 的远端 latest 为 2 也不改变 probe@1 对 dep@1 的要求。首次及完整缓存复用均真实链接运行，输出 `header_dep=1 linked_dep=1`；导入头、链接文件和 dladdr 的实际加载位置属于当前缓存的 dep/1、probe/1，而非生产前缀。

每托管首次只有 1 次 clone/ls-tree 和 3 次 HTTPS 下载（工具、dep@1、probe@1）；复用完整缓存时 HTTP/Git 增量均为 0。后续新 configure 请求 dep@2 时获取新快照，另一次请求有效摘要但包内名称错误的归档时也获取新快照，公共入口在 adapter marker 写入前拒绝并清理暂存，未登记正式包。最终每托管为 clone/ls-tree 各 3 次、5 次下载、API/Raw 0 次。错误头版本的运行拒绝由本地原生闭环既有双版本断言拥有，不新增相同故障实验。

完整入口缓存复制到新的含空格消费者后，关闭 HTTPS 服务并使测试 Git 来源不可达，两源均空重新 configure/build/CTest 各 1/1；HTTP/Git 增量仍为 0。生产源码、安装和归档、Git 元数据源、已准备 dep@1 及无关缓存的字节/类型/权限/mtime 保持不变，Linux 相对软链接随归档和复制保留。[下载 driver](../../../../tests/autolib/run-github.cmake)从源码根只复制入口与 smoke 至独立消费者，读取实际 `prepared-state.cmake` 运行库目录，由公共入口准备缓存；两托管的 driver 参数、精确运行路径及公共入口失败传播均通过实际脚本执行断言。

收尾简化合并重复测试，并在下载函数内固定二进制请求头；按用户决定，仅作静态检查，未重新编译或运行测试。上述运行结果是简化前的验证证据。

以上是受控本地 Git/HTTPS 与 Linux 原生结果，不是 GitHub/Gitee 真实托管通过。Windows 文件系统/Visual Studio、Linux aarch64、真实两托管及第四阶段 CI/发布均未验证；最低 CMake 3.20 未实测，按用户决定不安排任何形式的该项测试、不取得旧工具，仅以 109 现有 CMake 3.31.12 验收，历史未测事实保留。整体设计仍 proposed，第三阶段已通过用户验收，第四阶段设计须依据实际发现再确认。

## 风险

Git clone 仍依赖系统 Git 与目标平台网络，不保证服务永不限流或始终可达。摘要来自专用仓库写入者，只保证下载字节与该记录一致，不认证发布者或防止元数据和产物同时被恶意修改；身份与内容检查用于防误配，不是可信签名系统。

latest 可以独立指向尚无产物的版本，同名替换或提交失败也可能让新消费者无法导入；若不显式重跑修复，不一致可以持续存在，不承诺自动恢复。这是允许的失败状态，不承诺跨包、跨平台或 GitHub/Gitee 的原子发布与整体发行快照。已有缓存不会追赶修复，必要更新由用户清理后重取。

旧配方、目录和包头不增加兼容适配或自动迁移。实施时仍须保护实际读写的已有数据，并让新的入口、工具及元数据协议成套交付。设计更新本身不证明旧格式可被新工具消费，也不扩大任何历史验证结论。
