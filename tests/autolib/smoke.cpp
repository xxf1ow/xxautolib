#ifdef AUTOLIB_PYBIND_MODULE
#include <pybind11/pybind11.h>
PYBIND11_MODULE(autolib_binding, module)
{
    module.def("add", [](int left, int right) { return left + right; });
}
#else
#ifdef AUTOLIB_TEST_QUEUE
#include <queue/readerwriterqueue.h>
#endif
#ifdef AUTOLIB_TEST_LZ4
#include <lz4.h>
#endif
#ifdef AUTOLIB_TEST_FLANN
#include <flann/flann.h>
#include <flann/flann.hpp>
#endif

#ifdef AUTOLIB_TEST_LZ4
#include <cstring>
#endif
#ifdef AUTOLIB_TEST_EIGEN
#include <Eigen/Core>
#include <string>
#endif
#ifdef AUTOLIB_TEST_FREETYPE
#include <ft2build.h>
#include FT_FREETYPE_H
#endif
#ifdef AUTOLIB_TEST_HARFBUZZ
#include <hb.h>
#endif
#ifdef AUTOLIB_TEST_LIBUSB
#include <libusb.h>
#endif
#ifdef AUTOLIB_TEST_NLOHMANN
#include <nlohmann/json.hpp>
#endif
#ifdef AUTOLIB_TEST_OPENCV
#include <opencv2/core.hpp>
#include <opencv2/freetype.hpp>
#endif
#ifdef AUTOLIB_TEST_LIBREALSENSE
#include <librealsense2/rs.hpp>
#endif
#ifdef AUTOLIB_TEST_ONNXRUNTIME
#include <onnxruntime_c_api.h>
#endif
#ifdef AUTOLIB_TEST_ASIO
#include <asio.hpp>
#endif
#ifdef AUTOLIB_TEST_CLIPPER2
#include <clipper2/clipper.h>
#endif
#ifdef AUTOLIB_TEST_CPP_HTTPLIB
#include <cpp-httplib/httplib.h>
#endif
#ifdef AUTOLIB_TEST_EXPECTED
#include <tl/expected.hpp>
#endif
#ifdef AUTOLIB_TEST_GTEST
#include <gtest/gtest.h>
TEST(AutolibPackage, AssertionRuns) { EXPECT_EQ(6 * 7, 42); }
#endif
#ifdef AUTOLIB_TEST_LIBZMQ
#include <zmq.h>
#endif
#ifdef AUTOLIB_TEST_NATIVEFILEDIALOG_EXTENDED
#include <nfd.h>
#endif
#ifdef AUTOLIB_TEST_ONETBB
#include <oneapi/tbb/parallel_reduce.h>
#include <oneapi/tbb/blocked_range.h>
#endif
#ifdef AUTOLIB_TEST_PORTAUDIO
#include <portaudio.h>
#endif
#ifdef AUTOLIB_TEST_SDL3
#include <SDL3/SDL.h>
#endif
#ifdef AUTOLIB_TEST_SPDLOG
#include <spdlog/spdlog.h>
#include <spdlog/sinks/ostream_sink.h>
#include <sstream>
#endif
#ifdef AUTOLIB_TEST_TASKFLOW
#include <taskflow/taskflow.hpp>
#endif
#ifdef AUTOLIB_TEST_YAML_CPP
#include <yaml-cpp/yaml.h>
#endif
#ifdef AUTOLIB_TEST_ZLIB
#include <zlib.h>
#endif
#ifdef AUTOLIB_TEST_BOOST
#include <boost/filesystem/path.hpp>
#include <boost/regex.hpp>
#endif
#ifdef AUTOLIB_TEST_WEBSOCKETPP
#include <websocketpp/config/asio_no_tls_client.hpp>
#include <websocketpp/client.hpp>
#endif
#ifdef AUTOLIB_TEST_IMGUI
#include <imgui.h>
#endif
#ifdef AUTOLIB_TEST_PCL
#include <pcl/point_types.h>
#include <pcl/search/kdtree.h>
#endif
#include <filesystem>
#include <iostream>
#include <system_error>

#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#endif

#if defined(AUTOLIB_TEST_FLANN) || defined(AUTOLIB_TEST_OPENCV) || defined(AUTOLIB_TEST_LIBREALSENSE) || defined(AUTOLIB_TEST_ONNXRUNTIME)
namespace
{
bool check_runtime(const char *name, const void *address, const char *expected_root)
{
    std::filesystem::path module_path;
#ifdef _WIN32
    HMODULE module = nullptr;
    if (!GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                           reinterpret_cast<LPCWSTR>(address), &module))
        return false;
    wchar_t filename[32768] = {};
    const auto length = GetModuleFileNameW(module, filename, 32768);
    if (length == 0 || length == 32768)
        return false;
    module_path = filename;
#else
    Dl_info module = {};
    if (!dladdr(address, &module) || !module.dli_fname)
        return false;
    module_path = module.dli_fname;
#endif
    std::error_code error;
    const auto actual_path = std::filesystem::canonical(module_path, error);
    if (error)
        return false;
    const auto root = std::filesystem::canonical(expected_root, error);
    if (error)
        return false;
    std::cout << "runtime_" << name << '=' << actual_path.generic_u8string() << '\n';
    const auto relative = actual_path.lexically_relative(root);
    return !relative.empty() && !relative.is_absolute() && *relative.begin() != "..";
}
}
#endif

int main()
{
#ifdef AUTOLIB_TEST_BOOST
    if (!boost::regex_match(std::string("answer42"), boost::regex("answer[0-9]+"))
        || boost::filesystem::path("/one/../two").lexically_normal().generic_string() != "/two")
        return 42;
#endif
#ifdef AUTOLIB_TEST_WEBSOCKETPP
    websocketpp::client<websocketpp::config::asio_client> client;
    client.init_asio();
    websocketpp::lib::error_code connection_error;
    const auto connection = client.get_connection("ws://127.0.0.1:1/acceptance", connection_error);
    if (connection_error || !connection || connection->get_uri()->get_resource() != "/acceptance")
        return 43;
#endif
#ifdef AUTOLIB_TEST_IMGUI
    auto *gui_context = ImGui::CreateContext();
    if (!gui_context)
        return 44;
    float hue = 0, saturation = 0, brightness = 0;
    ImGui::ColorConvertRGBtoHSV(0, 1, 0, hue, saturation, brightness);
    ImGui::DestroyContext(gui_context);
    if (hue < 0.333f || hue > 0.334f || saturation != 1 || brightness != 1)
        return 45;
#endif
#ifdef AUTOLIB_TEST_PCL
    pcl::PointCloud<pcl::PointXYZ>::Ptr cloud(new pcl::PointCloud<pcl::PointXYZ>);
    cloud->push_back(pcl::PointXYZ(0, 0, 0));
    cloud->push_back(pcl::PointXYZ(1, 1, 1));
    cloud->push_back(pcl::PointXYZ(2, 2, 2));
    pcl::search::KdTree<pcl::PointXYZ> tree;
    tree.setInputCloud(cloud);
    std::vector<int> pcl_neighbors(1);
    std::vector<float> pcl_distances(1);
    if (tree.nearestKSearch((*cloud)[1], 1, pcl_neighbors, pcl_distances) != 1
        || pcl_neighbors[0] != 1 || pcl_distances[0] != 0)
        return 46;
#endif
#ifdef AUTOLIB_TEST_ASIO
    asio::io_context io;
    int posted_value = 0;
    asio::post(io, [&] { posted_value = 42; });
    if (io.run() != 1 || posted_value != 42)
        return 21;
#endif
#ifdef AUTOLIB_TEST_CLIPPER2
    const Clipper2Lib::Paths64 subject = {{{0, 0}, {10, 0}, {10, 10}, {0, 10}}};
    const Clipper2Lib::Paths64 clip = {{{5, 0}, {15, 0}, {15, 10}, {5, 10}}};
    const auto intersection = Clipper2Lib::Intersect(subject, clip, Clipper2Lib::FillRule::NonZero);
    if (Clipper2Lib::Area(intersection) != 50)
        return 22;
#endif
#ifdef AUTOLIB_TEST_CPP_HTTPLIB
    httplib::Response response;
    response.set_content("verified response", "text/plain");
    if (response.body != "verified response" || response.get_header_value("Content-Type") != "text/plain")
        return 23;
#endif
#ifdef AUTOLIB_TEST_EXPECTED
    const tl::expected<int, int> success = 42;
    const tl::expected<int, int> failure = tl::unexpected<int>(7);
    if (!success || *success != 42 || failure || failure.error() != 7)
        return 24;
#endif
#ifdef AUTOLIB_TEST_GTEST
    testing::InitGoogleTest();
    if (RUN_ALL_TESTS() != 0)
        return 25;
#endif
#ifdef AUTOLIB_TEST_LIBZMQ
    void *context = zmq_ctx_new();
    if (!context)
        return 26;
    void *sender = zmq_socket(context, ZMQ_PAIR);
    void *receiver = zmq_socket(context, ZMQ_PAIR);
    if (!sender || !receiver || zmq_bind(sender, "inproc://autolib-smoke") != 0
        || zmq_connect(receiver, "inproc://autolib-smoke") != 0)
        return 27;
    char message[4] = {};
    if (zmq_send(sender, "test", 4, 0) != 4 || zmq_recv(receiver, message, 4, 0) != 4
        || std::string(message, 4) != "test")
        return 28;
    if (zmq_close(sender) || zmq_close(receiver) || zmq_ctx_term(context))
        return 29;
#endif
#ifdef AUTOLIB_TEST_NATIVEFILEDIALOG_EXTENDED
    if (NFD_Init() != NFD_OKAY)
        return 30;
    NFD_Quit();
#endif
#ifdef AUTOLIB_TEST_ONETBB
    const int sum = oneapi::tbb::parallel_reduce(oneapi::tbb::blocked_range<int>(0, 100), 0,
        [](const auto &range, int value) { for (int i = range.begin(); i != range.end(); ++i) value += i; return value; },
        [](int left, int right) { return left + right; });
    if (sum != 4950)
        return 31;
#endif
#ifdef AUTOLIB_TEST_PORTAUDIO
    if (Pa_Initialize() != paNoError)
        return 32;
    const auto host_count = Pa_GetHostApiCount();
    if (host_count < 0 || Pa_Terminate() != paNoError)
        return 33;
    std::cout << "portaudio_host_apis=" << host_count << '\n';
#endif
#ifdef AUTOLIB_TEST_SDL3
    SDL_Surface *surface = SDL_CreateSurface(2, 2, SDL_PIXELFORMAT_RGBA32);
    if (!surface || !SDL_FillSurfaceRect(surface, nullptr, SDL_MapSurfaceRGB(surface, 10, 20, 30)))
        return 34;
    Uint8 red = 0, green = 0, blue = 0;
    const bool read_pixel = SDL_ReadSurfacePixel(surface, 1, 1, &red, &green, &blue, nullptr);
    SDL_DestroySurface(surface);
    if (!read_pixel || red != 10 || green != 20 || blue != 30)
        return 35;
#endif
#ifdef AUTOLIB_TEST_SPDLOG
    std::ostringstream logged;
    auto sink = std::make_shared<spdlog::sinks::ostream_sink_mt>(logged);
    spdlog::logger logger("smoke", sink);
    logger.set_pattern("%v");
    logger.info("answer {}", 42);
    logger.flush();
    if (logged.str() != "answer 42\n")
        return 36;
#endif
#ifdef AUTOLIB_TEST_TASKFLOW
    tf::Executor executor(2);
    tf::Taskflow taskflow;
    int task_value = 0;
    auto first = taskflow.emplace([&] { task_value = 20; });
    auto second = taskflow.emplace([&] { task_value += 22; });
    first.precede(second);
    executor.run(taskflow).get();
    if (task_value != 42)
        return 37;
#endif
#ifdef AUTOLIB_TEST_YAML_CPP
    const auto yaml = YAML::Load("answer: 42\nvalues: [1, 2, 3]\n");
    if (yaml["answer"].as<int>() != 42 || yaml["values"].size() != 3)
        return 38;
#endif
#ifdef AUTOLIB_TEST_ZLIB
    const unsigned char plain[] = "repeat repeat repeat repeat";
    unsigned char encoded[128] = {}, decoded[sizeof(plain)] = {};
    uLongf encoded_size = sizeof(encoded), decoded_size = sizeof(decoded);
    if (compress2(encoded, &encoded_size, plain, sizeof(plain), Z_BEST_COMPRESSION) != Z_OK
        || uncompress(decoded, &decoded_size, encoded, encoded_size) != Z_OK
        || decoded_size != sizeof(plain) || std::string(reinterpret_cast<char *>(decoded)) != "repeat repeat repeat repeat")
        return 39;
#endif
#ifdef AUTOLIB_TEST_FREETYPE
    FT_Library font_library = nullptr;
    if (FT_Init_FreeType(&font_library) != 0)
        return 11;
    if (FT_Done_FreeType(font_library) != 0)
        return 12;
#endif
#ifdef AUTOLIB_TEST_HARFBUZZ
    auto *buffer = hb_buffer_create();
    const bool allocated = hb_buffer_allocation_successful(buffer);
    hb_buffer_destroy(buffer);
    if (!allocated)
        return 13;
#endif
#ifdef AUTOLIB_TEST_LIBUSB
    libusb_context *usb_context = nullptr;
    if (libusb_init(&usb_context) != 0)
        return 14;
    libusb_exit(usb_context);
#endif
#ifdef AUTOLIB_TEST_NLOHMANN
    const auto document = nlohmann::json::parse(R"({"answer":42})");
    if (document.at("answer").get<int>() != 42)
        return 15;
#endif
#ifdef AUTOLIB_TEST_OPENCV
    const auto font = cv::freetype::createFreeType2();
    if (font.empty() || cv::countNonZero(cv::Mat::eye(2, 2, CV_8U)) != 2)
        return 16;
    if (!check_runtime("opencv", reinterpret_cast<const void *>(&cv::getTickCount), AUTOLIB_EXPECT_OPENCV_ROOT))
        return 17;
#endif
#ifdef AUTOLIB_TEST_LIBREALSENSE
    rs2::config camera_config;
    camera_config.disable_all_streams();
    if (!check_runtime("librealsense", reinterpret_cast<const void *>(&rs2_get_api_version), AUTOLIB_EXPECT_LIBREALSENSE_ROOT))
        return 18;
#endif
#ifdef AUTOLIB_TEST_ONNXRUNTIME
    const auto *api_base = OrtGetApiBase();
    if (!api_base || !api_base->GetApi(ORT_API_VERSION))
        return 19;
    const auto *api = api_base->GetApi(ORT_API_VERSION);
    OrtEnv *environment = nullptr;
    if (auto *status = api->CreateEnv(ORT_LOGGING_LEVEL_WARNING, "autolib-smoke", &environment)) {
        std::cerr << api->GetErrorMessage(status) << '\n';
        api->ReleaseStatus(status);
        return 40;
    }
    api->ReleaseEnv(environment);
    if (std::string(api_base->GetVersionString()) != "1.24.4")
        return 41;
    std::cout << "onnxruntime_version=" << api_base->GetVersionString() << '\n';
    if (!check_runtime("onnxruntime", reinterpret_cast<const void *>(&OrtGetApiBase), AUTOLIB_EXPECT_ONNXRUNTIME_ROOT))
        return 20;
#endif
#ifdef AUTOLIB_TEST_EIGEN
#ifdef EIGEN_PATCH_VERSION
    const auto eigen_version = std::to_string(EIGEN_MAJOR_VERSION) + "." + std::to_string(EIGEN_MINOR_VERSION) + "." + std::to_string(EIGEN_PATCH_VERSION);
#else
    const auto eigen_version = std::to_string(EIGEN_WORLD_VERSION) + "." + std::to_string(EIGEN_MAJOR_VERSION) + "." + std::to_string(EIGEN_MINOR_VERSION);
#endif
    std::cout << "eigen_version=" << eigen_version << '\n';
    if (eigen_version != AUTOLIB_EXPECT_EIGEN_VERSION)
        return 9;
    Eigen::Matrix2i matrix;
    matrix << 1, 2, 3, 4;
    if ((matrix * matrix).eval()(1, 1) != 22)
        return 10;
#endif
#ifdef AUTOLIB_TEST_QUEUE
    moodycamel::ReaderWriterQueue<int> queue;
    int value = 0;
    if (!queue.enqueue(17) || !queue.try_dequeue(value) || value != 17)
        return 1;
#endif

#ifdef AUTOLIB_TEST_LZ4
    const char input[] = "relocatable lz4 bytes: relocatable lz4 bytes";
    char compressed[LZ4_COMPRESSBOUND(sizeof(input))] = {};
    char restored[sizeof(input)] = {};
    const int compressed_size = LZ4_compress_default(input, compressed, sizeof(input), sizeof(compressed));
    if (compressed_size <= 0 || LZ4_decompress_safe(compressed, restored, compressed_size, sizeof(restored)) != sizeof(input)
        || std::memcmp(input, restored, sizeof(input)) != 0)
        return 2;
#endif

#ifdef AUTOLIB_TEST_FLANN
    float dataset[] = {0, 0, 1, 1, 2, 2};
    float speedup = 0;
    auto parameters = DEFAULT_FLANN_PARAMETERS;
    parameters.algorithm = flann::FLANN_INDEX_LINEAR;
    parameters.cores = 1;
    const auto index = flann_build_index_float(dataset, 3, 2, &speedup, &parameters);
    if (!index)
        return 3;
    if (flann_free_index_float(index, &parameters) < 0)
        return 4;

    flann::Matrix<float> points(dataset, 3, 2);
    flann::Index<flann::L2<float>> cpp_index(points, flann::LinearIndexParams());
    cpp_index.buildIndex();
    float query[] = {1, 1};
    int neighbor = -1;
    float distance = -1;
    flann::Matrix<float> queries(query, 1, 2);
    flann::Matrix<int> neighbors(&neighbor, 1, 1);
    flann::Matrix<float> distances(&distance, 1, 1);
    if (cpp_index.knnSearch(queries, neighbors, distances, 1, flann::SearchParams(-1)) != 1
        || neighbor != 1 || distance != 0)
        return 8;

    if (!check_runtime("flann", reinterpret_cast<const void *>(&flann_build_index_float), AUTOLIB_EXPECT_FLANN_ROOT))
        return 5;
#endif
    return 0;
}
#endif
