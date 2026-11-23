#include <gst/allocators/gstphysmemory.h>
#include <gst/app/gstappsink.h>
#include <gst/gst.h>
#include <gst/rtsp-server/rtsp-server.h>
#include <gst/sdp/gstsdpmessage.h>

#include <chrono>
#include <filesystem>
#include <iostream>
#include <memory>
#include <string>
#include <thread>

namespace {
namespace fs = std::filesystem;

void stop_pipeline(GstElement* pipeline)
{
    gst_element_set_state(pipeline, GST_STATE_NULL);
    gst_object_unref(pipeline);
}

using Pipeline = std::unique_ptr<GstElement, decltype(&stop_pipeline)>;

Pipeline play(const std::string& description)
{
    GError* error = nullptr;
    Pipeline pipeline(gst_parse_launch(description.c_str(), &error), stop_pipeline);
    if (error != nullptr) {
        std::cerr << error->message << '\n';
        g_error_free(error);
        pipeline.reset();
    }
    if (pipeline && gst_element_set_state(pipeline.get(), GST_STATE_PLAYING) == GST_STATE_CHANGE_FAILURE) {
        pipeline.reset();
    }
    if (!pipeline) {
        std::cerr << "Could not start pipeline: " << description << '\n';
    }
    return pipeline;
}

std::string quoted(const std::string& value)
{
    const std::unique_ptr<char, decltype(&g_free)> escaped(g_strescape(value.c_str(), nullptr), g_free);
    return std::string("\"") + escaped.get() + "\"";
}

bool require_factory(const char* name, const fs::path& plugins)
{
    const std::unique_ptr<GstElementFactory, decltype(&gst_object_unref)> factory(gst_element_factory_find(name), gst_object_unref);
    if (!factory) {
        std::cerr << "Missing GStreamer element: " << name << '\n';
        return false;
    }
    const std::unique_ptr<GstPlugin, decltype(&gst_object_unref)> plugin(
        gst_plugin_feature_get_plugin(GST_PLUGIN_FEATURE(factory.get())), gst_object_unref);
    const char* filename = plugin ? gst_plugin_get_filename(plugin.get()) : nullptr;
    std::error_code error;
    if (!filename || fs::canonical(filename, error).parent_path() != plugins || error) {
        std::cerr << "Unexpected plugin location for " << name << ": " << (filename ? filename : "(missing)") << '\n';
        return false;
    }
    std::cout << name << " = " << filename << '\n';
    return true;
}

bool write_video(const fs::path& output, const std::string& encoder, const std::string& parser)
{
    auto pipeline = play("videotestsrc num-buffers=6 ! video/x-raw,format=I420,width=128,height=128,framerate=30/1 ! " +
                         encoder + " speed-preset=ultrafast tune=zerolatency ! " + parser +
                         " ! matroskamux ! filesink location=" + quoted(output.generic_string()));
    if (!pipeline) {
        return false;
    }
    const std::unique_ptr<GstBus, decltype(&gst_object_unref)> bus(gst_element_get_bus(pipeline.get()), gst_object_unref);
    const std::unique_ptr<GstMessage, decltype(&gst_message_unref)> message(
        gst_bus_timed_pop_filtered(bus.get(), 15 * GST_SECOND, static_cast<GstMessageType>(GST_MESSAGE_ERROR | GST_MESSAGE_EOS)),
        gst_message_unref);
    if (!message || GST_MESSAGE_TYPE(message.get()) != GST_MESSAGE_EOS) {
        std::cerr << "Could not write " << output << '\n';
        return false;
    }
    return true;
}

bool read_frames(const std::string& input, int expected, GstClockTime timeout)
{
    auto pipeline = play(input + " ! appsink name=frames sync=false");
    if (!pipeline) {
        return false;
    }
    const std::unique_ptr<GstElement, decltype(&gst_object_unref)> sink(gst_bin_get_by_name(GST_BIN(pipeline.get()), "frames"), gst_object_unref);
    for (int frame = 0; frame < expected; ++frame) {
        const std::unique_ptr<GstSample, decltype(&gst_sample_unref)> sample(gst_app_sink_try_pull_sample(GST_APP_SINK(sink.get()), timeout), gst_sample_unref);
        if (!sample || gst_buffer_get_size(gst_sample_get_buffer(sample.get())) == 0) {
            std::cerr << "No decoded video at frame " << frame << '\n';
            const std::unique_ptr<GstBus, decltype(&gst_object_unref)> bus(
                gst_element_get_bus(pipeline.get()), gst_object_unref);
            const std::unique_ptr<GstMessage, decltype(&gst_message_unref)> message(
                gst_bus_pop_filtered(bus.get(), GST_MESSAGE_ERROR), gst_message_unref);
            if (message) {
                GError* error = nullptr;
                char* debug = nullptr;
                gst_message_parse_error(message.get(), &error, &debug);
                std::cerr << error->message << '\n';
                if (debug) {
                    std::cerr << debug << '\n';
                }
                g_error_free(error);
                g_free(debug);
            }
            return false;
        }
    }
    std::cout << "Decoded " << expected << " frame(s)\n";
    return true;
}

bool loopback(const std::string& publisher, const std::string& reader)
{
    auto pipeline = play("videotestsrc is-live=true num-buffers=360 ! video/x-raw,format=I420,width=128,height=128,framerate=30/1 ! "
                         "x264enc speed-preset=ultrafast tune=zerolatency key-int-max=15 ! h264parse config-interval=-1 ! " + publisher);
    if (!pipeline) {
        return false;
    }
    for (int attempt = 0; attempt < 5; ++attempt) {
        std::this_thread::sleep_for(std::chrono::milliseconds(200));
        if (read_frames(reader, 1, 2 * GST_SECOND)) {
            return true;
        }
    }
    return false;
}
} // namespace

int main(int argc, char** argv)
{
    if (argc != 6) {
        std::cerr << "Expected package and output paths plus RTSP, RTMP and HTTP URLs\n";
        return 1;
    }
    const fs::path package = fs::canonical(argv[1]);
    const fs::path output = fs::absolute(argv[2]);
    fs::create_directories(output);
    const fs::path plugins = package / "lib/gstreamer-1.0";
    auto scanner = package / "libexec/gstreamer-1.0/gst-plugin-scanner";
#if defined(_WIN32)
    scanner += ".exe";
#endif
    if (!fs::is_regular_file(scanner)) {
        std::cerr << "Missing scanner: " << scanner << '\n';
        return 1;
    }
    const fs::path registry = output / "registry.bin";
    fs::remove(registry);
    g_setenv("GST_PLUGIN_PATH_1_0", "", TRUE);
    g_setenv("GST_PLUGIN_SYSTEM_PATH_1_0", plugins.string().c_str(), TRUE);
    g_setenv("GST_PLUGIN_SCANNER_1_0", scanner.string().c_str(), TRUE);
    g_setenv("GST_REGISTRY_1_0", registry.string().c_str(), TRUE);
    gst_init(nullptr, nullptr);

    if (GST_TYPE_PHYS_MEMORY_ALLOCATOR == G_TYPE_INVALID || GST_TYPE_RTSP_SERVER == G_TYPE_INVALID ||
        GST_TYPE_SDP_MESSAGE == G_TYPE_INVALID) {
        std::cerr << "Could not load the GStreamer Allocators, RTSP Server and SDP SDK types\n";
        return 1;
    }

    guint major, minor, micro, nano;
    gst_version(&major, &minor, &micro, &nano);
    if (major != 1 || minor != 28 || micro != 9 || !fs::is_regular_file(registry)) {
        std::cerr << "Unexpected GStreamer version or registry: " << gst_version_string() << '\n';
        return 1;
    }
    const char* factories[] = {"appsrc", "appsink", "h264parse", "h265parse", "x264enc", "x265enc", "avdec_h264", "avdec_h265",
                               "qtdemux", "matroskademux", "matroskamux", "flvmux", "rtmpsrc", "rtmpsink", "rtspsrc", "rtspclientsink",
                               "rtph264pay", "rtph264depay", "rtph265pay", "rtph265depay", "jpegenc", "jpegdec"};
    for (const char* factory : factories) {
        if (!require_factory(factory, plugins)) {
            return 1;
        }
    }
#if defined(__APPLE__)
    for (const char* factory : {"avfvideosrc", "glimagesink"}) {
        if (!require_factory(factory, plugins)) {
            return 1;
        }
    }
#elif defined(_WIN32)
    for (const char* factory : {"mfvideosrc", "d3d11screencapturesrc", "d3d11convert", "d3d11download", "autovideosink", "curlhttpsrc"}) {
        if (!require_factory(factory, plugins)) {
            return 1;
        }
    }
#elif defined(__linux__) && defined(__x86_64__)
    if (!fs::is_regular_file(plugins / "libgstnvcodec.so") ||
        !fs::is_regular_file(package / "include/gstreamer-1.0/gst/cuda/gstcuda.h")) {
        std::cerr << "Missing nvcodec plugin or gst-cuda development header\n";
        return 1;
    }
#endif
    if (!read_frames("videotestsrc num-buffers=6 ! video/x-raw,format=I420,width=128,height=128 ! jpegenc ! jpegdec ! video/x-raw",
                     6, 2 * GST_SECOND)) {
        return 1;
    }
    const std::string http = argv[5];
    if (!http.empty() && !read_frames("uridecodebin uri=" + quoted(http) + " ! video/x-raw", 1, 8 * GST_SECOND)) {
        return 1;
    }
    const auto h264 = output / "h264.mkv";
    const auto h265 = output / "h265.mkv";
    if (!write_video(h264, "x264enc", "h264parse") || !write_video(h265, "x265enc", "h265parse") ||
        !read_frames("filesrc location=" + quoted(h264.generic_string()) + " ! matroskademux ! h264parse ! avdec_h264", 6, 2 * GST_SECOND) ||
        !read_frames("filesrc location=" + quoted(h265.generic_string()) + " ! matroskademux ! h265parse ! avdec_h265", 6, 2 * GST_SECOND)) {
        return 1;
    }
    const std::string rtsp = argv[3], rtmp = argv[4];
    if (!rtsp.empty() && !loopback("rtspclientsink protocols=tcp location=" + quoted(rtsp),
                                  "rtspsrc latency=0 protocols=tcp location=" + quoted(rtsp) + " ! rtph264depay ! h264parse ! avdec_h264")) {
        return 1;
    }
    if (!rtmp.empty() && !loopback("flvmux streamable=true ! rtmpsink sync=false location=" + quoted(rtmp),
                                  "rtmpsrc location=" + quoted(rtmp) + " ! flvdemux ! h264parse ! avdec_h264")) {
        return 1;
    }
    return 0;
}
