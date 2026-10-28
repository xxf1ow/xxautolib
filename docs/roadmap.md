# 路线图

本仓库按能力分批接入模块，先完成本地构建安装，再建立下游导入、二进制交付和 CI。本文维护阶段目标与迁移范围；各阶段的理由和约束由链接的 Agent Note 拥有，具体实现开始前再确认该批设计。

工具层重建及导入链路按 [Go 工具层与离线导入设计](agent-notes/proposed/architecture/2026-10-21-go-tooling-and-offline-import.md)分阶段推进：Go 生成器替换自有 Python 路径，保留 TOML 和原生构建契约；第二阶段实现本地打包、独立 importer 和单文件消费入口。queue、lz4、flann 在 Windows x64/v143、Linux aarch64 完成发行与准备包搬迁后的真实链接运行，第二阶段已通过用户验收。[分阶段消费设计](agent-notes/proposed/architecture/2026-10-25-target-import-and-release-consumption.md#已验收范围)的本地接口与三平台 GitHub 下载阶段已通过用户验收；builder 安装产物直接复用由独立设计拥有，Gitee 消费的本地实现及远端缺口见下节。

独立的 [CI 系统](agent-notes/implemented/architecture/2026-10-20-ci-system.md)完成三平台代表库的原生构建、Release 供给，不作为 Go 工具层的子阶段，也不要求先批量增加第三方库。[无全局清单包消费](agent-notes/proposed/architecture/2026-10-24-manifestless-package-consumption.md)的本地协议已通过用户验收，三平台新协议均完成搬迁链接运行。CI 的同名替换、分层编排、手动 workflow 和 token 写入已有真实证据；[每轮重建与文件名发布](agent-notes/implemented/simplification/2026-10-24-ci-rebuild-without-production-cache.md)替代跨运行生产缓存，三平台远端验证及文件名展示已核对；后续远端操作仍须授权。后续设计交接及扩库由用户确认，不自动推进。

## 状态与依据

[importer 压缩分发与功能分支手动发布](agent-notes/proposed/architecture/2026-10-27-importer-distribution-and-branch-release.md)提供正式工具去符号、CI 单程序 tar.xz 和 CMake 原生引导。压缩格式的跨平台、最低 CMake 及真实 CI 验收仍待完成；[Gitee 独立同步](agent-notes/proposed/architecture/2026-10-27-gitee-release-sync-workflow.md)已有本地实现，真实 runner 验收仍独立进行。

[CI 自动发现与双平台消费](agent-notes/implemented/architecture/2026-10-27-ci-discovery-and-release-sources.md)提供资格静默过滤、实际依赖层及正文摘要校验。本机验证空根引导、传递依赖链接运行及本地零网络；真实Gitee消费、Linux双架构及最低CMake尚无本轮证据，须另行授权发布匹配工具，不能由同步成功推定可用。

本仓库支持 TOML 定义、Go builder、原生 ExternalProject、固定来源、多资源、补丁、自定义步骤、平台输入及依赖闭包。下表已验证模块均通过 Windows x64、Linux aarch64 本地安装；除 CI 代表集外，Linux x86_64 实机及全部 v142 实机仍待验收。流程由[模块技能](../.agents/skills/add-third-party-module/SKILL.md)维护；当前职责见[架构](architecture.md)，验证入口见[测试](testing.md)。

“参考已实现”指参考实现中存在对应代码或模块，并有相关决定；不表示本仓库支持，也不表示本轮重新验证了参考实现的全部模块。“参考待实施”指已有方向但尚未落地。迁移不导入历史提交、旧发布数据或所有辅助机制。

## 建议接入顺序

以下为依赖顺序，不是一次实施授权或工期承诺；无需等全部模块迁完才接入下游验证。

| 阶段 | 参考状态 | 本仓库待做与完成标志 |
| --- | --- | --- |
| 本地获取与安装扩展 | 已实现 | 已验证：URL/SHA256/原生镜像回退、平台/架构来源、SOURCE_SUBDIR 和目录复制；lz4、ONNX Runtime 在 Windows x64、Linux aarch64 完成真实安装。lz4 另经三平台 CI；ONNX Runtime 的 Linux x86_64 仅验证声明选择。 |
| 本地输入与安装复用 | 已实现 | 已验证：持久 Git、哈希归档复用和 sentinel 短路，公共回归双端通过；已接入模块登记 sentinel，不把存在性当版本校验。 |
| 模块依赖 | 已实现 | 已验证 FLANN → lz4、HarfBuzz → FreeType、OpenCV → FreeType/HarfBuzz；叶子请求得到稳定闭包，显式依赖实际来自本仓库。Boost、PCL 待接入。 |
| 复杂模块与专用文件 | 已实现 | 已验证 OpenCV 4.14.0 contrib 路径引用、资源补丁、平台安装布局与链接运行；oneTBB HWLOC、ImGui 专用文件仍待接入。 |
| 下游导入与可搬迁包 | 已实现 | queue、lz4、flann 的显式包内 adapter 已完成双端搬迁链接运行，原安装树不变；其他模块按需提供 adapter，本地构建资格与发布资格分离。 |
| 固定槽交付 | 已实现 | 本地消费和 GitHub 发布已验证；CI 每轮重建，不维护跨运行生产包缓存，各槽独立更新、无全局发行清单，汇总只报告结果。Gitee 同步及消费已有本地实现，真实验收与后续远端写操作仍须授权。 |
| 三平台原生 CI | 已实现 | queue、lz4、flann 的三平台原生单 job、统一环境、FIFO 预算、真实供给已有验证证据。任意分支可手动运行；OpenCV 等其余库按需另行确认。 |
| 平台无关头文件槽位 | 已实现 | queue-any-any 的打包、导入、唯一 publisher 和三平台消费已验证，只有一个通用资产；机器码包与 importer 仍限定平台。 |

本地能力的既有决定见[模块扩展](agent-notes/proposed/architecture/2026-10-20-module-expansion.md)，本批方案见[本地复用与平台输入设计](agent-notes/implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)；导入、打包和固定槽见[二进制交付](agent-notes/proposed/architecture/2026-10-20-name-only-artifact-delivery.md)；流水线、环境、ABI、并行和通用槽位见独立的[CI 系统设计](agent-notes/implemented/architecture/2026-10-20-ci-system.md)。

FLANN、HarfBuzz 与 OpenCV 4.14.0 的具体取舍和双平台安装证据见[依赖与复杂资源决定](agent-notes/implemented/architecture/2026-10-20-flann-opencv-dependencies.md)；本地二进制闭环入口见[测试指南](testing.md#本地二进制闭环)。这些证据不表示其他复杂模块或远端二进制发行已经完成。

## 模块迁移候选

下表覆盖参考仓库的 36 个模块定义。分组用于选择有代表性的下一批，不表示组内模块复杂度相同或全部平台已验证；不把参考版本号和选项表复制为另一份维护清单。

| 分组 | 模块 | 本仓库状态或接入重点 |
| --- | --- | --- |
| 已验证 | zlib、queue、freetype、lz4、onnxruntime、flann、harfbuzz、opencv | Windows x64、Linux aarch64 本地构建或复制安装通过，不追加下游交付要求；保留系统可选依赖边界。 |
| 基础构建与头文件 | asio、clipper2、cpp-httplib、eigen、expected、ftxui、gtest、libzmq、nativefiledialog-extended、nlohmann、portaudio、pybind11、sdl3、spdlog、taskflow、websocketpp、yaml-cpp | 待迁移；按需挑选，覆盖归档、源码子目录、系统依赖和安装布局；不是要求一次迁移整组。 |
| 依赖链 | boost、pcl | 待迁移；本仓库产物的显式依赖与标准查找通道。 |
| 多资源及专用构建 | oneTBB、imgui、gtsam | 待迁移；附属编译输入、专用文件、补丁与依赖，不把库逻辑塞进引擎。 |
| 预编译输入 | mysql | 待迁移；按平台选择已登记归档及摘要，核对复制布局与独立运行要求。 |
| 专项模块 | sherpa-onnx、lastools、logo | 待按实际需求确定批次；分别涉及私有依赖参数、平台限制或资源产物。 |
| 暂缓 | fastdeploy、nndeploy | 初版不做；不因参考实现存在便引入硬编码 SDK 路径或自动后端探测。 |

参考实现的可搬迁导入适配覆盖 eigen、onnxruntime、portaudio、lz4、flann、pcl、boost、zlib；它们是后续代表集，不意味着其他模块不可本地构建，也不要求新仓库首批同时发布八包。

## 保留的范围限制

不新增源码原地自动升级、补丁自动回滚、独立构建状态数据库、通用恢复服务、交叉编译或硬件自动探测。根 CMake 保持已确认的参考骨架；未来扩展只作该批必要适配。历史发布资产不自动迁移或删除。每一批按实际风险验证，不为每库强制新增专用测试工程。
