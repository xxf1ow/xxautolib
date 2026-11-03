# 测试

Go fixture 验证声明、原生构建、安装及版本化本地和受控远端消费，不下载真实库。固定 SDK、模块缓存及 C/C++ 工具链为前提；真实代表集、平台和历史协议的证据分别记录，受控 Git/HTTPS 不替代真实托管验收。

## 执行

先按[开发指南](development.md)准备环境。Go 编译与测试仅在授权的 109 目录执行，当前进程 PATH 前置开发指南记录的 Make。阶段定向证据不替代真实托管和用户验收。

测试源码与各工具同目录。CMake 包装入口自动准备私有 SDK 和缓存；fixture 不下载真实依赖。builder 在 `build/tests/` 创建含空格和中文的隔离副本，importer 使用 Go 临时目录，结束时仅清理自身目录。SDK 获取、失败及离线复用、根入口与环境隔离由 [builder 测试](../src/autolib-builder/fixture_test.go)覆盖。定向验证用 `-run TestDefinition`；依赖接线用 `-run TestDirectDependencies`，资源补丁用 `-run TestPatch`，真实模块声明用 `-run TestRealModuleDeclarations`。只运行修改及直接受影响接口的增量用例，复用未失效证据；不运行全量 test、全量 vet 或重编无关模块。

版本化构建、安装和受控远端的既有证据见[统一交付设计](agent-notes/proposed/architecture/2026-10-29-versioned-artifact-delivery.md#版本化构建与安装验证)及[本地消费验证](agent-notes/proposed/architecture/2026-10-29-versioned-artifact-delivery.md#版本化本地消费验证)。它们不计为第四阶段真实托管通过，不重复全库构建。

仅修改文档时使用 doc-standards、agent-notes 技能校验链接、归属和 `scripts/doc-budgets.manifest.json` 预算；校验器不是构建依赖。仓库没有自动 Git hook 或覆盖率门槛；CI 的接线与实机验收由独立的 [CI 系统设计](agent-notes/implemented/architecture/2026-10-20-ci-system.md)拥有。

## 本地二进制闭环

先通过[普通根入口](development.md#准备与配置)选择代表集完成 configure/build，生成版本化安装、精确依赖描述及宿主工具；本节使用[实机验收](agent-notes/proposed/architecture/2026-10-29-versioned-artifact-delivery.md#版本化本地消费验证)中的独立生产根，不覆盖旧结果。

[安装消费 driver](../tests/autolib/run-installed.cmake)将入口和测试源码放入独立消费者，正常调用公共 `autolib_import`，由 importer 从只读安装源准备入口缓存，不打包或直接 include 生产 adapter。通过 configure/build/CTest 并记录导入属性、精确目录、链接输入和动态加载位置。SOURCE_ROOT 仅供 driver 复制入口及 smoke，生成的消费者不包含生产源码。

从安装复制验证代表集：

```text
cmake -DSOURCE_ROOT=/home/lxx/xxautolib -DINSTALL_ROOT=/home/lxx/xxautolib/build/versioned-stage2-install -DVALIDATION_ROOT=/home/lxx/xxautolib/build/validation/versioned-stage2/installed "-DGENERATOR=Unix Makefiles" "-DROOT_PACKAGES=flann;queue" -P tests/autolib/run-installed.cmake
```

再用[本地归档 driver](../tests/autolib/run-local.cmake)打包并消费；默认 relocate 模式将完整消费者及入口缓存搬迁、隐藏归档来源，并在两源均空时重新 configure/build/CTest：

```text
cmake -DSOURCE_ROOT=/home/lxx/xxautolib -DINSTALL_ROOT=/home/lxx/xxautolib/build/versioned-stage2-install -DRELEASE_DIR=/home/lxx/xxautolib/build/validation/versioned-stage2/archive/release -DVALIDATION_ROOT=/home/lxx/xxautolib/build/validation/versioned-stage2/archive -DBUILDER_FILE=/home/lxx/xxautolib/build/versioned-stage2-producer/tools/autolib-builder -DIMPORTER_FILE=/home/lxx/xxautolib/build/versioned-stage2-install/autolib-importer-linux-x86_64 -DTARGET_OS=linux -DTARGET_ARCH=x86_64 "-DGENERATOR=Unix Makefiles" "-DMODULES=queue;lz4;flann" "-DROOT_PACKAGES=flann;queue" -P tests/autolib/run-local.cmake
```

`MODULES` 是打包和 smoke 集合，`ROOT_PACKAGES` 是首次导入请求，默认等于 MODULES；flann、queue 的闭包为 `lz4;flann;queue`，生产描述固定 `lz4@1.10.0`。两者接受精确引用。Eigen 3.4.0 和 5.0.1 分别在独立消费者验证，设置两项均为对应精确引用，不在同次 configure 混用。`RELEASE_READY=ON` 使用 RELEASE_DIR 已有版本化资产，`VALIDATION_MODE=single` 仅消费一次；driver 不编译三方源码。VALIDATION_ROOT 每次新建运行子目录，RELEASE_DIR 必须在该根内；原安装和现成归档不变。

[原生闭环 fixture](../src/autolib-importer/versioned_local_loop_test.go)使用真实 builder、头版本与链接版本双断言及动态加载检查，覆盖复制、归档、隐藏生产树后缓存搬迁；错头版本负样本必须运行失败。真实 Linux 验收另核对相对软链接、原安装的内容/类型/权限/mtime/链接不变，删除测试缓存 latest 目标后重发现及无关缓存不变。只删除解析确认的测试自有版本目录，保留生产结果和日志。本轮新协议未验证 Windows、Linux aarch64、最低 CMake 3.20；历史三平台闭环见[CI 验证](#ci-验证)，不能替代这些缺口。

## Release 下载验证

[远端原生闭环](../src/autolib-importer/versioned_remote_loop_test.go)通过普通根入口和真实 builder 生成轻量 probe/dep 与 importer 归档；两种托管共用专用 Git 元数据协议和受控 HTTPS。空缓存获取工具与精确闭包后真实链接运行，校验当前缓存加载位置及头/机器码版本；有效摘要但错误包内身份在 adapter 执行前失败。完整缓存复制到新消费者、关闭测试 Git/HTTPS 后，两源均空仍通过 configure/build/CTest，原来源、精确依赖和无关缓存不变；缓存复用零远端操作，需要新包的新 configure 获取新快照。实际计数及缺口见[远端消费验证](agent-notes/proposed/architecture/2026-10-29-versioned-artifact-delivery.md#版本化远端消费验证)。

[下载 driver](../tests/autolib/run-github.cmake)支持 GitHub/Gitee 下载根，将入口及 smoke 复制到独立消费者，由公共入口准备缓存。`MODULES` 选择 smoke，`ROOT_PACKAGES` 选择导入根；默认分别为 `queue;lz4;flann`、`flann;queue`，只传 MODULES 时根跟随该集合。两者透传首次及后续 configure；两个精确 Eigen 版本各自调用 driver，使用独立消费者与缓存。运行库路径取自 `prepared-state.cmake`，不自行搬库。`TestGitHubValidationDriver` 执行参数、缓存路径及入口失败断言。真实托管和 Windows/Visual Studio、Linux aarch64 尚未验证；最低 CMake 按用户决定不安排任何形式测试，仅用 109 现有 CMake 3.31.12，不宣称最低版本通过。

历史 GitHub 三平台供给、裸 importer 消费及 Windows 下载 driver 依据见[消费验收](agent-notes/proposed/architecture/2026-10-25-target-import-and-release-consumption.md#已验收范围)；真实 Gitee 用户验收、受控 TLS、分页及失败检查见[双来源记录](agent-notes/implemented/architecture/2026-10-27-ci-discovery-and-release-sources.md#验证)。它们不证明压缩工具新格式、版本化交付、Linux 双架构专项或最低 CMake 通过。裸资产、API 探测或同步成功不证明完整消费，不能替换现有工具冒充空根引导。

## CI 验证

[CI 测试](../src/autolib-ci/main_test.go)覆盖精确计划、独立根验证、运输安全与所选平台完整性；workflow 定向测试核对事件条件、参数与接线。本地 fixture 和 workflow 片段执行不模拟完整 Actions，不替代三平台真实验收。109的工具构建命令：

```text
cmake -P src/autolib-builder/go-sdk.cmake -- build -mod=readonly -trimpath -o build/tools/autolib-ci ./src/autolib-ci
```

queue、lz4、flann 的历史三平台供给、逐槽消费、双搬迁及凭据写入，连同 ABI、FIFO、平台跳过项和运行依据，由[CI 验证记录](agent-notes/implemented/architecture/2026-10-20-ci-system.md#真实运行)和[每轮重建与空 label 发布](agent-notes/implemented/simplification/2026-10-24-ci-rebuild-without-production-cache.md)拥有；本地回归不替代新策略的真实远端验收，下载消费另按上节。

## 独立 Gitee 发布验证

使用 Python 3.12 和 Git，在仓库根创建专用 venv；Windows 命令如下，Linux 将解释器路径中的 `Scripts/python.exe` 换为 `bin/python`：

```text
python -m venv build/tools/gitee-sync-venv
build/tools/gitee-sync-venv/Scripts/python.exe -m pip install -r .github/actions/gitee-release/requirements.txt -c .github/actions/gitee-release/constraints.txt
build/tools/gitee-sync-venv/Scripts/python.exe -m unittest discover -s tests/ci/gitee-release -p test_publish.py -v
```

按修改所有者替换 `-p` 文件名，不重复整套发布测试。HTTP 替身不访问真实 Gitee；Git 测试只用本地临时裸库。Windows 无 Bash 跳过相关分支检查，真实 builder 用例只在 109 执行。已有通过数量与真实缺口由[交付证据](agent-notes/proposed/architecture/2026-11-02-gitee-delivery-reimplementation.md#已有受控证据)拥有。

## 真实交付与手动验收

真实发布、故障重跑及三平台匿名消费尚未执行。默认分支旧自动入口未隔离，目前不得触发；用户先审阅证据并授权具体目标与替换范围，负责 push 和公开新库。可观察的手动验收方案与未完成项见[交付设计](agent-notes/proposed/architecture/2026-11-02-gitee-delivery-reimplementation.md#实现证据与未完成验收)，不能以受控结果或提前合并代替。

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

FreeType 使用真实归档构建安装，核对 `include/freetype2/ft2build.h`、`include/freetype2/freetype/freetype.h`、实际静态库、`lib/cmake/freetype/` 包文件与 `lib/pkgconfig/freetype2.pc`。两端分别检查 configure/cache 中 ZLIB、BZip2、PNG、HarfBuzz、BrotliDec 的发现结果及导出依赖、路径；不对静态归档运行 ldd 来证明自包含。沿用参考可选依赖探测，不承诺依赖隔离、静态自包含或包可搬迁。

lz4 从根入口真实构建安装，检查公开头文件、Windows `lib/lz4.lib` 或 Linux `lib/liblz4.a`、`lib/cmake/lz4/` 和 `lib/pkgconfig/liblz4.pc`。ONNX Runtime 检查完整归档内容、实际库名、版本、许可证和运行库依赖；Linux 检查共享库名称可解析且无缺失依赖。安装后重新 configure/build 应显示 cached；跨 build 输入复用由隔离测试区分于 sentinel 短路，不删除真实安装。平台证据与限制见[本地复用设计](agent-notes/implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)。

FLANN、HarfBuzz、OpenCV 在 Windows x64/v143、Linux aarch64 完成真实构建；Go 入口另以全新状态、禁止 Python 探测验证 FreeType→HarfBuzz→OpenCV，两端 OpenCV smoke 各 1/1。显式依赖的 cache、pkg-config 与链接输入、FFmpeg/KleidiCV 状态及系统传递依赖见[验证记录](agent-notes/implemented/architecture/2026-10-20-flann-opencv-dependencies.md#验证结果)。

测试不验证上游算法、并发、性能或模型推理；不强制每库专用工程。除 CI 代表集外，Linux x86_64 仅验证声明选择；v142 仅验证参数保留，仍缺实机验收。
