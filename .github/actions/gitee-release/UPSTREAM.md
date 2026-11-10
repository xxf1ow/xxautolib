# Gitee Action 参考与基线

来源：[nICEnnnnnnnLee/action-gitee-release](https://github.com/nICEnnnnnnnLee/action-gitee-release/tree/44b412edade0c837fb0ad7d73f4674ec07c8b722)，固定提交 `44b412edade0c837fb0ad7d73f4674ec07c8b722`，许可证见同目录 LICENSE（MIT）。action.yml、gitee_release.py、requirements.txt、LICENSE、README.md 在原样基线提交中与该提交逐字节一致，包括原始换行和行尾空白；不为格式检查改写参考本体。

## 依赖

原始直接依赖为 requests 2.25.1（Apache-2.0）和 requests-toolbelt 0.9.1（Apache-2.0）。constraints.txt 固定本次解析并验证的传递依赖：certifi 2026.10.23（MPL-2.0）、chardet 4.0.0（LGPL）、idna 2.10（BSD-like）、urllib3 1.26.20（MIT）。它们只安装在 CI/验证 venv，不打包或分发给消费者；安装包保留各自许可。

## 原样入口验证

历史基线在 Windows x86_64 上 Python 3.8.20 与 3.12.13 各通过 3 个 unittest；本次独立同步分支在 Python 3.12.13 的隔离环境重新通过全部 3 项。测试通过 runpy 执行原始入口，只替换 requests 的 HTTP 请求，不替换 Gitee 或 MultipartEncoder；创建检查 Release ID，上传检查实际 multipart 字节，更新检查先删后传及无关附件保留。测试使用临时文件和假 token，不请求真实 Gitee。

隔离环境与完整测试命令由[测试指南](../../../docs/testing.md#独立-gitee-发布验证)拥有；原入口定向测试使用 `test_upstream_baseline.py`。

原样基线不证明 Gitee 认证、真实上传或三平台 CI 已通过；不承诺上游、系统包和 runner 的永久可复现。适配及真实发布验收范围由[交付设计](../../../docs/agent-notes/implemented/architecture/2026-11-02-gitee-delivery-reimplementation.md)拥有。

## Gitee 独立同步适配

`action.yml` 接收固定源码事实、有效缓存快照、仓库构建的 `autolib-ci` 路径，以及可信分支和 SHA，调用 `publish.py`。sync 模式从跨 run 累积的 Release 缓存按需下载；latest 模式仅更新显式指针，不查询缓存或清理附件。workflow 隔离安装依赖，仅交付步骤注入 `GITEE_TOKEN`；缓存子进程移除该凭据，只使用 GitHub 凭据。目标及运行前提见[开发指南](../../../docs/development.md#独立-gitee-发布)。

`sync_scheduler.py` 拥有依赖顺序、并发上传和单份 Git 工作副本的锁。唯一跳过依据是 Gitee Git 消费 JSON，三个字段语义相同才跳过；不下载或检查 Gitee 归档。普通变化项删除同名附件后上传，即时 commit/push 并确认；仅备用地址变化且 Gitee 字节已确认时只更新 JSON。超过 100000000 字节只登记 GitHub 地址，JSON 确认后删除精确受管 Gitee 旧副本，失败报告独立错误，重跑相同 JSON 仍清理。push 未确认后停写，不 rebase 或回滚；源码决定身份删除，未知内容保留。

主进程在至少一个 importer 实际上传后交付一次入口，只修改上传副本中的唯一空来源赋值；工具 JSON 等待入口成功，库独立继续，仅工具地址变化不更新入口。入口无摘要，latest 不要求全平台齐备；同版本更新不保证整组原子切换。消费 JSON/schema 2 由[双来源设计](../../../docs/agent-notes/implemented/architecture/2026-11-09-dual-source-artifact-downloads.md)拥有，`.publication.json` 保持 CI 缓存原职责。

`publish.py` 的 stdout 汇总 uploaded、metadata_only、skipped、failed、blocked、覆盖缺口、独立操作错误和已确认 HEAD；metadata_only 原因为 gitee_size_limit 或 delivery_metadata_changed。stderr 即时输出脱敏事件；缺缓存是待构建缺口，实际上传、清理、入口或 Git 失败返回非零。

`publish_one` 校验字节后调用 `attachment_upload`，在可终止子进程中执行附件更新的唯一重试循环：首次使用本轮完整附件列表，重试重新查询并删除重传，不读取 Gitee 归档内容。临时网络错误、HTTP 408、429 和 5xx 触发最多十次额外重试，间隔固定为 10 秒，全部请求与等待合计最多 600 秒；耗尽预算即杀死并回收子进程，不确认该项 JSON。认证、权限、输入或响应结构错误及未分类 409/422 直接失败；未确认删除不继续上传。Git 确认留在原协调器；超时的下轮恢复规则见[同步设计](../../../docs/agent-notes/implemented/architecture/2026-11-09-release-cache-and-independent-gitee-sync.md#gitee-摘要比较与逐项登记)。

`gitee_api.py` 拥有 Header 认证、JSON 普通请求及每线程连接。附件查询、删除、上传不在 HTTP 层重试；其他读取保留有界重试，创建仓库或 Release 的未知写入不自动重放。附件上传借用 `gitee_release.py` 无装饰器的 `upload_asset_details`，保留其 multipart 字段、boundary 和文件句柄管理；参考本体不变。

普通请求和服务端初始化顺序参考 [Cent 固定提交的 gitee.ts](https://github.com/glink25/Cent/blob/03ae57ad3a1d98c9db08fc71224704150765b96f/apps/cent/src/tidal/gitee.ts)，许可为 CC BY-NC-SA 4.0；只借鉴流程，不复制源码、Contents 存储或恢复机制。适配明确 404 才创建 `private=true`、`auto_init=true` 的库，随后核对默认分支、单数 `permission.push` 和认证用户名。Cent 不是附件上传依据；MIT 附件实现与 Cent 参考许可边界独立。

受控测试覆盖普通 HTTP、实际 multipart、本地 Git 裸库和逐项 commit/push；不证明真实 Gitee 托管、Actions 排队或匿名下游消费通过。真实远端验收须单独授权。
