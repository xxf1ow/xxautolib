package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheOwnership(t *testing.T) {
	f := newFixture(t)
	m := f.definition("demo", "cmake", testSource)
	dep := f.definition("eigen", "cmake", "config_dir='share/eigen3/cmake'\npackage_name='Eigen3'\n"+testSource)
	for _, key := range []string{"CMAKE_INSTALL_PREFIX", "CMAKE_BUILD_TYPE", "CMAKE_MSVC_RUNTIME_LIBRARY", "CMAKE_GENERATOR", "CMAKE_GENERATOR_PLATFORM", "CMAKE_GENERATOR_TOOLSET", "CMAKE_GENERATOR_INSTANCE", "CMAKE_MAKE_PROGRAM", "CMAKE_C_COMPILER", "CMAKE_CXX_COMPILER", "CMAKE_TOOLCHAIN_FILE", "CMAKE_SYSTEM_NAME", "CMAKE_SYSTEM_PROCESSOR", "Eigen3_DIR"} {
		t.Run(key, func(t *testing.T) {
			m.Cache[key] = "same-as-entry"
			defer delete(m.Cache, key)
			if _, err := renderModule(m, f.context, []*moduleDefinition{dep}); err == nil {
				t.Fatal("accepted owned cache", key)
			}
		})
	}
	m.ConfigDir = "my/config"
	m.Cache["Unrelated_DIR"] = "relative"
	if _, err := renderModule(m, f.context, []*moduleDefinition{dep}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"CMAKE_OSX_ARCHITECTURES", "CMAKE_OSX_DEPLOYMENT_TARGET", "CMAKE_OSX_SYSROOT"} {
		m.Cache[key] = f.context.CMakeDefaults[key]
		context := f.context
		context.SystemName = "Darwin"
		context.Architecture = "arm64"
		if _, err := renderModule(m, context, nil); err == nil {
			t.Error("accepted Darwin-owned cache", key)
		}
		delete(m.Cache, key)
	}
}

func TestRecipePathsAndSteps(t *testing.T) {
	for _, body := range []string{
		"COMMAND=['cmake','-E','echo','flat']\nDEPENDEES=['build']\n",
		"COMMAND=[[]]\nDEPENDEES=['build']\n", "COMMAND=[['']]\nDEPENDEES=['build']\n",
		"COMMAND=[['cmake']]\n", "COMMAND=[['cmake']]\nDEPENDEES=['missing']\n",
		"COMMAND=[['cmake']]\nDEPENDEES=['build']\nWORKING_DIRECTORY='../outside'\n",
		"COMMAND=[['cmake']]\nDEPENDEES=['build']\nWORKING_DIRECTORY='<SOURCE_DIR>/../outside'\n",
		"COMMAND=[['cmake']]\nDEPENDEES=['build']\nDEPENDS=['<UNKNOWN_DIR>/generated']\n",
		"COMMAND=[['cmake']]\nDEPENDEES=['build']\nWORKING_DIRECTORY='a</../../outside'\n",
		"COMMAND=[['cmake']]\nDEPENDEES=['build']\nDEPENDS=['$<IF:1,../outside,inside>']\n",
		"COMMAND=[['cmake']]\nDEPENDEES=['build']\nDEPENDS=['missing.cmake']\n",
	} {
		t.Run(body, func(t *testing.T) {
			_, path := contractRecipe(t, "type='cmake'\npackage_platforms=['any-any']\n"+testSource+"[[steps]]\nname='prepare'\n"+body)
			if _, err := loadDefinition(path, "Linux", "amd64"); err == nil {
				t.Fatal("accepted invalid step")
			}
		})
	}
	for _, name := range []string{"download", "update", "patch", "configure", "build", "install", "test"} {
		_, path := contractRecipe(t, "type='cmake'\npackage_platforms=['any-any']\n"+testSource+"[[steps]]\nname='"+name+"'\nCOMMAND=[['cmake']]\nDEPENDEES=['build']\n")
		if _, err := loadDefinition(path, "Linux", "amd64"); err == nil {
			t.Error("accepted native step name", name)
		}
	}
}

func TestDescribeEffectiveFacts(t *testing.T) {
	root, path := contractRecipe(t, "type='cmake'\npackage_platforms=['any-any']\n[system_dependencies]\napt=['pkg-config']\nchoco=['pkgconfiglite']\nbrew=['pkgconf']\n"+pinnedSource+"[[steps]]\nname='prepare'\nCOMMAND=[['cmake','-E','echo','<RESOURCE_SOURCE_DIR:extra>']]\nDEPENDEES=['download']\nDEPENDS=['<BINARY_DIR>/generated']\nWORKING_DIRECTORY='<BINARY_DIR>'\n[resources.extra]\nGIT_REPOSITORY='repo'\nGIT_TAG='0123456789012345678901234567890123456789'\n")
	describeRootInputs(t, root)
	for _, tc := range []struct{ system, arch, manager, want string }{{"Linux", "amd64", "apt", "pkg-config"}, {"Windows", "x64", "choco", "pkgconfiglite"}, {"Darwin", "arm64", "brew", "pkgconf"}} {
		d, err := describeModules(root, tc.system, tc.arch, []string{"demo"})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(d)
		if strings.Contains(string(encoded), `"configuration"`) {
			t.Fatal("configuration mirror remains")
		}
		deps := d.Modules[0].SystemDependencies
		if len(deps) != 1 || len(deps[tc.manager]) != 1 || deps[tc.manager][0] != tc.want {
			t.Fatal(deps)
		}
	}
	// The same dependency-owned cache must fail both entry paths.
	recipe := readFile(t, path) + "[cmake.cache]\nCMAKE_INSTALL_PREFIX='same'\n"
	putFile(t, path, recipe)
	if _, err := describeModules(root, "Linux", "amd64", []string{"demo"}); err == nil {
		t.Fatal("describe accepted owned cache")
	}
	context := buildContext{SourceRoot: root, BuildRoot: filepath.Join(root, "build"), SystemName: "Linux", Architecture: "amd64"}
	if err := generate(context, []string{"demo"}); err == nil {
		t.Fatal("generate accepted owned cache")
	}
}

func TestStepArgvExecution(t *testing.T) {
	f := newFixture(t)
	source := f.repository("steps", map[string]string{
		"CMakeLists.txt": "cmake_minimum_required(VERSION 3.20)\nproject(step NONE)\ninstall(FILES ${CMAKE_BINARY_DIR}/step.txt DESTINATION .)\n",
		"record.cmake":   "file(WRITE \"${OUT}\" \"${VALUE}\")\n",
	})
	resource := f.repository("step-resource", map[string]string{"record.cmake": "file(WRITE \"${OUT}\" \"${VALUE}\")\n"})
	m := f.definition("demo", "cmake", "[source]\n"+source+"[resources.extra]\n"+resource+"[[steps]]\nname='record'\nDEPENDEES=['build']\nDEPENDERS=['install']\nDEPENDS=['<RESOURCE_SOURCE_DIR:extra>/record.cmake']\nWORKING_DIRECTORY='<RESOURCE_SOURCE_DIR:extra>'\nCOMMAND=[['cmake','-DVALUE=one && bad','-DOUT=<BINARY_DIR>/step.txt','-P','<RESOURCE_SOURCE_DIR:extra>/record.cmake'],['cmake','-E','echo','done']]\n")
	f.project([]*moduleDefinition{m}, "")
	f.build()
	requireFile(t, f.installed("demo", "step.txt"), "one && bad")
}
