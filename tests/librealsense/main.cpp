#include <librealsense2/rs.hpp>
#include <librealsense2/rsutil.h>
#include <nlohmann/json.hpp>

#include <cmath>

static_assert(NLOHMANN_JSON_VERSION_MAJOR == 3);
static_assert(NLOHMANN_JSON_VERSION_MINOR == 12);
static_assert(NLOHMANN_JSON_VERSION_PATCH == 0);

int main()
{
    rs2_error* error = nullptr;
    const int api_version = rs2_get_api_version(&error);
    if (error != nullptr) {
        rs2_free_error(error);
        return 1;
    }
    if (api_version != RS2_API_VERSION) {
        return 2;
    }

    rs2::context context;
    rs2::pipeline pipeline(context);
    rs2::config config;
    config.enable_stream(RS2_STREAM_COLOR, RS2_FORMAT_RGB8);
    config.enable_stream(RS2_STREAM_DEPTH, RS2_FORMAT_Z16);
    rs2::align align_to_color(RS2_STREAM_COLOR);

    rs2_intrinsics intrinsics{};
    intrinsics.width = 640;
    intrinsics.height = 480;
    intrinsics.fx = 600.0f;
    intrinsics.fy = 600.0f;
    intrinsics.ppx = 320.0f;
    intrinsics.ppy = 240.0f;
    intrinsics.model = RS2_DISTORTION_NONE;
    const float pixel[2] = {320.0f, 240.0f};
    float point[3]{};
    rs2_deproject_pixel_to_point(point, &intrinsics, pixel, 1.0f);
    if (std::abs(point[0]) > 0.0001f || std::abs(point[1]) > 0.0001f ||
        std::abs(point[2] - 1.0f) > 0.0001f) {
        return 3;
    }

    const auto settings = nlohmann::json::parse(R"({"color":true,"depth":true})");
    return settings.at("color").get<bool>() && settings.at("depth").get<bool>() ? 0 : 4;
}
