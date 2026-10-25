# 开发

先准备 Git、CMake（3.20+）和 C/C++ 编译器，通过根入口构建安装。Go SDK 和固定模块自动准备；FLANN、OpenCV 还需可被找到的 pkg-config，FLANN 使用 OpenMP，FFmpeg 按机器开发环境探测。builder 和公共测试不需要 Python 或 uv；个人技能校验器自行准备其环境。

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

默认发现全部模块，分别安装到 `3rdparty/<模块名>/`。只选部分模块时追加 `"-DBUILD_LIST=opencv"`，自动包含 freetype、harfbuzz；选择 flann 自动包含 lz4。根 BUILD_LIST 不传入 OpenCV。configure 输出工具链、安装目录与模块摘要；获取和安装在 build 阶段执行。

公共入口仅为 Go 子进程设置项目 SDK、缓存、`GOENV=off`、`GOWORK=off`、`GOTOOLCHAIN=local` 和 `CGO_ENABLED=0`，不修改全局环境或用户 Go 配置。只读模块模式增量编译宿主 builder，宿主不随 C++ 目标架构改变；go.mod 固定依赖、go.sum 校验内容。每份工作副本统一用 build，不跨平台混用产物。子项目固定 Release，沿用父生成器、平台、完整工具集和 instance；父显式 CRT 原值传递，未指定时 MSVC Release 使用 `/MD`，静态库不自动改为 `/MT`。

已有 SDK 和模块缓存可离线复用；需要显式禁用 Go 模块联网时，仅在当前进程设置 `GOPROXY=off`、`GOSUMDB=off`。SDK 下载失败、摘要不符、解包失败、版本不符及模块缺失分别诊断，失败不执行残留 builder；损坏缓存按报错只处理对应路径，不删除校验文件或清空整个目录。该流程是生产端工具准备，与下游二进制导入分离。

补丁无法正向或反向检查时，构建明确失败，需人工重新准备该模块的干净源码；不会自动还原或清空源码。不要把修改补丁等同于自动替换已应用的旧补丁。

Git 主来源复用 `git/<模块名>/`，资源复用 `git/_resources/<模块名>/<资源名>/`；已有 `.git` 文件或目录即关闭下载，不核对提交或自动 update。非空无 `.git` 目录报错且保留，包括隐藏文件。引擎不迁移已有 build 内源码。更换来源、版本或需重建时，先保留人工修改，再人工准备受影响持久来源、`build/ep/<模块名>/` 和 `3rdparty/<模块名>/`；可移到备份位置，不清空整个 build 或其他模块。重新获取后核对提交与产物。

可选 `sentinel` 是模块安装前缀下的相对路径；configure 时存在即显示 cached，保留同名空目标和模块依赖顺序，不执行资源、补丁或步骤。安装后重新 configure 才切换为空目标；同 build 的普通重复构建仍依赖原生 stamp。sentinel 不校验版本、ABI 或完整性，缺失也不保证已有 stamp 自动修复安装。理由及路径规则见[本地复用设计](agent-notes/implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)。

归档 `URL` 是有序非空数组，`URL_HASH` 必须为 `SHA256=<64位十六进制摘要>`，可用单文件名 `DOWNLOAD_NAME`。版本、URL 与摘要同步维护；下载共用根 `git/`，匹配摘要的归档由原生流程复用，解压仍在 build。cmake 类型用 `[cmake] SOURCE_SUBDIR` 选择来源内子目录；copy 映射用互斥的 `files` 或 `directories`，`directories=["."]`、`destination="."` 复制完整内容。平台来源按公共→平台→架构整组替换，资源按名称整组替换；目标架构来自系统处理器，VS 显式平台优先，无匹配来源报错。具体声明参考 [lz4](../src/dependencies/lz4/module.toml) 与 [ONNX Runtime](../src/dependencies/onnxruntime/module.toml)。

## 本地打包与消费

先完成所选模块的安装，再独立构建 builder、importer；CI 开发另构建第三个目标。Windows 输出名用 `.exe`，Linux 去掉后缀：

```text
cmake -P src/autolib-builder/go-sdk.cmake -- build -mod=readonly -trimpath -o build/tools/autolib-builder.exe ./src/autolib-builder
cmake -P src/autolib-builder/go-sdk.cmake -- build -mod=readonly -trimpath -o build/tools/autolib-importer.exe ./src/autolib-importer
cmake -P src/autolib-builder/go-sdk.cmake -- build -mod=readonly -trimpath -o build/tools/autolib-ci.exe ./src/autolib-ci
```

`builder package` 必须显式提供源码、安装、输出根，目标和宿主身份、工具路径及重复模块选择；完整参数见[打包入口](../src/autolib-builder/release.go)。输出必须在本副本 build 内且不与安装树重叠；只打包显式模块，不自动补依赖。真实示例由[本地闭环测试](testing.md#本地二进制闭环)执行。

将 `src/autolib.cmake` 复制到消费者，include 后在其他同名库目标创建前调用 `autolib_import(LOCAL_ROOT <本地发行目录> PACKAGES flann queue)`，链接 `autolib::queue`、`autolib::lz4`、`autolib::flann`；lz4 从 flann 包头自动补齐。可选 `WORK_ROOT`、`TARGET_OS`、`TARGET_ARCH`，默认工作根为消费者 build 下的 autolib。系统支持 windows/linux，架构为 x86_64/aarch64；工具按宿主、库包按目标选择。同一次 configure 固定来源、工作根和目标，可增加根包，首次导入拒绝既有上游目标。

LOCAL_ROOT 平铺固定名宿主工具和 `<name>-<os>-<arch>.tar.zst`；通用归档使用 any-any。准备目录为 `LOCAL_ROOT/packages/<os>-<arch>/<name>/`，优先于归档。相对根按顶层源码目录解析；本地根、工作根及实际包路径不得含分号。现有完整目录可只读复用；缺包需要本地根可写，同根写入由调用方串行，独立构建使用各自工作根。

CLI 为 `importer prepare --local-root=<目录> --work-root=<目录> --os=<目标OS> --arch=<目标架构> --result=<文件> --package=<根包>`，package 可重复。CMake 接受 REMOTE_ROOT、CLI 接受 --remote-root 参数位置，但非空值明确报远端尚未启用，不联网。URI、--uri、--manifest 均拒绝，不提供兼容层；CI 消费验证也只使用本地入口。

包头及依赖格式见[包消费设计](agent-notes/proposed/architecture/2026-10-24-manifestless-package-consumption.md#包内身份与依赖)。已有目录仅核对包头，不逐文件散列；归档内容校验用于意外损坏检测，不认证来源，本地工具须由用户信任。坏目录、坏归档或缺输入直接失败，不回退低优先级来源或加载上次结果。刷新前保护本地改动，移走对应 `packages/<目标OS>-<目标架构>/<包名>/`，不删除共享依赖；本地归档仍优先，更新归档不会覆盖目录。FLANN 的包依赖仅 lz4，OpenMP、C++ ABI 和系统运行库由消费环境满足。

## CI 开发与运行

[手动流水线](../.github/workflows/build-platform.yml)只接受默认分支并检出事件提交；平台单选默认all，模块列表留空选择首期全部三库。每轮构建所选模块及依赖，逐槽验证后同名替换，不备份或回滚。汇总失败保留成功槽，重跑仍重新构建；不提供 force 输入，不设置资产 label。本地消费目录由用户按上节刷新。验证范围见[测试指南](testing.md#ci-验证)，后续远端操作仍须授权。命令见[入口](../src/autolib-ci/main.go)，固定目标和策略见 [CI 设计](agent-notes/implemented/architecture/2026-10-20-ci-system.md)。

## 日常修改

修改带注释的 module.toml 或同目录专用文件，不手改 `build/generated/`。Go/TOML、go.mod/go.sum、CMake helper 变化触发重新 configure；补丁及 DEPENDS 脚本由原生步骤跟踪。目录职责见[架构](architecture.md)，验证命令见[测试](testing.md)。不提交环境、来源、构建、安装和 `.superpowers/`；不整体跨平台复制，也不清空已有目录。

包输入先经 Windows 下载校验，再单独传入授权 ARM 目录；可传仅含上游历史的 Git bundle、归档及原生内部源码缓存，不传项目 `.git/`、凭据或宿主产物。OpenCV 在 ARM 使用进程环境 `OPENCV_DOWNLOAD_PATH=/home/lxx/xxautolib/git/opencv-internal-cache` 读取已传缓存，不在 ARM 重试下载。授权及数据保护见[取源规则](agent-notes/implemented/process/2026-10-19-repository-foundation.md#验证授权)。
