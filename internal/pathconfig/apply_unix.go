//go:build !windows

package pathconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func persist(change Change) error {
	if change.Manual || !change.Persist || change.AlreadyConfigured {
		return nil
	}
	if change.Target == "" || change.Directory == "" {
		return errors.New("PATH target and directory are required")
	}
	if strings.EqualFold(filepath.Base(change.Target), "dx-bootstrap-path.fish") {
		parent := filepath.Dir(change.Target)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return fmt.Errorf("create fish configuration directory: %w", err)
		}
	}
	marker := blockMarker(change.Directory)
	line := "export PATH=" + quotePOSIX(change.Directory) + ":\"$PATH\""
	if strings.EqualFold(change.Shell, "fish") {
		line = "set -gx PATH " + quotePOSIX(change.Directory) + " $PATH"
	}
	block := marker + " begin\n" + line + "\n" + marker + " end\n"
	for attempt := 0; attempt < 2; attempt++ {
		info, err := os.Lstat(change.Target)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("inspect shell startup file: %w", err)
		}
		if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			return errors.New("shell startup target is not a regular file")
		}
		if configured, err := hasPathBlock(change.Target, change.Directory); err != nil {
			return err
		} else if configured {
			return nil
		}
		file, err := os.OpenFile(change.Target, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
		if err != nil {
			return fmt.Errorf("open shell startup file: %w", err)
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return fmt.Errorf("inspect open shell startup file: %w", statErr)
		}
		if !openedInfo.Mode().IsRegular() || (info != nil && !os.SameFile(info, openedInfo)) {
			_ = file.Close()
			return errors.New("shell startup file changed while applying the PATH entry")
		}
		if info == nil {
			if err := file.Chmod(0o600); err != nil {
				_ = file.Close()
				return fmt.Errorf("restrict new shell startup file: %w", err)
			}
		}
		contents, readErr := os.ReadFile(change.Target)
		if readErr != nil {
			_ = file.Close()
			return fmt.Errorf("read shell startup file before append: %w", readErr)
		}
		if strings.Contains(string(contents), marker+" begin") && strings.Contains(string(contents), marker+" end") {
			_ = file.Close()
			return nil
		}
		if len(contents) > 0 && contents[len(contents)-1] != '\n' {
			block = "\n" + block
		}
		if _, err := file.WriteString(block); err != nil {
			_ = file.Close()
			return fmt.Errorf("append PATH entry to shell startup file: %w", err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close shell startup file: %w", err)
		}
		return nil
	}
	return errors.New("could not safely apply the PATH entry")
}
