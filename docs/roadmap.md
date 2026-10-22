# 路线图

本仓库按能力分批接入模块，先完成本地构建安装，再建立下游导入、二进制交付和 CI。本文维护阶段目标与迁移范围；各阶段的理由和约束由链接的 Agent Note 拥有，具体实现开始前再确认该批设计。

工具层重建及导入链路按 [Go 工具层与离线导入设计](agent-notes/proposed/architecture/2026-10-21-go-tooling-and-offline-import.md)分阶段推进：Go 生成器已替换自有 Python 路径，保留 TOML 和原生构建契约；本地二进制闭环、联网引导与工具发行仍待实施，不表示下游接口已可用。

## 状态与依据

本仓库支持 TOML 定义、Go builder、原生 ExternalProject、固定来源、多资源、补丁、自定义步骤、平台输入及依赖闭包。下表已验证模块均通过 Windows x64、Linux aarch64 本地安装；v142、Linux x86_64 实机仍待验收。流程由[模块技能](../.agents/skills/add-third-party-module/SKILL.md)维护；当前职责见[架构](architecture.md)，验证入口见[测试](testing.md)。

“参考已实现”指参考实现中存在对应代码或模块，并有相关决定；不表示本仓库支持，也不表示本轮重新验证了参考实现的全部模块。“参考待实施”指已有方向但尚未落地。迁移不导入历史提交、旧发布数据或所有辅助机制。

## 建议接入顺序

以下为依赖顺序，不是一次实施授权或工期承诺；无需等全部模块迁完才接入下游验证。

| 阶段 | 参考状态 | 本仓库待做与完成标志 |
| --- | --- | --- |
| 本地获取与安装扩展 | 已实现 | 已验证：URL/SHA256/原生镜像回退、平台/架构来源、SOURCE_SUBDIR 和目录复制；lz4、ONNX Runtime 在 Windows x64、Linux aarch64 完成真实安装。Linux x86_64 仅验证声明选择。 |
| 本地输入与安装复用 | 已实现 | 已验证：持久 Git、哈希归档复用和 sentinel 短路，公共回归双端通过；已接入模块登记 sentinel，不把存在性当版本校验。 |
| 模块依赖 | 已实现 | 已验证 FLANN → lz4、HarfBuzz → FreeType、OpenCV → FreeType/HarfBuzz；叶子请求得到稳定闭包，显式依赖实际来自本仓库。Boost、PCL 待接入。 |
| 复杂模块与专用文件 | 已实现 | 已验证 OpenCV 4.14.0 contrib 路径引用、资源补丁、平台安装布局与链接运行；oneTBB HWLOC、ImGui 专用文件仍待接入。 |
| 下游导入与可搬迁包 | 已实现 | 模块显式提供包内 adapter；用少量代表包完成搬迁后真实链接，打包不改原安装树；本地构建资格与发布资格分离。 |
| 固定槽交付与缓存 | 已实现 | 按名称消费、严格 manifest、摘要校验、生产端内部 build ID、GitHub 发布和 Gitee 镜像逐项接入；先用本地 fixture 验证，远端地址与写操作另行授权。 |
| 三平台原生 CI | 基础流水线已实现，单 job 优化待实施 | 接入 Windows x86_64、Linux x86_64/aarch64；以原生单 job 分层、统一环境与并行预算为目标，不先复制多 job 传完整包的结构再重写。 |
| 平台无关头文件槽位 | 待实施 | 仅对显式声明且安装内容确实平台无关的包启用 any/any；规划、导入、缓存、manifest、镜像共同支持，并通过三平台消费。 |

本地能力的既有决定见[模块扩展](agent-notes/proposed/architecture/2026-10-20-module-expansion.md)，本批方案见[本地复用与平台输入设计](agent-notes/implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)；导入、打包和固定槽见[二进制交付](agent-notes/proposed/architecture/2026-10-20-name-only-artifact-delivery.md)；环境、ABI、并行和通用槽位见[三平台 CI](agent-notes/proposed/process/2026-10-20-native-platform-ci.md)。

FLANN、HarfBuzz 与 OpenCV 4.14.0 的具体取舍和双平台证据见[依赖与复杂资源决定](agent-notes/implemented/architecture/2026-10-20-flann-opencv-dependencies.md)；这不表示其他复杂模块或二进制交付已经完成。

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

不新增源码原地自动升级、补丁自动回滚、独立构建状态数据库、通用恢复服务、交叉编译或硬件自动探测。根 CMake 保持已确认的参考骨架；未来扩展只作该批必要适配。README 暂缓，历史发布资产不自动迁移或删除。每一批按实际风险验证，不为每库强制新增专用测试工程。
