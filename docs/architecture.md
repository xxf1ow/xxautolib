# 架构

根 [CMakeLists.txt](../CMakeLists.txt) 设置编译选项、安装目录并输出构建环境摘要，通过 `BUILD_LIST` 或目录扫描选择模块。它启用 C/C++ 编译器，并通过[公共入口](../src/build_system/index.cmake)探测 uv 与 Git、校验锁文件、同步根目录 `.venv/`，一次生成并加载所选模块。环境或生成失败时 configure 直接失败，不回退到系统 Python，也不加载旧生成结果。

## 目录职责

模块定义与流水线引擎分为两层。`src/dependencies/<模块名>/module.toml` 只声明数据，中文注释说明维护约束；补丁与专用执行文件放在同一模块目录。默认发现全部 module.toml，`BUILD_LIST` 使用逗号分隔模块名并去重，缺少声明时报错。

`src/build_system/` 中 definition.py 解析与校验 TOML，cmake.py 输出原生 ExternalProject 声明，generate.py 组织生成并登记实际读取的 Python/TOML 输入。CMake 负责自动重配置；生成内容不变时保留时间戳。ExternalProject 负责下载、补丁、构建、安装及原生自定义步骤；apply_patch.cmake 检查并应用文件补丁，不自动恢复冲突源码。

zlib 使用 cmake 类型和静态专用补丁；queue 使用两个资源和明确文件复制；FreeType 使用多 URL 归档；lz4 从源码子目录构建静态库；ONNX Runtime 按目标平台、架构选归档并完整复制安装。Git 与归档由 ExternalProject 获取，不自建下载器。子项目沿用父生成器身份、显式 CRT，固定 Release；模块 cache 优先于父级值，再采用引擎 policy、PIC、C++ 标准兜底值，不向父级 cache 写默认值。本项目不提供模块依赖闭包或下游导入接口。

各工作副本的 `.venv/`、`git/`、`build/` 和 `3rdparty/` 不提交或跨平台传输。主 Git 位于 `git/<模块>/`，Git 资源位于 `git/_resources/<模块>/<资源>/`，归档下载共用根 `git/`；归档解压、编译和步骤目录留在 `build/ep/`，可读生成结果留在 `build/generated/`。生成 CMake 在 configure 时判断本地 `.git` 与安装 sentinel；命中 sentinel 只保留同名空目标，声明校验仍执行。复用限制与验收状态见[本地复用设计](agent-notes/implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)。`tests/build_system/` 验证公共引擎，库专用测试验证安装产物。

## 文档职责

[开发指南](development.md)拥有环境和构建命令，[测试指南](testing.md)拥有验证入口。长期理由与授权见[仓库基础设施决定](agent-notes/implemented/process/2026-10-19-repository-foundation.md)；模块定义与生成规则见[流水线设计](agent-notes/implemented/architecture/2026-10-19-toml-externalproject-zlib.md)。

[路线图](roadmap.md)区分本仓库已验收能力、待迁移能力及待实施方向，不作为当前接口说明。
