# Gitee Action 参考与基线

来源：[nICEnnnnnnnLee/action-gitee-release](https://github.com/nICEnnnnnnnLee/action-gitee-release/tree/44b412edade0c837fb0ad7d73f4674ec07c8b722)，固定提交 `44b412edade0c837fb0ad7d73f4674ec07c8b722`，许可证见同目录 LICENSE（MIT）。action.yml、gitee_release.py、requirements.txt、LICENSE、README.md 在原样基线提交中与该提交逐字节一致，包括原始换行和行尾空白；不为格式检查改写参考本体。

## 依赖

原始直接依赖为 requests 2.25.1（Apache-2.0）和 requests-toolbelt 0.9.1（Apache-2.0）。constraints.txt 固定本次解析并验证的传递依赖：certifi 2026.10.23（MPL-2.0）、chardet 4.0.0（LGPL）、idna 2.10（BSD-like）、urllib3 1.26.20（MIT）。它们只安装在 CI/验证 venv，不打包或分发给消费者；安装包保留各自许可。

## 原样入口验证

历史基线在 Windows x86_64 上 Python 3.8.20 与 3.12.13 各通过 3 个 unittest；本次独立同步分支在 Python 3.12.13 的隔离环境重新通过全部 3 项。测试通过 runpy 执行原始入口，只替换 requests 的 HTTP 请求，不替换 Gitee 或 MultipartEncoder；创建检查 Release ID，上传检查实际 multipart 字节，更新检查先删后传及无关附件保留。测试使用临时文件和假 token，不请求真实 Gitee。

隔离环境与完整测试命令由[测试指南](../../../docs/testing.md#独立-gitee-发布验证)拥有；原入口定向测试使用 `test_upstream_baseline.py`。

原样基线不证明 Gitee 认证、真实上传或三平台 CI 已通过；不承诺上游、系统包和 runner 的永久可复现。适配及真实发布验收范围由[交付设计](../../../docs/agent-notes/proposed/architecture/2026-11-02-gitee-delivery-reimplementation.md)拥有。

## Gitee 版本化发布适配

`action.yml` 接收 `host/mode/input/latest/repository/tag`，映射为 `PUBLISH_HOST/PUBLISH_MODE/PUBLISH_INPUT/PUBLISH_LATEST/GITEE_REPOSITORY/GITEE_RELEASE_TAG` 并调用 `publish.py`；写入步骤仅提供 `GITEE_TOKEN`。依赖由 workflow 隔离安装。artifacts 模式只读 publication.json；latest 模式无资产输入。上游 README 保留原始接口，不作为项目 Action 用法；信任和运行前提见[开发指南](../../../docs/development.md#独立-gitee-发布)。

`publish.py` 在 stdout 输出五字段汇总 JSON，在 stderr 输出失败资产的名称、`operation/outcome` 或阶段诊断。API 写入未确认标为 `unknown`，其他阶段未确认标为 `unconfirmed`；不输出异常正文、响应或凭据，也不据此自动重试写入。

`gitee_api.py` 独立拥有普通请求的 Header 认证、JSON 写入、每线程连接和单层有界读取重试。未确认写入抛出 `unknown`，不自动重放。附件上传只借用 `gitee_release.py` 无装饰器的 `upload_asset_details`，保留其 multipart 字段、boundary 和文件句柄管理；参考本体不变。`metadata_repository.py` 拥有单份 Git 工作副本及确认 push 后的元数据结果，`publication_rules.py` 校验发布清单并选择 latest，不扫描配方或追加资产。

普通请求和服务端初始化顺序参考 [Cent 固定提交的 gitee.ts](https://github.com/glink25/Cent/blob/03ae57ad3a1d98c9db08fc71224704150765b96f/apps/cent/src/tidal/gitee.ts)，许可为 CC BY-NC-SA 4.0；只借鉴流程，不复制其源码、Contents 存储或恢复机制。此适配使用 Python requests 和单份 Git 提交，明确 404 才创建 `private=true`、`auto_init=true` 的库，随后重新查询实际默认分支、单数 `permission.push` 和认证用户名。Cent 不是附件上传依据；MIT 上游附件实现与 Cent 参考许可边界独立。

受控测试核对普通 HTTP、实际 multipart 字节、本地 Git 裸库初始化及元数据 commit/push、未选文件保留和失败不伪成功；不请求真实 Gitee，不证明真实托管或匿名下游消费通过。完整发布约束由[Gitee 版本化交付设计](../../../docs/agent-notes/proposed/architecture/2026-11-02-gitee-delivery-reimplementation.md)拥有。
