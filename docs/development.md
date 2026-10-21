# 开发

本指南从已安装的 uv、Git、CMake 3.20+ 和 C/C++ 编译器开始，通过根入口构建安装。FLANN、OpenCV 还需可被找到的 pkg-config；FLANN 使用 OpenMP，FFmpeg 按机器开发环境探测。所有 Python 环境准备、生成器、测试及开发脚本统一经 uv 执行，不使用 pip、手工 venv 或系统 Python 回退。

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

默认发现全部模块，分别安装到 `3rdparty/<模块名>/`。只选部分模块时追加 `"-DBUILD_LIST=opencv"`，自动包含 freetype、harfbuzz；选择 flann 自动包含 lz4。根 BUILD_LIST 不传入 OpenCV。configure 输出工具链、安装目录与模块摘要；获取和安装在 build 阶段执行。

公共入口同步根 `.venv/`，忽略指向其他目录的 `UV_PROJECT_ENVIRONMENT`。Python 版本由 `.python-version` 选择；重复配置复用环境。每份工作副本统一用 build，不在 Windows 与 Linux 间混用产物。子项目固定 Release，沿用父生成器、平台、完整工具集和 instance；父显式 CRT 原值传递，未指定时 MSVC Release 使用 `/MD`，静态库不自动改为 `/MT`。

缺少 uv 或 Git 时，先安装对应工具并使其可被 CMake 找到，再重新配置。锁文件不一致时配置失败；只有有意修改 Python 项目元数据或依赖时，才运行 `uv lock` 更新并审阅 `uv.lock`，不要删除锁文件绕过校验。

补丁无法正向或反向检查时，构建明确失败，需人工重新准备该模块的干净源码；不会自动还原或清空源码。不要把修改补丁等同于自动替换已应用的旧补丁。

Git 主来源复用 `git/<模块名>/`，资源复用 `git/_resources/<模块名>/<资源名>/`；已有 `.git` 文件或目录即关闭下载，不核对提交或自动 update。非空无 `.git` 目录报错且保留，包括隐藏文件。引擎不迁移已有 build 内源码。更换来源、版本或需重建时，先保留人工修改，再人工准备受影响持久来源、`build/ep/<模块名>/` 和 `3rdparty/<模块名>/`；可移到备份位置，不清空整个 build 或其他模块。重新获取后核对提交与产物。

可选 `sentinel` 是模块安装前缀下的相对路径；configure 时存在即显示 cached，保留同名空目标和模块依赖顺序，不执行资源、补丁或步骤。安装后重新 configure 才切换为空目标；同 build 的普通重复构建仍依赖原生 stamp。sentinel 不校验版本、ABI 或完整性，缺失也不保证已有 stamp 自动修复安装。理由及路径规则见[本地复用设计](agent-notes/implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)。

归档 `URL` 是有序非空数组，`URL_HASH` 必须为 `SHA256=<64位十六进制摘要>`，可用单文件名 `DOWNLOAD_NAME`。版本、URL 与摘要同步维护；下载共用根 `git/`，匹配摘要的归档由原生流程复用，解压仍在 build。cmake 类型用 `[cmake] SOURCE_SUBDIR` 选择来源内子目录；copy 映射用互斥的 `files` 或 `directories`，`directories=["."]`、`destination="."` 复制完整内容。平台来源按公共→平台→架构整组替换，资源按名称整组替换；目标架构来自系统处理器，VS 显式平台优先，无匹配来源报错。具体声明参考 [lz4](../src/dependencies/lz4/module.toml) 与 [ONNX Runtime](../src/dependencies/onnxruntime/module.toml)。

## 日常修改

修改带注释的 module.toml 或同目录专用文件，不手改 `build/generated/`。依赖 TOML、引擎 helper 变化触发重新 configure；补丁及 DEPENDS 脚本由原生步骤跟踪。目录职责见[架构](architecture.md)，验证命令见[测试](testing.md)。不提交环境、来源、构建、安装和 `.superpowers/`；不整体跨平台复制，也不清空已有目录。

包输入先经 Windows 下载校验，再单独传入授权 ARM 目录；可传仅含上游历史的 Git bundle、归档及原生内部源码缓存，不传项目 `.git/`、凭据或宿主产物。OpenCV 在 ARM 使用进程环境 `OPENCV_DOWNLOAD_PATH=/home/lxx/xxautolib/git/opencv-internal-cache` 读取已传缓存，不在 ARM 重试下载。授权及数据保护见[取源规则](agent-notes/implemented/process/2026-10-19-repository-foundation.md#验证授权)。
