package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type buildContext struct {
	SourceRoot, BuildRoot, SystemName, Architecture string
	InstallRoot, MakeProgram, BuilderFile           string
	Generator, Platform, Toolset, Instance          string
	CMakeCommand, GitExecutable                     string
	MultiConfig                                     bool
	MSVCRuntimeLibrary                              *string
	CMakeDefaults                                   map[string]string
}

func cmakeArgument(value string) string {
	equals := ""
	for strings.Contains(value, "]"+equals+"]") || strings.HasSuffix(value, "]"+equals) {
		equals += "="
	}
	return "[" + equals + "[" + value + "]" + equals + "]"
}

type cmakeOption struct {
	key  string
	args []string
}

func option(key string, args ...string) cmakeOption { return cmakeOption{key, args} }
func resourceTarget(module, resource string) string {
	return fmt.Sprintf("_al_resource_%d_%s_%s", len(module), module, resource)
}
func slashJoin(parts ...string) string { return filepath.ToSlash(filepath.Join(parts...)) }
func sourceDirectory(source *sourceDefinition, local, prefix string) string {
	if source != nil && source.GitRepository != "" {
		return filepath.ToSlash(local)
	}
	return slashJoin(prefix, "src")
}
func directories(prefix, install, source, download string) []cmakeOption {
	return []cmakeOption{option("PREFIX", filepath.ToSlash(prefix)), option("SOURCE_DIR", source), option("BINARY_DIR", slashJoin(prefix, "bin")), option("STAMP_DIR", slashJoin(prefix, "stamp")), option("TMP_DIR", slashJoin(prefix, "tmp")), option("DOWNLOAD_DIR", filepath.ToSlash(download)), option("INSTALL_DIR", filepath.ToSlash(install))}
}
func cacheArgument(key string, value any) string {
	kind := "STRING"
	var text string
	switch v := value.(type) {
	case bool:
		kind = "BOOL"
		text = "OFF"
		if v {
			text = "ON"
		}
	case []string:
		text = strings.Join(v, ";")
	case int64:
		text = strconv.FormatInt(v, 10)
	case string:
		text = v
	}
	return "-D" + key + ":" + kind + "=" + text
}
func cacheStrings(value any) []string {
	switch v := value.(type) {
	case string:
		return strings.Split(v, ";")
	case []string:
		return slices.Clone(v)
	}
	return nil
}
func unique(values []string) []string {
	result := []string{}
	for _, value := range values {
		if !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}

func renderModule(module *moduleDefinition, context buildContext, dependencies []*moduleDefinition) (string, error) {
	prefix := filepath.Join(context.BuildRoot, "ep", module.Name)
	installRoot := context.InstallRoot
	if installRoot == "" {
		installRoot = filepath.Join(context.SourceRoot, "3rdparty")
	}
	install := slashJoin(installRoot, module.Name)
	persistent := filepath.Join(context.SourceRoot, "git")
	cmake := filepath.ToSlash(context.CMakeCommand)
	driver := slashJoin(context.SourceRoot, "src/autolib-builder/apply_patch.cmake")
	proxy := slashJoin(context.SourceRoot, "src/autolib-builder/configure_dependencies.cmake")
	setupPath := slashJoin(context.BuildRoot, "generated", module.Name+"-dependency-setup.cmake")
	separator := "__AL_LIST__"
	values := fmt.Sprintf("%#v %#v %#v %#v %#v %#v %#v", module, context, module.Cache, module.Source, module.Resources, module.Steps, module.Copies)
	for strings.Contains(values, separator) {
		separator += "_"
	}
	lines := []string{"# Generated from module.toml; do not edit.", ""}
	adapterPath := filepath.Join(filepath.Dir(module.Path), "autolib-import.cmake")
	_, adapterErr := os.Stat(adapterPath)
	if adapterErr != nil && !os.IsNotExist(adapterErr) {
		return "", adapterErr
	}
	consumable := adapterErr == nil
	prepare := func(phase string) []string {
		return []string{filepath.ToSlash(context.BuilderFile), "prepare-consumption", "--source-root=" + filepath.ToSlash(context.SourceRoot), "--install-root=" + filepath.ToSlash(installRoot), "--name=" + module.Name, "--os=" + strings.ToLower(context.SystemName), "--arch=" + normalizeArchitecture(context.Architecture), "--phase=" + phase}
	}
	if module.Sentinel != "" {
		lines = append(lines, "if(EXISTS "+cmakeArgument(slashJoin(install, module.Sentinel))+")", "    message(STATUS "+cmakeArgument("  [Info] ["+module.Name+"] cached")+")", "    add_custom_target("+cmakeArgument(module.Name)+" ALL)")
		if consumable {
			arguments := []string{}
			for _, arg := range prepare("reuse") {
				arguments = append(arguments, cmakeArgument(arg))
			}
			lines = append(lines, "    add_custom_command(TARGET "+cmakeArgument(module.Name)+" POST_BUILD COMMAND "+strings.Join(arguments, " ")+" VERBATIM)")
		}
		if len(dependencies) > 0 {
			args := []string{cmakeArgument(module.Name)}
			for _, dep := range dependencies {
				args = append(args, cmakeArgument(dep.Name))
			}
			lines = append(lines, "    add_dependencies("+strings.Join(args, " ")+")")
		}
		lines = append(lines, "else()")
	}
	emit := func(command string, positional []string, options []cmakeOption) {
		lines = append(lines, command+"(")
		for _, value := range positional {
			lines = append(lines, "    "+cmakeArgument(value))
		}
		for _, opt := range options {
			if opt.key == "_SOURCE_ARGS" {
				lines = append(lines, "    ${_al_source_args}")
				continue
			}
			lines = append(lines, "    "+opt.key)
			for _, arg := range opt.args {
				lines = append(lines, "        "+cmakeArgument(strings.ReplaceAll(arg, ";", separator)))
			}
		}
		lines = append(lines, ")", "")
	}
	patchOptions := func(resource string) []cmakeOption {
		options := []cmakeOption{}
		for _, patch := range module.Patches {
			if patch.Resource == resource {
				key := "COMMAND"
				if len(options) == 0 {
					key = "PATCH_COMMAND"
				}
				options = append(options, option(key, cmake, "-DGIT_EXECUTABLE="+filepath.ToSlash(context.GitExecutable), "-DSOURCE_DIR=<SOURCE_DIR>", "-DPATCH_FILE="+filepath.ToSlash(patch.File), "-P", driver))
			}
		}
		return options
	}
	patchDependencies := func(target, resource string) {
		files := []string{}
		for _, patch := range module.Patches {
			if patch.Resource == resource {
				files = append(files, filepath.ToSlash(patch.File))
			}
		}
		if len(files) > 0 {
			emit("ExternalProject_Add_StepDependencies", append(append([]string{target, "patch"}, files...), driver), nil)
		}
	}
	sourceOptions := func(source *sourceDefinition, local string) []cmakeOption {
		if source == nil {
			return []cmakeOption{option("DOWNLOAD_COMMAND", "")}
		}
		if source.GitRepository == "" {
			options := []cmakeOption{option("URL", source.URL...), option("URL_HASH", source.URLHash)}
			if source.DownloadName != "" {
				options = append(options, option("DOWNLOAD_NAME", source.DownloadName))
			}
			return options
		}
		location := cmakeArgument(filepath.ToSlash(local))
		lines = append(lines, "if(EXISTS "+cmakeArgument(slashJoin(local, ".git"))+")", `    set(_al_source_args DOWNLOAD_COMMAND "")`, "else()", "    if(EXISTS "+location+")", "        if(NOT IS_DIRECTORY "+location+")", "            message(FATAL_ERROR "+cmakeArgument(filepath.ToSlash(local)+": expected a Git directory")+")", "        endif()", "        file(GLOB _al_entries LIST_DIRECTORIES TRUE "+cmakeArgument(filepath.ToSlash(local)+"/*")+")", "        if(_al_entries)", "            message(FATAL_ERROR "+cmakeArgument(filepath.ToSlash(local)+": nonempty directory without .git; preserve or complete it manually")+")", "        endif()", "    endif()")
		emit("set", []string{"_al_source_args"}, []cmakeOption{option("GIT_REPOSITORY", source.GitRepository), option("GIT_TAG", source.GitTag)})
		lines = append(lines, "endif()")
		return []cmakeOption{option("_SOURCE_ARGS")}
	}
	resourceDirectory := func(name string) string {
		source := module.Resources[name]
		return sourceDirectory(&source, filepath.Join(persistent, "_resources", module.Name, name), filepath.Join(prefix, "res", name))
	}
	for _, name := range sortedKeys(module.Resources) {
		source := module.Resources[name]
		resourcePrefix := filepath.Join(prefix, "res", name)
		local := filepath.Join(persistent, "_resources", module.Name, name)
		options := directories(resourcePrefix, install, resourceDirectory(name), persistent)
		options = append(options, sourceOptions(&source, local)...)
		options = append(options, option("LIST_SEPARATOR", separator), option("UPDATE_COMMAND", ""), option("CONFIGURE_COMMAND", ""), option("BUILD_COMMAND", ""), option("INSTALL_COMMAND", ""))
		options = append(options, patchOptions(name)...)
		emit("ExternalProject_Add", []string{resourceTarget(module.Name, name)}, options)
		patchDependencies(resourceTarget(module.Name, name), name)
	}
	local := filepath.Join(persistent, module.Name)
	options := directories(prefix, install, sourceDirectory(module.Source, local, prefix), persistent)
	options = append(options, option("LIST_SEPARATOR", separator), option("UPDATE_COMMAND", ""))
	options = append(options, sourceOptions(module.Source, local)...)
	targets := []string{}
	for _, name := range sortedKeys(module.Resources) {
		targets = append(targets, resourceTarget(module.Name, name))
	}
	for _, dep := range dependencies {
		targets = append(targets, dep.Name)
	}
	if len(targets) > 0 {
		options = append(options, option("DEPENDS", targets...))
	}
	if module.Kind == "cmake" {
		if module.SourceSubdir != "" {
			options = append(options, option("SOURCE_SUBDIR", module.SourceSubdir))
		}
		options = append(options, option("CMAKE_COMMAND", cmake), option("CMAKE_GENERATOR", context.Generator))
		for _, opt := range []cmakeOption{option("CMAKE_GENERATOR_PLATFORM", context.Platform), option("CMAKE_GENERATOR_TOOLSET", context.Toolset), option("CMAKE_GENERATOR_INSTANCE", context.Instance)} {
			if opt.args[0] != "" {
				options = append(options, opt)
			}
		}
		cache := map[string]any{"CMAKE_POLICY_VERSION_MINIMUM": "3.5", "CMAKE_POSITION_INDEPENDENT_CODE": true, "CMAKE_CXX_STANDARD": "17", "CMAKE_INSTALL_LIBDIR": "lib"}
		for key, value := range context.CMakeDefaults {
			cache[key] = value
		}
		for key, value := range module.Cache {
			cache[key] = value
		}
		resolve := func(value string) (string, error) {
			if strings.Contains(value, "<PYTHON_EXECUTABLE>") {
				return "", fmt.Errorf("%s: PYTHON_EXECUTABLE: 当前生成器不支持解释器注入", module.Path)
			}
			return resourceReference.ReplaceAllStringFunc(value, func(match string) string { return resourceDirectory(resourceReference.FindStringSubmatch(match)[1]) }), nil
		}
		for _, key := range sortedKeys(cache) {
			switch value := cache[key].(type) {
			case string:
				resolved, err := resolve(value)
				if err != nil {
					return "", err
				}
				cache[key] = resolved
			case []string:
				result := make([]string, len(value))
				for i, v := range value {
					resolved, err := resolve(v)
					if err != nil {
						return "", err
					}
					result[i] = resolved
				}
				cache[key] = result
			}
		}
		// 配置身份和安装前缀只由公共入口确定。
		for _, key := range []string{"CMAKE_INSTALL_PREFIX", "CMAKE_BUILD_TYPE", "CMAKE_MSVC_RUNTIME_LIBRARY", "CMAKE_GENERATOR", "CMAKE_GENERATOR_PLATFORM", "CMAKE_GENERATOR_TOOLSET", "CMAKE_GENERATOR_INSTANCE"} {
			delete(cache, key)
		}
		if context.Generator == "Unix Makefiles" && context.MakeProgram != "" {
			cache["CMAKE_MAKE_PROGRAM"] = context.MakeProgram
		}
		prefixes := []string{}
		callerInclude := []string{}
		if len(dependencies) > 0 {
			for _, dep := range dependencies {
				location := slashJoin(installRoot, dep.Name)
				prefixes = append(prefixes, location)
				if dep.ConfigDir != "" {
					packageName := dep.PackageName
					if packageName == "" {
						packageName = dep.Name
					}
					cache[packageName+"_DIR"] = slashJoin(location, dep.ConfigDir)
				}
			}
			cache["CMAKE_PREFIX_PATH"] = unique(append(slices.Clone(prefixes), cacheStrings(cache["CMAKE_PREFIX_PATH"])...))
			callerInclude = cacheStrings(cache["CMAKE_PROJECT_INCLUDE"])
			cache["CMAKE_PROJECT_INCLUDE"] = setupPath
		}
		args := []string{}
		for _, key := range sortedKeys(cache) {
			args = append(args, cacheArgument(key, cache[key]))
		}
		args = append(args, "-DCMAKE_INSTALL_PREFIX:PATH=<INSTALL_DIR>")
		if !context.MultiConfig {
			args = append(args, "-DCMAKE_BUILD_TYPE:STRING=Release")
		}
		if context.MSVCRuntimeLibrary != nil {
			args = append(args, cacheArgument("CMAKE_MSVC_RUNTIME_LIBRARY", *context.MSVCRuntimeLibrary))
		}
		if len(dependencies) > 0 {
			source := "<SOURCE_DIR>"
			if module.SourceSubdir != "" {
				source += "/" + module.SourceSubdir
			}
			command := []string{cmake, "-S", source, "-B", "<BINARY_DIR>", "-G", context.Generator}
			for _, pair := range [][2]string{{"-A", context.Platform}, {"-T", context.Toolset}} {
				if pair[1] != "" {
					command = append(command, pair[:]...)
				}
			}
			if context.Instance != "" {
				command = append(command, "-DCMAKE_GENERATOR_INSTANCE:INTERNAL="+context.Instance)
			}
			for _, arg := range args {
				command = append(command, strings.ReplaceAll(arg, "<INSTALL_DIR>", install))
			}
			setup := []string{"if(CMAKE_SCRIPT_MODE_FILE)", "    set(_al_command)"}
			for _, arg := range command {
				setup = append(setup, "    list(APPEND _al_command "+cmakeArgument(strings.ReplaceAll(arg, ";", `\;`))+")")
			}
			pcPaths := []string{}
			for i, dep := range dependencies {
				if dep.PkgconfigDir != "" {
					pcPaths = append(pcPaths, slashJoin(prefixes[i], dep.PkgconfigDir))
				}
			}
			pathSeparator := ":"
			if context.SystemName == "Windows" {
				pathSeparator = ";"
			}
			setup = append(setup, "    set(_al_pkgconfig "+cmakeArgument(strings.Join(unique(pcPaths), pathSeparator))+")", "else()")
			for _, include := range callerInclude {
				setup = append(setup, "    include("+cmakeArgument(include)+")")
			}
			includes := []string{}
			for _, location := range prefixes {
				includes = append(includes, cmakeArgument(location+"/include"))
			}
			setup = append(setup, "    include_directories("+strings.Join(includes, " ")+")", "endif()\n")
			content := strings.ReplaceAll(strings.Join(setup, "\n"), "$", "$<1:$>")
			lines = append(lines, "file(GENERATE OUTPUT "+cmakeArgument(setupPath)+" CONTENT "+cmakeArgument(content)+")")
			options = append(options, option("CONFIGURE_COMMAND", cmake, "-DSETUP_FILE="+setupPath, "-DSOURCE_DIR=<SOURCE_DIR>", "-DBINARY_DIR=<BINARY_DIR>", "-P", proxy))
		} else {
			options = append(options, option("CMAKE_ARGS", args...))
		}
		options = append(options, option("BUILD_COMMAND", cmake, "--build", "<BINARY_DIR>", "--config", "Release"))
		if consumable {
			options = append(options, option("INSTALL_COMMAND", prepare("begin")...), option("COMMAND", cmake, "--build", "<BINARY_DIR>", "--target", "install", "--config", "Release"), option("COMMAND", prepare("installed")...))
		} else {
			options = append(options, option("INSTALL_COMMAND", cmake, "--build", "<BINARY_DIR>", "--target", "install", "--config", "Release"))
		}
	} else {
		options = append(options, option("CONFIGURE_COMMAND", ""), option("BUILD_COMMAND", ""))
		if consumable {
			options = append(options, option("INSTALL_COMMAND", prepare("begin")...))
		}
		for i, mapping := range module.Copies {
			destination := slashJoin(install, mapping.Destination)
			source := sourceDirectory(module.Source, local, prefix)
			if mapping.Resource != "" {
				source = resourceDirectory(mapping.Resource)
			}
			key := "COMMAND"
			if i == 0 && !consumable {
				key = "INSTALL_COMMAND"
			}
			options = append(options, option(key, cmake, "-E", "make_directory", destination))
			operation := "copy"
			entries := mapping.Files
			if mapping.Directories != nil {
				operation = "copy_directory"
				entries = mapping.Directories
			}
			args := []string{cmake, "-E", operation}
			for _, entry := range entries {
				args = append(args, slashJoin(source, entry))
			}
			args = append(args, destination)
			options = append(options, option("COMMAND", args...))
		}
		if consumable {
			options = append(options, option("COMMAND", prepare("installed")...))
		}
	}
	options = append(options, patchOptions("")...)
	emit("ExternalProject_Add", []string{module.Name}, options)
	if consumable {
		emit("add_custom_command", []string{"TARGET", module.Name, "POST_BUILD"}, []cmakeOption{option("COMMAND", prepare("reuse")...), option("VERBATIM")})
	}
	if len(dependencies) > 0 && module.Kind == "cmake" {
		emit("ExternalProject_Add_StepDependencies", []string{module.Name, "configure", setupPath, proxy}, nil)
	}
	patchDependencies(module.Name, "")
	for _, step := range module.Steps {
		options := []cmakeOption{}
		for _, opt := range []cmakeOption{option("DEPENDEES", step.Dependees...), option("DEPENDERS", step.Dependers...), option("DEPENDS", step.Depends...)} {
			if opt.args != nil {
				options = append(options, opt)
			}
		}
		if step.WorkingDirectory != "" {
			options = append(options, option("WORKING_DIRECTORY", step.WorkingDirectory))
		}
		for _, argv := range step.Commands {
			options = append(options, option("COMMAND", argv...))
		}
		emit("ExternalProject_Add_Step", []string{module.Name, step.Name}, options)
	}
	if module.Sentinel != "" {
		lines = append(lines, "endif()")
	}
	return strings.Join(lines, "\n"), nil
}
