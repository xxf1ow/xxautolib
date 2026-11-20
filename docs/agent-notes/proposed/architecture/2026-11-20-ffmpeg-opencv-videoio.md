# Agent Note: 独立 FFmpeg 配方与 OpenCV 视频后端

Status: proposed

## 问题

OpenCV 4.14.0 的配方请求启用 FFmpeg，但 Linux 和 macOS 的发布构建环境缺少 FFmpeg 开发库，最终产物没有该后端。Windows 使用 OpenCV 上游包装器。仓库需要在四个平台从同一份固定的 FFmpeg 源码构建开发库，使 OpenCV 能读 RTSP 并写出普通视频文件。

## 方案

新增独立的 `ffmpeg@8.1.3` 源码配方。8.1.3 是方案提出时最新的 8.x 稳定版，保留 OpenCV 4.14.0 所用的 `AVCodec` 字段。固定[官方源码归档](https://ffmpeg.org/releases/ffmpeg-8.1.3.tar.xz)和 SHA256 `7138d28c96d9d3e3af4ee3d8cad72741f8ffb40da90c1112235dea3ecd3178a3`。使用版本目录内的 CMake 适配调用上游 `configure` 和 GNU Make，复用[现有配方的下载、校验和安装入口](../../../subsystems/module-recipes.md)。

四个平台使用以下公共配置。保留 `avcodec`、`avformat`、`avutil`、`swscale` 和 `swresample`，保留内置 H.264、HEVC 解码及 MPEG-4 Part 2、MJPEG 编码。保留本地文件输入和 RTSP 所需的 TCP、UDP、RTP。H.264 编码不属于该模块的验收范围。FFmpeg 的外部依赖采用显式选择，保持默认 LGPL 配置。

```text
--enable-shared --disable-static
--disable-autodetect
--enable-network --enable-demuxer=rtsp
--disable-avdevice --disable-avfilter
--disable-programs --disable-doc --disable-debug
```

FFmpeg 配方负责安装头文件、共享库、Windows 导入库及可搬迁的查找文件。OpenCV 配方在四个平台声明精确依赖 `ffmpeg@8.1.3`，从该安装前缀探测 FFmpeg，关闭上游 Windows 包装器下载。必需库未通过探测或链接检查时，OpenCV 配置失败。此前由环境提供 Linux FFmpeg、由上游提供 Windows 包装器的做法见[原接入记录](../../implemented/architecture/2026-10-20-flann-opencv-dependencies.md)。

### GitHub CI 与交付

在 `xxf1ow/xxautolib` 上通过现有 `Representative releases` 验收，先在 Linux x86_64 使 FFmpeg 和 OpenCV 各自通过，再对其余三个平台做同样的定向验收。每次运行的 `modules` 只填一个待验证的精确根包。在编译前根据本次运行采用的缓存快照，确认依赖闭包均可恢复。OpenCV 的依赖包括 FFmpeg、FreeType、HarfBuzz 和 zlib。任一依赖缺少有效缓存时，在编译前停止该次运行。将缺失项作为独立的定向任务处理，待缓存就绪后再运行目标包。以平台结果中的构建与复用状态核对实际重建范围。

将针对 FFmpeg 或 OpenCV 的功能检查接到现有单包消费验证中。验证合格的包按[现有流水线](../../../development.md#ci-开发与运行)写入发布缓存，成功运行触发现有 Gitee 同步。从触发构建到 Gitee 同步结束，任务分支保持指向同一提交。失败修复继续针对同一个包运行，已验证的有效缓存按现有规则复用。任务分支的推送按仓库的[远端操作约定](../../../development.md#远端构建与源码传输授权)执行。

## 备选方案

**引用预编译 SDK。** 调查未找到统一来源下满足四平台开发库布局和 RTSP 要求的一组归档。Linux x86_64 同 CI 镜像的 FFmpeg 8.1.3 源码构建已测得约两分钟，源码配方能统一版本、功能和工具链来源。

**继续由系统探测 FFmpeg，或将 FFmpeg 做成 OpenCV 私有资源。** 系统探测无法保证新建发布环境启用后端。独立配方能通过精确依赖自动取得 FFmpeg，并独立构建、复用和验收同一份开发库。

**另设只验证的 CI 工作流。** 现有发布工作流已有单包消费验证和依赖缓存，成功的定向构建可以直接进入交付流程。另设只验证的工作流会重复准备和构建工作。

## 验收标准

- 四个平台通过仓库根入口从干净状态构建目标包，依赖项从已验证的缓存恢复。CI 记录配置和编译安装耗时，平台结果能区分本次构建与缓存复用。
- FFmpeg 安装包通过真实链接和加载检查。OpenCV 的配置摘要为 `FFMPEG: YES`，实际链接输入来自 `ffmpeg@8.1.3` 的仓库安装前缀。
- OpenCV 通过 `autolib::opencv` 和 `cv::CAP_FFMPEG` 读取固定的本地 RTSP H.264 流并取得视频帧。`VideoWriter` 写出 `mp4v` MP4，重新打开能读回有效帧。将依赖闭包搬迁后重复消费，核对 FFmpeg 运行库来自搬迁后的依赖包。
- GitHub CI 的目标包验证通过后能看到对应平台缓存记录，并跟踪同次运行触发的 Gitee 同步结果。

## 风险

Ubuntu 20.04 自带的 NASM 2.14.02 无法完成 FFmpeg 8.1.3 的 x86 汇编，已验证 Linux x86_64 可使用 NASM 2.16.03。Windows 在 MSYS2 Bash 和 GNU Make 内使用 MSVC v143，实编核对 `.dll` 和 `.lib` 及其加载位置。此前的 Linux 计时使用较宽的 FFmpeg 功能配置，当前裁剪组合、与 OpenCV 的链接以及其余平台由 GitHub CI 提供证据。

`modules` 选择根包，现有流程在依赖缓存未命中时会进入源码构建。因此，核对本次缓存中的依赖是否可恢复是每次定向运行的执行条件。已完成的单包结果可以先于整个工作流结束写入缓存，OpenCV 功能验收须在其单包结果写入前结束。
