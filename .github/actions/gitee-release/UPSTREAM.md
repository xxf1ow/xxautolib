# Gitee Action 参考与基线

来源：[nICEnnnnnnnLee/action-gitee-release](https://github.com/nICEnnnnnnnLee/action-gitee-release/tree/44b412edade0c837fb0ad7d73f4674ec07c8b722)，固定提交 `44b412edade0c837fb0ad7d73f4674ec07c8b722`，许可证见同目录 LICENSE（MIT）。action.yml、gitee_release.py、requirements.txt、LICENSE、README.md 在原样基线提交中与该提交逐字节一致，包括原始换行和行尾空白；不为格式检查改写参考本体。

## 依赖

原始直接依赖为 requests 2.25.1（Apache-2.0）和 requests-toolbelt 0.9.1（Apache-2.0）。constraints.txt 固定本次解析并验证的传递依赖：certifi 2026.10.23（MPL-2.0）、chardet 4.0.0（LGPL）、idna 2.10（BSD-like）、urllib3 1.26.20（MIT）。它们只安装在 CI/验证 venv，不打包或分发给消费者；安装包保留各自许可。

## 原样入口验证

历史基线在 Windows x86_64 上 Python 3.8.20 与 3.12.13 各通过 3 个 unittest；本次独立同步分支在 Python 3.12.13 的隔离环境重新通过全部 3 项。测试通过 runpy 执行原始入口，只替换 requests 的 HTTP 请求，不替换 Gitee 或 MultipartEncoder；创建检查 Release ID，上传检查实际 multipart 字节，更新检查先删后传及无关附件保留。测试使用临时文件和假 token，不请求真实 Gitee。

隔离环境与完整测试命令由[测试指南](../../../docs/testing.md#独立-gitee-同步验证)拥有；原入口定向测试使用 `test_upstream_baseline.py`。

原样基线不证明 Gitee 认证、真实上传或三平台 CI 已通过；不承诺上游、系统包和 runner 的永久可复现。适配及真实镜像验收范围由[独立设计](../../../docs/agent-notes/proposed/architecture/2026-10-27-gitee-release-sync-workflow.md)拥有。

## 同步适配

`gitee_release.py` 保留上游操作与 multipart 构造，增加可选 request 注入、返回原始 Response 的创建/上传/删除接口，以及身份、仓库、Release 正文和分页附件接口。独立同步只调用无装饰器的细节接口，有界重试由 `sync_api.py` 单独负责，不叠加上游重试。原始入口三项测试继续验证创建、上传和更新的可观察结果；请求替身不区分 HTTP 方法字母大小写。

`sync_rules.py` 拥有来源筛选与正文索引纯规则，`sync_api.py` 拥有响应确认、下载校验和每线程连接，`sync.py` 拥有配置、单附件池、成功索引合并和退出码。它们仅属于独立同步，不作为 builder 或 importer 依赖。

`action.yml` 使用 `packages/repository/tag` 输入，映射为 `SYNC_PACKAGES/GITEE_REPOSITORY/GITEE_RELEASE_TAG`，只运行预先准备好的 Python 入口，不安装依赖；调用步骤通过环境提供 `GITHUB_TOKEN/GITEE_TOKEN`。Linux 运行使用进程 CPU affinity，Windows 仅作受控测试。工作流、变量、信任与失败重跑见[开发指南](../../../docs/development.md#独立-gitee-同步)。上游 README 保留原文，描述原始接口，不作为适配入口用法。
