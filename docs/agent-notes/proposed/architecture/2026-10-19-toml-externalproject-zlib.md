# Agent Note: 通用流水线引擎与 zlib、queue 接入

Status: proposed

## 问题

项目需要按模块定制依赖库的获取、源码修改、编译选项和安装流程。例如 OpenCV 需要选择模块，zlib 需要通过修改上游构建定义，只编译和安装静态库。模块增加后，维护者应能在一个库目录内找到该库的声明、补丁和专用文件，而不必跨目录追踪生成代码。

把 CMake 声明嵌入 Python 字符串，或让每个 recipe 自行计算路径、判断平台、拼接命令，会混合模块数据与通用生成逻辑。仅把这些代码移入模板或结构化 Python 对象，不能保证模块定义保持纯声明。另一方面，ExternalProject 已能执行和跟踪构建步骤，不需要 Python 再实现一套执行器。

通用引擎需要同时覆盖真实编译和只安装文件两类流程。zlib 可以验证固定源码、静态专用补丁和 CMake 编译安装；queue 可以验证没有主来源、同时获取两个附属资源并复制安装。只接入 zlib 不能证明 copy 类型和多资源流程可用。

## 方案

采用两层架构：TOML 模块定义只保存声明数据，通用流水线引擎读取定义并生成 CMake。生成结果使用原生 `ExternalProject_Add` 和 `ExternalProject_Add_Step`，由 ExternalProject 在构建时执行获取、修改、配置、编译和安装。

本记录是待实现架构，不表示当前入口已经支持 TOML。基础设施和设备授权由[仓库基础设施决定](../../implemented/process/2026-10-19-repository-foundation.md)拥有；两份记录分别约束基础设施与模块构建，不相互替代。实现后更新当前架构、开发和测试文档，不把未来能力提前写成已有能力。

### 职责与执行流程

```text
CMake configure
  → 公共入口准备根目录 uv 环境，传入明确的构建上下文
  → Python 通用引擎读取选中的 TOML，生成 CMake 及输入依赖列表
  → CMake 登记 configure 依赖，包含生成文件，建立目标
CMake build
  → ExternalProject 执行声明的步骤与步骤依赖
  → 安装到 3rdparty/<模块名>/
```

根 CMake 拥有模块选择、构建环境和生成结果的加载。Python 引擎拥有 TOML 解析、公共路径解析、上下文选择、参数编码和 CMake 生成。TOML 拥有模块名称、固定来源、附属资源、配置选项、文件引用、安装映射及自定义步骤。ExternalProject 拥有执行顺序、步骤完成记录和增量执行。

TOML 不包含 Python 函数、条件表达式、路径计算或 CMake 生成逻辑。平台差异可以声明为数据，由引擎根据 CMake 提供的上下文选择。引擎不包含按 `zlib`、`opencv` 等模块名分支的专用逻辑，不执行下载或构建，不建立独立状态数据库、插件系统或任意表达式语言。Python 使用标准库，不为 TOML 解析引入额外依赖。

纯声明不禁止描述命令或引用脚本。确实需要执行的模块专用逻辑放在同目录的补丁、CMakeLists 或执行脚本中，由 TOML 声明其引用及执行位置；不把它改写成通用引擎中的特例，也不构建 TOML 版 CMake 编程语言。

### 目录与维护入口

源码沿用基础设施确定的按维护对象布局：

```text
src/
  build_system/          # 通用入口、解析与 CMake 生成
    index.cmake          # 调用生成器、登记输入依赖、加载生成结果
    generate.py          # Python 入口：接收上下文并组织生成
    definition.py        # TOML 读取、字段校验和平台数据选择
    cmake.py             # 通用 CMake 生成与参数编码
    apply_patch.cmake    # 公共补丁驱动，由 ExternalProject 调用
  dependencies/
    zlib/
      module.toml        # 模块声明入口
      ...                # 本模块补丁及专用执行文件
    queue/
      module.toml        # 两个资源与头文件安装映射
tests/
  build_system/          # 通用引擎与入口验证
  zlib/                  # 本模块补丁及安装产物验证
  queue/                 # 资源获取与安装头文件验证
build/
  generated/
    zlib.cmake           # 生成结果，不手工维护
    queue.cmake
3rdparty/
  zlib/                  # 安装结果
  queue/
```

库专用文件集中在 `src/dependencies/<模块名>/`。补丁较多时可在该目录内分类，不另建顶层补丁或 recipe 目录。ImGui 专用 `CMakeLists.txt` 等文件也归对应模块目录；模块专用测试放在对应测试目录，不移入公共引擎。

`src/build_system/` 是通用流水线引擎的源码目录。index.cmake 只连接 CMake 与 Python；generate.py 组织一次生成；definition.py 解释声明数据；cmake.py 输出原生构建声明；apply_patch.cmake 在构建阶段调用已探测的 Git 检查和应用补丁。这些文件不保存任何模块专用配置或模块名分支。

`zlib.cmake` 仍存在，但它是 `build/generated/` 中可检查的生成结果，不是需要手写维护的模块源文件。日常调整 zlib 时修改 TOML 或同目录专用文件；排查生成结果时查看生成的 CMake。生成内容直接展示 ExternalProject 参数与步骤，不以多层 helper 隐藏实际行为。

每份工作副本统一使用根 `.venv/`、`build/` 和 `3rdparty/`。Python 环境准备、生成器、测试及开发脚本全部经 uv 执行，使用受跟踪的 `.python-version`、`pyproject.toml` 与 `uv.lock`，不创建 build 专属环境，不回退到系统 Python 或 pip。构建中间产物放在 `build/`，不提供多构建目录管理或交叉编译支持，同一个 build 不混用 Windows 与 Linux 产物。

ExternalProject 使用紧凑的显式目录：`PREFIX` 为 `build/ep/<模块名>/`，其下 `src/`、`bin/`、`stamp/`、`tmp/`、`dl/` 分别对应 SOURCE_DIR、BINARY_DIR、STAMP_DIR、TMP_DIR、DOWNLOAD_DIR。最终安装仍使用 `3rdparty/<模块名>/`，生成声明仍使用 `build/generated/`。此布局减少路径嵌套，不保证任意深度工作区都能构建；Windows 长路径能力仍取决于系统及工具，不自动修改系统设置。

附属资源使用模块内的独立工作目录，例如 `build/ep/queue/res/readerwriterqueue/`，不与主来源或其他资源共用源码、stamp 和临时文件。资源目录与内部目标名由模块名及资源名确定，不能因不同模块使用相同资源名而冲突。

### 根入口与模块发现

根 `CMakeLists.txt` 以现有参考骨架为基线，保留编译设置、环境摘要、`BUILD_LIST`、默认目录发现及模块去重的组织，仅为 TOML 模块发现、生成与加载作必要适配，不顺手重构。最低 CMake 版本维持根入口的 3.20，不因采用 Python 或 TOML 自动提高版本。

未指定 `BUILD_LIST` 时发现 `src/dependencies/<模块名>/module.toml`；指定时沿用逗号分隔模块名的入口，只选择指定模块。缺少声明时失败，重复模块不重复建立目标。configure 阶段通过公共入口运行一次 Python，生成本次选择所需的声明，再包含生成结果；不为每个字段或 helper 单独启动进程。

模块间依赖与附属资源不同。未来接入有依赖的库时，依赖目标的顺序及安装前缀、包目录等内部构建参数需要正确接线，不能只排目标顺序；这不属于被排除的下游项目接入。zlib 与 queue 之间没有依赖，首批不实现模块间依赖闭包或版本求解；queue 内部的两个资源依赖属于本批范围。

首批不预留只能为空的 `dependencies` 字段；解析器遇到它或其他未知字段时明确报错，不静默忽略。第一个有模块依赖的库接入时，再定义可选依赖字段及其构建接线规则，不要求无依赖模块增加空字段。

### 模块声明与注释

模块顶层使用 `name`、`type` 和可选的 `version`。name 与模块目录名一致，type 为 cmake 或 copy；version 是供维护者阅读的版本标识，不代替来源中的固定提交。包含不同上游的聚合模块不要求编造统一版本号。

主来源放在 `[source]`，附属资源放在 `[resources.<资源名>]`；Git 来源使用原生 `GIT_REPOSITORY`、`GIT_TAG` 字段，首批三个上游均固定完整提交。Python 只把来源声明生成为 ExternalProject 参数，不自行获取源码。固定提交不启用不适用的浅克隆，也不引入自动跟随分支的更新策略。

文件补丁使用 `[[patches]]` 的 `file` 字段，路径相对于 module.toml 所在目录。引擎将文件列表生成原生 PATCH_COMMAND，按声明顺序执行应用检查、应用和反向检查，文件内容作为步骤输入。其他时机或其他工具的源码修改仍使用原生 steps，不受文件补丁格式限制。

copy 类型以 `[[copy]]` 声明安装映射：resource 指定本模块的附属资源，files 是相对于该资源源码目录的文件列表，destination 是相对于模块安装前缀的目标目录。未指定 resource 时使用主来源；两者都不存在或文件不存在时失败。首批按明确文件列表复制，不增加通配匹配、目录同步或删除目标文件的语义。

所有库的 module.toml 必须有中文维护注释。文件开头说明用途和交付类型；固定版本、非默认选项、补丁及自定义步骤说明需要维护者知道的约束或理由。使用 TOML 的 `#` 注释，不要求逐行复述字段名，不增加注释解析机制。公共的生成器、CRT、Release 和路径策略不在每库重复配置。

### 平台差异的数据格式

公共 CMake cache 选项放在 `[cmake.cache]`，公共自定义步骤放在 `[[steps]]`。平台差异只使用固定的 `platforms.windows`、`platforms.linux` 表，由 CMake 传入的 CMAKE_SYSTEM_NAME 分别以 Windows、Linux 精确选择，不从宿主环境猜测，不支持条件表达式、通配匹配或 targets 过滤语法。

本批平台表支持 `cmake.cache` 与 `steps`：选中平台的 cache 按键覆盖公共同名值，其余公共值保留；平台 steps 追加到公共 steps 列表，但步骤执行次序仍只由原生依赖字段决定。未选中平台的配置不参与生成，未知平台键报错。不提供任意字段的通用递归合并规则。

以下示例说明声明格式，不表示接口已实现或命令已验证：

```toml
[platforms.linux.cmake.cache]
# Linux 静态库使用位置无关代码。
CMAKE_POSITION_INDEPENDENT_CODE = true

# 此步骤仅用于展示平台选择，不是库的必要构建操作。
[[platforms.linux.steps]]
name = "linux_notice"
DEPENDEES = ["patch"]
DEPENDERS = ["configure"]
COMMAND = ["cmake", "-E", "echo", "Linux-only step"]
```

该步骤仅在 Linux 生成，并直接位于 patch 与 configure 的依赖链中；平台选择不改变 DEPENDEES、DEPENDERS 的含义。

### 生成类型与附属资源

通用引擎区分两种生成类型：`cmake` 负责常规 CMake 配置、编译和安装；`copy` 负责获取输入并按映射复制安装，不执行配置或编译。头文件库和预编译包可以使用 copy，不为每个库新增类型。

模块可以声明主来源及多个附属资源。资源分别声明固定来源、版本或哈希和独立目录，并在实际使用它的步骤之前准备好。OpenCV contrib、oneTBB 所需的 HWLOC 属于编译输入，不要求拆成独立公开库模块。

copy 模块允许没有主来源。queue 保持一个模块和一个对外目标，把 readerwriterqueue 与 concurrentqueue 作为两个附属资源，分别复制所需文件到同一安装前缀，不拆成两个库模块。辅助资源的内部获取目标由引擎按实际执行需要生成，不改变模块的维护归属。

首批实现通用引擎的 TOML 解析、cmake 与 copy 生成、附属资源获取接线、文件补丁、原生自定义步骤、平台数据选择、参数编码及输入依赖登记，并分别通过 zlib 与 queue 验证。不仅生成两个模块的固定文本，也不追求完整封装 ExternalProject 的全部 API。OpenCV、oneTBB 等其他模块不在本批接入范围。

无主来源的 copy 模块显式禁用主目标的获取、configure 和 build 命令，安装步骤仅执行声明的复制。资源准备由内部 ExternalProject 目标执行，原生步骤依赖保证使用资源前准备完成；queue 的 install 依赖两个资源均已就绪。Python 不提前复制或下载，也不以 configure 时资源尚未存在为错误。

### 原生步骤与源码修改

自定义步骤直接沿用 `ExternalProject_Add_Step` 的字段和依赖语义，以 [CMake 3.20 ExternalProject 文档](https://cmake.org/cmake/help/v3.20/module/ExternalProject.html#explicit-step-management)为准。TOML 声明步骤名、`DEPENDEES`、`DEPENDERS` 和 `COMMAND`，需要文件依赖或工作目录时使用原生 `DEPENDS`、`WORKING_DIRECTORY`，不改名为另一套阶段接口。

引擎不提供 before/after 包装，不把相邻阶段之间拆成两个位置。对 mkdir、download、update、patch、configure、build、install、test 等实际存在的步骤，通过依赖字段直接指定自定义步骤的位置。test 未启用时不人为添加空 test 步骤。自定义步骤的依赖图按声明生成，不按 TOML 出现顺序暗自串联。

以下示例说明拟采用的模块声明格式，不表示接口已实现或命令已验证：

```toml
[[steps]]
# Windows 下修正安装所用的 pkg-config 库名。
name = "fix_pkgconfig_libs"
DEPENDEES = ["build"]
DEPENDERS = ["install"]
COMMAND = [
    "bash", "-c",
    "sed -i 's|-lportaudio|-lportaudio_x64|g' \"<BINARY_DIR>/portaudio-2.0.pc\"",
]
```

`COMMAND` 保存程序及参数，不默认拼成 shell 字符串；需要 shell 时显式调用 `bash -c`。一个步骤需要多条命令时，以参数数组的有序列表表示，生成重复的原生 COMMAND；单命令数组保留上例写法。命令内的 `<SOURCE_DIR>`、`<BINARY_DIR>`、`<INSTALL_DIR>` 沿用 ExternalProject 占位符。

补丁是用途，不是文件格式要求。可以声明 `.patch` 文件，也可以保留 sed 命令、`git apply`、`cmake -P` 或模块专用脚本。源码 patch 阶段可使用原生 `PATCH_COMMAND`，其他时机的修改使用原生自定义步骤，不强制将所有 sed 转换为 patch 文件。

工具依赖必须与命令一致。zlib 补丁使用入口探测到的 Git，不假设 Windows 自带 GNU patch；声明 bash/sed 的模块需要相应工具，不假设纯 CMD 或 PowerShell 环境自动具备它们，也不为此增加工具安装器。补丁文件引用与自定义命令不是同一保证：zlib 明确检查可应用、已应用和源码不匹配，任意 sed 命令不会自动获得相同检查能力。

### 配置身份与 Release

子项目强制沿用父项目的生成器，以及适用且非空的平台、完整工具集和 Visual Studio instance。通过 ExternalProject 的原生 CMake 生成器选项传递，不依靠父 cache 的隐式继承，不擅自切换 Ninja 或平台默认工具集。

父项目选择 `v142,host=x64` 时，子项目必须保留完整值，不能截断后缀或改用默认 v143；指定工具集不可用时应失败。v142 实机一致性留待后续人工验收，不安装工具集、不阻塞本批开发，也不以 v143 成功替代它。

本批构建和安装固定 Release。单配置生成器在子项目 configure 时设置 `CMAKE_BUILD_TYPE=Release`；Visual Studio 等多配置生成器在 build 和 install 时显式选择 `--config Release`，不能仅设置 CMAKE_BUILD_TYPE 便认为已经选定配置。

MSVC CRT 与库本身的静态或动态类型分别选择，静态 zlib 不意味着必须使用静态 CRT。父项目显式设置 `CMAKE_MSVC_RUNTIME_LIBRARY` 时，按原值传给子项目与内部安装验证工程，不用模块专用设置覆盖；未设置时沿用 CMake 默认选择，本批 Release 对应 `/MD`，不自动改为 `/MT`。运行时选择要求子项目启用 CMP0091 NEW；[zlib v1.3.1 的上游定义](https://raw.githubusercontent.com/madler/zlib/v1.3.1/CMakeLists.txt)已声明 policy 上限 3.15，无需为该 policy 额外修改上游文件。传递规则以 [CMake 运行时选择文档](https://cmake.org/cmake/help/v3.20/variable/CMAKE_MSVC_RUNTIME_LIBRARY.html)为依据，验收核对实际编译设置而不只看传入的 cache 值。

### 参数编码、输入依赖与失败

引擎按单个参数生成 CMake，集中处理空格、反斜杠、引号、分号和字面变量字符，不使用未经转义的字符串拼接。带空格的单个路径与包含多个元素的 CMake 列表分开处理；`LIST_SEPARATOR` 用于需要传递列表的参数，不把它视为解决所有空格问题的开关。

生成器同时输出实际读取的 Python 源文件和 TOML 输入列表，供根入口以 `CMAKE_CONFIGURE_DEPENDS` 显式登记；不能只登记启动脚本而漏掉 helper。CMake 包含生成的 `.cmake`。补丁或专用脚本作为执行输入登记到相应步骤的文件依赖，必要时触发对应步骤，不以重新生成一份相同文本代替实际失效处理。

对执行输入使用原生文件依赖，对阶段先后使用原生步骤依赖。例如一个源码修改步骤声明 DEPENDEES patch、DEPENDERS configure 及 DEPENDS 输入文件后，输入变化使其 stamp 失效，并沿 configure、build、install 的原生依赖链触发后续步骤。不能只声明 DEPENDEES 或目标级顺序便认为后续文件依赖已经完整，也不增加独立级联失效机制。

输入内容变化后应重新生成；引擎在内存中生成完整内容，直接与目标文件内容比较，只有不同才写入。内容相同时保持生成文件时间戳不变，不强制采用 hash 比较或临时文件替换。此要求避免无意义的时间戳变化干扰增量构建，不把每次覆盖写入等同于必然发生 configure 循环；算法由引擎与测试拥有，不作为开发指南中的用户操作。

uv 环境、锁文件、TOML 解析或 Python 生成失败时，configure 非零退出并报告失败对象，不继续包含旧的生成结果。获取和构建失败由 ExternalProject 与构建工具报告，引擎不自行重试，不以头文件或安装目录存在判定构建已完成。

### zlib 声明与验证

zlib 验证本地获取、修改、配置、编译、安装及安装结果验证的闭环。使用 `https://github.com/madler/zlib.git` 的 v1.3.1，固定完整提交 `51b7f2abdade71cd9bb0e7a373ef2610ec6f9daf`，不跟随浮动标签或分支，不为固定提交开启不适用的浅克隆。

以下为本批的 zlib 模块定义，表示拟实现接口，不是构建成功证据：

```toml
# zlib：通用无损压缩库，只编译和安装静态库。
name = "zlib"
type = "cmake"
version = "1.3.1"

# 固定 v1.3.1 对应提交；升级时须检查静态专用补丁。
[source]
GIT_REPOSITORY = "https://github.com/madler/zlib.git"
GIT_TAG = "51b7f2abdade71cd9bb0e7a373ef2610ec6f9daf"

[cmake.cache]
# 使静态库可以被链接进共享库。
CMAKE_POSITION_INDEPENDENT_CODE = true
# 只构建库，不构建上游示例程序。
ZLIB_BUILD_EXAMPLES = false

# 移除动态库目标及其安装规则，不能仅依靠 BUILD_SHARED_LIBS=OFF。
[[patches]]
file = "zlib-v1.3.1-static-only.patch"
```

保留静态专用补丁的有效内容，移除动态库目标及相关安装定义，不仅传入 `BUILD_SHARED_LIBS=OFF`。保留必要许可证与归属，不借补丁重写上游 CMake。禁用示例，启用位置无关代码，最终安装到源码根的 `3rdparty/zlib/`。

补丁驱动先检查能否应用，成功后应用；不能应用时进行反向检查，反向检查成功表示已应用，两者均失败则报告源码不匹配。重复运行不重复破坏源码。

文件变化触发补丁步骤，不代表任意新补丁都能替换已应用的旧补丁。上述检查只保证同一补丁的重复执行；新补丁与已修改源码冲突时必须明确失败，并提示需人工重新准备该模块的干净源码。不自动逆向旧补丁、回滚源码或清空构建目录，不把源码恢复机制纳入本批。

### queue 声明与验证

queue 是无主来源的 header-only 聚合模块，获取两个固定资源并安装六个头文件到 `3rdparty/queue/include/queue/`。模块本身不配置或编译上游工程；仓库内部验证程序会编译和运行安装头文件，以证明安装结果完整可用。两者不混淆为 queue 模块需要 build 步骤。

资源固定为 readerwriterqueue 提交 `6867b56452352acf077fccd5f6cc7e3a8cfde0fb` 和 concurrentqueue 提交 `683b9e31ea15eb69f1b81cc1defc7850d5f20b71`。这两个值经只读上游 refs 查询核对，不在实际构建时重新解析 master。

以下声明表示拟实现接口，资源版本彼此独立，不增加聚合版本号：

```toml
# queue：组合 SPSC 与 MPMC 队列，仅安装头文件。
# 无主来源；两个上游作为本模块的附属资源。
name = "queue"
type = "copy"

# SPSC 队列；固定提交，升级时检查头文件清单。
[resources.readerwriterqueue]
GIT_REPOSITORY = "https://github.com/cameron314/readerwriterqueue.git"
GIT_TAG = "6867b56452352acf077fccd5f6cc7e3a8cfde0fb"

# MPMC 队列；与 SPSC 资源独立获取和固定版本。
[resources.concurrentqueue]
GIT_REPOSITORY = "https://github.com/cameron314/concurrentqueue.git"
GIT_TAG = "683b9e31ea15eb69f1b81cc1defc7850d5f20b71"

# 安装 SPSC 公开头文件及其同目录依赖，保留相对 include 关系。
[[copy]]
resource = "readerwriterqueue"
files = [
    "atomicops.h",
    "readerwriterqueue.h",
    "readerwritercircularbuffer.h",
]
destination = "include/queue"

# 安装 MPMC 与阻塞队列头文件及其信号量依赖。
[[copy]]
resource = "concurrentqueue"
files = [
    "concurrentqueue.h",
    "lightweightsemaphore.h",
    "blockingconcurrentqueue.h",
]
destination = "include/queue"
```

queue 目标完成意味着两个资源均已准备好且所有映射文件安装完成；不能只创建一个没有正确依赖的聚合目标。内部 smoke 验证仅使用安装前缀的 include 路径，覆盖两组头文件的编译及简单入队、出队结果；检查六个文件齐全，不扩展为上游队列并发正确性或性能测试。

### 本批交付范围

本批是通用引擎、zlib 与 queue 的一个完整验收范围，两种类型及资源流程均通过后交回验收，不把 copy 或 queue 留作下一批。BUILD_LIST 可以分别选择 zlib、queue 或二者；默认发现两者，不因选择其中一个而获取或安装另一个。

本批不提供下游接入脚本、公开 Imported Target、包发现适配器、打包发布或 CI。安装结果验证是仓库内部测试，zlib 使用安装树的头文件和静态库绝对路径，不使用无路径库名或 `-lz`，不允许系统 zlib 替代本次安装；queue 使用安装头文件，不从源码目录或系统头文件补齐缺失输入。未来下游导入文件可与模块同目录维护，但不因此纳入本批实现。README 暂不创建，已验证能力更新到现有架构、开发和测试文档。

除 zlib 与 queue 外不迁移其他库；FastDeploy、nndeploy 不在初版范围内。不实现硬件自动探测、后端自动选择、交叉编译、源码或二进制缓存框架、旧配置兼容、自动恢复或独立构建状态数据库。

### 测试边界与后续模块接入

测试对象是本仓库的生成、构建和安装流程，不是上游库的算法、并发正确性或性能。通用引擎测试集中在 `tests/build_system/`，覆盖声明校验、参数编码、配置传递、原生步骤依赖和增量触发，使用小型隔离 fixture，不为每个库重复一套。

本批 zlib 与 queue 是两条真实集成路径。zlib 的最小链接运行验证检查安装的静态库与头文件能够一起使用；queue 的最小编译运行验证检查两个资源的头文件安装完整，不需要源码或系统路径补齐。压缩解压和入队出队仅用于确认本次安装产物可用，不扩展成上游功能测试。

后续新增模块沿用已有能力时，执行构建安装并核对预期产物，不强制新建专用测试程序或 `tests/<模块名>/`。模块修改了上游构建或安装规则时，针对修改结果补必要检查；新增通用引擎能力时，在公共测试中增加回归验证。目录中的 `tests/zlib/` 与 `tests/queue/` 只表示本批集成验证的维护归属，不构成每库测试目录要求。

## 备选方案

**继续以 CMake 编写模块和全部公共规则。** 原生声明容易对照，但复杂 helper 的作用域、变量和字符串处理仍与模块配置混合。保留 CMake 执行层，用 TOML 数据与 Python 引擎分开维护模块定义和生成规则。

**Python 内嵌 CMake、独立 CMake 模板或每库结构化 Python recipe。** 这些写法可以改变排版，却允许每个模块继续包含计算与生成逻辑，不能满足纯声明要求。选择 TOML 限定定义层，专用执行代码通过文件引用维护。

**Python 直接执行完整构建流水线。** 能集中控制命令，却需要重做 ExternalProject 的执行和步骤跟踪；Python 仅生成原生声明。

**在原生步骤外增加 before/after 接口。** 相邻阶段间只有一个位置，再包装会引入重复位置及额外解释规则；直接使用 DEPENDEES、DEPENDERS，不改变上游语义。

**把全部源码修改统一为 patch 文件。** 无法直接保留现有 sed 和构建期间的修改命令，也不适合复制专用 CMakeLists 等操作；保留命令与文件引用，按需要选择工具。

**预留只能为空的 dependencies 字段。** 不能提供实际依赖能力，却会增加无效配置和验证规则；在真正接入依赖模块时增加可选字段，不提前承诺未实现的语义。

## 验收标准

1. zlib 和 queue 使用带中文维护注释的 TOML 纯数据定义，通用引擎没有模块名专用分支。各库声明及专用补丁、脚本集中在对应模块目录，`build/generated/zlib.cmake` 与 `queue.cmake` 可直接阅读且不提交。定向检查公共与平台 cache 的覆盖、仅所选平台步骤参与生成，以及未知字段和未知平台键报错。
2. 根入口保留已确认骨架，仅适配 TOML 发现、BUILD_LIST 选择、生成和加载。验证分别选择两个模块及默认选择的结果，未选模块不产生获取安装操作。全部 Python 入口经根 uv 环境运行；环境或生成失败时 configure 失败，不加载旧结果。
3. 自定义步骤直接生成原生依赖字段及命令，支持 sed 或脚本命令。定向验证一个 build 与 install 之间的步骤确实按依赖执行，多条命令不被拆坏或擅自交给 shell。
4. Windows x64、Visual Studio 2022、v143、Release 与指定 Linux 设备的 GCC、Release 分别通过相同的公开 CMake 入口获取、编译和安装 zlib，并获取 queue 两个资源及安装头文件。Linux 设备与目录沿用基础设施记录的授权，不用交叉配置代替实机执行。
5. 子项目保留父生成器、平台、完整工具集和适用的 instance。自动检查保留 `v142,host=x64` 参数；实际 v142 编译由后续人工验收，不阻塞本批完成。核对显式 CRT 设置的传递及默认 Release 的 `/MD`，并通过实际编译设置与内部消费者链接验证所选 CRT 一致，不增加完整 CRT 配置矩阵。
6. 两端安装树包含 `include/zlib.h`、`include/zconf.h` 及实际平台对应的静态库；构建和安装结果不产生 zlib 动态库。内部消费者以绝对路径链接本次安装，压缩、解压并逐字节核对；缺少必需头文件或库时失败，而不是回退系统库。
7. 两端在带空格的工作副本路径下使用 build 完成相同验证。定向检查特殊字符参数、TOML 与 helper 变更触发 configure、无内容变化不重写、重复构建复用步骤、补丁已应用及不匹配边界；不穷举工具链矩阵。
8. 干净源码副本不依赖其他分支未跟踪文件或已有安装结果。验证不清空真实构建或安装树；生成物、下载源码、环境和安装产物不进入 Git。文档只记录实际执行的命令与证据，明确保留 v142 人工验收缺口。
9. 在隔离 fixture 中使补丁输入文件时间戳更新，验证对应步骤及后续 configure、build、install 实际执行；另用受控、可重复应用的源码修改验证更新后的编译结果，不只检查 stamp 存在。真实补丁内容与已修改源码冲突时明确失败，不静默跳过或使用旧产物冒充成功。不创建补丁自动替换或恢复框架。
10. queue 没有主来源，仍通过一个对外目标完成两个资源的准备和六个头文件的安装。实际资源源码处于独立目录，copy 流程不调用上游 configure 或 build。两端内部程序只包含安装头文件，使用 SPSC 与 MPMC 队列入队、出队并核对结果，同时编译覆盖其余安装头文件。
11. 在隔离 fixture 中验证缺失资源文件时安装失败，缺失已安装头文件时内部验证失败，不能从源码或系统路径补齐。检查原生依赖确保资源准备完成后才复制；再次无修改构建复用已完成步骤。保留上游许可证与必要归属，不迁入上游构建和测试框架。

## 风险

TOML 到 CMake 的参数编码与输入依赖登记是新增维护点，需要真实参数捕获、自动重配置和双平台构建证据。TOML 可以声明命令，不意味着命令跨平台或重复执行安全；专用脚本的工具要求与修改行为仍由对应模块维护。

TOML 提高常见模块的可读性，但复杂库仍可能需要专用 CMakeLists 或脚本；不能以保持单文件为由把这些逻辑塞回引擎，也不能为尚未接入的库预建完整语法。zlib 与 queue 通过证明本批 cmake、copy、多资源及公共步骤路径可行，不证明其他库、模块间依赖图或完整 ExternalProject API 已获支持。相关能力接入时按实际模块补齐设计和验证。

下载依赖网络与上游可用性，本批不承诺离线构建或自动重试。保留原生步骤语义意味着维护者需要理解 ExternalProject 依赖，而不是另学包装语义。v142 的参数保留检查不能证明真实编译器选择正确，人工实机验收缺口必须持续明确。

短目录布局只能降低 Windows 路径长度风险，不能替代所有工具的长路径支持。对冲突补丁采取失败并人工重新准备源码的处理，放弃自动升级已修改源码的能力，以避免扩大数据修改范围和维护恢复状态。
