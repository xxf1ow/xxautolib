# 架构

根 [CMakeLists.txt](../CMakeLists.txt) 设置编译选项、安装目录并输出构建环境摘要，通过 `BUILD_LIST` 或目录扫描选择模块。它启用 C/C++ 编译器，并通过[公共入口](../src/build_system/index.cmake)探测 uv 与 Git、校验锁文件、同步根目录 `.venv/`，一次生成并加载所选模块。环境或生成失败时 configure 直接失败，不回退到系统 Python，也不加载旧生成结果。

## 目录职责

模块定义与流水线引擎分为两层。`src/dependencies/<模块名>/module.toml` 只声明数据，中文注释说明维护约束；补丁与专用执行文件放在同一模块目录。默认发现全部 module.toml，`BUILD_LIST` 使用逗号分隔模块名并去重，缺少声明时报错。

`src/build_system/` 中 definition.py 解析与校验 TOML，cmake.py 输出原生 ExternalProject 声明，generate.py 组织生成并登记实际读取的 Python/TOML 输入。CMake 负责自动重配置；生成内容不变时保留时间戳。ExternalProject 负责下载、补丁、构建、安装及原生自定义步骤；apply_patch.cmake 检查并应用文件补丁，不自动恢复冲突源码。

zlib 使用 cmake 类型和静态专用补丁；queue 使用无主来源的 copy 类型，获取两个独立资源并安装六个头文件。子项目沿用父生成器身份、显式 CRT，固定 Release；本项目不提供模块依赖闭包或下游导入接口。

各工作副本仅使用根 `.venv/`、`build/` 和 `3rdparty/<模块名>/`，均不提交或跨平台传输。`build/generated/` 保存可检查的生成 CMake，`build/ep/` 保存独立的来源、编译及步骤目录。`tests/build_system/` 验证公共引擎，`tests/zlib/`、`tests/queue/` 验证安装产物。

## 文档职责

[开发指南](development.md)拥有环境和构建命令，[测试指南](testing.md)拥有验证入口。长期理由与授权见[仓库基础设施决定](agent-notes/implemented/process/2026-10-19-repository-foundation.md)；模块定义与生成规则见[流水线设计](agent-notes/implemented/architecture/2026-10-19-toml-externalproject-zlib.md)。
