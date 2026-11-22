#include <gst/app/gstappsrc.h>
#include <gst/rtsp-server/rtsp-server.h>
#include <gst/video/video.h>
#include <libavcodec/avcodec.h>
#include <libavcodec/version.h>
#include <libavfilter/avfilter.h>
#include <libavfilter/version.h>
#include <libavformat/avformat.h>
#include <libavformat/version.h>
#include <libavutil/avutil.h>
#include <libavutil/version.h>
#include <orc/orc.h>

#include <stdio.h>

int main(void)
{
    gst_init(NULL, NULL);
    GstVideoInfo info;
    gst_video_info_init(&info);
    if (!gst_app_src_get_type() || !gst_rtsp_server_get_type() || !orc_version_string() ||
        avcodec_version() != LIBAVCODEC_VERSION_INT || avformat_version() != LIBAVFORMAT_VERSION_INT ||
        avutil_version() != LIBAVUTIL_VERSION_INT || avfilter_version() != LIBAVFILTER_VERSION_INT ||
        !avfilter_get_by_name("buffer")) {
        fputs("Package metadata consumer could not use the expected media libraries\n", stderr);
        return 1;
    }
    puts("GStreamer and FFmpeg linked and ran through pkg-config");
    return 0;
}
