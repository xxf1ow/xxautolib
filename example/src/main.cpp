#include <queue/readerwriterqueue.h>
#include <lz4.h>
#include <flann/flann.h>

#include <cstring>
#include <iostream>

int main()
{
    moodycamel::ReaderWriterQueue<int> queue;
    int value = 0;
    if (!queue.enqueue(1) || !queue.try_dequeue(value) || value != 1) {
        std::cerr << "queue: enqueue/dequeue failed\n";
        return 1;
    }
    std::cout << "queue: OK\n";

    const char input[] = "xxautolib local package example";
    char compressed[LZ4_COMPRESSBOUND(sizeof(input))] = {};
    char restored[sizeof(input)] = {};
    const int compressed_size = LZ4_compress_default(input, compressed, sizeof(input), sizeof(compressed));
    if (compressed_size <= 0
        || LZ4_decompress_safe(compressed, restored, compressed_size, sizeof(restored)) != sizeof(input)
        || std::memcmp(input, restored, sizeof(input)) != 0) {
        std::cerr << "lz4: compression round trip failed\n";
        return 1;
    }
    std::cout << "lz4: OK\n";

    float dataset[] = {0, 0, 1, 1, 2, 2};
    float query[] = {1, 1};
    float speedup = 0;
    auto parameters = DEFAULT_FLANN_PARAMETERS;
    parameters.algorithm = flann::FLANN_INDEX_LINEAR;
    parameters.cores = 1;
    const auto index = flann_build_index_float(dataset, 3, 2, &speedup, &parameters);
    if (!index) {
        std::cerr << "flann: index creation failed\n";
        return 1;
    }
    int nearest = -1;
    float distance = -1;
    const int search_status = flann_find_nearest_neighbors_index_float(
        index, query, 1, &nearest, &distance, 1, &parameters);
    const int free_status = flann_free_index_float(index, &parameters);
    if (search_status < 0 || free_status < 0 || nearest != 1 || distance != 0) {
        std::cerr << "flann: nearest-neighbor query failed\n";
        return 1;
    }
    std::cout << "flann: OK\n";
    return 0;
}
