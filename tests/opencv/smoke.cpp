#include <opencv2/calib3d.hpp>
#include <opencv2/freetype.hpp>
#include <opencv2/ximgproc/disparity_filter.hpp>

int main()
{
    auto matcher = cv::StereoSGBM::create(0, 16, 3);
    auto filter = cv::ximgproc::createDisparityWLSFilter(matcher);
    auto text = cv::freetype::createFreeType2();
    return matcher.empty() || filter.empty() || text.empty() ? 1 : 0;
}
