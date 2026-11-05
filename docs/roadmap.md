# 路线图

本仓库按能力分批接入模块，先完成本地构建安装，再建立下游导入、二进制交付和 CI。本文维护阶段目标与迁移范围；各阶段的理由和约束由链接的 Agent Note 拥有，具体实现开始前再确认该批设计。

后续开发遵循[架构基准](architecture.md)。版本化构建安装、本地消费、Git 元数据远端消费及[Gitee 交付](agent-notes/implemented/architecture/2026-11-02-gitee-delivery-reimplementation.md)四个阶段均已通过用户验收；实际证据与未记录专项见[验收范围](testing.md#真实交付与手动验收)，不推定全部第三方库已全量编译通过。sherpa 独立接入。

[Go 工具层与离线导入设计](agent-notes/proposed/architecture/2026-10-21-go-tooling-and-offline-import.md)的 Go 重建与本地交付阶段已通过用户验收；[分阶段消费设计](agent-notes/proposed/architecture/2026-10-25-target-import-and-release-consumption.md#已验收范围)记录本地接口和三平台 GitHub 下载验收。具体平台、搬迁链接及协议证据由对应设计拥有，Gitee 消费验收见下节。

独立的[CI 系统](agent-notes/implemented/architecture/2026-10-20-ci-system.md)完成三平台代表库的原生构建与发布，不依赖扩库进度。[无全局清单包消费](agent-notes/proposed/architecture/2026-10-24-manifestless-package-consumption.md)的本地协议及三平台搬迁链接已验收；[每轮重建与文件名发布](agent-notes/implemented/simplification/2026-10-24-ci-rebuild-without-production-cache.md)记录替代生产缓存后的三平台远端证据。后续设计交接、扩库及远端操作仍须确认，不自动推进。

## 状态与依据

[importer 压缩分发](agent-notes/proposed/architecture/2026-10-27-importer-distribution-and-branch-release.md)提供工具去符号、单程序 tar.xz 和 CMake 原生引导依据；[Gitee 同步参考](agent-notes/proposed/architecture/2026-10-27-gitee-release-sync-workflow.md)保留附件接入理由。当前交付只使用独立 Gitee 发布，不经 GitHub Release 中转；验证范围由[交付记录](agent-notes/implemented/architecture/2026-11-02-gitee-delivery-reimplementation.md#验证证据)拥有。最低 CMake 按用户决定不安排测试。

[CI 自动发现与双平台消费](agent-notes/implemented/architecture/2026-10-27-ci-discovery-and-release-sources.md#验证)提供资格静默过滤、实际依赖层及正文摘要校验。真实 Gitee 消费已通过用户手动验收；平台、最低 CMake 及新 CI 策略的证据分别记录，不从该结果推定全部专项通过。

下表已验证模块均通过 Windows x64、Linux aarch64 本地安装；Linux x86_64 的追加消费范围见[adapter 验证](testing.md#adapter-定向验证)，全部 v142 实机仍待验收。流程由[模块技能](../.agents/skills/add-third-party-module/SKILL.md)维护；当前职责见[架构](architecture.md)，验证入口见[测试](testing.md)。

“参考已实现”指参考实现中存在对应代码或模块，并有相关决定；不表示本仓库支持，也不表示本轮重新验证了参考实现的全部模块。“参考待实施”指已有方向但尚未落地。迁移不导入历史提交、旧发布数据或所有辅助机制。

## 后续维护方向

待实施：增加轻量回归CI，复用现有Go测试、vet及独立发布单元测试；不持有发布凭据、不修改Release、不重编全部第三方库。完成标志是代码检查自动运行、失败可见且不触发发布；触发条件与平台范围在实施前确认。

当前 31 个配方版本均具备统一格式的 adapter，五类基准与新增步骤由[模块技能参考](../.agents/skills/add-third-party-module/references/adapter-baselines.md)维护；原七个缺失包及 LZ4 已完成 109 的真实消费和打包搬迁。其余包及平台的验证范围见[导入适配记录](agent-notes/proposed/architecture/2026-11-04-package-adapter-standardization.md#验证结果)，不将文件齐全等同于全库发行通过。

## 建议接入顺序

以下为依赖顺序，不是一次实施授权或工期承诺；无需等全部模块迁完才接入下游验证。

| 阶段 | 参考状态 | 本仓库待做与完成标志 |
| --- | --- | --- |
| 本地获取与安装扩展 | 已实现 | 已验证：URL/SHA256/原生镜像回退、平台/架构来源、SOURCE_SUBDIR 和目录复制；lz4、ONNX Runtime 在 Windows x64、Linux aarch64 完成真实安装，并在 Linux x86_64 完成公共入口消费与搬迁。lz4 另经三平台 CI。 |
| 本地输入与安装复用 | 已实现 | 已验证：持久 Git、哈希归档复用和 sentinel 短路，公共回归双端通过；已接入模块登记 sentinel，不把存在性当版本校验。 |
| 模块依赖 | 已实现 | 叶子请求得到稳定闭包，显式依赖来自本仓库；FLANN、HarfBuzz、OpenCV 已有双端验证。Boost、PCL 的依赖在 109 完成构建与来源核对；PCL 消费链接待 CI 验收。 |
| 复杂模块与专用文件 | 已实现 | OpenCV 的资源补丁、平台布局与链接运行已有双端证据；oneTBB 的 HWLOC 绑定及 ImGui 专用构建在 109 完成安装核对；Windows HWLOC 待 CI。 |
| 下游导入与可搬迁包 | 已实现 | queue、lz4、flann 的显式包内 adapter 已完成双端搬迁链接运行；当前全部配方版本有 adapter，追加真实消费范围见上文，本地构建资格与发布资格分离。 |
| 固定槽交付 | 已实现 | 本地消费和 GitHub 发布已验证；CI 每轮重建，不维护跨运行生产包缓存，各槽独立更新、无全局发行清单，汇总只报告结果。Gitee 消费已通过用户手动验收；同步专项证据独立记录，后续远端写操作仍须授权。 |
| 三平台原生 CI | 已实现 | queue、lz4、flann 的三平台原生单 job、统一环境、FIFO 预算、真实供给已有验证证据。任意分支可手动运行；OpenCV 等其余库按需另行确认。 |
| 平台无关头文件槽位 | 已实现 | queue-any-any 的打包、导入、唯一 publisher 和三平台消费已验证，只有一个通用资产；机器码包与 importer 仍限定平台。 |

机制与设计索引见[架构](architecture.md)；FLANN、HarfBuzz、OpenCV 的双平台证据见[依赖与复杂资源决定](agent-notes/implemented/architecture/2026-10-20-flann-opencv-dependencies.md)，不证明其他模块或远端发行通过。

## 模块迁移候选

下表保留参考仓库的迁移候选，并记录本仓库另行接入的模块。分组用于选择有代表性的下一批，不表示组内模块复杂度相同或全部平台已验证；不把参考版本号和选项表复制为另一份维护清单。

| 分组 | 模块 | 本仓库状态或接入重点 |
| --- | --- | --- |
| 已验证 | zlib、queue、freetype、lz4、onnxruntime、flann、harfbuzz、opencv | Windows x64、Linux aarch64 本地构建或复制安装通过，不追加下游交付要求；保留系统可选依赖边界。 |
| 已接入的 RSUSB 依赖链 | librealsense、libusb、nlohmann | Windows x64/v143、Linux aarch64 本地安装及无相机检查通过；具备 adapter，Linux x86_64 公共入口消费与搬迁通过，不表示实机取流验收。构建依据见[RSUSB 决定](agent-notes/implemented/architecture/2026-10-27-librealsense-rsusb.md)。 |
| 首批常规迁移 | asio、clipper2、cpp-httplib、eigen、expected、gtest、libzmq、nativefiledialog-extended、portaudio、pybind11、sdl3、spdlog、taskflow、websocketpp、yaml-cpp、boost | 配方及导入适配已迁入；109 的 Linux x86_64 构建安装及含 zlib 闭包打包通过，消费链接与三平台 CI 待用户手动验收，见[分批迁移记录](agent-notes/proposed/architecture/2026-10-28-batch-module-migration.md#首批验证结果)。 |
| 第二批迁移 | oneTBB、imgui、pcl | 配方及导入适配已迁入；109 一次整批构建安装通过，消费链接与三平台 CI 待用户手动验收，见[第二批验证结果](agent-notes/proposed/architecture/2026-10-28-batch-module-migration.md#第二批验证结果)。 |
| 暂停迁入 | sherpa-onnx | 版本管理完成后再恢复，使 PCL 与 sherpa 的独立项目分别选择 Eigen 3.4.0 与 5.0.1，不全局升级；已确认基线与未完成状态见[迁移记录](agent-notes/proposed/architecture/2026-10-28-batch-module-migration.md#第三批暂停状态)。 |
| 明确排除 | fastdeploy、nndeploy、ftxui、logo、mysql、gtsam、lastools | 不迁入，不列为待迁候选。 |

参考实现的可搬迁导入适配覆盖 eigen、onnxruntime、portaudio、lz4、flann、pcl、boost、zlib；它们是后续代表集，不意味着其他模块不可本地构建，也不要求新仓库首批同时发布八包。

## 保留的范围限制

不新增源码原地自动升级、补丁自动回滚、独立构建状态数据库、通用恢复服务、交叉编译或硬件自动探测。根 CMake 保持已确认的参考骨架；未来扩展只作该批必要适配。历史发布资产不自动迁移或删除。每一批按实际风险验证，不为每库强制新增专用测试工程。
