package packagemetadata

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// CheckProducerReferences restricts producer paths to this package version and its exact direct dependencies.
func CheckProducerReferences(text, prefix string, allowed []string) error {
	text = strings.ReplaceAll(text, `\`, "/")
	prefix = filepath.ToSlash(prefix)
	for rest := text; ; {
		i := strings.Index(rest, prefix)
		if i < 0 {
			return nil
		}
		rest = rest[i+len(prefix):]
		if !strings.HasPrefix(rest, "/") {
			return fmt.Errorf("unhandled producer path at %s%s", prefix, rest)
		}
		end := strings.IndexAny(rest, "\"'\t\r\n;)")
		if end < 0 {
			end = len(rest)
		}
		reference := path.Clean(strings.TrimSuffix(strings.TrimSpace(rest[:end]), "/"))
		valid := false
		for _, candidate := range allowed {
			if reference == "/"+candidate || strings.HasPrefix(reference, "/"+candidate+"/") {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("producer reference must name this package version or an exact direct dependency: %s", reference)
		}
	}
}
