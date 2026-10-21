# 开发

本指南从已安装的 uv、Git、CMake 3.20+ 和 C/C++ 编译器开始，通过根入口构建并安装模块。获取 Git 或归档来源需要网络。所有 Python 环境准备、生成器、测试及开发脚本统一经 uv 执行，不使用 pip、手工 venv 或系统 Python 回退。

## 准备与配置

在仓库根目录准备开发环境，再选择平台生成器；`dev` 组包含技能校验所需的 PyYAML，版本由 uv.lock 锁定：

```text
uv sync --locked
```

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

默认构建 zlib、queue、freetype，分别安装到 `3rdparty/<模块名>/`。只选部分模块时在 configure 命令追加 `-DBUILD_LIST=freetype` 或 `-DBUILD_LIST=zlib,queue`。configure 输出工具链、安装目录与模块摘要；源码获取和安装在 build 阶段执行。

公共入口同步根 `.venv/`，忽略指向其他目录的 `UV_PROJECT_ENVIRONMENT`。Python 版本由 `.python-version` 选择；重复配置复用环境。每份工作副本统一用 build，不在 Windows 与 Linux 间混用产物。子项目固定 Release，沿用父生成器、平台、完整工具集和 instance；父显式 CRT 原值传递，未指定时 MSVC Release 使用 `/MD`，静态库不自动改为 `/MT`。

缺少 uv 或 Git 时，先安装对应工具并使其可被 CMake 找到，再重新配置。锁文件不一致时配置失败；只有有意修改 Python 项目元数据或依赖时，才运行 `uv lock` 更新并审阅 `uv.lock`，不要删除锁文件绕过校验。

补丁无法正向或反向检查时，构建明确失败，需人工重新准备该模块的干净源码；不会自动还原或清空源码。不要把修改补丁等同于自动替换已应用的旧补丁。

主来源和附属资源不自动 update。更换来源地址或固定提交时，仅修改 TOML 并重新 configure 不能视为升级完成。维护者须先保留需要的本地修改，再人工重新准备受影响模块的 `build/ep/<模块名>/`（含资源、构建和 stamp）及 `3rdparty/<模块名>/`；可移到备份位置，不清空整个 build 或其他模块。重新获取后核对源码提交与安装产物。普通配置选项和执行输入的增量跟踪不等于源码版本迁移，理由见[流水线决定](agent-notes/implemented/architecture/2026-10-19-toml-externalproject-zlib.md)。

归档来源的 `URL` 是有序非空数组，`URL_HASH` 必须为 `SHA256=<64位十六进制摘要>`；需要统一镜像文件名时使用单文件名 `DOWNLOAD_NAME`。版本、URL 与摘要同步维护，主来源和附属资源适用同一规则；镜像回退和解压使用 ExternalProject 原生行为，不添加下载脚本。更换归档同样按上述流程保护并重新准备受影响模块。

## 日常修改

修改带注释的 module.toml 或同目录专用文件，不手改 `build/generated/`。TOML、实际引擎 helper 变化会触发重新 configure；补丁与声明为 DEPENDS 的脚本由原生步骤依赖跟踪。目录职责见[架构](architecture.md)，验证命令见[测试](testing.md)。不提交或传输 `.venv/`、`build/`、`3rdparty/`、`.git/` 与忽略的 `.superpowers/`；不为获取干净结果清空已有目录。
