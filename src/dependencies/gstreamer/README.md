# GStreamer

`autolib_import(TARGET app PRIVATE gstreamer@1.28.9)` 为应用准备 GStreamer 和 `ffmpeg@8.1.3`，并链接 GStreamer core、base、app、video。Linux 消费端使用系统 GLib 开发文件。

应用启动 `gst_init()` 前，在本进程设置下列环境变量。下表的 `<gst>` 是 `AUTOLIB_PACKAGE_DIR_gstreamer`，`<ffmpeg>` 是 `AUTOLIB_PACKAGE_DIR_ffmpeg`。

| 变量 | 值 |
| --- | --- |
| `GST_PLUGIN_PATH_1_0` | 空字符串。设备专用插件部署时，在此显式登记经验证的插件目录。 |
| `GST_PLUGIN_SYSTEM_PATH_1_0` | `<gst>/lib/gstreamer-1.0` |
| `GST_PLUGIN_SCANNER_1_0` | `<gst>/libexec/gstreamer-1.0/gst-plugin-scanner`，Windows 文件名带 `.exe` |
| `GST_REGISTRY_1_0` | 应用私有的可写 registry 文件路径 |
| `PATH`（Windows） | 在原路径前加入 `<gst>/bin` 和 `<ffmpeg>/bin` |

命令行工具和项目启动器沿用同一组进程设置。包内 `gst-inspect-1.0` 可核对插件版本和 `Filename`。 [GStreamer 运行环境](https://gstreamer.freedesktop.org/documentation/gstreamer/running.html)
