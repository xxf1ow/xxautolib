# 架构

根 [CMakeLists.txt](../CMakeLists.txt) 设置编译选项、安装目录并输出构建环境摘要，通过 `BUILD_LIST` 或目录扫描选择模块。它启用 C/C++ 编译器，并在 [公共入口](../src/build_system/index.cmake) 中探测 uv 与 Git、校验锁文件、同步根目录 `.venv/`。配置失败时直接报告错误，不回退到系统 Python。

## 目录职责

源码按维护对象组织：`src/build_system/` 存放通用构建代码，库专用内容按已确认布局归入 `src/dependencies/<库名>/`。依赖目录为空，没有库构建目标。根入口保留 `.cmake` 模块发现与加载：默认扫描 `src/dependencies/*.cmake`，指定 `BUILD_LIST` 时使用逗号分隔的模块名；缺少文件则报错。Python recipe 接口及按库子目录的发现、生成与加载尚未接入，不将现有加载流程视为 recipe API。

`tests/build_system/` 验证基础入口，`tests/<库名>/` 保存库专用验证。各工作副本只使用根目录 `.venv/`；`build/` 保存中间产物，`3rdparty/` 保存安装产物，三者均不提交，也不在 Windows 与 Linux 间传输。

## 文档职责

[开发指南](development.md) 拥有环境准备与配置命令，[测试指南](testing.md) 拥有验证入口。长期理由与取舍见 [仓库基础设施决定](agent-notes/implemented/process/2026-10-19-repository-foundation.md)，未来方案进入 proposed Agent Note，不作为当前构建能力。
