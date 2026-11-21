# Agent Note: Darwin 自动识别本机架构

Status: implemented

## 问题

Apple Silicon 上 CMake 已能识别本机，但项目入口仍要求用户显式设置 `CMAKE_OSX_ARCHITECTURES=arm64`。这使 Darwin 比其他原生平台多一个冗余输入。

## 决定

Darwin 根入口在首次 `project()` 前读取本机架构；当 `CMAKE_OSX_ARCHITECTURES` 未设置或为空时，将该架构写入 CMake cache。入口继续拒绝非 ARM64、多架构及交叉编译配置。显式设置的目标仍受相同校验。根项目将架构传给子项目；独立消费者未设置该参数时使用 `CMAKE_SYSTEM_PROCESSOR`，并拒绝非 ARM64 目标。

此决定调整 [Darwin 原生构建设计](../architecture/2026-11-05-darwin-aarch64-native-build-and-ci.md)中要求调用方显式传入架构的约定；Apple Silicon 之外的平台范围不变。

## 备选方案

**继续要求显式设置架构。** 该方案保持原配置方式，但让 Darwin 独有一个无需调用方提供的本机目标输入，因此未采用。

## 影响

Apple Silicon 的普通配置和独立消费者不再需要 `-DCMAKE_OSX_ARCHITECTURES=arm64`。非 ARM64 或多架构目标仍在配置阶段失败，构建器和产物身份仍使用既有 Darwin ARM64 规则。`TestDarwinEntryCompiler` 及 `TestDarwinEntryIdentity` 在 Apple Silicon 上分别验证根/子项目参数传递和独立消费者自动识别。
