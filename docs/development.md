# 开发

本指南从安装好的 uv、Git、CMake 和 C/C++ 编译器开始，完成根 Python 环境准备与空模块仓库配置。CMake 最低版本由根入口声明为 3.20；本地验证环境为 Windows 10 x64、uv 0.11.14、Git 2.37.1.windows.1、CMake 4.2.3、MSVC 19.42.34444.0 和 uv 管理的 Python 3.12.13。

## 准备与配置

在仓库根目录执行：

```text
uv sync --locked
cmake -S . -B build
```

命令成功后，解释器位于根 `.venv/`，CMake 工程位于 `build/`，输出包含工具链、安装目录和模块列表摘要。公共入口也会同步根环境，显式忽略指向其他目录的 `UV_PROJECT_ENVIRONMENT`。重复配置复用该环境；空模块仓库不执行库下载、编译或安装。

缺少 uv 或 Git 时，先安装对应工具并使其可被 CMake 找到，再重新配置。锁文件不一致时配置失败；只有有意修改 Python 项目元数据或依赖时，才运行 `uv lock` 更新并审阅 `uv.lock`，不要删除锁文件绕过校验。

Python 版本由 `.python-version` 选择。所有 Python 生成器、测试、开发脚本和后续 CI 入口统一经 uv 运行，不另用 pip、手工 venv 或系统 Python 回退。

## 日常修改

目录职责见 [架构](architecture.md)，验证命令见 [测试](testing.md)。不提交 `.venv/`、`build/`、`3rdparty/` 或忽略的 `.superpowers/`。现有目录可能含其他任务产物，不为获取干净结果清空它们。
