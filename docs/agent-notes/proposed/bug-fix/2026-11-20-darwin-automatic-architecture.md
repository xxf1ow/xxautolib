# Agent Note: Darwin 自动识别本机架构

Status: proposed

## 问题

Apple Silicon 上 CMake 已能识别本机，但项目入口仍要求用户显式设置 `CMAKE_OSX_ARCHITECTURES=arm64`。这使 Darwin 比其他原生平台多一个冗余输入。

## 方案

Darwin 根入口在首次 `project()` 前读取本机架构；当 `CMAKE_OSX_ARCHITECTURES` 未设置或为空时，将该架构写入 CMake cache。入口继续拒绝非 ARM64、多架构及交叉编译配置。显式设置的目标仍受相同校验。根项目将架构传给子项目；独立消费者未设置该参数时使用 `CMAKE_SYSTEM_PROCESSOR`，并拒绝非 ARM64 目标。

此方案调整 [Darwin 原生构建设计](../../implemented/architecture/2026-11-05-darwin-aarch64-native-build-and-ci.md)中要求调用方显式传入架构的约定；Apple Silicon 之外的平台范围不变。

## 备选方案

**继续要求显式设置架构。** 该方案保持原配置方式，但让 Darwin 独有一个无需调用方提供的本机目标输入，因此不采用。

## 验收标准

Apple Silicon 的根项目和独立消费者在未设置 `CMAKE_OSX_ARCHITECTURES` 时均能使用自动识别的 ARM64。非 ARM64、多架构和交叉编译配置仍在配置阶段失败。

## 风险

根入口在 `project()` 前探测本机；后续 CMake 项目可能基于 cache 中已初始化的 ARM64 目标配置。已有非 ARM64 cache 值继续触发错误，不自动覆盖。
