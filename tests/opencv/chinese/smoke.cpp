#include <opencv2/core.hpp>
#include <opencv2/freetype.hpp>
#include <opencv2/imgproc.hpp>
#include <ft2build.h>
#include FT_FREETYPE_H
#include <hb-ft.h>
#include <hb.h>

#include <fstream>
#include <iostream>
#include <string>

int main(int argc, char **argv)
{
    if (argc != 3)
        return 1;
    FT_Library library = nullptr;
    FT_Face face = nullptr;
    if (FT_Init_FreeType(&library) || FT_New_Face(library, argv[1], 0, &face))
        return 2;
    // 字体必须包含真实中文字形，避免缺字方框也产生像素而误通过。
    if (!FT_Get_Char_Index(face, 0x4e2d) || !FT_Get_Char_Index(face, 0x6587)
        || FT_Set_Pixel_Sizes(face, 0, 56))
        return 3;
    const std::string text = "\xe4\xb8\xad\xe6\x96\x87";
    auto *font = hb_ft_font_create_referenced(face);
    auto *buffer = hb_buffer_create();
    hb_buffer_add_utf8(buffer, text.c_str(), static_cast<int>(text.size()), 0, -1);
    hb_buffer_guess_segment_properties(buffer);
    hb_shape(font, buffer, nullptr, 0);
    unsigned int count = 0;
    const auto *glyphs = hb_buffer_get_glyph_infos(buffer, &count);
    if (count != 2 || !glyphs[0].codepoint || !glyphs[1].codepoint)
        return 4;
    hb_buffer_destroy(buffer);
    hb_font_destroy(font);
    FT_Done_Face(face);
    FT_Done_FreeType(library);

    auto renderer = cv::freetype::createFreeType2();
    renderer->loadFontData(argv[1], 0);
    cv::Mat image = cv::Mat::zeros(180, 220, CV_8UC1);
    renderer->putText(image, text, cv::Point(8, 80), 56, cv::Scalar(255), -1, cv::LINE_AA, false);
    const int pixels = cv::countNonZero(image);
    if (pixels < 50)
        return 5;
    const auto bounds = cv::boundingRect(image);
    if (bounds.x <= 0 || bounds.y <= 0 || bounds.br().x >= image.cols || bounds.br().y >= image.rows)
        return 7;
    std::ofstream output(argv[2], std::ios::binary);
    output << "P5\n" << image.cols << ' ' << image.rows << "\n255\n";
    output.write(reinterpret_cast<const char *>(image.data), static_cast<std::streamsize>(image.total()));
    if (!output)
        return 6;
    std::cout << "chinese_glyphs=" << count << " rendered_pixels=" << pixels
              << " harfbuzz=" << hb_version_string() << '\n';
    return 0;
}
