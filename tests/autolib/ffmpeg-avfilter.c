#include <libavfilter/avfilter.h>
#include <libavfilter/version.h>

#include <stdio.h>

int main(void)
{
    const unsigned version = avfilter_version();
    if (version != LIBAVFILTER_VERSION_INT) {
        fprintf(stderr, "libavfilter version %u differs from headers %u\n",
                version, (unsigned)LIBAVFILTER_VERSION_INT);
        return 1;
    }
    if (avfilter_get_by_name("buffer") == NULL || avfilter_get_by_name("buffersink") == NULL) {
        fputs("libavfilter is missing buffer or buffersink\n", stderr);
        return 1;
    }
    return 0;
}
