package main

import "encoding/json"

type moduleDescription struct {
	SystemName    string            `json:"system_name"`
	Architecture  string            `json:"architecture"`
	Modules       []describedModule `json:"modules"`
	GoModuleFiles []string          `json:"go_module_files"`
	BuilderFiles  []string          `json:"builder_files"`
}

type describedModule struct {
	Name                string              `json:"name"`
	Depends             []string            `json:"depends"`
	PlatformIndependent bool                `json:"platform_independent"`
	SystemDependencies  map[string][]string `json:"system_dependencies"`
	Configuration       json.RawMessage     `json:"configuration"`
	InputFiles          []string            `json:"input_files"`
	AdapterFile         string              `json:"adapter_file"`
}

type ciPlanOptions struct {
	SourceRoot, BuilderFile string
	Platforms, Modules      *string
	Force                   bool
}

type ciPlatformPlan struct {
	ID                 string              `json:"id"`
	Description        moduleDescription   `json:"description"`
	Layers             [][]string          `json:"layers"`
	SystemDependencies map[string][]string `json:"system_dependencies"`
}

type ciPlan struct {
	Platforms        []ciPlatformPlan `json:"platforms"`
	RequestedModules []string         `json:"requested_modules"`
	Publisher        string           `json:"publisher"`
	Force            bool             `json:"force"`
}

type ciProductionContext struct {
	OS, Arch, Generator, Platform, Toolset, Configuration, Runtime string
	MultiConfig                                                    bool
	CMakeDefaults                                                  map[string]string
}

type ciBuildInputs struct {
	SourceRoot, BaselineFile string
	Context                  ciProductionContext
}

type ciIdentity struct {
	BuildID       string `json:"build_id"`
	BuildInputsID string `json:"build_inputs_id"`
}

type ciRunOptions struct {
	SourceRoot, PlanFile, RunIdentityFile, Platform, Layer, WorkRoot string
	BuilderFile, ImporterFile, CMakeCommand, MakeProgram             string
	Budget                                                           int
}

type ciSlotResult struct {
	Slot     ciSlot `json:"slot"`
	Asset    string `json:"asset"`
	SHA256   string `json:"sha256"`
	BuildID  string `json:"build_id"`
	Commit   string `json:"commit"`
	Reused   bool   `json:"reused"`
	Built    bool   `json:"built"`
	Verified bool   `json:"verified"`
	Uploaded bool   `json:"uploaded"`
}

type ciImporterResult struct {
	Asset    string `json:"asset"`
	SHA256   string `json:"sha256"`
	Verified bool   `json:"verified"`
	Uploaded bool   `json:"uploaded"`
}

type ciPlatformResult struct {
	Run      ciRunIdentity    `json:"run"`
	Platform string           `json:"platform"`
	Packages []ciSlotResult   `json:"packages"`
	Importer ciImporterResult `json:"importer"`
	Complete bool             `json:"complete"`
	Error    string           `json:"error"`
}
