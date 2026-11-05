# Adapter 规范与五类基准

为 `autolib-import.cmake` 选择导入方式、编写正文并验证消费。五类示例均使用虚构包 `sample`，不对应真实模块、版本或下游工程；它们是互斥的格式参考，不是依次尝试的回退链。

## 新增与规范化步骤

1. 核对实际安装的头文件、Config、Find Module、`.pc`、库文件及直接依赖。迁移已有库时，查阅旧仓库对应文件开头注释中的消费示例，保留仍适用的使用要求，不照搬旧路径和多余检查。
2. 只需要头文件时，直接包含头文件，即使存在 Config。否则，有可用的 CMake Config 就使用 Config；没有 Config 时，Module 与 pkg-config 平级，选择能完整表达依赖、适配目标平台且较少需要手工补充的方式；均无合适方式时才使用 `find_library`。选定后固定使用，不编写自动回退链。
3. 按对应基准编写正文，替换包名、路径、上游目标或 `.pc` 名称，并补齐实际需要的编译要求和依赖。独立包依赖在 `module.toml` 的 `depends` 中声明，并链接对应的 `autolib::<dependency>`；不在 adapter 下载或构建依赖。
4. 在授权环境中通过正常消费入口验证重复导入、跨目录目标可见性和实际编译链接；动态库还需验证运行时加载。原安装与打包搬迁的检查按[测试指南](../../../../docs/testing.md#本地二进制闭环)执行。记录实际验证的包、平台和未验证项，不能以虚构示例替代真实包的消费证据。

## 公共格式

- 开头用简短中文块注释说明库用途、导入方式和公共目标；有非显然的消费条件时一并说明。
- 保留 `if(TARGET autolib::<name>)` 的重复导入 guard。每包只提供一个 `GLOBAL INTERFACE IMPORTED` 公共目标，不保留大写兼容别名。
- 以 `CMAKE_CURRENT_LIST_DIR` 定位本包，只设置优先搜索位置；不添加 `if(NOT IS_DIRECTORY ...)`、载荷存在性预检查、`NO_DEFAULT_PATH` 或查找结果来源核验。查找失败使用标准 `REQUIRED` 报错，不另建回退系统包的分支。
- Config 和 Module 示例假定上游提供普通 imported target；将跨目录使用的上游导入目标提升为 GLOBAL。真实包含别名或多个相互依赖目标时，按实际导出关系处理，不泛化扫描任意目标或 Config。
- 包的公共消费目标封装其支持的库及声明依赖，不聚合互斥的静动态产物，也不把某个下游程序的可选组件组合固化成所有下游的共同依赖。
- 短命令保持单行，仅在参数或列表较长、换行有助于阅读时拆行；不同逻辑段之间留一个空行，不给每条命令单独插入空行。

## 纯头文件

只设置实际需要的包含目录；有最低 C++ 标准、宏或传递依赖时按需补充，不为了读取这些要求而绕到 Config 导入。

```cmake
#[=[
Sample：示例头文件库，直接包含头文件。
公共目标：autolib::sample。
#]=]

if(TARGET autolib::sample)
    return()
endif()

add_library(autolib::sample INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::sample INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
```

## CMake Config

示例假定 Config 安装于 `lib/cmake/Sample`，导出 `Sample::Sample`；实际目录、包名和目标名以安装产物为准。

```cmake
#[=[
Sample：示例链接库，通过 CMake Config 导入。
公共目标：autolib::sample。
#]=]

if(TARGET autolib::sample)
    return()
endif()

set(Sample_DIR "${CMAKE_CURRENT_LIST_DIR}/lib/cmake/Sample")
find_package(Sample CONFIG REQUIRED)
set_property(TARGET Sample::Sample PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::sample INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::sample INTERFACE Sample::Sample)
```

## CMake Module

示例假定可用的 `FindSample.cmake` 支持 `Sample_ROOT`，并提供 `Sample::Sample`。实际使用时先检查 Module 的查找提示和导出接口；包自带的 Module 按其安装目录设置 `CMAKE_MODULE_PATH`，不为套用基准另写一份 Find Module。

```cmake
#[=[
Sample：示例链接库，通过 CMake Find Module 导入。
公共目标：autolib::sample。
#]=]

if(TARGET autolib::sample)
    return()
endif()

set(Sample_ROOT "${CMAKE_CURRENT_LIST_DIR}")
find_package(Sample MODULE REQUIRED)
set_property(TARGET Sample::Sample PROPERTY IMPORTED_GLOBAL TRUE)

add_library(autolib::sample INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::sample INTERFACE Sample::Sample)
```

## pkg-config

示例假定包在标准 pkg-config 目录提供 `sample.pc`，通过安装前缀优先查找。非标准 `.pc` 目录按实际布局补充查找位置。示例按普通动态链接消费；静态链接时需另外核对 `.pc` 的私有依赖，不能假定普通 imported target 已包含 `--static` 的结果。

```cmake
#[=[
Sample：示例链接库，通过 pkg-config 导入。
公共目标：autolib::sample。
#]=]

if(TARGET autolib::sample)
    return()
endif()

list(PREPEND CMAKE_PREFIX_PATH "${CMAKE_CURRENT_LIST_DIR}")
find_package(PkgConfig REQUIRED)
pkg_check_modules(_autolib_sample REQUIRED IMPORTED_TARGET GLOBAL sample)

add_library(autolib::sample INTERFACE IMPORTED GLOBAL)
target_link_libraries(autolib::sample INTERFACE PkgConfig::_autolib_sample)
```

## find_library

示例假定头文件和库分别位于 `include`、`lib`。显式填写实际库名和查找提示，由 `find_library` 处理平台文件名前后缀；必要的系统库及其他传递依赖由 adapter 按实际产物补齐，不以静态库推定依赖自包含。

```cmake
#[=[
Sample：示例链接库，通过 find_library 查找库文件。
公共目标：autolib::sample。
#]=]

if(TARGET autolib::sample)
    return()
endif()

find_library(_autolib_sample_library NAMES sample HINTS "${CMAKE_CURRENT_LIST_DIR}/lib" REQUIRED)

add_library(autolib::sample INTERFACE IMPORTED GLOBAL)
target_include_directories(autolib::sample INTERFACE "${CMAKE_CURRENT_LIST_DIR}/include")
target_link_libraries(autolib::sample INTERFACE "${_autolib_sample_library}")
```
