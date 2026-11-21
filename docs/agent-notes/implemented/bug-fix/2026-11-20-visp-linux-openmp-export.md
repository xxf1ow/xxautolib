# Agent Note: ViSP Linux ARM64 OpenMP 导出

Status: implemented

## 问题

ViSP 导出的 `VISPModules.cmake` 会携带构建机器上 GCC 的 `libgomp.so` 绝对路径。其他 ARM64 Linux 主机可能有兼容的 `libgomp.so.1`，但没有构建机所用 GCC 版本的开发路径。

## 决定

Linux ARM64 的 ViSP 配方在生成导出目标前，将目标属性中的 OpenMP 库文件依赖替换为 `OpenMP::OpenMP_CXX`。已安装的 `VISPConfig.cmake` 在加载导出目标前调用 `find_package(OpenMP)`，由消费机上的 CMake 查找其 OpenMP 编译和链接依赖。修复位于 ViSP 包导出，不修改 AutoLib importer。

## 备选方案

**在 AutoLib importer 中改写导出文件或搜索 `libgomp.so.1`。** 这会让消费端修补生产包的导出内容，并把 GCC 库名和查找逻辑放进 importer；根因在 ViSP 导出阶段，因此未采用。

**要求目标机器安装构建机相同版本的 GCC。** 这保留构建机绝对路径依赖，且目标机已有兼容 OpenMP 运行库时仍无必要，因此未采用。

## 影响

补丁仅应用于 Linux ARM64 配方。锁定的 ViSP 3.7.0 上游源码通过 `patch --dry-run` 检查。尚未在 Linux ARM64 上重建、消费或发布新包；既有归档仍含原导出内容。
