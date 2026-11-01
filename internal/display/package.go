package display

import "fmt"

// PackageOwnership formats package-manager ownership consistently across the
// command-line and terminal interfaces.
func PackageOwnership(manager, id, version, source string) string {
	if version == "" {
		version = "version unknown"
	}
	return fmt.Sprintf("%s / %s %s (%s)", manager, id, version, source)
}
