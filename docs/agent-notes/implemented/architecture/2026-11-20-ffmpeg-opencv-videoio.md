# Agent Note: 独立 FFmpeg 配方与 OpenCV 视频后端

Status: implemented

## 问题

OpenCV 4.14.0 的 FFmpeg 后端依赖构建机环境。Linux 和 macOS 的发布环境缺少 FFmpeg 开发库，Windows 则使用上游包装器。四个平台需要从统一来源交付可读 RTSP、可写视频文件的 OpenCV。

## 决定

[FFmpeg 配方](../../../../src/dependencies/ffmpeg/8.1.3/module.toml)固定官方 8.1.3 源码及摘要。8.1.3 属于 OpenCV 4.14.0 可消费的 8.x 稳定系列，保留 OpenCV 所用的 `AVCodec` 字段。配方调用上游 `configure` 和 GNU Make，交付 `avcodec`、`avfilter`、`avformat`、`avutil`、`swscale`、`swresample` 的共享库、头文件和可搬迁的 pkg-config 文件。Windows 另交付导入库。`libavfilter.pc` 供 gst-libav 联编，`FFMPEGConfig.cmake` 保留 OpenCV 使用的 CMake 链接接口。 [GStreamer 交付设计](../../proposed/architecture/2026-11-21-gstreamer-1.28.9-delivery.md)

四个平台使用内置编解码与默认 LGPL 配置，关闭自动探测外部依赖，省去 CLI、`avdevice`、文档和调试信息。保留 H.264、HEVC 解码，MPEG-4 Part 2、MJPEG 编码，以及本地文件和 RTSP 所需的 TCP、UDP、RTP。H.264 编码超出验收范围。

[OpenCV 配方](../../../../src/dependencies/opencv/4.14.0/module.toml)声明精确依赖 `ffmpeg@8.1.3`，仅从该安装前缀探测 FFmpeg，关闭上游 Windows 包装器下载。FFmpeg 探测或链接失败会中止 OpenCV 配置。Linux 和 macOS 的 OpenCV 通过相对运行路径查找 FFmpeg。macOS 专属补丁关闭 CMake 从链接目录生成安装 RPATH 的行为，因为 OpenCV dylib 会优先采用遗留的绝对目录。

## 备选方案

**引用预编译 SDK。** 调查未找到统一来源下覆盖四平台、含开发文件且支持 RTSP 的一组归档。源码配方统一版本、功能和工具链，Linux 实编约两分钟。

**依赖系统探测，或让 OpenCV 私有构建 FFmpeg。** 系统探测无法保证发布成品有该后端。独立配方使 OpenCV 和其他消费者共享同一版本，并可分别构建、验收和复用。

**另设验证工作流。** 现有发布工作流可选择单包、要求依赖命中缓存，并将验收通过的成品直接入库。另设工作流会重复准备和构建。

## 影响

Linux x86_64 的 Ubuntu 20.04 默认 NASM 2.14.02 无法完成 FFmpeg 8.1.3 的 x86 汇编，配方使用 NASM 2.16.03。Windows 使用 MSVC v143、MSYS2 Bash 和 GNU Make。平台工具链要求见[开发指南](../../../development.md)。

OpenCV 入库前，[媒体检查](../../../../tests/autolib/opencv-videoio.cpp)通过 `autolib::opencv` 在原安装和搬迁副本上读取本地 H.264 RTSP，写出并读回 `mp4v` MP4，同时核对 FFmpeg 的实际加载路径。CI 指纹用于构建和同步选择，下游消费依据发布元数据中的归档摘要和精确依赖。

## 验证

- [FFmpeg 四平台 CI](https://github.com/xxf1ow/xxautolib/actions/runs/44737606332)验证共享库链接、加载位置和所需媒体能力。[Gitee 同步](https://github.com/xxf1ow/xxautolib/actions/runs/44739224133)确认四份归档交付。Linux x86_64、Linux ARM64、macOS ARM64、Windows x64 的编译安装步骤分别约为 123、113、126、332 秒。
- [OpenCV 的 Linux 和 Windows CI](https://github.com/xxf1ow/xxautolib/actions/runs/44739352172)与[修复后的 macOS CI](https://github.com/xxf1ow/xxautolib/actions/runs/44756109347)均确认 `FFMPEG: YES`，实际依赖来自仓库安装前缀。每个平台只编译 OpenCV，四个依赖均从缓存复用。原安装和搬迁副本各读取三帧 RTSP 并写入、读回三帧 MP4。
- [macOS Gitee 同步](https://github.com/xxf1ow/xxautolib/actions/runs/44757713509)使用修复提交。[Linux 和 Windows Gitee 补同步](https://github.com/xxf1ow/xxautolib/actions/runs/44764347981)固定三份归档的原构建提交 `2208c10`。公共配方登记 macOS 补丁会改变三个非 macOS 平台的 CI 缓存指纹，补同步复用原构建提交下已验收的归档。
