//go:build !windows

package regional

import "developers-club-dx/internal/platform"

func operatingSystemRegion(_ platform.Info) string { return "" }
