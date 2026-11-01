//go:build windows

package regional

import (
	"developers-club-dx/internal/platform"
	"golang.org/x/sys/windows/registry"
)

func operatingSystemRegion(_ platform.Info) string {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Control Panel\International`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, err := key.GetStringValue("LocaleName")
	if err != nil {
		return ""
	}
	return value
}
