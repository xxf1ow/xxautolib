extern "C" {
#include <libavcodec/avcodec.h>
}

int main()
{
    // 只链接 avcodec，验证其依赖的 swresample 能由加载器找到。
    return avcodec_find_decoder(AV_CODEC_ID_OPUS) ? 0 : 1;
}
