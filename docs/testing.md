# 测试

基础测试通过真实 CMake 入口验证根环境准备、依赖探测、锁文件失败传播、构建摘要和构建列表校验。测试不下载依赖库，需要可用的 C/C++ 工具链。

## 执行

先按 [开发指南](development.md) 准备环境，再在仓库根目录运行：

```text
uv run --locked python -B -m unittest discover -s tests/build_system -v
```

应收集并通过 6 项测试。测试在 `build/tests/` 创建名称含空格的隔离副本，结束时仅清理各自的临时目录，不删除真实环境或安装产物。测试使用离线 uv 缓存，首次执行前必须完成环境准备。

修改基础入口时运行上述测试；仅修改文档时检查链接、内容归属及 `scripts/doc-budgets.manifest.json` 的预算，并使用 doc-standards 和 agent-notes 技能附带的校验器。校验器是助手的文档检查工具，不是项目构建依赖；仓库没有自动 Git hook 或 CI。

## 覆盖范围

本分支已验证 Windows 上的空模块 configure 和基础测试。库编译、安装产物链接运行、Linux 构建及 CTest 不在此入口中，不能用其他分支或已有 `3rdparty/` 文件替代验证。v142 实机工具集一致性检查保留为后续人工验收，不阻塞基础设施开发。
