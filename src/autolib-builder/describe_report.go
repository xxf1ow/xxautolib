package main

import (
	"errors"
	"slices"
)

var errDescriptionBoundary = errors.New("referenced path escapes source root")

type failedRoot struct {
	Root    string   `json:"root"`
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Chain   []string `json:"chain"`
}

type moduleSelectionError struct {
	err   error
	chain []string
}

func (e *moduleSelectionError) Error() string { return e.err.Error() }
func (e *moduleSelectionError) Unwrap() error { return e.err }

func describeReport(sourceRoot, systemName, architecture string, requests []string, discover bool) (moduleDescription, error) {
	result, root, err := prepareDescription(sourceRoot, systemName, architecture, requests, discover)
	if err != nil {
		return result, err
	}
	if discover {
		refs, failures, err := discoverModuleCatalog(root, true)
		if err != nil {
			return result, err
		}
		for _, ref := range refs {
			requests = append(requests, ref.String())
		}
		for _, failure := range failures {
			result.RequestedRoots = append(result.RequestedRoots, failure.Root)
			result.FailedRoots = append(result.FailedRoots, failure)
		}
	}
	merged := map[string]bool{}
	for _, request := range requests {
		key := request
		ref, err := parseModuleRef(request)
		if err != nil {
			return result, err
		}
		if resolved, err := resolveModuleRef(root, ref); err == nil {
			key = resolved.String()
		}
		if slices.Contains(result.RequestedRoots, key) {
			continue
		}
		result.RequestedRoots = append(result.RequestedRoots, key)
		part, err := describeSelection(root, systemName, architecture, []string{request}, false)
		if err != nil {
			if errors.Is(err, errDescriptionBoundary) {
				return result, err
			}
			chain := []string{key}
			var selection *moduleSelectionError
			if errors.As(err, &selection) {
				chain = slices.Clone(selection.chain)
				chain[0] = key
			}
			result.FailedRoots = append(result.FailedRoots, failedRoot{key, "recipe_error", err.Error(), chain})
			continue
		}
		result.Roots = append(result.Roots, part.Roots...)
		result.FilteredRoots = append(result.FilteredRoots, part.FilteredRoots...)
		for _, module := range part.Modules {
			key := (moduleRef{module.Name, module.Version}).String()
			if !merged[key] {
				result.Modules = append(result.Modules, module)
				merged[key] = true
			}
		}
	}
	return result, nil
}
