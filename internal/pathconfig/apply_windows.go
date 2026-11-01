//go:build windows

package pathconfig

import (
	"errors"
	"fmt"

	"developers-club-dx/internal/domain/tool"
	"golang.org/x/sys/windows/registry"
)

func persist(change Change) error {
	if change.Manual || !change.Persist || change.AlreadyConfigured {
		return nil
	}
	if change.Directory == "" {
		return errors.New("PATH directory is required")
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open current-user environment settings: %w", err)
	}
	defer key.Close()

	raw, valueType, err := key.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("read current-user PATH: %w", err)
	}
	expand := valueType == registry.EXPAND_SZ
	comparison := raw
	if expand {
		if expanded, expandErr := registry.ExpandString(raw); expandErr == nil {
			comparison = expanded
		}
	}
	if contains(comparison, change.Directory, tool.Windows) {
		return nil
	}
	if raw != "" {
		raw += ";"
	}
	raw += change.Directory
	if expand {
		if err := key.SetExpandStringValue("Path", raw); err != nil {
			return fmt.Errorf("write current-user PATH: %w", err)
		}
	} else if err := key.SetStringValue("Path", raw); err != nil {
		return fmt.Errorf("write current-user PATH: %w", err)
	}
	return nil
}
