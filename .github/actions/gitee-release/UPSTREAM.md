# Gitee Action 参考与基线

来源：[nICEnnnnnnnLee/action-gitee-release](https://github.com/nICEnnnnnnnLee/action-gitee-release/tree/44b412edade0c837fb0ad7d73f4674ec07c8b722)，固定提交 `44b412edade0c837fb0ad7d73f4674ec07c8b722`，许可证见同目录 LICENSE（MIT）。action.yml、gitee_release.py、requirements.txt、LICENSE、README.md 在原样基线提交中与该提交逐字节一致，包括原始换行和行尾空白；不为格式检查改写参考本体。

## 依赖

原始直接依赖为 requests 2.25.1（Apache-2.0）和 requests-toolbelt 0.9.1（Apache-2.0）。constraints.txt 固定本次解析并验证的传递依赖：certifi 2026.10.23（MPL-2.0）、chardet 4.0.0（LGPL）、idna 2.10（BSD-like）、urllib3 1.26.20（MIT）。它们只安装在 CI/验证 venv，不打包或分发给消费者；安装包保留各自许可。

## 原样入口验证

历史基线在 Windows x86_64 上 Python 3.8.20 与 3.12.13 各通过 3 个 unittest；本次独立同步分支在 Python 3.12.13 的隔离环境重新通过全部 3 项。测试通过 runpy 执行原始入口，只替换 requests 的 HTTP 请求，不替换 Gitee 或 MultipartEncoder；创建检查 Release ID，上传检查实际 multipart 字节，更新检查先删后传及无关附件保留。测试使用临时文件和假 token，不请求真实 Gitee。

隔离环境与完整测试命令由[测试指南](../../../docs/testing.md#独立-gitee-发布验证)拥有；原入口定向测试使用 `test_upstream_baseline.py`。

原样基线不证明 Gitee 认证、真实上传或三平台 CI 已通过；不承诺上游、系统包和 runner 的永久可复现。适配及真实发布验收范围由[交付设计](../../../docs/agent-notes/implemented/architecture/2026-11-02-gitee-delivery-reimplementation.md)拥有。

## Gitee 版本化发布适配

`action.yml` 接收 `host/mode/input/latest/repository/tag`，映射为 `PUBLISH_HOST/PUBLISH_MODE/PUBLISH_INPUT/PUBLISH_LATEST/GITEE_REPOSITORY/GITEE_RELEASE_TAG` 并调用 `publish.py`，从 GitHub 运行上下文传入可信 `GITHUB_REPOSITORY`；写入步骤仅提供 `GITEE_TOKEN`。依赖由 workflow 隔离安装。artifacts 模式只读 publication.json，并独立核对清单来源仓库；latest 模式不读取构建输入或要求构建来源。上游 README 保留原始接口，不作为项目 Action 用法；信任和运行前提见[开发指南](../../../docs/development.md#独立-gitee-发布)。

发布清单包含完整构建集合的库、各平台 importer 和唯一 `autolib.cmake` 入口。入口按普通附件校验实际字节、尺寸和 SHA256 并计入上传结果，但不写入 Git 摘要或 latest；入口失败使整体发布失败，其他成功工具和库仍按既有规则登记。`publication_rules.metadata_path` 对合法入口返回 `None`，协调器只登记非 `None` 摘要路径。元数据仓库白名单与上游附件实现保持独立。

发布准备只接受精确 run/attempt 的 `ci-package-*`：库和工具由原生 CI 验证后调用同提交 builder 生成，准备端重算实际大小和 SHA256 并逐字节分发，不执行 builder、不解开或重建内部归档。旧安装树运输要求重新构建，不提供重打包兜底。交接协议由[成品交接记录](../../../docs/agent-notes/implemented/architecture/2026-11-04-ci-packaged-artifact-handoff.md)拥有。

发布准备保留实际 checkout SHA 检查，并从原构建 Git 提交读取 `src/autolib.cmake`，全部运输入口与验证记录须与该字节一致。`prepare_publication(..., repository, tag, source_repository)` 的三个关键字参数分别是实际 Gitee 目标、原始 tag 和独立可信源码仓库。入口上传副本仅替换唯一完整空行 `set(_autolib_release_root "")`；注入点缺失、重复或非空均失败。tag 不得含空白或控制字符，合法值按单个 URL 路径段编码；注入和上传使用同一组 workflow 目标变量，源码、安装和运输输入保持不变。

`publish.py` 成功解析输入后在 stdout 输出 `selected/uploaded/skipped/failed/metadata_committed/metadata_commit` 汇总 JSON；`metadata_committed` 表示本次产生新的元数据提交；`metadata_commit` 为已确认远端 SHA，无变化时返回已有 SHA，未确认时为 null。退出成功要求附件无失败且元数据 SHA 已确认，无变化也成功。`skipped` 表示远端实际字节的 SHA256 与本次输入一致，不能算作重新上传。上传及跳过项均参与摘要登记和缺失 latest 初始化；入口仍不写 Git 元数据。stderr 输出失败资产的名称、`operation/outcome` 或阶段诊断。API 失败另带 `http_status`、固定原因 `reason` 和异常类型 `exception_type`；未取得状态码或没有底层异常时，对应字段为 null。重试日志另带 `retry` 和 `delay_seconds`。非法输入返回非零，stdout 为空，stderr 说明固定输入规则，不回显输入值；日志不输出底层异常正文、完整 URL、响应或凭据。

附件更新由 `publish_one` 拥有唯一重试循环：固定本地快照，查询唯一同名附件并下载核对 SHA256，相同则跳过，不同则先删后传，缺失则直接上传。临时网络故障、HTTP 408、429 和 5xx 在退避后从查询重新执行；首次尝试之外最多重试十次，等待为 2、4、8、16、32、32、32、32、32、32 秒。认证、权限、非法输入及响应校验错误直接失败。查询失败不能当作附件缺失，未确认删除不能直接继续上传。下载流式计算摘要，只接受目标仓库的 HTTPS 初始地址及 HTTPS 重定向，凭据只发往 Gitee。

`gitee_api.py` 拥有 Header 认证、JSON 普通请求及每线程连接；附件查询、下载、删除、上传均不在 HTTP 层重试。其他普通读取保留有界重试，建库和创建 Release 的未知写入仍不自动重放。API 写入未确认可在诊断中标为 `unknown`，其他阶段未确认标为 `unconfirmed`；附件临时故障由更新循环重新查询，而不是盲目重发失败步骤。附件上传只借用 `gitee_release.py` 无装饰器的 `upload_asset_details`，保留其 multipart 字段、boundary 和文件句柄管理；参考本体不变。`metadata_repository.py` 拥有单份 Git 工作副本及确认 push 后的元数据结果，`publication_rules.py` 校验发布清单并选择 latest，不扫描配方或追加资产。

普通请求和服务端初始化顺序参考 [Cent 固定提交的 gitee.ts](https://github.com/glink25/Cent/blob/03ae57ad3a1d98c9db08fc71224704150765b96f/apps/cent/src/tidal/gitee.ts)，许可为 CC BY-NC-SA 4.0；只借鉴流程，不复制其源码、Contents 存储或恢复机制。此适配使用 Python requests 和单份 Git 提交，明确 404 才创建 `private=true`、`auto_init=true` 的库，随后重新查询实际默认分支、单数 `permission.push` 和认证用户名。Cent 不是附件上传依据；MIT 上游附件实现与 Cent 参考许可边界独立。

受控测试核对普通 HTTP、实际 multipart 字节、本地 Git 裸库初始化及元数据 commit/push、未选文件保留和失败不伪成功；不请求真实 Gitee，不证明真实托管或匿名下游消费通过。完整发布约束由[Gitee 版本化交付设计](../../../docs/agent-notes/implemented/architecture/2026-11-02-gitee-delivery-reimplementation.md)拥有。
