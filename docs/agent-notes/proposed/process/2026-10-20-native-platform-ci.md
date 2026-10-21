# Agent Note: 三平台原生 CI 与发布环境方向

Status: proposed

## 问题

本地双平台安装验证不能替代三平台二进制发布，也没有定义发布环境 ABI。按依赖层分别启动 job、重复准备环境和传输完整包，会增加时间及存储；合并 job 又会改变重跑边界与资源占用，需要保留已有分层和失败语义。

## 方案

沿用三平台原生单 job 的既定方向，不将其写成已实现能力。参考流水线的现行 Linux 环境是 manylinux 2.28，并维护 APT/YUM/Chocolatey 元数据；Ubuntu 20.04、APT-only、FIFO 并行和通用头文件单槽位属于待实施方案。本仓库尚无 CI，按[路线图](../../../roadmap.md)分批接入，不先复制全部过渡结构。

### 原生分层与并行

Windows x86_64、Linux x86_64 和 Linux aarch64 各使用一个原生构建 job，平台间可以并行。不使用交叉编译、Zig 或 QEMU。Linux 每个平台共用一个 Actions 原生 container job；Windows 保留 Visual Studio/MSBuild，不传 Linux Make 参数。规划和最终汇总可保留独立辅助 job。

每平台按具名步骤执行环境准备、依赖层、OpenCV、后续层、消费验证和汇总；OpenCV 与后续层顺序执行，不增加后台调度器。空层可跳过，非空前置层失败时不继续依赖构建或报告成功。后续实际模块较少时，不为形式完整创建空层。

环境和该计划所需系统依赖集中准备一次；后续层复用本 job 已校验的安装产物、归档和元数据。每个成功层保留固定槽 Release 发布能力，让失败后重跑可复用成功产物，不另存步骤完成数据库。只在获得发布授权时启用写操作，分支限制遵循[二进制交付决定](../architecture/2026-10-20-name-only-artifact-delivery.md)。

不上传供层间传输的完整包 Actions artifacts，正式产物与固定槽缓存使用 Release；独立规划/finalize 实际需要的小型元数据仍可用 Actions artifacts。单 job 不能单独重跑某一步，接受重跑时重新准备环境的成本。

### 环境与 ABI

| 对象 | 沿用的目标 | 边界 |
| --- | --- | --- |
| Linux 基础环境 | Docker Official Image Ubuntu 20.04，GCC/G++ 9.4 来自官方 APT | glibc 2.31；不加 Toolchain PPA，不逐包换编译器；实际镜像 digest 须覆盖两架构。 |
| Linux 运行库 | Ubuntu 官方配套 libstdc++ 与 libgcc，动态链接 | 不要求运行库包版本号等于 GCC，不静态捆绑运行库或切换旧字符串 ABI。 |
| Linux 并行 | GNU Make 4.4.1 FIFO jobserver | 公共入口传 `--jobserver-style=fifo` 并统一预算，父子用同一 Make，不逐包追加独立 -j。 |
| CI CMake | Kitware 官方 CMake 3.31.12，显式固定补丁版 | 版本更新须修改配置并验证；项目最低版本仍为 3.20。 |
| Windows | windows-2022 原生 VS 2022 x64、Release、普通模块默认 v143 | 保留完整父生成器身份和显式 CRT；不全局降为 v142，不承诺 runner 的 MSVC/SDK 补丁固定。 |

工具从官方来源取得并校验，版本更新须显式修改，不动态选择 latest。Python 环境和执行仍统一由根 uv 环境及锁文件管理。Linux 系统依赖只维护 APT，Windows 保留 Chocolatey；不为只支持一个 Linux 发行版的目标复制 YUM/DNF 准备。各模块声明自己的系统依赖，不靠其他模块缓存未命中时偶然安装。

后续公共配置沿用 `CMAKE_INSTALL_LIBDIR=lib` 与 `CMAKE_POLICY_VERSION_MINIMUM=3.5`，后者只提供低版本 policy 下限，不覆盖上游更高要求。policy 已由普通根入口传入子项目，机制见[模块扩展决定](../architecture/2026-10-20-module-expansion.md)；固定安装库目录仍待接入。Linux 源码构建产物的 glibc 需求不得高于 2.31；C++ 运行需求独立记录 GLIBCXX、CXXABI、libgcc 符号，不用 glibc 版本代替 C++ ABI 判断。静态归档须经实际下游链接验证，预编译输入单独记录兼容要求，不能因默认 GCC 9.4 就视为同基线源码产物。

正式构建记录工具版本、来源和实际发行版包修订，Windows 记录 runner image、cl、MSBuild、SDK 与 CMake 实际版本。容器内只对实际 checkout 设置必要的 Git 信任，不修改用户机器的全局信任列表。镜像更换不等于现有 build ID 自动失效；新仓库不复用旧发布集合，不为此预建通用工具链状态管理。

GCC 9.4 是配置决定，不是全部模块已成功的声明；不额外全包试编译来重新决定选型。正常 CI 失败如实报告，不自动升级编译器。Ubuntu 20.04 的工具来源可信不等于仍有免费安全维护，落地时须明确镜像及补丁来源；任何改变基线的方案须另行确认。

### 平台无关头文件单槽位

只对显式声明且安装树及导出配置均不含平台相关内容的包启用通用槽，不以“没有 DLL/so”推断。manifest 使用成对的 `os=any`、`arch=any`，固定资产名为 `<name>-any-any.tar.zst`；不支持半通用槽，同包不得同时有通用和平台限定槽，不增加回退优先级。

规划在本次选中平台中按 Linux x86_64、Linux aarch64、Windows x86_64 顺序唯一选择 publisher。其他平台继续正常消费验证，可使用既有缓存或本地产物，不上传、不等待 publisher，也不在失败后自动接管；publisher 失败则该包发布失败。切换 publisher 不改变包身份。

规划、内部 build ID、包内导入、manifest、缓存、镜像和测试须同步支持通用身份；机器码包继续严格匹配 OS/架构。参考代表为 Eigen，不因此自动扩大发布模块集合。新仓库从空发布集合开始，不迁移旧发布资产，不新增历史槽位兼容或自动远端清理。

## 备选方案

**每层独立 job。** 可以单独重跑并跨 runner 并行部分层，但重复环境准备和整包传输；选择单平台共享环境，保留具名步骤。

**交叉编译或预制第三方构建镜像。** 前者增加目标工具链与运行验证，后者增加镜像发布及补丁维护；采用原生 runner 和官方小镜像现场准备，实际瓶颈出现后再评估。

**升级 CMake 4 或逐包追加 -j。** 前者提高最低要求，后者可能超额并行；沿用公共 FIFO jobserver 方案。

**三个头文件平台记录共用一个资产。** 仍维护重复摘要和身份映射；显式 any/any 单槽直接表达范围，机器码包不放松检查。

## 验收标准

- 获得远端授权后，三平台正常流水线各在原生单 job 完成所需分层、打包及真实消费；准备只执行一次，空层和前置失败不误报。
- 缓存全命中、部分缺失及重跑保留完整性检查和成功层发布复用，不通过 Actions artifacts 传完整包。
- Linux 嵌套构建有实际并行 worker 证据且无 jobserver 单线程回退；同一公共预算覆盖父子 Make，不靠逐包命令修改。
- 正常发布记录工具与符号需求，源码与预编译覆盖分开报告；Windows 工具链和 CRT 实际一致，v142 专项仍由人工实机验收。
- 通用包在多平台及单平台请求中只有唯一 publisher 和一个 any/any 槽；三平台正常消费成功，混合身份、槽共存和机器码错平台均拒绝。
- 在一致模块与缓存条件下报告环境准备、平台/整体墙钟、累计 runner 时间和磁盘峰值，不承诺单 job 必然更快。

## 风险

合并 job 减少跨 runner 并行，可能增加整体墙钟；同 job 构建与产物长期共存会提高磁盘峰值，不能提前删除后续层仍需内容。运行环境、下载源和 hosted runner 会变化，本记录不把历史局部实验当作三平台全量验证，也不以提前增加自动恢复或自托管 runner 消除此限制。
