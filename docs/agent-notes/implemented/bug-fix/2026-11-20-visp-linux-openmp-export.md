# Agent Note: ViSP Linux ARM64 OpenMP 导出

Status: implemented

## 问题

ViSP 的 `modules/core/CMakeLists.txt` 遍历 `OpenMP_CXX_LIB_NAMES`，将 `${OpenMP_${lib_}_LIBRARY}` 加进 `opt_libs`；`cmake/VISPModule.cmake` 再将其作为模块的 `PUBLIC` 链接依赖。导出的 `VISPModules.cmake` 因而会携带构建机 GCC 的 `libgomp.so` 绝对路径。Linux ARM64 消费记录已在 `visp_core` 的 `INTERFACE_LINK_LIBRARIES` 中观察到 `/usr/lib/gcc/aarch64-linux-gnu/10/libgomp.so`。

## 决定

Linux ARM64 的 ViSP 配方在生成导出目标前，将目标属性中的 OpenMP 库文件依赖替换为 `OpenMP::OpenMP_CXX`。已安装的 `VISPConfig.cmake` 在加载导出目标前调用 `find_package(OpenMP)`，由消费机上的 CMake 查找其 OpenMP 编译和链接依赖。修复位于 ViSP 包导出，不修改 AutoLib importer。

## 备选方案

**在 AutoLib importer 中改写导出文件或搜索 `libgomp.so.1`。** 这会让消费端修补生产包的导出内容，并把 GCC 库名和查找逻辑放进 importer；绝对路径由 ViSP 模块的公开链接依赖产生，因此未采用。

**要求目标机器安装构建机相同版本的 GCC。** 这保留构建机绝对路径依赖，且目标机已有兼容 OpenMP 运行库时仍无必要，因此未采用。

## 影响

补丁仅应用于 Linux ARM64 配方。[GitHub Actions run 44870543131](https://github.com/xxf1ow/xxautolib/actions/runs/44870543131) 在 Linux ARM64、GCC 10.5 上重建 ViSP 并通过消费测试。捕获的 `visp_core` 导入属性引用 `OpenMP::OpenMP_CXX`；同一消费机上的 OpenMP imported target 解析到 GCC 10.5 的本机路径，符合本机解析的预期。[Gitee sync run 44871795988](https://github.com/xxf1ow/xxautolib/actions/runs/44871795988) 成功上传 `visp-3.7.0-linux-aarch64.tar.zst` 并提交元数据。此验证未使用 GCC 11；168 板子的消费结果仍未直接验证。
