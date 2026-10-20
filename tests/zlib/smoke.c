#include <string.h>
#include <zlib.h>

int main(void)
{
    const unsigned char input[] = "installed static zlib round trip";
    unsigned char compressed[128];
    unsigned char restored[sizeof(input)];
    uLongf compressed_size = sizeof(compressed);
    uLongf restored_size = sizeof(restored);
    if (compress(compressed, &compressed_size, input, sizeof(input)) != Z_OK)
        return 1;
    if (uncompress(restored, &restored_size, compressed, compressed_size) != Z_OK)
        return 2;
    return restored_size != sizeof(input) || memcmp(input, restored, sizeof(input)) != 0;
}
