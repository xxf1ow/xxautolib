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

源码及本地入口内置远端地址为空；发布入口在上传副本内带实际 Release 地址。调用方不能通过 `AUTOLIB_LOCAL_ROOT`、`AUTOLIB_REMOTE_ROOT` 普通变量、cache 变量或环境变量覆盖来源。完整工具和精确闭包命中时不访问 Git 或 HTTP；只有必需输入缺失且入口内置地址非空时才获取元数据或下载。已有损坏候选立即失败，不自动覆盖；不回退系统库、源码编译或旧格式。直接内部调用参数见 [CLI](../src/autolib-importer/main.go)。

远程使用者先将已发布的单个 `autolib.cmake` 下载到专用可写目录，例如不纳入版本控制的 `3rdparty/`，再 include 并调用 `autolib_import`。入口下载 URL 可为 HTTP(S) 或 `file://`；下载文件不会携带原目录的工具或包，入口也不根据下载 URL 推导来源。远端工具和包沿用 Git 元数据、SHA256 和安全解包校验；完整缓存可离线复用，缺输入时远程入口仍可能尝试联网。严格禁止联网须使用内置地址为空的入口，不提供离线开关。同根写入由调用方串行，目录中的既有文件不会自动清理。

已有目录核对五行包头和导入文件，adapter 使用标准发现命令或直接暴露头文件，不预检查完整载荷或逐文件散列；内容校验不认证来源，工具及包内 CMake 必须可信。缓存包和 latest 不自动追随远端，精确请求不改 latest；删除缓存 latest 指向的版本目录后，裸名请求重新发现远端选择，不更新无关缓存。刷新前保护修改，只处理入口缓存的 `<包名>/<版本>/`，不删共享依赖；损坏包不是缺包，必须明确处理而非自动重选。FLANN 的包依赖仅 lz4，OpenMP、C++ ABI 和系统运行库由消费环境满足。

每次成功导入都打印 `-- [Info] [<消费目标>] <库名> : <版本>`，包含传递依赖、缓存包和重复调用，单次调用内去重。仅实际下载和解压打印进度及成功消息；缓存复用、入口和工具获取不打印过程。进度模式及短版本规则见[统一交付设计](agent-notes/proposed/architecture/2026-10-29-versioned-artifact-delivery.md#包导入消息)。

## CI 开发与运行

[手动流水线](../.github/workflows/build-platform.yml)接受当前源码仓库的普通分支、拒绝 tag，检出事件提交；workflow 将 `GITHUB_REPOSITORY` 作为可信仓库传给运行、运输和汇总校验，支持 fork。默认/all 为 Windows x86_64、Linux x86_64/aarch64、darwin-aarch64，也可单选。Darwin 固定 macos-15，核对 15.x/arm64；[准备脚本](../.github/scripts/prepare-darwin.sh)在任务目录校验并准备 CMake 3.31.12 和私有 Go，有效库才编译 Make 4.4.1。生产与消费沿用普通根入口及上述 Apple 参数。

模块未提供或空字符串只准备 importer 和入口，原始输入严格等于 `all` 才发现全部合资格配方版本，也可选择 `eigen@3.4.0,eigen@5.0.1,queue,lz4,flann`。纯空白、混合或变形的 `all` 及单独 importer 选择均拒绝。缺自身或传递依赖 adapter 的根按资格过滤；计划与汇总分别展示请求根、有效根和过滤根，过滤不算警告或失败。

零有效库仍保留所选平台，真实验证工具与入口，但不执行库构建或消费；普通根的空 `BUILD_LIST` 仍发现全部配方。非空库逐根验证精确闭包，随后打包运输成品、候选及原样入口，协议见[成品交接](agent-notes/implemented/architecture/2026-11-04-ci-packaged-artifact-handoff.md)。最终汇总不写 Release。环境见[CI 设计](agent-notes/implemented/architecture/2026-10-20-ci-system.md)，证据见[测试指南](testing.md#ci-验证)。

### 独立 Gitee 发布

[发布流水线](../.github/workflows/publish-artifacts.yml)仅写 Gitee。源码仓库及其分叉须显式配置 `GITEE_REPOSITORY`、`GITEE_RELEASE_TAG` 和密钥 `GITEE_TOKEN`，缺失或非法即失败，无上游默认目标。可信来源取自 `GITHUB_REPOSITORY`，不采用清单自报仓库。自动发布等待同仓库成功构建；手动指定同仓库 `source_run_id/source_attempt`。两者核对原提交、全部验证记录及成品后，原样分发库和工具、生成唯一目标注入入口，不二次筛选、编译 builder 或重打包；入口不写 Git 元数据。可附 `latest` 映射；独立更新指针要求非空映射，不读取或要求构建输入。接口与目标编码见[发布接口](../.github/actions/gitee-release/UPSTREAM.md#gitee-版本化发布适配)。

执行前必须信任控制代码与原构建提交；代码可以外传令牌，脱敏不是沙箱。同名附件核对实际 SHA256，相同跳过，不同先删后传，无回滚；临时故障退避后从查询重试，参数见发布接口。部分失败登记就绪项但任务失败，push 未确认也失败。重跑复用同一成品，不仅凭同名、大小或已登记摘要跳过；旧安装树输入须重新构建，同源码重建不保证相同字节。真实触发须取得目标及删除替换授权，并核对默认分支自动入口，不能假定它使用任务分支代码。新库保持私有，公开后由用户按[验收交接](testing.md#真实交付与手动验收)验证。

## 远端构建与源码传输授权

用户永久授权向可信局域网设备 `lxx@192.168.0.109` 的 `/home/lxx/xxautolib` 传输任务源码，同类传输不重复询问；仍须工具审批、逐文件摘要核对并保留已有数据，不覆盖未知差异或清空目标目录，不传项目 `.git/`、凭据、SDK、缓存或宿主产物。第三方库迁移的编译安装、修复及增量续跑按用户确认执行；版本化交付可编译轻量夹具、执行定向测试并写自有日志。本机默认禁止编译，包括 Go。此授权不含其他目标、push、发布或触发 CI；push 由用户执行，CI 另按授权验收。

[本任务的原生验证授权](agent-notes/implemented/architecture/2026-11-05-darwin-aarch64-native-build-and-ci.md#工作目录验证环境与授权)允许在新的任务隔离目录准备固定工具，编译必要的宿主工具、轻量原生输入及 queue/lz4，完成对应消费和打包；不覆盖既有安装、来源和缓存。[统一配方第一阶段](agent-notes/implemented/architecture/2026-11-06-module-recipe-contract-and-macos-validation.md)另授权同一隔离根的两版 Eigen 真实安装与直接/搬迁消费；全包调查不属于第一阶段。第二阶段按[已确认的同步范围](agent-notes/implemented/architecture/2026-11-06-module-recipe-contract-and-macos-validation.md#原平台基线与-macos-同步范围)推进，允许必要宿主工具与定向检查；包的正式验收使用 macos-15，原平台配方冻结。

通用定向验证优先使用上述远端环境；最低 CMake 按用户决定不安排任何形式测试，不下载或传输旧工具。

109 使用既有 Make 4.4.1：`/home/lxx/xxautolib/build/task3-make-V1Bt5n/install/bin/make`，仅为测试进程前置 PATH；清理 build 须保留该目录。发布归档测试使用已有且支持 xz 的 `/home/lxx/.local/share/uv/python/cpython-3.12.14-linux-x86_64-gnu/bin/python3.12`，不重装工具。

## 日常修改

修改带注释的 module.toml 或同目录专用文件，不手改 `build/generated/`。Go/TOML、go.mod/go.sum、CMake helper 变化触发重新 configure；补丁及 DEPENDS 脚本由原生步骤跟踪。目录职责见[架构](architecture.md)，验证命令见[测试](testing.md)。不提交环境、来源、构建、安装和 `.superpowers/`；不整体跨平台复制，也不清空已有目录。

包输入先经 Windows 下载校验，再单独传入授权 ARM 目录；可传仅含上游历史的 Git bundle、归档及原生内部源码缓存，不传项目 `.git/`、凭据或宿主产物。OpenCV 在 ARM 使用进程环境 `OPENCV_DOWNLOAD_PATH=/home/lxx/xxautolib/git/opencv-internal-cache` 读取已传缓存，不在 ARM 重试下载。授权及数据保护见[取源规则](agent-notes/implemented/process/2026-10-19-repository-foundation.md#验证授权)。
