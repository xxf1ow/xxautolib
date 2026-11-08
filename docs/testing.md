# 测试

Go fixture 验证声明、原生构建、安装及版本化本地和受控远端消费，不下载真实库。固定 SDK、模块缓存及 C/C++ 工具链为前提；真实代表集、平台和历史协议的证据分别记录，受控 Git/HTTPS 不替代真实托管验收。

## 执行

先按[开发指南](development.md)准备环境。通用 Go 编译与测试优先在授权的 109 目录执行，当前进程 PATH 前置开发指南记录的 Make。本任务在新的本机隔离目录执行必要工具、轻量原生输入和代表库验证，范围及例外见[验证授权](development.md#远端构建与源码传输授权)。阶段定向证据不替代真实托管和用户验收。

测试源码与各工具同目录。CMake 包装入口自动准备私有 SDK 和缓存；fixture 不下载真实依赖。builder 在 `build/tests/` 创建含空格和中文的隔离副本，importer 使用 Go 临时目录，结束时仅清理自身目录。SDK 获取、失败及离线复用、根入口与环境隔离由 [builder 测试](../src/autolib-builder/fixture_test.go)覆盖。定向验证用 `-run TestDefinition`；依赖接线用 `-run TestDirectDependencies`，资源补丁用 `-run TestPatch`，真实模块声明用 `-run TestRealModuleDeclarations`。只运行修改及直接受影响接口的增量用例，复用未失效证据；不运行全量 test、全量 vet 或重编无关模块。

版本化构建、安装和受控远端的既有证据见[统一交付设计](agent-notes/proposed/architecture/2026-10-29-versioned-artifact-delivery.md#版本化构建与安装验证)及[本地消费验证](agent-notes/proposed/architecture/2026-10-29-versioned-artifact-delivery.md#版本化本地消费验证)。它们不计为第四阶段真实托管通过，不重复全库构建。

仅修改文档时使用 doc-standards、agent-notes 技能校验链接、归属和 `scripts/doc-budgets.manifest.json` 预算；校验器不是构建依赖。仓库没有自动 Git hook 或覆盖率门槛；CI 的接线与实机验收由独立的 [CI 系统设计](agent-notes/implemented/architecture/2026-10-20-ci-system.md)拥有。

## 本地二进制闭环

先通过[普通根入口](development.md#准备与配置)选择代表集完成 configure/build，生成版本化安装、精确依赖描述及宿主工具；本节使用[实机验收](agent-notes/proposed/architecture/2026-10-29-versioned-artifact-delivery.md#版本化本地消费验证)中的独立生产根，不覆盖旧结果。

[安装消费 driver](../tests/autolib/run-installed.cmake)只将 smoke 源码放入独立消费者，直接 include `INSTALL_ROOT/autolib.cmake`，由公共 `autolib_import` 消费同根精确闭包。安装根必须已有入口、宿主工具及包。driver 执行 configure/build/CTest，并记录导入属性、精确目录、链接输入和动态加载位置；SOURCE_ROOT 仅用于取得 smoke，不复制生产入口或安装包。

[原生闭环 fixture](../src/autolib-importer/versioned_local_loop_test.go)通过普通根真实安装入口和轻量 probe/dep，独立消费者直接 include 安装入口。头版本与机器码版本须同时为 1，运行加载路径、导入属性和链接输入须位于同一安装根；重复消费不改安装。错头版本负样本必须运行失败。运行此闭环使用：

```text
cmake -P src/autolib-builder/go-sdk.cmake -- test -mod=readonly ./src/autolib-importer -run 'TestVersionedLocalLoop$' -count=1 -v
```

`MODULES` 选择 smoke 集合，`ROOT_PACKAGES` 选择首次导入根，均接受精确引用。Eigen 3.4.0 与 5.0.1 使用独立消费者，不在同次 configure 混用。VALIDATION_ROOT 必须位于 SOURCE_ROOT 的 build 内；可选 EVIDENCE_DIRECTORY 也须位于该 build 内，缺省使用 VALIDATION_ROOT。driver 在 evidence 下生成唯一运行目录，保存 configure/build/CTest 日志；CI 指定的目录位于 workflow 诊断收集范围内。driver 不编译三方源码，不修改原安装。

### Adapter 定向验证

[头文件定向工程](../tests/adapters/headers/CMakeLists.txt)覆盖重复导入和跨目录使用；受控载荷不代表真实 API。

真实七包[smoke](../tests/autolib/smoke.cpp)使用 `ROOT_PACKAGES=opencv@4.14.0;librealsense@2.58.1;onnxruntime@1.24.4`、`MODULES=freetype;harfbuzz;libusb;nlohmann;lz4;opencv;librealsense;onnxruntime`。两个消费driver在Windows上将已准备精确闭包内的DLL目录加入CTest进程PATH；直接运行smoke时自行设置PATH。含双版本 Eigen 的结果与未测范围见[导入适配记录](agent-notes/implemented/architecture/2026-11-04-package-adapter-standardization.md#验证结果)。

## Release 下载验证

[远端原生闭环](../src/autolib-importer/versioned_remote_loop_test.go)复用真实轻量库。Gitee 子例经生产 CI transport 打包运输，再从 `prepare_publication` 返回清单取得实际入口和资产，验证非上游仓库与编码 tag 的工具及库 URL；GitHub 保留消费协议。空缓存引导后真实链接运行，完整缓存和损坏已有候选均零远端操作；错误身份及损坏候选在 adapter 前拒绝。关闭受控 Git/HTTPS、迁移完整缓存后仍链接运行，原输入及无关缓存不变。证据及缺口由[自定位入口记录](agent-notes/implemented/architecture/2026-11-03-entry-delivery-and-tool-only-ci.md#验证)拥有。

109 使用[已有 Python 环境](development.md#远端构建与源码传输授权)，按下节发布测试方式选择 `-p test_entry_delivery.py`。该[桥接测试](../tests/ci/gitee-release/test_entry_delivery.py)从仓库根调度 RemoteLoop，不重复单跑同一 Go 用例；原目录存在未知源码差异时，只在逐摘要核对的新隔离源码根运行测试快照，不覆盖未知文件。

[下载 driver](../tests/autolib/run-github.cmake)用 `ENTRY_URL` 下载发布入口到独立消费者的 `3rdparty/`，再 include 并消费，不读取源码入口或用户来源变量。`ENTRY_URL` 支持 CMake 下载的 HTTP(S) 和 `file://`；`MODULES`、`ROOT_PACKAGES` 分别选择 smoke 和导入根。driver 保留发布入口字节，执行构建、CTest 和缓存 reconfigure。受控 Git/HTTPS fixture 不替代真实托管验收。

历史 GitHub 三平台供给、裸 importer 消费及 Windows 下载 driver 依据见[消费验收](agent-notes/proposed/architecture/2026-10-25-target-import-and-release-consumption.md#已验收范围)；真实 Gitee 用户验收、受控 TLS、分页及失败检查见[双来源记录](agent-notes/implemented/architecture/2026-10-27-ci-discovery-and-release-sources.md#验证)。它们不证明压缩工具新格式、版本化交付、Linux 双架构专项或最低 CMake 通过。裸资产、API 探测或同步成功不证明完整消费，不能替换现有工具冒充空根引导。

## CI 验证

Darwin 实机 queue/lz4 的生产、直接及搬迁消费、Mach-O 与 FIFO 证据和未测项见[Darwin 验证](agent-notes/implemented/architecture/2026-11-05-darwin-aarch64-native-build-and-ci.md#第一阶段验证证据)。工具专用及代表库的 macos-15 运行已通过，见[CI 实机验收](agent-notes/implemented/architecture/2026-11-05-darwin-aarch64-native-build-and-ci.md#macos-15-实机验收)；Gitee 自动同步仍由默认分支旧平台校验拒绝。Go 定向检查的 PATH 前置固定 CMake/Make。

Darwin 工具准备及 workflow 的受控检查分别执行 `python3.12 -B -m unittest discover -s tests/ci -p test_darwin_preparation.py -v`、`python3.12 -B -m unittest discover -s tests/ci -p test_darwin_workflow.py -v`。四平台选择、精确成品与元数据的增量证据见[第二阶段本地验证](agent-notes/implemented/architecture/2026-11-05-darwin-aarch64-native-build-and-ci.md#第二阶段本地验证)。这些既有证据不要求补跑 Linux 实机；原样 example 依赖当时未支持 Darwin 的 FLANN，未纳入该轮验收。统一配方的[阶段证据与缺口](agent-notes/implemented/architecture/2026-11-06-module-recipe-contract-and-macos-validation.md#验证结果)分别记录本机闭环和后续 macos-15、同 Linux Eigen 归档跨平台、PCL/example 验收。

新增消费入口与同归档预检分别选择 `test_consumer_runtime.py`、`test_eigen_portability.py`，沿用上述命令按需验证。

[CI 测试](../src/autolib-ci/main_test.go)覆盖精确计划、独立根验证、原生 builder 打包、成品摘要与数量、运输安全及所选平台完整性；workflow 定向测试核对事件条件、参数与接线。本地 fixture 和 workflow 片段执行不替代真实 Actions 验收。109的工具构建命令：

```text
cmake -P src/autolib-builder/go-sdk.cmake -- build -mod=readonly -trimpath -o build/tools/autolib-ci ./src/autolib-ci
```

脚本回归按修改选择文件。系统工具使用替身；DLL路径在Linux注入Windows分支，不替代Windows真实加载验证。

queue、lz4、flann 的历史三平台供给、逐槽消费、双搬迁及凭据写入，连同 ABI、FIFO、平台跳过项和运行依据，由[CI 验证记录](agent-notes/implemented/architecture/2026-10-20-ci-system.md#真实运行)和[每轮重建与空 label 发布](agent-notes/implemented/simplification/2026-10-24-ci-rebuild-without-production-cache.md)拥有；本地回归不替代新策略的真实远端验收，下载消费另按上节。

## 独立 Gitee 发布验证

使用 Python 3.12 和 Git，在仓库根创建专用 venv；Windows 命令如下，Linux 将解释器路径中的 `Scripts/python.exe` 换为 `bin/python`：

```text
python -m venv build/tools/gitee-sync-venv
build/tools/gitee-sync-venv/Scripts/python.exe -m pip install -r .github/actions/gitee-release/requirements.txt -c .github/actions/gitee-release/constraints.txt
build/tools/gitee-sync-venv/Scripts/python.exe -m unittest discover -s tests/ci/gitee-release -p test_publish.py -v
```

按修改所有者替换 `-p` 文件名，不重复整套发布测试。HTTP 替身不访问真实 Gitee；Git 测试只用本地临时裸库。Windows 无 Bash 跳过相关分支检查，真实 builder 用例只在 109 执行。成品交接、两次准备字节一致及就绪资产重复发布零写入的定向证据由[成品交接记录](agent-notes/implemented/architecture/2026-11-04-ci-packaged-artifact-handoff.md#验证)拥有；既有交付证据由[交付证据](agent-notes/implemented/architecture/2026-11-02-gitee-delivery-reimplementation.md#已有受控证据)拥有。

## 真实交付与手动验收

Gitee 版本化交付及 CI 成品交接已通过用户手动验收。早期构建与摘要空白修复见[交付证据](agent-notes/implemented/architecture/2026-11-02-gitee-delivery-reimplementation.md#真实构建与用户验收)；三平台原生构建 #30、发布 #27 与全部跳过且无新元数据提交的重复发布 #28 见[成品交接验收](agent-notes/implemented/architecture/2026-11-04-ci-packaged-artifact-handoff.md#真实托管验收)。默认分支自动发布、三平台匿名消费及真实故障注入尚无专项通过证据；发布 #27/#28 均没有实际重试，不将成功发布等同于故障恢复通过。

复验成品交接时使用成功的 `Representative releases` 构建；未合并时，手动选择任务分支的 `Publish artifacts` 并指定 run/attempt，核对实际控制提交，不假定默认分支自动入口使用任务分支代码。全部附件与元数据就绪后，仅重跑发布并保持 run/attempt、artifacts、目标和 latest 不变，预期 `uploaded=0`、`skipped=selected`、`failed=0`、`metadata_committed=false`，无 DELETE/POST 或新元数据提交。fork 采用流程及受控输入审查，不要求第二账号。CI、Gitee 写入与故障注入按具体授权执行；push 和新库公开由用户负责。

## 安装产物集成验证

先按[开发指南](development.md)构建安装模块。以下命令在仓库根目录执行；将 `<zlib绝对前缀>`、`<queue绝对前缀>` 分别替换为当前副本的 `3rdparty/zlib/<版本>`、`3rdparty/queue/<版本>` 绝对路径：

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

验证工程沿用开发指南的 `-G`、Windows 的 `-A`/`-T`，Linux 添加 `-DCMAKE_BUILD_TYPE=Release`；若父项目显式指定 `CMAKE_MSVC_RUNTIME_LIBRARY`，这里传入相同值。上述工程的既有验证各通过一项检查，不表示已重验版本化安装。zlib 使用该前缀静态库；queue 检查六个安装头文件；OpenCV 前缀替换为 `3rdparty/opencv/<版本>` 绝对路径，精确查找 4.14.0，创建 StereoSGBM、WLS 和 freetype。Windows 测试进程 PATH 需含实际版本前缀的 `x64/vc17/bin`。缺失输入即失败，不回退系统库。

librealsense 按[无相机验证说明](../tests/librealsense/README.md)运行两项检查，不替代实机取流。

FreeType 核对安装头文件、静态库、CMake Config 与 `.pc`，检查仓库 zlib 链接输入、`FT_REQUIRE_ZLIB=ON`、`FT_DISABLE_HARFBUZZ=ON` 和 `HB_HAVE_FREETYPE=ON`；能力取舍见[配方注释](../src/dependencies/freetype/2.13.2/module.toml)。确认 PNG、BZip2、Brotli 三项能力未启用，导出依赖中也未残留这些库；静态库不以 ldd 证明自包含。[中文消费工程](../tests/opencv/chinese/CMakeLists.txt)检查真实字形、整形、完整绘图边界；打包搬迁仍须独立验收。

lz4 从根入口真实构建安装，检查公开头文件、Windows `lib/lz4.lib` 或 Linux `lib/liblz4.a`、`lib/cmake/lz4/` 和 `lib/pkgconfig/liblz4.pc`。ONNX Runtime 检查完整归档内容、实际库名、版本、许可证和运行库依赖；Linux 检查共享库名称可解析且无缺失依赖。安装后重新 configure/build 应显示 cached；跨 build 输入复用由隔离测试区分于 sentinel 短路，不删除真实安装。平台证据与限制见[本地复用设计](agent-notes/implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)。

FLANN、HarfBuzz、OpenCV 在 Windows x64/v143、Linux aarch64 完成真实构建；Go 入口另以全新状态、禁止 Python 探测验证 FreeType→HarfBuzz→OpenCV，两端 OpenCV smoke 各 1/1。显式依赖的 cache、pkg-config 与链接输入、FFmpeg/KleidiCV 状态及系统传递依赖见[验证记录](agent-notes/implemented/architecture/2026-10-20-flann-opencv-dependencies.md#验证结果)。

测试不验证上游算法、并发、性能或模型推理；不强制每库专用工程。Linux x86_64 的实际消费范围见上文 adapter 定向验证，其余模块不由声明选择推定通过；v142 仅验证参数保留，仍缺实机验收。
