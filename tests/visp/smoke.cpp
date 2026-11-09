#include <visp3/core/vpConfig.h>
#include <visp3/mbt/vpMbGenericTracker.h>
#include <visp3/visual_features/vpFeatureThetaU.h>
#include <visp3/visual_features/vpFeatureTranslation.h>
#include <visp3/vs/vpServo.h>

#include <cmath>
#include <iostream>
#include <map>
#include <string>
#include <vector>

#if !defined(VISP_HAVE_OPENCV) || !defined(HAVE_OPENCV_IMGPROC) || !defined(HAVE_OPENCV_VIDEO)
#error "The mixed tracker requires ViSP OpenCV imgproc/video integration"
#endif
#if !defined(VISP_HAVE_OPENMP) || !defined(VISP_HAVE_LAPACK_BUILT_IN)
#error "The recipe requires OpenMP and built-in LAPACK"
#endif
#if defined(VISP_HAVE_REALSENSE2) || defined(VISP_HAVE_LIBUSB_1) || defined(VISP_HAVE_PCL) || \
    defined(VISP_HAVE_NLOHMANN_JSON) || defined(VISP_HAVE_EIGEN3)
#error "The recipe must not enable camera, PCL, JSON or Eigen integration"
#endif

namespace {
bool check_tracking(const std::string &model)
{
    constexpr unsigned int width = 640;
    constexpr unsigned int height = 480;
    vpImage<unsigned char> image(height, width, 230);
    std::vector<vpColVector> points(width * height, vpColVector(3));
    // 已知平面位于相机前方 0.8 m；纹理提供 KLT 角点，外轮廓提供移动边缘。
    for (unsigned int row = 0; row < height; ++row) {
        for (unsigned int col = 0; col < width; ++col) {
            const bool on_plane = row >= 165 && row <= 315 && col >= 245 && col <= 395;
            const double depth = on_plane ? 0.8 : 1.5;
            auto &point = points[row * width + col];
            point[0] = (static_cast<double>(col) - 320) * depth / 600;
            point[1] = (static_cast<double>(row) - 240) * depth / 600;
            point[2] = depth;
            if (on_plane) {
                const bool border = row < 169 || row > 311 || col < 249 || col > 391;
                image[row][col] = border ? 0 : ((row / 12 + col / 12) % 2 ? 80 : 180);
            }
        }
    }

    const std::vector<std::string> names{"color", "depth"};
    const std::vector<int> types{
        vpMbGenericTracker::EDGE_TRACKER | vpMbGenericTracker::KLT_TRACKER,
        vpMbGenericTracker::DEPTH_DENSE_TRACKER | vpMbGenericTracker::DEPTH_NORMAL_TRACKER};
    vpMbGenericTracker tracker(names, types);
    const vpCameraParameters camera(600, 600, 320, 240);
    tracker.setCameraParameters({{"color", camera}, {"depth", camera}});
    tracker.setCameraTransformationMatrix("depth", vpHomogeneousMatrix());
    tracker.setScanLineVisibilityTest(true);
    tracker.setDepthDenseSamplingStep(8, 8);
    tracker.setDepthNormalSamplingStep(8, 8);
    tracker.setDepthNormalFeatureEstimationMethod(vpMbtFaceDepthNormal::ROBUST_FEATURE_ESTIMATION);
    vpMe moving_edge;
    moving_edge.setRange(8);
    moving_edge.setSampleStep(4);
    tracker.setMovingEdge(moving_edge);
    tracker.loadModel(model);

    const vpHomogeneousMatrix pose(0, 0, 0.8, 0, 0, 0);
    std::map<std::string, const vpImage<unsigned char> *> images{{"color", &image}, {"depth", &image}};
    tracker.initFromPose(images, {{"color", pose}, {"depth", pose}});
    std::map<std::string, const std::vector<vpColVector> *> clouds{{"depth", &points}};
    std::map<std::string, unsigned int> widths{{"depth", width}}, heights{{"depth", height}};
    tracker.track(images, clouds, widths, heights);

    std::cout << "features: edge=" << tracker.getNbFeaturesEdge()
              << " klt=" << tracker.getNbFeaturesKlt()
              << " dense=" << tracker.getNbFeaturesDepthDense()
              << " normal=" << tracker.getNbFeaturesDepthNormal() << '\n';
    if (!tracker.getNbFeaturesEdge() || !tracker.getNbFeaturesKlt() ||
        !tracker.getNbFeaturesDepthDense() || !tracker.getNbFeaturesDepthNormal()) {
        return false;
    }
    const auto tracked_pose = tracker.getPose();
    return std::isfinite(tracked_pose[0][3]) && std::isfinite(tracked_pose[1][3]) &&
        std::isfinite(tracked_pose[2][3]) && std::abs(tracked_pose[0][3]) < 0.005 &&
        std::abs(tracked_pose[1][3]) < 0.005 && std::abs(tracked_pose[2][3] - 0.8) < 0.005;
}

bool check_servo()
{
    vpFeatureTranslation translation(vpFeatureTranslation::cdMc), desired_translation(vpFeatureTranslation::cdMc);
    vpFeatureThetaU rotation(vpFeatureThetaU::cdRc), desired_rotation(vpFeatureThetaU::cdRc);
    translation.buildFrom(vpHomogeneousMatrix(0.01, 0, 0, 0, 0, 0));
    rotation.buildFrom(vpHomogeneousMatrix());
    vpServo task;
    task.setServo(vpServo::EYEINHAND_CAMERA);
    task.setInteractionMatrixType(vpServo::CURRENT);
    task.setLambda(0.5);
    task.addFeature(translation, desired_translation);
    task.addFeature(rotation, desired_rotation);
    const auto velocity = task.computeControlLaw();
    if (velocity.size() != 6 || !std::isfinite(velocity[0]) || std::abs(velocity[0] + 0.005) > 1e-9) {
        return false;
    }
    for (unsigned int index = 1; index < velocity.size(); ++index) {
        if (!std::isfinite(velocity[index]) || std::abs(velocity[index]) > 1e-9) {
            return false;
        }
    }
    return true;
}
}

int main(int argc, char **argv)
{
    if (argc != 2) {
        std::cerr << "Expected a CAO model path\n";
        return 1;
    }
    try {
        if (!check_tracking(argv[1]) || !check_servo()) {
            std::cerr << "Mixed tracking or visual servo verification failed\n";
            return 1;
        }
    }
    catch (const vpException &error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
    return 0;
}
