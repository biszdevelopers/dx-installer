package display

import (
	"fmt"
	"strings"

	"developers-club-dx/internal/detection"
)

// PackageOwnership formats package-manager ownership consistently across the
// command-line and terminal interfaces.
func PackageOwnership(manager, id, version, source string) string {
	if version == "" {
		version = "version unknown"
	}
	return fmt.Sprintf("%s / %s %s (%s)", manager, id, version, source)
}

// ArchitectureVerification summarizes executable format verification for an
// inventory instance without implying that script files expose an architecture.
func ArchitectureVerification(instance detection.Instance) string {
	architectures := make([]string, len(instance.Architecture))
	for index, architecture := range instance.Architecture {
		architectures[index] = string(architecture)
	}
	if len(architectures) == 0 {
		return string(instance.ArchitectureStatus)
	}
	return fmt.Sprintf("%s (%s)", instance.ArchitectureStatus, strings.Join(architectures, ", "))
}
