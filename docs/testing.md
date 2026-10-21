# 测试

公共测试验证 TOML 校验、CMake 参数传递、生成器身份、补丁、步骤依赖、增量构建及真实根入口。Git 与 tar.xz fixture 在本地创建，不下载真实依赖；需要 uv 缓存与可用 C/C++ 工具链。本地 HTTP 服务使用动态端口，退出和异常时释放线程及套接字，覆盖首地址成功、404/摘要错误回退、候选全部失败、资源归档和单顶层目录展开。独立后端与普通入口回归验证 policy、PIC、C++ 标准到达子项目，默认值不写入父级 cache，以及模块覆盖、父级值更新和移除后恢复兜底值。

## 执行

先按 [开发指南](development.md) 准备环境，再在仓库根目录运行：

```text
uv run --locked python -B -m unittest discover -s tests/build_system -v
```

应收集并通过 37 项测试。测试在 `build/tests/` 创建含空格的隔离副本，结束时仅清理自身临时目录。基础入口测试使用离线 uv 缓存，首次执行前须完成环境准备。定向验证可追加 `-p test_definition.py` 等文件过滤参数；修改公共入口或跨文件接线时运行全集。

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

这些测试只确认本仓库安装流程和产物可用性，不测试上游算法、并发或性能。后续模块沿用既有能力时构建安装并核对产物，不强制新增专用测试目录；引擎新增行为则补公共回归验证。v142 参数保留由公共测试覆盖，实机工具集一致性仍由后续人工验收，不以 v143 成功替代。
