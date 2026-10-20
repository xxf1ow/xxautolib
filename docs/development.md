# 开发

本指南从已安装的 uv、Git、CMake 3.20+ 和 C/C++ 编译器开始，通过根入口构建并安装模块。获取固定 Git 来源需要网络。所有 Python 环境准备、生成器、测试及开发脚本统一经 uv 执行，不使用 pip、手工 venv 或系统 Python 回退。

## 准备与配置

在仓库根目录准备环境，然后选择平台配置：

```text
uv sync --locked
```

Windows x64、Visual Studio 2022：

```text
cmake -S . -B build -G "Visual Studio 17 2022" -A x64 -T "v143,host=x64"
```

Linux、GCC：

```text
cmake -S . -B build -G "Unix Makefiles" -DCMAKE_BUILD_TYPE=Release
```

两端构建命令相同：

```text
cmake --build build --config Release
```

默认构建 zlib、queue，安装到 `3rdparty/zlib/`、`3rdparty/queue/`。只选部分模块时在 configure 命令追加 `-DBUILD_LIST=zlib`、`-DBUILD_LIST=queue` 或 `-DBUILD_LIST=zlib,queue`。configure 输出工具链、安装目录与模块摘要；源码获取和安装在 build 阶段执行。

公共入口同步根 `.venv/`，忽略指向其他目录的 `UV_PROJECT_ENVIRONMENT`。Python 版本由 `.python-version` 选择；重复配置复用环境。每份工作副本统一用 build，不在 Windows 与 Linux 间混用产物。子项目固定 Release，沿用父生成器、平台、完整工具集和 instance；父显式 CRT 原值传递，未指定时 MSVC Release 使用 `/MD`，静态库不自动改为 `/MT`。

缺少 uv 或 Git 时，先安装对应工具并使其可被 CMake 找到，再重新配置。锁文件不一致时配置失败；只有有意修改 Python 项目元数据或依赖时，才运行 `uv lock` 更新并审阅 `uv.lock`，不要删除锁文件绕过校验。

补丁无法正向或反向检查时，构建明确失败，需人工重新准备该模块的干净源码；不会自动还原或清空源码。不要把修改补丁等同于自动替换已应用的旧补丁。

主来源和附属资源不自动 update。更换来源地址或固定提交时，仅修改 TOML 并重新 configure 不能视为升级完成。维护者须先保留需要的本地修改，再人工重新准备受影响模块的 `build/ep/<模块名>/`（含资源、构建和 stamp）及 `3rdparty/<模块名>/`；可移到备份位置，不清空整个 build 或其他模块。重新获取后核对源码提交与安装产物。普通配置选项和执行输入的增量跟踪不等于源码版本迁移，理由见[流水线决定](agent-notes/implemented/architecture/2026-10-19-toml-externalproject-zlib.md)。

## 日常修改

修改带注释的 module.toml 或同目录专用文件，不手改 `build/generated/`。TOML、实际引擎 helper 变化会触发重新 configure；补丁与声明为 DEPENDS 的脚本由原生步骤依赖跟踪。目录职责见[架构](architecture.md)，验证命令见[测试](testing.md)。不提交或传输 `.venv/`、`build/`、`3rdparty/`、`.git/` 与忽略的 `.superpowers/`；不为获取干净结果清空已有目录。
