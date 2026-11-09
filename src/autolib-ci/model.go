package main

type moduleDescription struct {
	SystemName     string            `json:"system_name"`
	Architecture   string            `json:"architecture"`
	Roots          []string          `json:"roots"`
	RequestedRoots []string          `json:"requested_roots"`
	FilteredRoots  []filteredRoot    `json:"filtered_roots"`
	FailedRoots    []failedRoot      `json:"failed_roots"`
	Modules        []describedModule `json:"modules"`
	GoModuleFiles  []string          `json:"go_module_files"`
	BuilderFiles   []string          `json:"builder_files"`
}

type filteredRoot struct {
	Root  string   `json:"root"`
	Code  string   `json:"code"`
	Chain []string `json:"chain"`
}

type failedRoot struct {
	Root    string   `json:"root"`
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Chain   []string `json:"chain"`
}

type describedModule struct {
	Name               string              `json:"name"`
	Version            string              `json:"version"`
	Target             string              `json:"target"`
	Depends            []string            `json:"depends"`
	PackagePlatform    string              `json:"package_platform"`
	SystemDependencies map[string][]string `json:"system_dependencies"`
	InputFiles         []string            `json:"input_files"`
	AdapterFile        string              `json:"adapter_file"`
}

type ciPlanOptions struct {
	SourceRoot, BuilderFile string
	Platforms, Modules      *string
}

type ciPlatformPlan struct {
	ID                 string              `json:"id"`
	RequestedRoots     []string            `json:"requested_roots"`
	FilteredRoots      []string            `json:"filtered_roots"`
	FailedRoots        []failedRoot        `json:"failed_roots"`
	Roots              []string            `json:"roots"`
	Description        moduleDescription   `json:"description"`
	Layers             [][]string          `json:"layers"`
	SystemDependencies map[string][]string `json:"system_dependencies"`
}

type ciPlan struct {
	Platforms        []ciPlatformPlan  `json:"platforms"`
	RequestedModules []string          `json:"requested_modules"`
	Publishers       map[string]string `json:"publishers"`
}

type ciProductionContext struct {
	OS, Arch, Generator, Platform, Toolset, Configuration, Runtime string
	MultiConfig                                                    bool
	CMakeDefaults                                                  map[string]string
}

type ciRunOptions struct {
	CacheFile, PreparedImporter                                      string
	Environment                                                      []string
	Repository                                                       string
	SourceRoot, PlanFile, RunIdentityFile, Platform, Layer, WorkRoot string
	ImporterFile, CMakeCommand, MakeProgram                          string
	Budget                                                           int
}

type ciSlotResult struct {
	Status            string         `json:"status"`
	Error             string         `json:"error"`
	DependencyChain   []string       `json:"dependency_chain"`
	Record            *ciCacheRecord `json:"record,omitempty"`
	EvidenceDirectory string         `json:"evidence_directory,omitempty"`
	Slot              ciSlot         `json:"slot"`
	Built             bool           `json:"built"`
	Verified          bool           `json:"verified"`
}

type ciImporterResult struct {
	Status   string         `json:"status"`
	Error    string         `json:"error"`
	Record   *ciCacheRecord `json:"record,omitempty"`
	File     string         `json:"file"`
	Verified bool           `json:"verified"`
}

type ciEntryResult struct {
	File     string `json:"file"`
	SHA256   string `json:"sha256"`
	Verified bool   `json:"verified"`
}

type ciRootResult struct {
	Root              string `json:"root"`
	Verified          bool   `json:"verified"`
	EvidenceDirectory string `json:"evidence_directory,omitempty"`
}

type ciPlatformResult struct {
	FailedRoots        []failedRoot      `json:"failed_roots"`
	SystemDependencies ciDependencyState `json:"system_dependencies"`
	Processed          bool              `json:"processed"`
	Run                ciRunIdentity     `json:"run"`
	Platform           string            `json:"platform"`
	Packages           []ciSlotResult    `json:"packages"`
	Roots              []ciRootResult    `json:"roots"`
	Importer           ciImporterResult  `json:"importer"`
	Entry              ciEntryResult     `json:"entry"`
	Complete           bool              `json:"complete"`
	Error              string            `json:"error"`
}
