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
#include <filesystem>
#include <iostream>
#include <system_error>

#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#endif

int main()
{
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

    std::filesystem::path module_path;
#ifdef _WIN32
    HMODULE module = nullptr;
    if (!GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                           reinterpret_cast<LPCWSTR>(&flann_build_index_float), &module))
        return 5;
    wchar_t filename[32768] = {};
    const auto length = GetModuleFileNameW(module, filename, 32768);
    if (length == 0 || length == 32768)
        return 6;
    module_path = filename;
#else
    Dl_info module = {};
    if (!dladdr(reinterpret_cast<void *>(&flann_build_index_float), &module) || !module.dli_fname)
        return 5;
    module_path = module.dli_fname;
#endif
    std::error_code error;
    const auto actual_path = std::filesystem::canonical(module_path, error);
    if (error)
        return 7;
    std::cout << "runtime_flann=" << actual_path.generic_u8string() << '\n';
#endif
    return 0;
}
