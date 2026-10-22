# 测试

公共 Go 测试验证声明校验、参数传递、生成器身份、补丁、步骤依赖、增量构建及真实根入口。Git 与 tar.xz fixture 在本地创建，HTTP 服务使用动态端口并在退出时释放，不下载真实依赖；需要固定 Go SDK、模块缓存和 C/C++ 工具链。回归覆盖平台/架构整组选源、跨 build 输入复用与人工修改保留、隐藏文件目录保护、sentinel 全流程短路、源码子目录和完整目录复制；归档包括镜像失败、摘要错误、缓存复用及资源解压。公共 CMake 默认值、父级覆盖和模块覆盖均从真实子项目结果检查。

## 执行

先按 [开发指南](development.md) 准备环境，再在仓库根目录运行：

```text
cmake -P src/autolib-builder/go-sdk.cmake -- test ./src/autolib-builder -count=1 -v
cmake -P src/autolib-builder/go-sdk.cmake -- vet ./src/autolib-builder
```

测试源码与 builder 同目录，包含表驱动子例。CMake 包装入口自动准备并使用项目私有 SDK 和缓存；fixture 使用已有 SDK 与缓存，不下载真实依赖。测试在 `build/tests/` 创建含空格和中文的隔离副本，结束时仅清理自身临时目录。SDK 测试通过本地 HTTP fixture 验证首次获取、摘要与解包失败、断网复用 SDK/归档；真实根入口验证跨 build 复用、用户 Go 配置隔离、模块缺失、编译失败不加载旧结果、实际 Go 输入失效及阻断 uv/Python 后成功安装。定向验证用 `-run TestDefinition`；依赖接线用 `-run TestDirectDependencies`，资源补丁用 `-run TestPatch`，真实模块声明用 `-run TestRealModuleDeclarations`。修改公共入口或跨文件接线时运行全集；同批定向验证后在批末运行一次全集。

仅修改文档时使用 doc-standards、agent-notes 技能校验链接、归属和 `scripts/doc-budgets.manifest.json` 预算；校验器不是构建依赖。仓库没有自动 Git hook、CI 或覆盖率门槛。

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
