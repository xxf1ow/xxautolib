# GStreamer

`autolib_import(TARGET app PRIVATE gstreamer@1.28.9)` 为应用准备 GStreamer 和 `ffmpeg@8.1.3`，并链接 GStreamer core、base、app、video。Linux 消费端使用系统 GLib 开发文件。

下文的 `<gst>` 是 `AUTOLIB_PACKAGE_DIR_gstreamer`，`<ffmpeg>` 是 `AUTOLIB_PACKAGE_DIR_ffmpeg`。

Windows 从启动应用的父进程把 `<gst>/bin` 和 `<ffmpeg>/bin` 加到 `PATH` 前端。Windows 在应用进入 `main()` 前加载链接的 DLL。

应用在调用 `gst_init()` 前设置插件环境。

| 变量 | 值 |
| --- | --- |
| `GST_PLUGIN_PATH_1_0` | 空字符串。设备专用插件部署时，在此显式登记经验证的插件目录。 |
| `GST_PLUGIN_SYSTEM_PATH_1_0` | `<gst>/lib/gstreamer-1.0` |
| `GST_PLUGIN_SCANNER_1_0` | `<gst>/libexec/gstreamer-1.0/gst-plugin-scanner`，Windows 文件名带 `.exe` |
| `GST_REGISTRY_1_0` | 应用私有的可写 registry 文件路径 |

命令行工具和项目启动器沿用同一组进程设置。包内 `gst-inspect-1.0` 可核对插件版本和 `Filename`。 [GStreamer 运行环境](https://gstreamer.freedesktop.org/documentation/gstreamer/running.html)

Linux 使用 `<gst>/bin/gst-run /path/to/app` 启动应用，或用同一入口运行包内工具。启动器和包内 scanner 会把两份包的库目录放在动态库搜索路径前端。Jetson 在 `GST_PLUGIN_PATH_1_0` 登记明确的 JetPack 插件目录，并为该部署指定独立 registry。

macOS 和 Windows 随包提供 GLib/GIO 头文件、共享库及链接元数据。需要 `glib-compile-resources` 等工具的项目从各自的 GLib 开发环境获取。

Windows 的含空格安装路径使用 `pkgconf 3.0.7` 验证。使用 `pkgconf 3.0.3` 查询这类路径时，工具会重复转义空格。CMake 项目可以直接使用 `autolib::gstreamer`。
