# 测试

公共 Go 测试通过受控 Git、归档及本机 HTTP/HTTPS fixture 验证声明、原生构建、安装、打包、消费和 CI 接线，不下载真实三方库。需要固定 Go SDK、模块缓存及 C/C++ 工具链；真实库与跨平台结果由下文独立验收，不从 fixture 推定。

## 执行

先按 [开发指南](development.md) 准备环境，再在仓库根目录运行：

```text
cmake -P src/autolib-builder/go-sdk.cmake -- test ./src/autolib-builder ./src/autolib-importer ./src/autolib-ci -timeout=30m -count=1 -v
cmake -P src/autolib-builder/go-sdk.cmake -- vet ./src/autolib-builder ./src/autolib-importer ./src/autolib-ci
```

测试源码与各工具同目录。CMake 包装入口自动准备私有 SDK 和缓存；fixture 不下载真实依赖。builder 在 `build/tests/` 创建含空格和中文的隔离副本，importer 使用 Go 临时目录，结束时仅清理自身目录。SDK 获取、失败及离线复用、根入口与环境隔离由 [builder 测试](../src/autolib-builder/fixture_test.go)覆盖。定向验证用 `-run TestDefinition`；依赖接线用 `-run TestDirectDependencies`，资源补丁用 `-run TestPatch`，真实模块声明用 `-run TestRealModuleDeclarations`。修改公共入口或跨文件接线时运行全集；同批定向验证后在批末运行一次全集。

仅修改文档时使用 doc-standards、agent-notes 技能校验链接、归属和 `scripts/doc-budgets.manifest.json` 预算；校验器不是构建依赖。仓库没有自动 Git hook 或覆盖率门槛；CI 的接线与实机验收由独立的 [CI 系统设计](agent-notes/implemented/architecture/2026-10-20-ci-system.md)拥有。

## 本地二进制闭环

先通过[普通根入口](development.md#准备与配置)选择 queue、flann 完成 configure/build，自动准备三库、消费描述及宿主工具。先直接消费原安装根，Windows x64/v143 在本仓库目录执行：

```text
cmake -DSOURCE_ROOT=D:/Work/xxautolib -DINSTALL_ROOT=D:/Work/xxautolib/3rdparty -DVALIDATION_ROOT=D:/Work/xxautolib/build/validation/direct-installed "-DGENERATOR=Visual Studio 17 2022" -DGENERATOR_PLATFORM=x64 "-DGENERATOR_TOOLSET=v143,host=x64" -P tests/autolib/run-installed.cmake
```

[直接消费 driver](../tests/autolib/run-installed.cmake)只配置、构建、运行消费者，不打包、不搬库、不复制工具，显式空远端。Windows 真实 API 验证通过；原生 Linux 直接消费尚无实测记录，现有授权目录未被覆盖，证据与缺口见[直接消费设计](agent-notes/implemented/architecture/2026-10-25-direct-install-consumption.md#验证结果)。Linux 使用本机绝对根和 Unix Makefiles、不传 VS 参数，并检查 importer 执行位、并行依赖时序及真实链接运行。

再验证打包双搬迁（完整命令）：

```text
cmake -DSOURCE_ROOT=D:/Work/xxautolib -DINSTALL_ROOT=D:/Work/xxautolib/3rdparty -DRELEASE_DIR=D:/Work/xxautolib/build/validation/direct-distribution/release -DVALIDATION_ROOT=D:/Work/xxautolib/build/validation/direct-distribution -DEVIDENCE_DIRECTORY=D:/Work/xxautolib/build/validation/direct-distribution -DBUILDER_FILE=D:/Work/xxautolib/build/tools/autolib-builder.exe -DIMPORTER_FILE=D:/Work/xxautolib/3rdparty/autolib-importer-windows-x86_64.exe -DTARGET_OS=windows -DTARGET_ARCH=x86_64 "-DGENERATOR=Visual Studio 17 2022" -DGENERATOR_PLATFORM=x64 "-DGENERATOR_TOOLSET=v143,host=x64" "-DROOT_PACKAGES=flann;queue" -P tests/autolib/run-local.cmake
```

Linux aarch64 使用本机绝对根及 `autolib-importer-linux-aarch64`，参数为 `-DTARGET_OS=linux -DTARGET_ARCH=aarch64 "-DGENERATOR=Unix Makefiles"`，不传 VS 平台或工具集。INSTALL_ROOT 必须对应真实生产前缀，不能先搬动未修正的安装输入。driver 不构建源码，在 VALIDATION_ROOT 新建运行目录记录打包、安装摘要、导入属性、链接输入及 CTest。Windows 本轮双搬迁两次 CTest 各 1/1，实际 FLANN 动态库来自搬迁包，生产安装及消费包字节不变；安装树的类型、权限、链接及修改时间另由 `TestStageInstallationUnchanged` 检查。

MODULES表示生产和链接集合，ROOT_PACKAGES只控制首次请求、默认等于MODULES；flann、queue的闭包为 `lz4;flann;queue`。RELEASE_READY=ON使用已有固定名资产。三平台历史闭环见[CI验证](#ci-验证)。

importer覆盖包头、优先级、依赖图、通用包、安全解包及CRC失败清理。[入口测试](../src/autolib-importer/entry_test.go)覆盖接口、作用域、多个消费者、平台及失败保护；UNC仅词法覆盖。受控PATH阻断运行时和下载器，验证本地消费零网络。真实消费验证FLANN两种API及LZ4，无需补链。本地回归不代替三宿主发行验证。

## GitHub 下载验证

`TestCMakeImporterArchive` 及[远端入口测试](../src/autolib-importer/remote_entry_test.go)使用真实归档、受控 PATH 和本机 HTTPS 核对字节、优先级、失败保护及分页复用。定向命令：`cmake -P src/autolib-builder/go-sdk.cmake -- test ./src/autolib-importer -run '^Test(CMakeImporterArchive|CMakeGitHubBootstrap|CMakeGitHubEnumerationReuse)$' -count=1 -v`。CMake 接受原生重定向，importer 拒绝降级；Windows 需正常用户 TLS 凭据。真实下载前须先发布本平台工具归档，仅裸资产不满足新入口；命名、最低版本及平台证据见[压缩分发验证](agent-notes/proposed/architecture/2026-10-27-importer-distribution-and-branch-release.md#本地验证与待验收范围)。

真实验收运行 `cmake -DSOURCE_ROOT=<仓库绝对路径> -DVALIDATION_ROOT=<仓库绝对路径>/build/validation/github -DREMOTE_ROOT=https://github.com/xxf1ow/xxautolib/releases/tag/autolib-release -P tests/autolib/run-github.cmake`，生成器参数沿用本地 driver。[下载 driver](../tests/autolib/run-github.cmake)从空根下载工具及 flann、queue，构建、运行 smoke 并重新配置；不编译第三方源码或发布。

代表集此前使用裸 importer 的三平台供给和真实 GitHub 消费已通过验收；Windows 由下载 driver 验证，Linux 由用户确认通过。供给摘要、公共目标及验收依据集中记录在[消费设计](agent-notes/proposed/architecture/2026-10-25-target-import-and-release-consumption.md#已验收范围)。该历史验收不证明压缩工具新格式或 Gitee 可用。

## CI 验证

CI 测试覆盖同名替换、每轮重建、动态依赖层及失败汇总。`TestCIDiscoveryFiltering` 通过真实 builder 验证自动发现、闭包过滤、通用 publisher 和空工作。`TestCIDynamicRelease` 真实编译、打包并消费 Windows 三层依赖及 queue 通用包，核对上传字节与汇总；无资格资产拒绝上传，全过滤不访问远端。最终复用既有工程验证有效闭包的双搬迁。

`TestCIWorkflowContract` 执行分支检查、Windows 层循环及 Make 准备片段，不模拟完整 Actions。Windows 不验证 Linux FIFO；无法用权限位阻断读取时跳过不可读目录及 adapter 用例。本地 fixture 不代替远端或 Linux 双架构验收。

queue、lz4、flann 的三平台供给、逐槽消费、双搬迁及凭据写入已有真实证据；ABI、-j/FIFO 和历史运行见[CI验证记录](agent-notes/implemented/architecture/2026-10-20-ci-system.md#真实运行)。[每轮重建与空 label 发布](agent-notes/implemented/simplification/2026-10-24-ci-rebuild-without-production-cache.md)的本地回归不替代新策略的三平台远端验收及 GitHub 页面核对；下载消费另按上节验收。

## 独立 Gitee 同步验证

使用 Python 3.12 和 Git，在仓库根创建专用 venv；Windows 命令如下，Linux 将解释器路径中的 `Scripts/python.exe` 换为 `bin/python`：

```text
python -m venv build/tools/gitee-sync-venv
build/tools/gitee-sync-venv/Scripts/python.exe -m pip install -r .github/actions/gitee-release/requirements.txt -c .github/actions/gitee-release/constraints.txt
build/tools/gitee-sync-venv/Scripts/python.exe -m unittest discover -s tests/ci/gitee-release -p 'test_*.py' -v
```

测试仅替换 HTTP，验证实际 multipart 字节、正文必填字段、索引、失败重跑、CPU 两倍并发及各操作独立的六次请求上限；不访问远端。Windows 使用 Git Bash 执行分支检查，没有 Bash 会明确跳过；CPU affinity 在 Windows 测试中替换，不证明 Linux 调度或网络性能。真实验收须另获远端写入授权：检查默认分支自动触发、变化同步和相同内容跳过，下载至少一个 Gitee 附件核对 SHA256，并记录 CPU、元数据/传输/总耗时、429 与失败数，按[同步设计](agent-notes/proposed/architecture/2026-10-27-gitee-release-sync-workflow.md#汇总验证范围与后续条件)评价效果。

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

测试不验证上游算法、并发、性能或模型推理；不强制每库专用工程。除 CI 代表集外，Linux x86_64 仅验证声明选择；v142 仅验证参数保留，仍缺实机验收。
