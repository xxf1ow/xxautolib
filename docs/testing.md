# 测试

公共 Go 测试验证声明校验、参数传递、生成器身份、补丁、步骤依赖、增量构建及真实根入口。Git 与 tar.xz fixture 在本地创建，HTTP 服务使用动态端口并在退出时释放，不下载真实依赖；需要固定 Go SDK、模块缓存和 C/C++ 工具链。回归覆盖平台/架构整组选源、跨 build 输入复用与人工修改保留、隐藏文件目录保护、sentinel 全流程短路、源码子目录和完整目录复制；归档包括镜像失败、摘要错误、缓存复用及资源解压。公共 CMake 默认值、父级覆盖和模块覆盖均从真实子项目结果检查。

## 执行

先按 [开发指南](development.md) 准备环境，再在仓库根目录运行：

```text
cmake -P src/autolib-builder/go-sdk.cmake -- test ./src/autolib-builder -count=1 -v
cmake -P src/autolib-builder/go-sdk.cmake -- test ./src/autolib-importer -count=1 -v
cmake -P src/autolib-builder/go-sdk.cmake -- test ./src/autolib-ci -count=1 -v
cmake -P src/autolib-builder/go-sdk.cmake -- vet ./src/autolib-builder ./src/autolib-importer ./src/autolib-ci
```

测试源码与各工具同目录。CMake 包装入口自动准备私有 SDK 和缓存；fixture 不下载真实依赖。builder 在 `build/tests/` 创建含空格和中文的隔离副本，importer 使用 Go 临时目录，结束时仅清理自身目录。SDK 获取、失败及离线复用、根入口与环境隔离由 [builder 测试](../src/autolib-builder/fixture_test.go)覆盖。定向验证用 `-run TestDefinition`；依赖接线用 `-run TestDirectDependencies`，资源补丁用 `-run TestPatch`，真实模块声明用 `-run TestRealModuleDeclarations`。修改公共入口或跨文件接线时运行全集；同批定向验证后在批末运行一次全集。

仅修改文档时使用 doc-standards、agent-notes 技能校验链接、归属和 `scripts/doc-budgets.manifest.json` 预算；校验器不是构建依赖。仓库没有自动 Git hook 或覆盖率门槛；CI 的接线与实机验收由独立的 [CI 系统设计](agent-notes/proposed/architecture/2026-10-20-ci-system.md)拥有。

## 本地二进制闭环

先准备真实 queue、lz4、flann 安装树，再按[开发指南](development.md#本地打包与消费)构建两个宿主工具。Windows x64/v143 在本仓库目录执行：

```text
cmake -DSOURCE_ROOT=D:/Work/xxautolib -DINSTALL_ROOT=D:/Work/xxautolib/3rdparty -DRELEASE_DIR=D:/Work/xxautolib/build/validation/manifestless-local/release -DVALIDATION_ROOT=D:/Work/xxautolib/build/validation/manifestless-local -DEVIDENCE_DIRECTORY=D:/Work/xxautolib/build/validation/manifestless-local -DBUILDER_FILE=D:/Work/xxautolib/build/tools/autolib-builder.exe -DIMPORTER_FILE=D:/Work/xxautolib/build/tools/autolib-importer.exe -DTARGET_OS=windows -DTARGET_ARCH=x86_64 "-DGENERATOR=Visual Studio 17 2022" -DGENERATOR_PLATFORM=x64 "-DGENERATOR_TOOLSET=v143,host=x64" "-DROOT_PACKAGES=flann;queue" -P tests/autolib/run-local.cmake
```

Linux aarch64 将绝对根和程序名替换为本机值，使用 `-DTARGET_OS=linux -DTARGET_ARCH=aarch64 "-DGENERATOR=Unix Makefiles"`，不传 VS 平台或工具集。INSTALL_ROOT 必须对应元数据中的真实生产前缀，不能先搬动未修正的安装输入。driver 不构建源码；它在 `build/validation/manifestless-local/` 新建运行目录，记录打包、安装摘要、导入属性、链接输入和 CTest 日志。发行根搬迁后空缓存导入，再搬迁准备好的包并重新编译运行；两次 CTest 各 1/1，实际 FLANN 动态库来自搬迁包，生产安装及消费包字节不变。

本地阶段验证使用builder的 `-run 'Test(PackageDependencyHeader|PackageArchiveChecksum|StagePackage|StageFixup|PackageArchive|LocalPackageAssets|PackageCLI|UniversalIdentity|UniversalPackageAcrossTargets|DescribeParity|DescribeInputs|DescribeCLI)$'`、importer全集和三工具vet；经上述SDK包装执行，test加`-count=1 -v`。全部受影响阶段完成后做全量回归。

MODULES表示生产和链接集合，ROOT_PACKAGES默认等于MODULES，只控制首次请求。根包结果必须为 `lz4;flann;queue`；RELEASE_READY=ON核对固定工具和归档，无需manifest。Windows x64/v143已复验，两次CTest各1/1；Linux aarch64既有证据不代替新协议复验。

importer覆盖四行头、优先级、共享依赖、环、通用包、安全解包及CRC失败清理。真实CMake检查同次configure输入固定、当前结果完整才加载及同名目标拒绝。本地根和工作根拒绝分号，UNC仅词法覆盖；受控PATH阻断语言运行时和下载器，首次完整/缺包路径HTTP与代理请求为零。不含HTTPS、Linux x86_64工具执行或三宿主发行证据。

## CI 验证

CI 测试使用本地资产服务验证缓存恢复、同名替换、逐层结果和失败汇总，并真实编译、消费 Windows 小型依赖。接线定向项为 `-run TestCIWorkflowContract`，执行分支检查和 Make 准备片段，不模拟完整 Actions；Windows 跳过 Linux FIFO 探针，不证明 Linux 并行。

真实触发、token 写权限、三平台供给及 Linux 新协议尚待另获授权验收；首次真实编译即核验 `-j`、FIFO 预算和重叠，缓存命中不能替代。实机门槛见 [CI 系统设计](agent-notes/proposed/architecture/2026-10-20-ci-system.md#验收标准)；HTTPS 未启用。

## 安装产物集成验证

先按[开发指南](development.md)构建安装模块。以下命令在仓库根目录执行；将 `<zlib绝对前缀>`、`<queue绝对前缀>` 分别替换为当前副本的 `3rdparty/zlib`、`3rdparty/queue` 绝对路径：

```text
cmake -S tests/zlib -B build/check/zlib "-DZLIB_INSTALL_PREFIX:PATH=<zlib绝对前缀>"
cmake --build build/check/zlib --config Release
ctest --test-dir build/check/zlib -C Release --output-on-failure
cmake -S tests/queue -B build/check/queue "-DQUEUE_INSTALL_PREFIX:PATH=<queue绝对前缀>"
cmake --build build/check/queue --config Release
ctest --test-dir build/check/queue -C Release --output-on-failure
cmake -S tests/opencv -B build/check/opencv "-DOPENCV_INSTALL_PREFIX:PATH=<opencv绝对前缀>"
cmake --build build/check/opencv --config Release
ctest --test-dir build/check/opencv -C Release --output-on-failure
```

验证工程沿用开发指南的 `-G`、Windows 的 `-A`/`-T`，Linux 添加 `-DCMAKE_BUILD_TYPE=Release`；若父项目显式指定 `CMAKE_MSVC_RUNTIME_LIBRARY`，这里传入相同值。每工程应通过 1 项 CTest。zlib 使用该前缀静态库；queue 检查六个安装头文件；OpenCV 前缀替换为 `3rdparty/opencv` 绝对路径，精确查找 4.14.0，创建 StereoSGBM、WLS 和 freetype。Windows 测试进程 PATH 需含实际 `opencv/x64/vc17/bin`。缺失输入即失败，不回退系统库。

FreeType 使用真实归档构建安装，核对 `include/freetype2/ft2build.h`、`include/freetype2/freetype/freetype.h`、实际静态库、`lib/cmake/freetype/` 包文件与 `lib/pkgconfig/freetype2.pc`。两端分别检查 configure/cache 中 ZLIB、BZip2、PNG、HarfBuzz、BrotliDec 的发现结果及导出依赖、路径；不对静态归档运行 ldd 来证明自包含。沿用参考可选依赖探测，不承诺依赖隔离、静态自包含或包可搬迁。

lz4 从根入口真实构建安装，检查公开头文件、Windows `lib/lz4.lib` 或 Linux `lib/liblz4.a`、`lib/cmake/lz4/` 和 `lib/pkgconfig/liblz4.pc`。ONNX Runtime 检查完整归档内容、实际库名、版本、许可证和运行库依赖；Linux 检查共享库名称可解析且无缺失依赖。安装后重新 configure/build 应显示 cached；跨 build 输入复用由隔离测试区分于 sentinel 短路，不删除真实安装。平台证据与限制见[本地复用设计](agent-notes/implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)。

FLANN、HarfBuzz、OpenCV 在 Windows x64/v143、Linux aarch64 完成真实构建；Go 入口另以全新状态、禁止 Python 探测验证 FreeType→HarfBuzz→OpenCV，两端 OpenCV smoke 各 1/1。显式依赖的 cache、pkg-config 与链接输入、FFmpeg/KleidiCV 状态及系统传递依赖见[验证记录](agent-notes/implemented/architecture/2026-10-20-flann-opencv-dependencies.md#验证结果)。

测试不验证上游算法、并发、性能或模型推理；不强制每库专用工程。Linux x86_64 仅验证声明选择；v142 仅验证参数保留，均缺实机验收。
