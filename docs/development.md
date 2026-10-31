# 开发

先准备 Git、CMake（3.20+）和 C/C++ 编译器，通过根入口构建安装。Go SDK 和固定模块自动准备；FLANN、OpenCV 还需可被找到的 pkg-config，FLANN 使用 OpenMP，FFmpeg 按机器开发环境探测。builder 和 Go 测试不需要 Python 或 uv；独立 Gitee 同步使用 Python，个人技能校验器自行准备其环境。

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

引擎为 CMake 子项目提供 policy 下限 `3.5`、PIC `ON`、C++ 标准 `17` 的兜底值，通过原生 `CMAKE_ARGS` 传入，不向父级 cache 写默认值；不依赖 preset、IDE 设置或环境变量。根 configure 的 `-D` 或 toolchain 中已定义的值可覆盖兜底值，模块显式 cache 优先于父级值；修改公共值后重新 configure/build。已有 cache 条目保留为父级输入，不自动删除。policy 下限不降低上游较高声明，旧声明被提高后的兼容性仍须实际验证。CMake Tools 可沿用 Kit/Variant，选择已有编译器和配置。

默认发现全部配方版本，安装到 `3rdparty/<包名>/<版本>/`。`BUILD_LIST` 用逗号分隔 `包名@版本`；裸名只读包级 `package.toml` 的 `latest`，不排序或改写版本。依赖闭包自动补齐，librealsense 不提供 adapter。裸名 eigen 选 3.4.0，显式可选 5.0.1；PCL 固定依赖 `eigen@3.4.0`。根 BUILD_LIST 不传入 OpenCV。configure 输出工具链、安装目录与模块摘要，build 执行获取和安装。

公共入口仅为 Go 子进程设置项目 SDK、缓存、`GOENV=off`、`GOWORK=off`、`GOTOOLCHAIN=local` 和 `CGO_ENABLED=0`，不修改全局环境或用户 Go 配置。只读模块模式分别编译宿主 builder 和 importer，宿主不随 C++ 目标架构改变；go.mod 固定依赖、go.sum 校验内容。每份工作副本统一用 build，不跨平台混用产物。子项目固定 Release，沿用父生成器、平台、完整工具集和 instance；父显式 CRT 原值传递，未指定时 MSVC Release 使用 `/MD`，静态库不自动改为 `/MT`。

已有 SDK 和模块缓存可离线复用；需要显式禁用 Go 模块联网时，仅在当前进程设置 `GOPROXY=off`、`GOSUMDB=off`。SDK 下载失败、摘要不符、解包失败、版本不符及模块缺失分别诊断，失败不执行残留 builder；损坏缓存按报错只处理对应路径，不删除校验文件或清空整个目录。该流程是生产端工具准备，与下游二进制导入分离。

补丁无法正向或反向检查时，构建明确失败，需人工重新准备该模块的干净源码；不会自动还原或清空源码。不要把修改补丁等同于自动替换已应用的旧补丁。

Git 主来源复用 `git/<包名>/<版本>/`，资源复用 `git/_resources/<包名>/<版本>/<资源名>/`；已有 `.git` 文件或目录即关闭下载，不核对提交或自动 update。非空无 `.git` 目录报错且保留，包括隐藏文件。引擎不迁移旧平铺来源或安装数据。更换来源、版本或需重建时，先保留人工修改，再人工准备受影响持久来源、`build/ep/<包名>/<版本>/` 和 `3rdparty/<包名>/<版本>/`；可移到备份位置，不清空整个 build 或其他版本。重新获取后核对提交与产物。

可选 `sentinel` 是模块安装前缀下的相对路径；configure 时存在即显示 cached，保留同名目标和模块依赖顺序，不执行资源、补丁或安装步骤，但仍检查和准备消费文件。安装后重新 configure 才切换为复用目标；同 build 的普通重复构建仍依赖原生 stamp。sentinel 不校验版本、ABI 或完整性，缺失也不保证已有 stamp 自动修复安装。理由及路径规则见[本地复用设计](agent-notes/implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)。

归档 `URL` 是有序非空数组，`URL_HASH` 必须为 `SHA256=<64位十六进制摘要>`，可用单文件名 `DOWNLOAD_NAME`。版本、URL 与摘要同步维护；下载共用根 `git/`，匹配摘要的归档由原生流程复用，解压仍在 build。cmake 类型用 `[cmake] SOURCE_SUBDIR` 选择来源内子目录；copy 映射用互斥的 `files` 或 `directories`，`directories=["."]`、`destination="."` 复制完整内容。平台来源按公共→平台→架构整组替换，资源按名称整组替换；目标架构来自系统处理器，VS 显式平台优先，无匹配来源报错。具体声明参考 [lz4](../src/dependencies/lz4/1.10.0/module.toml) 与 [ONNX Runtime](../src/dependencies/onnxruntime/1.24.4/module.toml)。

## 本地打包与消费

自定义安装根在 configure 前设置环境变量 `AUTOLIB_INSTALL_ROOT`，不是同名 `-D` 参数；相对值以顶层源码目录解析，含空格路径须保持完整。更换根需重新 configure。普通根 build 完成后，宿主 importer 和 `<包名>/<版本>/` 可作为独立消费者的只读本地来源，不要求先打包。

下游在 include 前将 `AUTOLIB_LOCAL_ROOT` 指向该安装根、`AUTOLIB_REMOTE_ROOT` 设为空，然后正常调用 `autolib_import(TARGET app PRIVATE queue flann)`。所需闭包完整时消费不联网；生产端准备 SDK、模块及三方源码不受此约束。Windows importer 为 `autolib-importer-windows-x86_64.exe`，Linux 按宿主架构命名且须可执行。没有 adapter 的模块不能快捷导入。

实际安装前，消费文件被置为未完成；安装失败后不能仅靠残留 sentinel 补齐。保护需要的数据，再按上节移走受影响安装目录和原生构建状态，重新完成真实安装；不删除其他包。已完成安装的精确依赖被冻结，reuse 不能把它重标为源码 latest。adapter 正文更新通过普通根重新 configure/build 的复用目标刷新，不手改已安装描述。

需要分发时再使用 `builder package`。CI 开发另构建独立工具，Windows 示例（Linux 去掉 `.exe`）：

```text
cmake -P src/autolib-builder/go-sdk.cmake -- build -mod=readonly -trimpath -o build/tools/autolib-ci.exe ./src/autolib-ci
```

`builder package` 必须显式提供源码、安装、输出根，目标和宿主身份、工具路径及重复模块选择；完整参数见[打包入口](../src/autolib-builder/release.go)。模块可写 `包名@版本`，裸名只读取安装根 latest；包内身份和依赖来自已安装描述，不重新解析源码 latest。输出必须在本副本 build 内且不与安装树重叠；只打包显式模块，不自动补依赖。版本化库 tar.zst、宿主工具 tar.xz、各自摘要和包级 latest 一同交付。真实示例由[本地闭环测试](testing.md#本地二进制闭环)执行。

将 `src/autolib.cmake` 复制到独立工程 `3rdparty/`，在其他同名库发现之前调用 `autolib_import(TARGET app PRIVATE queue flann)`；也可请求 `eigen@3.4.0` 等精确引用。入口所在目录固定为缓存，来源首次 include 后跨作用域固定；工作根为 `${CMAKE_BINARY_DIR}/autolib`。平台从当前 CMake 目标归一，VS 显式平台优先，工具按宿主选择。公共入口校验完整精确闭包，再按依赖顺序加载 adapter；同次 configure 拒绝同包不同版本。返回 `AUTOLIB_PREPARED_PACKAGES` 及每包的 `AUTOLIB_PACKAGE_DIR_<name>`、`AUTOLIB_PACKAGE_VERSION_<name>`，下游不推断目录层级。

`AUTOLIB_LOCAL_ROOT` 默认空；非空时解析真实绝对路径并拒绝与入口缓存相同，包括链接别名。本地根、缓存和工作路径不得含分号。工具依次使用入口裸程序、来源裸程序或来源单程序 tar.xz，最终只写入口缓存。库依次使用入口 `<name>/<version>/`、来源同布局安装、来源 `<name>-<version>-<os>-<arch>.tar.zst`；通用包使用 any-any。只有不存在才继续，坏候选立即失败。复制在缓存暂存区修正路径，来源工具、归档和安装树均只读；Linux 保留包内相对软链接及权限，拒绝绝对或越界链接，Windows 未承诺同等链接语义。同缓存根写入由调用方串行。

直接 CLI 的必填路径、平台及可重复 package 参数见[工具入口](../src/autolib-importer/main.go)；正常 CMake 使用者不需要直接调用。完整入口缓存允许本地和远端都关闭；缺输入直接报错，不回退系统库、源码编译或旧格式。

版本化远端消费尚未接通，本地用法必须显式设 `AUTOLIB_REMOTE_ROOT` 为空；既有远端默认值和历史发行格式不代表新协议可用。后续元数据获取及 CI/发布由[统一交付设计](agent-notes/proposed/architecture/2026-10-29-versioned-artifact-delivery.md)拥有，历史供给证据见[测试指南](testing.md#release-下载验证)。

已有目录核对五行包头和导入文件，adapter 检查其所需文件，不逐文件散列；内容校验不认证来源，工具及包内 CMake 必须可信。缓存包和 latest 不自动追随来源，精确请求不改 latest；删除缓存 latest 指向的版本目录后，裸名请求重新发现来源选择，不更新无关缓存。刷新前保护修改，只处理入口缓存的 `<包名>/<版本>/`，不改来源、不删共享依赖；损坏包不是缺包，必须明确处理而非自动重选。FLANN 的包依赖仅 lz4，OpenMP、C++ ABI 和系统运行库由消费环境满足。

## CI 开发与运行

版本化身份与独立发布尚未接通；本节仅描述既有CI，不能作为新交付协议的运行指南。

[手动流水线](../.github/workflows/build-platform.yml)接受同仓库任意分支、拒绝tag，检出事件提交；平台单选默认all，模块列表留空自动发现声明及 adapter，显式列表可选子集。自身或传递依赖缺 adapter 的根包静默过滤；全被过滤时成功且零发布。各平台顺序执行有效闭包的全部依赖层，逐槽验证后同名替换，不备份或回滚。汇总失败保留成功槽，重跑仍重新构建；不提供 force 输入，不设置资产 label。本地消费目录由用户按上节刷新。验证范围见[测试指南](testing.md#ci-验证)，后续远端操作仍须授权。命令见[入口](../src/autolib-ci/main.go)，环境和发布策略见 [CI 设计](agent-notes/implemented/architecture/2026-10-20-ci-system.md)。

### 独立 Gitee 同步

[同步 workflow](../.github/workflows/sync-gitee.yml)需要 secret `GITEE_TOKEN`；repository variables `GITEE_REPOSITORY`、`GITEE_RELEASE_TAG` 默认分别为 `xxf1ow/autolib-release`、`autolib-release`，两端使用同 tag。自动入口检查全部现存来源附件，包括历史遗漏；手动 `packages` 留空选全部，或填 `lz4,queue` 精确选择各包全部平台，importer 须显式选择 `autolib-importer`。任一包未匹配会在创建 Release 等写入前失败。

手动执行前必须信任所选分支提交：同步代码可读取和外传令牌，脱敏不是沙箱。人工修改 Gitee 正文或附件须避开同步，区块外描述也可能被覆盖。部分失败后重跑独立同步，无需重建；成功索引可跳过，未确认项可能重传，同名重复须人工处理。该入口尚需[真实验收](testing.md#独立-gitee-同步验证)，与只读消费独立；适配入口见 [Action 说明](../.github/actions/gitee-release/UPSTREAM.md#同步适配)。

## 远端构建与源码传输授权

109 机器（`lxx@192.168.0.109`）是局域网内安全可信设备。用户永久授权将本仓库任务相关源码传输至该机器的 `/home/lxx/xxautolib`。本轮第三方库迁移只在该目录执行整批编译安装验证，失败后的修复与增量续跑按用户确认执行；本机不执行任何编译，包括 Go 工具编译。传输前检查并保留已有数据，不传本仓库 `.git/`、凭据或 Windows 构建产物，不清空目标目录。该授权不包含 Git 推送、Release 上传或触发 CI；本轮迁移全部完成后，由用户手动推送并触发 CI 验收。

## 日常修改

修改带注释的 module.toml 或同目录专用文件，不手改 `build/generated/`。Go/TOML、go.mod/go.sum、CMake helper 变化触发重新 configure；补丁及 DEPENDS 脚本由原生步骤跟踪。目录职责见[架构](architecture.md)，验证命令见[测试](testing.md)。不提交环境、来源、构建、安装和 `.superpowers/`；不整体跨平台复制，也不清空已有目录。

包输入先经 Windows 下载校验，再单独传入授权 ARM 目录；可传仅含上游历史的 Git bundle、归档及原生内部源码缓存，不传项目 `.git/`、凭据或宿主产物。OpenCV 在 ARM 使用进程环境 `OPENCV_DOWNLOAD_PATH=/home/lxx/xxautolib/git/opencv-internal-cache` 读取已传缓存，不在 ARM 重试下载。授权及数据保护见[取源规则](agent-notes/implemented/process/2026-10-19-repository-foundation.md#验证授权)。
