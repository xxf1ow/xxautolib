#include <opencv2/videoio.hpp>

#include <filesystem>
#include <iostream>
#include <system_error>
#include <vector>

#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#endif

namespace {
std::filesystem::path loaded_avcodec_path()
{
#ifdef _WIN32
    const auto module = GetModuleHandleW(AUTOLIB_AVCODEC_DLL);
    if (!module)
        return {};
    wchar_t filename[32768];
    const auto length = GetModuleFileNameW(module, filename, 32768);
    if (length == 0 || length == 32768)
        return {};
    return filename;
#else
    const auto symbol = dlsym(RTLD_DEFAULT, "avcodec_version");
    Dl_info module = {};
    if (!symbol || !dladdr(symbol, &module) || !module.dli_fname)
        return {};
    return module.dli_fname;
#endif
}
}

int main(int argc, char **argv)
{
    if (argc != 4)
        return 1;
    const std::vector<int> timeouts = {cv::CAP_PROP_OPEN_TIMEOUT_MSEC, 10000, cv::CAP_PROP_READ_TIMEOUT_MSEC, 8000};
    cv::VideoCapture capture(cv::String(argv[1]), cv::CAP_FFMPEG, timeouts);
    cv::Mat frame;
    if (!capture.read(frame) || frame.empty()) {
        std::cerr << "FFmpeg could not read an RTSP frame\n";
        return 2;
    }
    std::error_code error;
    const auto loaded = std::filesystem::canonical(loaded_avcodec_path(), error);
    if (error) {
        std::cerr << "Could not locate the FFmpeg codec library loaded by OpenCV\n";
        return 3;
    }
    const auto root = std::filesystem::canonical(argv[3], error);
    const auto relative = loaded.lexically_relative(root);
    std::cout << "runtime_ffmpeg=" << loaded.generic_u8string() << '\n';
    if (error || relative.empty() || relative.is_absolute() || *relative.begin() == "..")
        return 4;

    const auto expected_size = frame.size();
    const cv::String output(argv[2]);
    cv::VideoWriter writer(output, cv::CAP_FFMPEG, cv::VideoWriter::fourcc('m','p','4','v'), 10, expected_size);
    if (!writer.isOpened()) {
        std::cerr << "FFmpeg could not open the mp4v writer\n";
        return 5;
    }
    for (int i = 0; i < 3; ++i) {
        if (i != 0 && (!capture.read(frame) || frame.size() != expected_size)) {
            std::cerr << "Could not read RTSP frame " << i + 1 << " at " << expected_size << '\n';
            return 6;
        }
        writer.write(frame);
    }
    writer.release();
    cv::VideoCapture saved(output, cv::CAP_FFMPEG);
    for (int i = 0; i < 3; ++i) {
        if (!saved.read(frame) || frame.size() != expected_size) {
            std::cerr << "Could not read MP4 frame " << i + 1 << " at " << expected_size
                      << " from " << output << '\n';
            return 7;
        }
    }
    std::cout << "rtsp_frames=3 mp4_frames=3\n";
}
