# Agent Note: 第三方库分批迁移与交付

Status: proposed

## 问题

构建、导入、打包和 CI 基础能力已有闭环，剩余第三方库需要沿用参考配置迁入，形成可消费、可打包的模块。常规模块的配方转换无需逐库设计或新增测试工程；专用构建和复杂依赖则需要留出独立处理边界。

## 方案

参考仓库为 `D:/Work/AutoLibBuilder`。沿用[模块扩展约束](2026-10-20-module-expansion.md)，迁移模块声明、必要补丁、专用文件及导入适配，不修改参考仓库，不重构根 CMake。公共引擎仅将通用包的 queue 专用资格检查改为按声明复制映射核对实际载荷，不增加逐库白名单或新配置字段。

当前配方的 adapter 格式统一、七份缺失补齐及实际消费与搬迁证据由[导入适配决定](../../implemented/architecture/2026-11-04-package-adapter-standardization.md)拥有，不改变本记录的迁移配置、既有验证结论或 sherpa-onnx 暂停安排，也不将 ONNX Runtime adapter 解释为升级其版本。

### 范围与阶段

首批一次迁入 16 库：asio、clipper2、cpp-httplib、eigen、expected、gtest、libzmq、nativefiledialog-extended、portaudio、pybind11、sdl3、spdlog、taskflow、websocketpp、yaml-cpp、boost。

第二批一次迁入三库：oneTBB、imgui、pcl。sherpa-onnx 后置，目标是升级至实施前确认的最新上游版本；版本、功能选项、依赖与适配方案须单独讨论确认，本批不迁入，也不提前改动其依赖。

第三批 sherpa-onnx 迁入与配套 onnxruntime 1.28.2 升级一并后置，待版本管理完成后另行恢复。当前分支交付范围仅为前两批共 19 库及其必要接线，onnxruntime 保持 1.24.4。下文保留已确认的 sherpa-onnx v2.0.1 与 onnxruntime 1.28.2 适配基线，不代表迁入或升级已验证完成，也不包含 VoiceAsst 源码或模型修改。

明确排除 fastdeploy、nndeploy、ftxui、logo、mysql、gtsam、lastools，不迁入，也不保留为待迁候选。lastools 的参考实现仅支持 Windows；排除该库，不为它扩展公共引擎或 CI 的模块平台筛选。已有模块不重复迁移；只为本批依赖闭包补齐必需的既有模块导入适配，例如 Boost 依赖的 zlib，不扩成全库适配。

### 第二批适配范围

oneTBB 保留共享库、内存分配器及代理、IPO 和 HWLOC 搜索；Windows HWLOC 固定归档作为附属资源，并沿用安装后复制 DLL 的步骤。Linux 的 HWLOC 由系统开发包提供。

imgui 声明依赖 sdl3，保留参考专用 CMake 构建、SDL3/OpenGL3 后端、静态库及 imconfig 配置修改；专用文件和补丁归本模块，不迁入与构建无关的 Orange Pi 系统初始化、自启动脚本。

pcl 声明依赖 lz4、flann、boost、eigen、zlib，保留静态 PCL、共享 FLANN、OpenMP 和参考裁剪选项。导入适配使用实际安装的精确 Config 路径，封装参考列出的组件及声明依赖，不扫描任意配置或回退系统同名库。

三库均沿用现有 TOML、资源、补丁及自定义步骤能力，不修改公共引擎或根 CMake。第二批只在 109 通过普通根入口进行一次整批配置、编译安装，复用既有依赖；核对头文件、静动态库、实际导出配置、运行依赖及打包内容。不新增测试或消费者编译，失败后的修复续跑仍按下文约定确认，三平台消费验收由用户手动触发 CI。

### 暂停原因与版本管理前置要求

版本管理面向不同项目选择同一库的不同版本，而不是在同一个程序内混用两个 Eigen 版本。PCL 项目保留 Eigen 3.4.0，sherpa-onnx 项目使用 Eigen 5.0.1；不能为迁入 sherpa 而全局升级 Eigen、迫使 PCL 一起适配。当前 PCL 的 Eigen3 查找约束与 Eigen 5 的版本配置不兼容，统一升级不是本次迁移的必要步骤。

后续版本管理须支持同一包的不同版本分别发布和保留，并让各项目选择所需版本及其依赖闭包；安装和消费结果不得相互覆盖或混用。具体发布标识、目录布局和版本选择接口留待独立设计，本轮不实现，也不引入自动依赖求解。

恢复 sherpa 迁入前，须先验证 PCL 与 sherpa 两个独立项目分别取得指定 Eigen 版本及对应依赖，不依赖修改全局单一版本。恢复时再确认上游发行版与下文基线是否需要更新；本轮不继续修补或编译 sherpa。

### sherpa-onnx 适配基线（暂停）

以 [v2.0.1 正式发行版](https://github.com/k2-fsa/sherpa-onnx/releases/tag/v2.0.1)为来源，Git 固定完整提交 `af2d8d81dbfa61c4cea4c568da78f3982edca005`。沿用参考静态 CPU 构建，启用 TTS 和说话人分离，使用本仓库独立 onnxruntime 的共享 CPU 运行库；静态 sherpa 不意味着整个依赖链静态自包含。

只交付 `sherpa-onnx-core` 原生 C++ 接口。`SHERPA_ONNX_ENABLE_C_API`、`SHERPA_ONNX_ENABLE_PYTHON`、`SHERPA_ONNX_ENABLE_JNI`、WASM 总开关及所有 WASM 子开关关闭，不构建语言绑定；上游依赖 C API 的 `sherpa-onnx-cxx-api` 封装也不交付。PortAudio、WebSocket、工具程序、C/C++ 接口示例、测试、断言检查及 Sanitizer 关闭；GPU、DirectML、D3D 和各专用硬件后端保持关闭。Windows 保留动态 CRT `/MD`，Linux 不静态链接 libstdc++，不以语言接口裁剪为由关闭 TTS 或说话人分离算法。

接受 v2.0 的 TTS 前端调整，不恢复内置 espeak-ng 或 piper-phonemize，也不新增外部音素转换模块。依赖这些组件的模型须由消费应用采用词典或自行提供音素；普通文本推理并未整体删除。VoiceAsst 的默认 `matcha-icefall-zh-baker` 配置使用词典、tokens、FST 与 vocos，不配置 espeak 数据目录；源码核对不构成新版模型推理验收，发音、标点、停顿和流式分块仍可能变化。旧 Generate 重载仍存在且已标记弃用，本阶段不修改下游调用。行为依据为[上游 TTS 前端变更](https://github.com/k2-fsa/sherpa-onnx/pull/4055)。

onnxruntime 继续完整复制 Microsoft 官方 CPU 归档，三种目标来源统一为 1.28.2，不换成 sherpa 作者的另一套预编译包，不在 sherpa 资源内重复构建或下载运行库。新增其模块导入适配，供 sherpa 声明 `depends=["onnxruntime"]` 并传递唯一的 `autolib::onnxruntime`。升级配套更新版本、三平台 URL、SHA256 和查找元数据；已有来源、构建状态或安装内容先核对并保留备份，不能因旧 sentinel 存在而跳过升级。

sherpa 只通过模块局部补丁适配本仓库运行库位置，保留参考 Windows CPU 的 DLL/导入库定位修正；不扩展公共引擎。安装原生 C++ 头文件、必要依赖头文件和构建产物，按新版删除 espeak/piper 静态依赖并加入 utf8proc。`autolib::sherpa-onnx` 精确列举本次配置的静态库及系统链接要求，传递 sibling onnxruntime，不扫描任意库或照抄仍含 C API 的上游静态 `.pc` 链接清单。

第三批仍只在 109 通过普通根入口进行一次配置与编译安装，不新增测试或消费者编译。核对原生 C++ 头文件、TTS/说话人分离开关、静态库符号、C/JNI/Python/WASM 产物缺席、实际 onnxruntime 路径及共享库依赖，随后使用已有工具核对两包闭包与打包内容。不启动模型推理，也不把编译、符号或归档检查当成语音效果验收；Windows、Linux aarch64 和真实消费链接由用户 CI 验收。失败后先报告原因，取得确认再修复续跑。

### 接入与交付

配方字段、通用包资格及 macOS 验收的替代提案见[统一模块配方字段与 macOS 全包验收](2026-11-06-module-recipe-contract-and-macos-validation.md)，本记录的模块来源、功能范围及既有验证证据继续有效。

首批及第二批各库使用现有 `src/dependencies/<name>/module.toml` 和同目录 `autolib-import.cmake`。版本、归档摘要、库特定选项及补丁保持参考基线；Git 标签或分支解析为迁移时确认的完整提交，不顺带升级版本或改变功能开关。源码构建、头文件复制、源码子目录、平台覆盖和系统依赖沿用已有能力。第三批 sherpa-onnx 及其 onnxruntime 依赖不适用保留参考版本的约束，升级范围由上文拥有。

纯头文件 copy 模块可声明 `platform_independent=true`，通用包检查不绑定模块名、文件数量或文件名；支持主源码与附属资源的文件和目录复制，核对 include 下的实际文件集合及来源字节，拒绝额外载荷或非普通文件。平台无关性由配方维护者确认，字节核对不代替该判断。Clipper2 1.5.4 对 GCC 9 使用模块局部补丁：移除其不识别的 `-Wno-c++20-compat`，仅将 Clipper2Z 的 sign-conversion 保留为警告；其他告警仍按上游 `-Werror` 处理，不修改算法或关闭 Z 坐标库。

每包提供唯一的 `autolib::<name>` 公共目标，封装参考支持的上游组件及实际直接依赖，不回退系统同名包，不在 adapter 下载或构建。WebSocket++ 沿用默认 Boost 后端，声明 `websocketpp → boost → zlib` 闭包；Asio、cpp-httplib、Taskflow 传递系统 `Threads::Threads`。纯头文件包只有在内容确实平台无关时才使用通用槽位；通用头文件可声明平台包依赖，importer 按消费平台准备该依赖。沿用现有自动发现、打包和 CI 规则，不新增逐库发布编排。

### 执行与授权

实际配置、编译、安装和产物核对统一在 `lxx@192.168.0.109` 的 `/home/lxx/xxautolib` 执行。本机只编辑、检查和传输源码，不执行 C/C++ 或 Go 编译，也不运行会隐式编译工具或 fixture 的本地命令。该机器的安全信任及永久源码传输授权由[开发指南](../../../development.md#远端构建与源码传输授权)拥有。

传输前检查目标目录并保留已有源码修改、安装树和无关数据，不整体复制工作副本或清空目标。第三方输入沿用[先在 Windows 下载校验再传输](../../implemented/process/2026-10-19-repository-foundation.md#验证授权)的规则，不传本仓库 `.git/`、凭据或 Windows 构建产物。缺少系统开发包时先说明实际缺项，不靠关闭参考功能绕过。

书面设计确认后，在当前工作目录创建普通任务分支，将设计作为该分支首个本地提交；后续实现和验证留在同一分支，保留并排除已有无关修改。助手不推送、不触发远端 CI、不上传 Release；全部迁移完成后由用户手动推送并触发 CI 验收。

## 备选方案

**按少量代表库拆成多个小批次。** 常规配方迁移已能使用现有能力，反复进行小批次设计和验收不能带来相称收益；首批集中完成全部常规模块，复杂包留到后续阶段。

**为每库新增测试代码或测试工程。** 本批主要风险是来源、构建配置、安装布局和导入接线；按本轮约定，通过真实构建与产物核对验证，包括已确认的通用包校验调整，不扩建测试基础设施。公共回归、消费链接及跨平台缺口由用户后续 CI 验收。

## 验收标准

首批 16 库通过普通根入口在 109 机器完成实际构建安装；纯头文件库核对复制或安装结果。核对代表头文件、实际库文件、导出配置和必要运行文件，确认参考配置中的依赖与产物一致。不得只以 configure 成功或 sentinel 命中作为构建证据。

不新增测试代码或测试工程。109 整批编译失败后，经用户确认修复并在原工作目录增量续跑，不额外编译搬迁消费者、不逐库重复编译、不在其他机器复测。使用本次安装结果核对 adapter、依赖闭包和打包内容；公共回归、消费链接及三平台验证留给用户手动触发的 CI。保留简明构建与产物记录，未验证的平台和运行能力如实说明。

首批本地验收后再进入后续阶段。所有范围内模块完成后，向用户交付源码、实际验证结果及遗留限制；三平台 CI 的最终构建、发布和消费验收由用户手动触发，不以 109 机器的结果替代。

## 风险

参考实现可能包含旧安装路径、隐式系统依赖或平台专用步骤；只做完成迁移所需的接口适配。遇到必须改变参考功能、扩展公共引擎或偏离已确认方案的情况，暂停受影响模块并说明最小处理建议，不自行扩大范围。

实际编译及产物核对不覆盖全部运行功能，头文件安装也不证明所有模板或可选后端可用。109 机器的本地验证不代表其他平台通过；这些边界由实际记录和用户 CI 验收共同说明。

## 首批验证结果

109 为 Ubuntu 20.04 x86_64，使用 GCC 9.4、CMake 3.31.12。首批 16 库及 zlib 已通过普通根入口完成构建或复制安装；最终增量构建日志为 `/home/lxx/xxautolib/build/build-4.log`，退出码 0。此前失败记录保留，分别涉及通用包白名单、Clipper2 的 GCC 9 告警、Asio 未完成消费描述及未传输的 Wayland 子模块；处理后按用户确认续跑，未清空工作目录或关闭参考功能。

安装核对覆盖代表头文件、实际库、精确 Config 和 pkg-config 路径。Clipper2 普通及 Z 静态库均存在；PortAudio 保留静动态库，libzmq 保留静动态库及共享库链接，二者在 109 的动态依赖均可解析。SDL3 保留静态库及 OpenGL/OpenGLES、X11、Wayland 配置。Boost cache 的 zlib include 与 Release 库均指向本仓库 `3rdparty/zlib`，iostreams 导出依赖为 `ZLIB::ZLIB`。系统 GTK、Wayland、ALSA 及 C/C++ 运行库仍由消费环境满足，不承诺静态自包含。

本轮 builder 最终打包退出码为 0，日志为 `build/stage1-package-final.log`，输出在 `/home/lxx/xxautolib/build/stage1-release-final/`：17 份 tar.zst 和 Linux x86_64 importer。全部归档通过 Zstd 完整性检查，包头与 Boost 的 zlib 依赖已核对；asio、cpp-httplib、expected、taskflow、websocketpp 使用 any/any，其余为 linux/x86_64。归档保留动态库文件及链接，pkg-config 的生产前缀在 staging 中修正，importer 与安装工具字节一致。

依赖接线补齐后，既有 builder 已刷新消费描述并重新打包，没有重编第三方库。importer 对本地安装的 WebSocket++ 请求实际返回 `zlib;boost;websocketpp` 准备顺序；最终归档包含 Boost 依赖头及三个头文件库的 Threads 接线。该核对不执行 CMake adapter，也不证明消费者编译链接通过。

首批使用 Windows 校验后预传的第三方来源，不验证空缓存自动取源。未在本机编译，未新增或运行测试工程，未编译消费者，未验证 Windows、Linux aarch64、跨平台 ABI 或完整运行功能。公共 Go 回归、adapter 实际加载链接及搬迁验证留给用户手动触发 CI；助手未推送或触发 CI。

## 第二批验证结果

oneTBB、imgui、pcl 在同一台 109 通过普通根入口完成一次整批配置、编译安装，两者退出码均为 0；日志为 `/home/lxx/xxautolib/build/stage2-configure.log` 与 `build/stage2-build.log`，退出码另存同名 `.exit` 文件。oneTBB 的系统前置包为 libhwloc-dev 2.1.0；参考 yum 声明不迁入，当前引擎只支持 apt/choco，本批不扩展 Fedora 自动安装能力。

oneTBB 安装共享 tbb、tbbmalloc、tbbmalloc_proxy 及 tbbbind_2_0，保留严格告警与 IPO；绑定库的动态依赖解析到系统 libhwloc.so.15。代理需要运行环境能找到同包的 libtbbmalloc.so.2；在本包 lib 加入 LD_LIBRARY_PATH 后依赖解析完整。Windows HWLOC 2.12.2 归档摘要与 include/lib/bin 路径已静态核对，DLL 由模块局部安装补丁复制，不扩展公共引擎。

imgui 的静态归档包含核心及 SDL3/OpenGL3 后端对象，两个后端头文件按参考平铺到 `include/ImGui`；五项 imconfig 修改均在安装头中生效，SDL3 头文件与静态库复制保留。adapter 传递 sibling SDL3，不重复链接复制的 SDL3 库，并保留系统 OpenGL、动态加载及线程要求。

PCL 的十一项消费组件静态库、公开头和 Config 均已核对；Linux Config 实装为 `share/pcl-1.15/PCLConfig.cmake`，据此修正参考的 Windows 专用 `cmake` 路径，Windows 布局不变。cache 和编译输入中的 lz4、flann、boost、eigen、zlib 均指向本仓库 `3rdparty`，FLANN 保持共享，OpenMP 保持开启；静态 PCL 不等于系统依赖自包含。

既有 importer 对三个根包返回九包准备闭包，未执行 CMake adapter。builder 打包退出码为 0，日志 `build/stage2-package.log`，输出 `/home/lxx/xxautolib/build/stage2-release/` 含九份 linux-x86_64 归档及宿主 importer。全部归档通过 Zstd 完整性检查；包头、代表静动态库、链接和后端头齐全，PCL Config 的 FLANN 生产前缀在 staging 中改为 sibling 相对位置，importer 与安装工具字节一致。

本阶段不新增测试、不在本机编译、不编译消费者，未验证 adapter 实际加载链接、搬迁运行、Windows 或 Linux aarch64；系统 HWLOC、OpenGL、OpenMP 与 C/C++ 运行环境仍由消费方满足，三平台验收留给用户手动触发 CI。助手未推送、发布或触发 CI。sherpa-onnx 尚未迁入，本记录保留 proposed 状态。

## 第三批暂停状态

109 的第三批根配置退出码为 0，首次构建退出码为 2，日志为 `build/stage3-configure.log` 与 `build/stage3-build.log`。失败发生在 sherpa 子配置阶段：新增许可证安装规则把 json、kissfft 的 `LICENSES` 目录交给 `install(FILES)`，尚未进入 sherpa C++ 编译。该额外安装规则不属于必要迁移适配，恢复迁入时应撤回，不扩修许可证搜集机制。

sherpa 配方、补丁、导入适配及 onnxruntime 升级均未完成安装、消费和打包验收，不计入前两批成果。五个草稿文件已完整备份至本地忽略目录 `.superpowers/sherpa-paused-draft/`，移出模块发现范围；onnxruntime 配方恢复为分支已有的 1.24.4，不新增其导入适配。该本地备份不随 Git 交付，后续恢复前须核对草稿与正式适配基线，不直接视为可发布模块。109 的源码、安装和构建数据保持原样，不作为恢复后版本已就绪的依据。
