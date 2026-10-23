package main

type sourceDefinition struct {
	GitRepository, GitTag string
	URL                   []string
	URLHash, DownloadName string
}

type patchDefinition struct{ File, Resource string }
type stepDefinition struct {
	Name                          string
	Commands                      [][]string
	Dependees, Dependers, Depends []string
	WorkingDirectory              string
}
type copyDefinition struct {
	Resource, Destination string
	Files, Directories    []string
}
type moduleDefinition struct {
	Path, Name, Kind, Version                                    string
	PlatformIndependent                                          bool
	Sentinel, SourceSubdir, ConfigDir, PkgconfigDir, PackageName string
	Depends                                                      []string
	SystemDependencies                                           map[string][]string
	Source                                                       *sourceDefinition
	Resources                                                    map[string]sourceDefinition
	Cache                                                        map[string]any
	Patches                                                      []patchDefinition
	Steps                                                        []stepDefinition
	Copies                                                       []copyDefinition
}
