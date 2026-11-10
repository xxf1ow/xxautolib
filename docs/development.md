# 开发

先准备 Git、CMake（3.20+）和 C/C++ 编译器，通过根入口构建安装。Go SDK 和固定模块自动准备；FLANN、HarfBuzz 消费和 OpenCV 构建需要 pkg-config，FLANN 使用 OpenMP，FFmpeg 按机器环境探测。builder 和普通 Go 测试不需要 Python 或 uv；发布及其整链夹具需要 Python 3.12，技能校验器自行准备环境。

## 准备与配置

首次 configure 联网获取官方 SDK 1.27.3，按仓库固定摘要校验并准备 Go 模块；不需要系统安装 Go、设置 PATH 或手工下载依赖。SDK目录为 `.tools/go/sdk/go1.27.3/<宿主平台>/go/`，模块与编译缓存也在 `.tools/go/`，均被 Git 忽略，清理 build 不影响它们。版本由 go.mod 与 SDK 元数据固定，不自动升级。

Windows x64、Visual Studio 2022：

```text
cmake -S . -B build -G "Visual Studio 17 2022" -A x64 -T "v143,host=x64"
cmake --build build --config Release
```

Linux、GCC：

```text
cmake -S . -B build -G "Unix Makefiles" -DCMAKE_BUILD_TYPE=Release
cmake --build build --config Release
```

Darwin ARM64（Apple Silicon），使用固定 CMake 3.31.12、GNU Make 4.4.1：

```sh
cmake -S . -B build -G "Unix Makefiles" \
  -DBUILD_LIST=queue@45db9a8b4431,lz4@1.10.0 -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_OSX_ARCHITECTURES=arm64 -DCMAKE_OSX_DEPLOYMENT_TARGET=15.0 \
  "-DCMAKE_C_COMPILER=$(xcrun --find clang)" \
  "-DCMAKE_CXX_COMPILER=$(xcrun --find clang++)" \
  "-DCMAKE_OSX_SYSROOT=$(xcrun --sdk macosx --show-sdk-path)" \
  -DCMAKE_MAKE_PROGRAM=/absolute/path/to/make-4.4.1
cmake --build build --verbose -- -j2 --jobserver-style=fifo
```

Darwin 根和子项目固定以上五项 Apple 参数，拒绝配方冲突；其他平台和默认值保持下述优先级。

引擎为 CMake 子项目提供 policy 下限 `3.5`、PIC `ON`、C++ 标准 `17` 的兜底值，通过原生 `CMAKE_ARGS` 传入，不向父级 cache 写默认值；不依赖 preset、IDE 设置或环境变量。根 configure 的 `-D` 或 toolchain 中已定义的值可覆盖兜底值，模块显式 cache 优先于父级值；修改公共值后重新 configure/build。已有 cache 条目保留为父级输入，不自动删除。policy 下限不降低上游较高声明，旧声明被提高后的兼容性仍须实际验证。IDE 可沿用已有编译器和配置。

默认发现全部配方版本并过滤平台不支持的根，显式请求不支持时失败；平台资格由各版本 package_platforms 及其精确依赖决定，见[字段参考](subsystems/module-recipes.md)。安装到 `3rdparty/<包名>/<版本>/`。`BUILD_LIST` 用逗号分隔 `包名@版本`；裸名只读包级 `package.toml` 的 `latest`，不排序或改写版本。依赖闭包自动补齐。裸名 eigen 选 3.4.0，显式可选 5.0.1；PCL 固定依赖 `eigen@3.4.0`。根 BUILD_LIST 不传入 OpenCV。configure 输出工具链、安装目录与模块摘要，build 执行获取和安装。

公共入口仅为 Go 子进程设置项目 SDK、缓存、`GOENV=off`、`GOWORK=off`、`GOTOOLCHAIN=local` 和 `CGO_ENABLED=0`，不修改全局环境或用户 Go 配置。只读模块模式分别编译宿主 builder 和 importer，宿主不随 C++ 目标架构改变；go.mod 固定依赖、go.sum 校验内容。每份工作副本统一用 build，不跨平台混用产物。子项目固定 Release，沿用父生成器、平台、完整工具集和 instance；父显式 CRT 原值传递，未指定时 MSVC Release 使用 `/MD`，静态库不自动改为 `/MT`。

已有 SDK 和模块缓存可离线复用；需要显式禁用 Go 模块联网时，仅在当前进程设置 `GOPROXY=off`、`GOSUMDB=off`。SDK 下载失败、摘要不符、解包失败、版本不符及模块缺失分别诊断，失败不执行残留 builder；损坏缓存按报错只处理对应路径，不删除校验文件或清空整个目录。

补丁无法正向或反向检查时，构建明确失败，需人工重新准备该模块的干净源码；不会自动还原或清空源码。不要把修改补丁等同于自动替换已应用的旧补丁。

Git 主来源复用 `git/<包名>/<版本>/`，资源复用 `git/_resources/<包名>/<版本>/<资源名>/`；已有 `.git` 文件或目录即关闭下载，不核对提交或自动 update。非空无 `.git` 目录报错且保留，包括隐藏文件。引擎不迁移旧平铺来源或安装数据。更换来源、版本或需重建时，先保留人工修改，再人工准备受影响持久来源、`build/ep/<包名>/<版本>/` 和 `3rdparty/<包名>/<版本>/`；可移到备份位置，不清空整个 build 或其他版本。重新获取后核对提交与产物。

可选 `sentinel` 是模块安装前缀下的相对路径；configure 时存在即显示 cached，保留同名目标和模块依赖顺序，不执行资源、补丁或安装步骤，但仍检查和准备消费文件。安装后重新 configure 才切换为复用目标；同 build 的普通重复构建仍依赖原生 stamp。sentinel 不校验版本、ABI 或完整性，缺失也不保证已有 stamp 自动修复安装。理由及路径规则见[本地复用设计](agent-notes/implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)。

归档 `URL` 是有序非空数组，`URL_HASH` 必须为 `SHA256=<64位十六进制摘要>`，可用单文件名 `DOWNLOAD_NAME`。版本、URL 与摘要同步维护；下载共用根 `git/`，匹配摘要的归档由原生流程复用，解压仍在 build。cmake 类型用 `[cmake] SOURCE_SUBDIR` 选择来源内子目录；copy 映射用互斥的 `files` 或 `directories`，`directories=["."]`、`destination="."` 复制完整内容。平台来源按公共→`platforms.<完整平台ID>` 整组替换，资源按名称整组替换；目标架构来自系统处理器，VS 显式平台优先，无匹配来源报错。具体声明参考 [lz4](../src/dependencies/lz4/1.10.0/module.toml) 与 [ONNX Runtime](../src/dependencies/onnxruntime/1.24.4/module.toml)。

## 本地打包与消费

自定义安装根在 configure 前设置环境变量 `AUTOLIB_INSTALL_ROOT`，不是同名 `-D` 参数；相对值以顶层源码目录解析，含空格路径须保持完整。更换根需重新 configure。消费安装根须包含 `autolib.cmake`、宿主 importer 和 `<包名>/<版本>/`，不要求先打包。

下游通过绝对路径直接 include 安装根的 `autolib.cmake`，再调用 `autolib_import(TARGET app PRIVATE queue flann)`，不复制入口或包到另一缓存。所需精确闭包完整时消费不联网；缺工具或包时，本地入口明确失败。Windows importer 为 `autolib-importer-windows-x86_64.exe`，Linux/Darwin 按宿主架构命名且须可执行；Darwin 消费者传入上述 Apple 参数及 Make。没有 adapter 的模块不能快捷导入。

现有配方均有 adapter；新增或修改按[五类基准与编写步骤](../.agents/skills/add-third-party-module/references/adapter-baselines.md)核对安装并完成[消费验证](testing.md#adapter-定向验证)，文件齐全不代表各包各平台均已实测。

实际安装前，消费文件被置为未完成；安装失败后不能仅靠残留 sentinel 补齐。保护需要的数据，再按上节移走受影响安装目录和原生构建状态，重新完成真实安装；不删除其他包。已完成安装的精确依赖被冻结，reuse 不能把它重标为源码 latest。adapter 正文更新通过普通根重新 configure/build 的复用目标刷新，不手改已安装描述。

本地需要分发时使用 `builder package`；CI 在验证后显式调用同一命令，不按 CI 环境改变 builder 行为。CI 工具准备见[CI 验证](testing.md#ci-验证)。

`builder package` 必须显式提供源码、安装、输出根，目标和宿主身份、工具路径及重复模块选择；完整参数见[打包入口](../src/autolib-builder/release.go)。模块可写 `包名@版本`，裸名只读取安装根 latest；包内身份和依赖来自已安装描述，不重新解析源码 latest。输出必须在本副本 build 内且不与安装树重叠；只打包显式模块，不自动补依赖。版本化库 tar.zst、宿主工具 tar.xz、各自摘要和包级 latest 一同交付。真实示例由[本地闭环测试](testing.md#本地二进制闭环)执行。

入口自身实际路径所在目录是唯一工具和包根，也是远端下载缓存；目录不得含分号。同次 configure 固定入口根和目标平台，工作目录为 `${CMAKE_BINARY_DIR}/autolib`。平台从当前 CMake 目标归一，VS 显式平台优先，工具按宿主选择。公共入口校验完整精确闭包，再按依赖顺序加载 adapter；同次 configure 拒绝同包不同版本。返回 `AUTOLIB_PREPARED_PACKAGES` 及每包的 `AUTOLIB_PACKAGE_DIR_<name>`、`AUTOLIB_PACKAGE_VERSION_<name>`，下游不推断目录层级。

消费者负责管理 CMake 查找路径、cache 和已有目标，避免 `<Package>_DIR`（如 `Eigen3_DIR`）或其他外部设置将依赖指向另一份安装。本仓库不清理、覆盖或为这类错误配置增加检测与兜底；导入错误版本时由消费者修正设置并重新 configure。

源码及本地入口内置远端地址为空；发布入口在上传副本内带实际 Release 地址。调用方不能通过 `AUTOLIB_LOCAL_ROOT`、`AUTOLIB_REMOTE_ROOT` 普通变量、cache 变量或环境变量覆盖来源。完整工具和精确闭包命中时不访问 Git 或 HTTP；只有必需输入缺失且入口内置地址非空时才获取元数据或下载。已有损坏候选立即失败，不自动覆盖；不回退系统库、源码编译或旧格式。直接内部调用参数见 [CLI](../src/autolib-importer/main.go)。

远程使用者将发布的 `autolib.cmake` 放入专用可写、忽略版本控制的目录，再按公共入口导入。工具和包读取JSON 中的完整地址并校验大小和摘要，完整缓存可离线复用；缺输入仍可联网，同根写入须串行。入口下载方式及实际根验证见[下载验证](testing.md#release-下载验证)。

已有目录核对五行包头和导入文件，adapter 使用标准发现命令或直接暴露头文件，不预检查完整载荷或逐文件散列；内容校验不认证来源，工具及包内 CMake 必须可信。缓存包和 latest 不自动追随远端，精确请求不改 latest；删除缓存 latest 指向的版本目录后，裸名请求重新发现远端选择，不更新无关缓存。刷新前保护修改，只处理入口缓存的 `<包名>/<版本>/`，不删共享依赖；损坏包不是缺包，必须明确处理而非自动重选。FLANN 的包依赖仅 lz4，OpenMP、C++ ABI 和系统运行库由消费环境满足。

成功导入消息包含传递依赖、缓存包和重复调用，单次调用内去重。包进度按 JSON 大小显示来源与百分比，换源清零；缓存及工具成功获取静默，工具换源打印脱敏诊断。消息格式、计时与失败边界见[双来源设计](agent-notes/implemented/architecture/2026-11-09-dual-source-artifact-downloads.md)。

## CI 开发与运行

[手动流水线](../.github/workflows/build-platform.yml)使用当前仓库的普通分支与事件提交，支持fork，拒绝tag。默认四个平台，也可单选；平台、工具与ABI基准见[架构](architecture.md#包与版本)。模块空字符串只准备工具，严格等于`all`才发现全部合资格版本，也可填写逗号分隔的精确引用或裸名；纯空白和变形的`all`拒绝。过滤不算失败；配方错误只阻断相关依赖者，无关项继续。

共享准备生成缓存快照，各平台`run --cache`显式读取。每项恢复或构建、消费验证后立即入库；失败运行中的成功成品仍可复用，汇总保持整体失败。缓存为当前源码仓库的`autolib-cache`，共享准备和平台使用`contents: write`；令牌仅注入缓存步骤，子进程不继承GitHub/Gitee令牌。容量、精确同名文件自动替换及无关文件保护见[缓存设计](agent-notes/implemented/architecture/2026-11-09-release-cache-and-independent-gitee-sync.md#release-成品缓存)。运行互斥不按分支拆组，队列最多等待100个运行。

同版本输入变化沿依赖链失效，latest单独变化不重建库。命中归档恢复到新安装根，不复用旧安装；CI 仅将已恢复并验证的精确依赖传入 `AUTOLIB_RESTORED_PACKAGES`，其构建目标只读校验冻结安装，不要求上游源码。普通本地复用仍执行原来源比对；Artifacts只保存计划、结果和诊断。零有效库仍验证工具与入口。验证命令及边界见[测试指南](testing.md#ci-验证)。

### 独立 Gitee 发布

[同步流水线](../.github/workflows/publish-artifacts.yml)提供 `sync/latest` 手动入口，同仓库 `Representative releases` 整体成功后自动触发 `sync`。配置 `GITEE_REPOSITORY`、`GITEE_RELEASE_TAG` 和 `GITEE_TOKEN`，缺失或非法即失败。同步任务开始执行后解析普通分支最新 SHA 并只 checkout 一次，不以触发构建的旧 SHA 清理；允许可信 fork，拒绝 tag、PR ref 及跨仓库构建事件。

`sync` 复用全部有效缓存并按源码清理；缺缓存只报告待构建，失效不等于删除。超限包只登记GitHub来源，JSON 确认后清理同名 Gitee 旧副本。`latest` 仅校验显式映射，不查询缓存、清理附件或交付入口；合法指针不保证全平台齐备。记录与恢复规则见下述 Action 说明。

同步通过仓库 SDK 编译宿主 builder/autolib-ci，只进行源码描述、指纹和缓存操作；不编译库/importer 或重打包。源码描述及依赖安装不接收 Gitee token，缓存 helper 移除该凭据。输入、重试、入口与结果接口见[Action 说明](../.github/actions/gitee-release/UPSTREAM.md#gitee-独立同步适配)。

执行前必须信任同步分支代码，脱敏不是沙箱。实际写入须取得目标及删除替换授权；两个 workflow 共用互斥组，人工维护须等待它们结束。push 未确认会停止本轮后续元数据写入；同版本产物替换不提供整组原子性。新库私有，公开后按[验收交接](testing.md#真实交付与手动验收)验证。

## 远端构建与源码传输授权

允许本机编译和定向验证；产物、编译缓存及临时文件均放在仓库 `build/` 下的新隔离目录，并行度最多4线程。Go仅使用仓库管理并校验的固定SDK，不使用系统Go；隔离方式见[测试指南](testing.md#执行)。

用户永久授权向可信局域网设备 `lxx@192.168.0.109` 的 `/home/lxx/xxautolib` 传输任务源码，同类传输不重复询问；仍须工具审批、逐文件摘要核对并保留已有数据，不覆盖未知差异或清空目标目录，不传项目 `.git/`、凭据、SDK、缓存或宿主产物。第三方库迁移的编译安装、修复及增量续跑按用户确认执行；版本化交付可编译轻量夹具、执行定向测试并写自有日志。此授权不含其他目标、push、发布或触发 CI；push 由用户执行，CI 另按授权验收。

[本任务的原生验证授权](agent-notes/implemented/architecture/2026-11-05-darwin-aarch64-native-build-and-ci.md#工作目录验证环境与授权)允许在新的任务隔离目录准备固定工具，编译必要的宿主工具、轻量原生输入及 queue/lz4，完成对应消费和打包；不覆盖既有安装、来源和缓存。[统一配方第一阶段](agent-notes/implemented/architecture/2026-11-06-module-recipe-contract-and-macos-validation.md)另授权同一隔离根的两版 Eigen 真实安装与直接/搬迁消费；全包调查不属于第一阶段。第二阶段按[已确认的同步范围](agent-notes/implemented/architecture/2026-11-06-module-recipe-contract-and-macos-validation.md#原平台基线与-macos-同步范围)推进，允许必要宿主工具与定向检查；包的正式验收使用 macos-15，原平台配方冻结。

通用定向验证可使用本机隔离目录或上述远端环境；最低 CMake 按用户决定不安排任何形式测试，不下载或传输旧工具。

109 使用既有 Make 4.4.1：`/home/lxx/xxautolib/build/task3-make-V1Bt5n/install/bin/make`，仅为测试进程前置 PATH；清理 build 须保留该目录。发布归档测试使用已有且支持 xz 的 `/home/lxx/.local/share/uv/python/cpython-3.12.14-linux-x86_64-gnu/bin/python3.12`，不重装工具。

## 日常修改

修改带注释的 module.toml 或同目录专用文件，不手改 `build/generated/`。Go/TOML、go.mod/go.sum、CMake helper 变化触发重新 configure；补丁及 DEPENDS 脚本由原生步骤跟踪。目录职责见[架构](architecture.md)，验证命令见[测试](testing.md)。不提交环境、来源、构建、安装和 `.superpowers/`；不整体跨平台复制，也不清空已有目录。

包输入先经 Windows 下载校验，再单独传入授权 ARM 目录；可传仅含上游历史的 Git bundle、归档及原生内部源码缓存，不传项目 `.git/`、凭据或宿主产物。OpenCV 在 ARM 使用进程环境 `OPENCV_DOWNLOAD_PATH=/home/lxx/xxautolib/git/opencv-internal-cache` 读取已传缓存，不在 ARM 重试下载。授权及数据保护见[取源规则](agent-notes/implemented/process/2026-10-19-repository-foundation.md#验证授权)。
