//go:build windows

package platform

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func RefreshSearchPath() error {
	paths := []string{os.Getenv("Path")}
	paths = append(paths, registryPath(registry.CURRENT_USER, `Environment`))
	paths = append(paths, registryPath(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`))
	seen := make(map[string]bool)
	merged := make([]string, 0, len(paths)*8)
	for _, value := range paths {
		for _, path := range strings.Split(value, ";") {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			key := strings.ToLower(path)
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, path)
		}
	}
	return os.Setenv("Path", strings.Join(merged, ";"))
}

func registryPath(root registry.Key, keyPath string) string {
	key, err := registry.OpenKey(root, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, err := key.GetStringValue("Path")
	if err != nil {
		return ""
	}
	expanded, err := registry.ExpandString(value)
	if err == nil {
		return expanded
	}
	return value
}
