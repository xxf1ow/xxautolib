# Agent Note: FLANN 与 OpenCV 的模块依赖及复杂资源接入

Status: proposed

## 问题

现有模块可以验证独立源码构建、归档、附属资源和预编译复制，却不能验证只请求叶子模块时的依赖发现、构建顺序及安装位置传递。OpenCV 还需要使用 contrib 源码并修正其 freetype 探测，只有资源下载能力不足以完成接入。

本批需要同时用小依赖链确认公共路径，用复杂模块确认实际消费：FLANN 验证 lz4 的查找接线，OpenCV 验证多依赖、contrib 资源、资源修改及平台安装布局。目标是形成可用的本地构建安装基线，不是迁移完整参考框架或实现使用这些库的应用。

## 方案

新增 `flann`、`harfbuzz`、`opencv` 三个 `cmake` 模块，复用已有 lz4 1.10.0 与 FreeType 2.13.2。沿用[模块扩展总体约束](2026-10-20-module-expansion.md)，本文拥有本批具体依赖字段、资源消费及版本配置；已实现的来源与复用语义仍由[流水线记录](../../implemented/architecture/2026-10-19-toml-externalproject-zlib.md)和[本地复用记录](../../implemented/architecture/2026-10-20-local-reuse-and-platform-inputs.md)拥有。

### 范围与参考基线

参考仓库为 `D:/Work/AutoLibBuilder`，只读使用 `src/dependencies/flann.cmake`、`harfbuzz.cmake`、`opencv.cmake` 和 `src/build-system/tp_setup.cmake`、`tp_depends.cmake`。参考本体不修改；迁移只作纯数据定义、公共引擎接口及本节明确版本与选项变化所需的适配。先完成构建安装基线，不顺手优化参考逻辑，不重构根 CMake 骨架。

本批包括公共依赖接线、资源路径引用、资源文件补丁、三库接入、必要系统工具说明，以及新增模块技能的同步核对。不接 Boost、PCL、oneTBB、ImGui 等其他候选，不做下游 adapter、可搬迁包、发布、CI、交叉编译、源码自动升级或额外缓存数据库，也不增加应用层 C++ 封装或改变上游公开 API。

本批作为一个可独立验收的成果：先用 FLANN 确认依赖路径，再以 OpenCV 验证复杂情况；不把前者通过当成后者通过，也不以后续库迁移为本批完成条件。

### 依赖关系与来源

`depends` 表示当前模块构建时直接需要的独立模块。实际关系为 `flann → lz4`、`harfbuzz → freetype`、`opencv → freetype, harfbuzz`；箭头指向依赖。contrib 是 opencv 的附属输入，不是独立公开模块。

| 模块或资源 | 固定来源 | 约束 |
| --- | --- | --- |
| flann | `https://github.com/flann-lib/flann.git`，提交 `f9caaf609d8b8cb2b7104a85cf59eb92c275a25d` | 沿用参考固定提交；不改为跟随分支或另一 release。 |
| harfbuzz | `https://github.com/harfbuzz/harfbuzz/archive/refs/tags/8.5.0.zip` | 沿用参考 8.5.0 与 SHA256 `734985456b60c198da41f4c85280963795b9baa6fb6f3b91c6b85026da837933`，下载名 `harfbuzz-8.5.0.zip`。 |
| opencv | `https://github.com/opencv/opencv/archive/refs/tags/4.14.0.zip` | 使用 4.14.0，下载名 `opencv-4.14.0.zip`，不沿用参考 4.9.0。 |
| opencv_contrib | `https://github.com/opencv/opencv_contrib/archive/refs/tags/4.14.0.zip` | 与主库同为 4.14.0，下载名 `opencv_contrib-4.14.0.zip`，归属 opencv。 |

OpenCV 两份新归档的 SHA256 在实施取源时计算、核对并登记到各自声明，之后由原生 ExternalProject 校验；不复制 4.9.0 的摘要，不使用空值或关闭校验。此设计不把尚未测量的摘要写成事实。HarfBuzz 归档仍须通过已登记摘要校验。

独立 FLANN 与 OpenCV 的 `opencv_flann` 不是同一模块。本批不添加 `opencv → flann` 外部依赖，也不替换 OpenCV 自带的该模块。FreeType 保留既有可选系统依赖探测，不新增 `freetype → harfbuzz` 构建边，不为避免潜在探测结果而擅自设置 `FT_DISABLE_*`。

### 模块依赖声明与生成

模块顶层新增可选 `depends` 字符串数组，元素使用模块目录的准确名称，无依赖时省略。依赖不按平台或架构变化，本批不提供条件依赖、版本范围或版本求解。重复依赖去重并保留首次声明顺序。

生成器从根入口选中的模块出发递归读取依赖声明，按依赖先于使用方的稳定顺序生成，每模块只生成一次；独立模块保持入口顺序，依赖按声明顺序访问。缺失依赖、非法名称、自依赖或依赖环在 configure 阶段失败，诊断包含声明路径及相关依赖链；未选中且不在闭包中的模块不获取、不构建。所有实际读取的依赖 TOML 进入现有 configure 输入登记。

使用方的 ExternalProject 通过原生 `DEPENDS` 等待直接依赖安装完成；资源依赖与模块依赖合并，而不是互相覆盖。copy 类型如果声明依赖，同样建立目标顺序，但不添加不存在的 configure 命令。

sentinel 不改变依赖图：声明仍完整解析，所选闭包的目标仍生成；命中的模块只保留同名空目标，不执行其资源、补丁或步骤。空目标保留与直接模块依赖的顺序关系，查找元数据仍可提供给其他模块。sentinel 只证明登记文件存在，不验证版本、ABI 或安装完整性，也不保证自动修复已有安装。

### 安装查找位置与配置身份

模块顶层新增可选 `config_dir`、`pkgconfig_dir`、`package_name`。前两者是安装前缀内的相对目录，沿用 sentinel 的路径限制；`package_name` 是 CMake 包名，省略时使用模块名，仅在声明 `config_dir` 时使用。不声明并不触发目录扫描或自动猜测。`platforms.windows`、`platforms.linux` 可分别覆盖这三个字段和 `sentinel`；其余平台规则不变，不扩展为任意字段的递归合并。

| 模块 | CMake 包目录 | pkg-config 目录 | sentinel |
| --- | --- | --- | --- |
| lz4 | `lib/cmake/lz4` | `lib/pkgconfig` | 保留 `include/lz4.h`。 |
| freetype | `lib/cmake/freetype` | `lib/pkgconfig` | 保留 `include/freetype2/ft2build.h`。 |
| flann | `lib/cmake/flann` | `lib/pkgconfig` | `include/flann/flann.hpp`。 |
| harfbuzz | 不预设 CMake 包目录 | `lib/pkgconfig` | `include/harfbuzz/hb.h`。 |
| opencv | Windows 为 `.`，Linux 为 `lib/cmake/opencv4`；包名为 `OpenCV` | 不新增参考未声明的目录 | Windows 为 `OpenCVConfig.cmake`，Linux 为 `lib/cmake/opencv4/OpenCVConfig.cmake`。 |

OpenCV 保留上游原生安装布局。Windows 根配置选择 `x64/vc17/` 下的实际库；不为匹配参考的 `lib` 假设添加安装重布局选项。包查找元数据与安装检查均使用安装根配置，Linux 规则不变。

使用方 configure 只接收其直接依赖的查找位置，不把全部所选模块或兄弟模块安装树混在一起。标准接线包括：安装前缀加入 `CMAKE_PREFIX_PATH`，声明的 CMake 包目录生成 `<package_name>_DIR`，声明的 `.pc` 目录加入 `PKG_CONFIG_PATH`；沿用参考通过 `CMAKE_PROJECT_INCLUDE` 注入依赖 include 目录的逻辑，不将其改造成上游源码重写。包名大小写按声明保留。

查找路径按声明顺序去重，本仓库依赖路径优先，保留已有环境用于上游可选系统依赖。不承诺关闭全部系统搜索；验收必须核对显式依赖实际来自本仓库，若误用系统同名库则本批未通过。库私有参数或上游查找缺陷由模块声明或专用文件适配，不在通用引擎中加入库名判断。

子配置环境在当前模块内生效，不写全局环境变量或机器配置。`PKG_CONFIG_PATH` 等环境路径使用目标原生平台的分隔符；CMake 列表沿用现有安全参数编码，含空格路径不得拆成多个参数。环境代理仍执行原生 CMake configure 并传播非零结果，不另建构建调度器；现有生成器、平台、完整工具集、instance、显式 CRT、Release、policy/PIC/C++ 标准优先级保持不变。新增依赖参数不应覆盖已有 `CMAKE_PROJECT_INCLUDE` 而丢失调用者配置，需合并执行。

### 附属资源路径与修改

在 `cmake.cache` 字符串及字符串数组元素中支持 `<RESOURCE_SOURCE_DIR:资源名>`，生成器把它替换为当前模块所选资源的实际源码根。OpenCV 的 `OPENCV_EXTRA_MODULES_PATH` 使用该引用后接 `/modules`，不在模块中硬编码 `build/ep/` 布局。未知资源名在声明处理阶段报错；资源源码目录此时尚未存在不构成失败。不扩展为任意变量求值、跨模块资源引用或通用模板语言。

`[[patches]]` 新增可选 `resource` 名称。省略时作用于主目标，提供时作用于该资源的内部 ExternalProject；未知资源名报错。资源来源仍是原有纯来源表，不将执行字段混入 Git/URL 选源规则。现有主目标 `[[steps]]` 的字段和原生排序语义不变；本批资源修正可由文件补丁完成，不另外扩展资源自定义步骤接口。

文件补丁复用现有驱动、输入跟踪及可应用/已应用/冲突规则，补丁根为被选目标的源码根，不能误指向主库。资源 patch 必须包含在资源完成依赖中，使主库 configure 前修改已完成。若模块 sentinel 命中，资源及所有修改都跳过。

OpenCV 保留参考对 contrib freetype 注入 `find_package(PkgConfig REQUIRED)` 的修正。其维护文件放在 `src/dependencies/opencv/`，以作用于 `opencv_contrib` 的文件补丁表达同一源码修改，使用现有驱动代替参考临时文件和 sed 注入命令；不改写 freetype 的探测算法。补丁应在资源下载解压后、主库 configure 前生效。无需因此要求 OpenCV 构建具备 bash/sed；pkg-config 仍是实际前置工具，Windows 不假设上游根配置已自动找到它。

### FLANN 与 HarfBuzz 配置

FLANN 保留参考 cache：`BUILD_C_BINDINGS=ON`、`USE_OPENMP=ON`；`BUILD_PYTHON_BINDINGS`、`BUILD_MATLAB_BINDINGS`、`BUILD_CUDA_LIB`、`BUILD_EXAMPLES`、`BUILD_TESTS`、`BUILD_DOC`、`USE_MPI` 均为 OFF。不新增全局静动态开关，不为固定提交的旧 CMake 声明另写构建系统；复用公共 policy 兜底，兼容性由实际构建确认。

HarfBuzz 保留 `HB_HAVE_FREETYPE=ON`，使用本仓库 FreeType。未在参考中指定的其他库选项保留上游行为；不因本次只消费文字绘制便顺手裁剪 subset 等组件。两库均不重复声明公共生成器、CRT、Release、PIC 和 C++ 标准。

### OpenCV 4.14.0 配置

配置以参考为基线，主库与 contrib 同步升级到 4.14.0。以下为本批库特定配置，不替代公共引擎参数；没有列出的模块继续按上游依赖与平台探测决定，不将此表改写成 OpenCV 的 `BUILD_LIST` 白名单，也不把“未禁用”写成“保证构建”。根项目选择模块的同名 `BUILD_LIST` 不传入 OpenCV 子项目。

| 配置 | 值或处理 |
| --- | --- |
| `OPENCV_EXTRA_MODULES_PATH` | 本模块 contrib 源码根下的 `modules`。 |
| `OPENCV_ENABLE_NONFREE`、`BUILD_opencv_world`、`BUILD_opencv_freetype` | ON。 |
| `WITH_FFMPEG` | ON；验收记录实际探测结果，不把请求值当后端可用证据。 |
| `WITH_GSTREAMER`、`WITH_PROTOBUF` | OFF。 |
| `BUILD_opencv_signal` | OFF，作为 4.14 contrib 新增模块显式禁用。 |
| `calib3d`、`features2d`、`opencv_flann`、`ximgproc`、`video` | 保留启用路径，不新增关闭开关。 |
| `WITH_KLEIDICV` | 不覆盖 4.14 的平台默认值，不添加全平台 OFF。 |
| `WITH_FASTCV` | 沿用上游默认 OFF，不增加冗余设置。 |

保留参考下列 OFF 选项，不因升级恢复上游默认值：

```text
BUILD_DOCS
BUILD_EXAMPLES
BUILD_PACKAGE
BUILD_TESTS
BUILD_PERF_TESTS
BUILD_opencv_apps
BUILD_JAVA
BUILD_opencv_java_bindings_generator
BUILD_opencv_js
BUILD_opencv_js_bindings_generator
BUILD_opencv_python2
BUILD_opencv_python3
BUILD_opencv_python_bindings_generator
BUILD_opencv_python_tests
BUILD_opencv_objc_bindings_generator
BUILD_opencv_ts
BUILD_opencv_ml
BUILD_opencv_dnn
BUILD_opencv_objdetect
BUILD_opencv_gapi
BUILD_opencv_bgsegm
BUILD_opencv_optflow
BUILD_opencv_stereo
BUILD_opencv_superres
BUILD_opencv_tracking
BUILD_opencv_videostab
BUILD_opencv_datasets
BUILD_opencv_phase_unwrapping
BUILD_opencv_bioinspired
BUILD_opencv_hfs
BUILD_opencv_saliency
BUILD_opencv_rapid
BUILD_opencv_rgbd
BUILD_opencv_surface_matching
BUILD_opencv_structured_light
BUILD_opencv_ccalib
```

[4.14 calib3d](https://github.com/opencv/opencv/blob/4.14.0/modules/calib3d/CMakeLists.txt)直接依赖 features2d 和 opencv_flann，因此不直接使用特征检测不构成关闭这两个模块的理由。[ximgproc](https://github.com/opencv/opencv_contrib/blob/4.14.0/modules/ximgproc/CMakeLists.txt)直接依赖 video；保留其 WLS 视差过滤能力不要求启用 contrib optflow 或 tracking。关闭 contrib stereo 不等于关闭 calib3d 的双目校准、校正和 StereoBM/StereoSGBM。

contrib 4.9.0 与 4.14.0 的模块目录对照结果仅新增 fastcv、signal，没有删除或改名；该结论来自源码目录而非安装模块清单。signal 的一维信号重采样不在本批能力需要内，fastcv 不主动启用。KleidiCV 按 [4.14 根配置](https://github.com/opencv/opencv/blob/4.14.0/CMakeLists.txt)的平台条件处理；本批仅记录 ARM 构建的实际启用状态、工具链及必要限制，不承诺性能提升，不增加性能基准或自动后端切换。

保留参考对非自由算法的许可开关，不据此宣称所有启用模块具有相同许可证。OpenCV 的共享库默认行为不因其依赖含静态库而改变；不新增全静态、自包含、跨工具链 ABI 或安装树可搬迁保证。

### 工具、取源与已有数据

各模块自行说明实际前置工具及系统开发包，不依赖同批兄弟模块碰巧安装它们，也不新增 apt/choco 安装器。pkg-config 用于 FLANN 与文字库探测，保留必要的 OpenMP 和所选 FFmpeg 后端环境要求；配置摘要记录哪些可选项实际可用。若上游配置或生成步骤需要 Python，使用根 uv 环境中的解释器，不回退到系统 Python，不因禁用 Python 绑定就假定完全不需要解释器。

所有包按[基础设施取源规则](../../implemented/process/2026-10-19-repository-foundation.md#验证授权)先在 Windows 下载、校验，再传到 ARM；Git 来源可使用仅含对应上游历史的 bundle，接收后核对固定提交。OpenCV 内部的 IPP、Windows FFmpeg、KleidiCV 或其他附属下载仍沿用 4.14 原生缓存及校验机制，ARM 所需缓存同样先在 Windows 准备，不在 ARM 反复联网重试。两份主归档的摘要不代表内部下载均已固定或离线可用；不导入 4.9 专用缓存，不新增通用下载器、镜像健康状态或恢复服务。

实施前检查并保护直接受影响的本地源码、构建状态和安装树。持久 Git、归档及 sentinel 的语义不变；更换版本不得用重新 configure 或文件已存在冒充升级成功。按[开发指南](../../../development.md)只准备受影响路径，不清空 build、git、3rdparty 根目录，不删除或搬迁其他模块及用户修改。若需要偏离参考选项或本设计才能完成构建，暂停受影响工作，说明具体影响并取得人工决定。

### 验证、技能与文档交接

新增公共行为在 `tests/build_system/` 用现有 unittest 与隔离 fixture 验证，不新建测试框架：叶子请求发现闭包、去重与顺序，缺失及环诊断，依赖 TOML 触发重配置，原生目标顺序、实际收到的包/前缀/环境路径，资源引用及选定目标的补丁，以及 sentinel 命中仍可作为依赖且不执行资源。含空格路径、列表和平台环境分隔符围绕新增接线定向覆盖，不穷举模块开关组合。

先在 Windows x64 使用现有 VS2022/v143 环境完成真实根入口构建，再在 `lxx@192.168.0.127` 的 `/home/lxx/xxautolib` 完成 Linux aarch64 验证。源码传输和数据保护遵守[基础设施授权](../../implemented/process/2026-10-19-repository-foundation.md)，仅传任务源码及已校验的第三方输入，不传本仓库 `.git/`、`.venv/`、Windows 构建安装产物或凭据；第三方输入单独传输，不整体复制 `git/`、`build/`、`3rdparty/`。该授权不包含其他机器、目录、托管平台写操作或远端 CI。

两端分别只请求 flann，确认得到 lz4 与 flann；只请求 opencv，确认得到 freetype、harfbuzz 与 opencv，不引入独立 flann。已有安装可按 sentinel 复用，但新增三库必须有真实编译安装证据，资源修改必须有实际执行证据，不能全部由旧安装命中替代。

产物核对包括版本与来源、公开头文件、实际库名及静动态类型、CMake 配置和 `.pc` 路径、FFmpeg 等可选后端状态。通过配置日志、cache、`.pc` 解析值和实际链接输入确认 lz4、FreeType、HarfBuzz 来自本仓库；FreeType 的其他可选外部依赖照实记录，不要求消除。OpenCV 的配置摘要与实际库必须同时包含 calib3d、features2d、flann、ximgproc、video、freetype，world 构建不能掩盖 freetype 被静默禁用。

对 OpenCV 使用安装产物作最小链接运行检查，引用 StereoSGBM、WLS 过滤器及 freetype 创建接口，确认对应符号与文字库依赖可链接、运行库可加载；不要求相机、字体下载、模型、完整视差计算或应用工程。此检查属于内部接入验证，不产出下游 adapter，也不扩成上游算法正确性测试或每库一套专用测试项目。

按实际影响运行定向公共回归，本批完成后再运行公共测试全集。Linux x86_64 实机和 v142 实机保持未覆盖，不以 Linux aarch64 或 v143 成功替代。ARM 上记录 KleidiCV 是否实际启用；若编译器或平台限制阻止已确认配置构建，先报告并确认最小调整，不自行关闭或升级工具链。

同步核对本仓库 [add-third-party-module 技能](../../../../.agents/skills/add-third-party-module/SKILL.md)：新增能力实现后再说明可执行的依赖和资源写法，引用真实模块，不另存完整模板，不把内部接线误写成下游交付。检查技能元数据、本地链接及新增/迁移/升级触发范围与运行时问题排除范围，不以措辞匹配测试代替实际接入结果。

当前架构、开发与验证命令在能力落地时分别更新到对应文档；路线图只登记本批实际验收范围，不把所有复杂模块或迁移候选标成已完成。本设计批准后先建普通任务分支，将设计作为首提交，再编写并审阅实现计划；不在主分支提交设计，不创建 worktree，不自动 push 或创建 PR。

## 备选方案

**只迁移 FLANN，暂不验证 OpenCV。** 小依赖链成本较低，但不能证明多依赖与资源修改能够协同工作，达不到本批同时覆盖路径与复杂情况的目标。

**依赖只排目标顺序，依靠系统环境发现库。** 目标可能构建成功却消费错误安装；同时传递参考已有的标准查找位置，并以实际解析及链接结果验收。

**OpenCV 沿用 4.9.0，或按应用需要改成全新模块白名单。** 前者不满足本批版本要求；后者会同时改变大量默认模块及依赖行为，不利于保留参考基线。本批只升级匹配版本并执行明确的参数取舍。

**将独立库作为资源重复构建，或把复杂库消费规则写进公共引擎。** 前者破坏独立模块复用，后者使引擎依赖具体库布局；独立库保持模块身份，contrib 保持附属输入，专用修正放在 opencv 模块目录。

## 验收标准

- Windows x64、Linux aarch64 均通过真实根入口构建安装三个新增模块；仅请求叶子即可取得所需依赖，不选择无关模块。
- FLANN 使用固定提交并消费本仓库 lz4；HarfBuzz 为 8.5.0 并消费本仓库 FreeType；OpenCV 与 contrib 均为 4.14.0，新归档摘要已登记并在取源时校验。
- 依赖查找元数据、平台布局、sentinel 空目标与原生执行顺序协同工作；新增公共接口有定向回归，不改变既有来源复用和配置身份。
- contrib 在主库配置前准备并完成 freetype 修正；要求保留的模块实际构建，禁用项按设计生效，安装产物通过最小链接运行检查。
- 两端记录实际静动态库、显式依赖来源、可选外部依赖及后端状态；ARM 加速状态与未覆盖平台如实保留，不承诺自包含或性能提升。
- 模块技能与实际接口一致，文档、链接及公共测试检查通过；参考仓库未修改，无未经授权的下游、发布或 CI 扩展。

## 风险

4.14 的主归档及内部下载依赖上游网络可用性，原生下载失败不由本批恢复服务兜底。固定 FLANN 提交与 HarfBuzz 8.5.0 的旧构建逻辑，以及 ARM 优化路径，都需要当前工具链的真实编译证据；现阶段不宣称已有构建成功。

上游会根据机器环境自动启用或禁用可选能力；配置请求值不是安装清单。尤其 freetype 探测失败可能不终止 OpenCV 配置，因此必须核对实际模块与链接结果。共享 OpenCV 消费静态文字库时还需关注 PIC、CRT 及其传递外部依赖，不能以安装了静态归档证明已自包含。

sentinel 不感知版本或依赖变化，已有源码也不自动校准固定提交；本批保留人工准备边界而非补建通用失效系统。本设计只确认本地构建安装，不保证打包、搬迁、其他平台工具链或未验证可选模块可用。
