# 测试

公共测试验证声明校验、参数传递、生成器身份、补丁、步骤依赖、增量构建及真实根入口。Git 与 tar.xz fixture 在本地创建，HTTP 服务使用动态端口并在退出时释放，不下载真实依赖；需要 uv 缓存和 C/C++ 工具链。回归覆盖平台/架构整组选源、跨 build 输入复用与人工修改保留、隐藏文件目录保护、sentinel 全流程短路、源码子目录和完整目录复制；归档包括镜像失败、摘要错误、缓存复用及资源解压。公共 CMake 默认值、父级覆盖和模块覆盖均从真实子项目结果检查。

## 执行

先按 [开发指南](development.md) 准备环境，再在仓库根目录运行：

```text
uv run --locked python -B -m unittest discover -s tests/build_system -v
```

应收集并通过 49 项测试。测试在 `build/tests/` 创建含空格的隔离副本，结束时仅清理自身临时目录。基础入口测试使用离线 uv 缓存，首次执行前须完成环境准备。定向验证可追加 `-p test_definition.py` 等文件过滤参数；修改公共入口或跨文件接线时运行全集。

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
```

验证工程沿用开发指南的 `-G`、Windows 的 `-A`/`-T`，Linux 添加 `-DCMAKE_BUILD_TYPE=Release`；若父项目显式指定 `CMAKE_MSVC_RUNTIME_LIBRARY`，这里传入相同值。每工程应通过 1 项 CTest。zlib 必须使用该前缀的头文件及静态库绝对路径；queue 检查六个安装头文件并仅使用安装 include 路径。缺失输入即失败，不回退系统库。

FreeType 使用真实归档构建安装，核对 `include/freetype2/ft2build.h`、`include/freetype2/freetype/freetype.h`、实际静态库、`lib/cmake/freetype/` 包文件与 `lib/pkgconfig/freetype2.pc`。两端分别检查 configure/cache 中 ZLIB、BZip2、PNG、HarfBuzz、BrotliDec 的发现结果及导出依赖、路径；不对静态归档运行 ldd 来证明自包含。沿用参考可选依赖探测，不承诺依赖隔离、静态自包含或包可搬迁。

lz4 从根入口真实构建安装，检查公开头文件、Windows `lib/lz4.lib` 或 Linux `lib/liblz4.a`、`lib/cmake/lz4/` 和 `lib/pkgconfig/liblz4.pc`。ONNX Runtime 检查完整归档内容、实际库名、版本、许可证和运行库依赖；Linux 检查共享库名称可解析且无缺失依赖。安装后重新 configure/build 应显示 cached；跨 build 输入复用由隔离测试区分于 sentinel 短路，不删除真实安装。平台证据与限制见[本地复用设计](agent-notes/implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)。

这些测试只确认安装流程和产物，不测试上游算法、并发、性能或模型推理；沿用既有能力的模块不强制新增专用测试目录。Linux x86_64 只验证声明选择，实机安装仍有缺口。v142 参数保留有公共回归，实机一致性保留人工验收，不以 v143 成功替代。
