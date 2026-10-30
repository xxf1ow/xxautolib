# 参与贡献

欢迎提交缺陷修复、文档改进和第三方库接入。小范围修正可以直接提交 PR；改变公共接口、包格式或发布流程前，请先开 Issue 说明使用场景、现有问题及建议方案，避免投入到不符合项目定位的实现。

## 报告问题

先搜索[已有 Issue](https://github.com/xxf1ow/xxautolib/issues)，避免重复报告。新 Issue 请提供：

- 操作系统、CPU 架构、编译器和 CMake 版本。
- 使用的库、包文件名，以及能够复现问题的命令或最小工程。
- 预期结果、实际结果和完整错误信息；日志中移除 token、凭据及私密路径。

## 准备开发环境

Fork 本仓库，将自己的 Fork 克隆到本地，从最新 `master` 创建普通任务分支。环境要求和构建命令以[开发指南](docs/development.md)为准；首次运行生产端入口会准备固定 Go SDK，无需自行升级 Go 或依赖版本。

每个 PR 聚焦一个问题。保留工作区已有修改，不顺手重构无关模块，也不增加没有实际需求的兼容层或扩展机制。

## 贡献新包

先检查 `src/dependencies/` 中的现有模块，避免重复接入。源码构建接入与快捷导入是两项能力：

1. 创建 `src/dependencies/<name>/package.toml`，用 `latest` 指向明确版本；在 `<version>/module.toml` 填写用途、类型和同目录版本。CMake 构建参考 [lz4](src/dependencies/lz4/1.10.0/module.toml)，仅复制安装参考 [queue](src/dependencies/queue/45db9a8b443122184c1f6b9db2c57453a9da1c6bcc18a5d7cfa4da29832fafed/module.toml)。Git 固定完整提交，归档固定 SHA256；声明直接依赖及系统要求。
2. 将必要的补丁、专用脚本放在同一版本目录，只保留实际需要的构建选项。独立库通过 `depends` 复用，不在使用方重复构建；需要固定依赖时使用 `包名@版本`。
3. 通过[根 CMake 入口](docs/development.md#准备与配置)设置 `BUILD_LIST=<name>@<version>`，检查 `<安装根>/<name>/<version>/` 及依赖闭包；裸名只选 latest，空列表发现全部版本。configure 成功不等于构建通过。
4. 若提供快捷导入，增加同目录 `autolib-import.cmake`，仅暴露 `autolib::<name>`，封装所支持的上游目标并传递已声明的包依赖；adapter 不下载、编译或回退系统库。再完成[打包与真实消费验证](docs/testing.md#本地二进制闭环)：消费者只列根包即可链接运行，搬迁后不依赖生产路径。

PR 附来源版本、许可证和实测平台。版本化布局的消费、打包与 CI 尚待适配；上述 adapter 和消费检查不表示新版交付已验收。仅有构建配方的模块不进入发布集合，实际发行仍须维护者明确授权运行。

## 修改与验证

先阅读[架构说明](docs/architecture.md)。公共构建引擎、消费入口和发布编排分别由 builder、importer/CMake、CI 维护，不互相混入业务实现。

缺陷修复应包含能够复现问题的回归测试。按[测试指南](docs/testing.md#执行)选择拥有该行为的测试；修改公共入口或跨模块接线时运行对应全集，公共消费接口还需验证真实消费者。纯文档改动检查链接、参数和示例命令，不需要重编所有第三方库。

行为变化同时更新对应文档。不要手改生成文件，或提交 `build/`、`3rdparty/`、下载缓存、私有 SDK、凭据和 `.superpowers/`。第三方代码与补丁需保留许可证及来源说明。

## 提交 Pull Request

向本仓库的 `master` 发起 [Pull Request](https://github.com/xxf1ow/xxautolib/compare)，在描述中写清楚：

1. 解决什么问题，以及关联的 Issue（如有）。
2. 主要改动和对现有用法的影响；公共行为改变时附新的最小用法。
3. 实际执行的验证命令、结果和平台；未验证的平台或场景明确列出。

涉及第三方库时，另列来源版本、依赖和许可证。不要把“成功编译”写成“全部平台通过”，也不要把 fixture 测试当作真实发行验证。

发布 workflow 可手动选择可信分支，不接受 tag；运行会替换正式 Release 的固定名资产，须由维护者明确授权。贡献者无需提供发布 token 或为 PR 触发发布；评审后在原任务分支追加修改即可。
