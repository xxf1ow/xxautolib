# Agent Note: ViSP Linux ARM64 OpenMP 导出

Status: proposed

## 问题

ViSP 导出的 `VISPModules.cmake` 会携带构建机器上 GCC 的 `libgomp.so` 绝对路径。其他 ARM64 Linux 主机可能有兼容的 `libgomp.so.1`，但没有构建机所用 GCC 版本的开发路径。

## 方案

Linux ARM64 的 ViSP 配方在生成导出目标前，将其 OpenMP 库文件依赖替换为 `OpenMP::OpenMP_CXX`。已安装的 `VISPConfig.cmake` 在加载导出目标前调用 `find_package(OpenMP)`，由消费机上的 CMake 查找其 OpenMP 编译和链接依赖。修复后重新构建并发布 ARM64 包；不改导入器。

## 备选方案

**在 AutoLib importer 中改写导出文件或搜索 `libgomp.so.1`。** 这会让消费端修补生产包的导出内容，并把 GCC 库名和查找逻辑放进 importer；根因在 ViSP 导出阶段，因此不采用。

**要求目标机器安装构建机相同版本的 GCC。** 这保留构建机绝对路径依赖，且目标机已有兼容 OpenMP 运行库时仍无必要，因此不采用。

## 验收标准

Linux ARM64 的新导出目标使用 `OpenMP::OpenMP_CXX`，不包含 GCC 10 的绝对 `libgomp.so` 路径。消费测试在 GCC 11 环境解析系统 OpenMP 并成功链接和运行；新包通过 ARM64 构建、消费和发布流程。

## 风险

消费端需要 CMake 的 `FindOpenMP` 模块及本机 OpenMP 开发/链接能力。旧归档不因本地源码修复而变化。
