package pathconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/platform"
)

type Change struct {
	Directory         string
	Target            string
	Shell             string
	ManualCommand     string
	Persist           bool
	Manual            bool
	AlreadyConfigured bool
}

func Plan(machine platform.Info, home string, directories []string, currentPath string) ([]Change, error) {
	if !filepath.IsAbs(home) {
		return nil, errors.New("PATH planning requires an absolute home directory")
	}
	changes := make([]Change, 0, len(directories))
	seen := make(map[string]bool, len(directories))
	for _, directory := range directories {
		if !filepath.IsAbs(directory) || strings.ContainsAny(directory, "\r\n\x00") {
			return nil, fmt.Errorf("PATH directory %q must be absolute and cannot contain line breaks", directory)
		}
		normalized := filepath.Clean(directory)
		key := normalized
		if machine.OS == tool.Windows {
			key = strings.ToLower(normalized)
		}
		if seen[key] || contains(currentPath, normalized, machine.OS) {
			continue
		}
		seen[key] = true
		change := Change{Directory: normalized}
		if machine.OS == tool.Windows {
			change.Target = "Current-user PATH (Windows environment registry)"
			change.Shell = "Windows"
			change.Persist = true
			change.ManualCommand = powershellCommand(normalized)
			changes = append(changes, change)
			continue
		}
		target, shell, err := startupTarget(machine, home)
		if err != nil {
			change.Target = "manual PATH update"
			change.Shell = machine.Shell
			change.Manual = true
			change.ManualCommand = unixCommand(machine.Shell, normalized)
			changes = append(changes, change)
			continue
		}
		change.Target = target
		change.Shell = shell
		change.Persist = true
		if configured, err := hasPathBlock(target, normalized); err != nil {
			change.Persist = false
			change.Manual = true
			change.Target = "manual PATH update (startup file could not be safely inspected)"
			change.ManualCommand = unixCommand(shell, normalized)
		} else if configured {
			change.Persist = false
			change.AlreadyConfigured = true
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func Apply(changes []Change) error {
	var failures []error
	for _, change := range changes {
		if change.Persist && !change.AlreadyConfigured {
			if err := persist(change); err != nil {
				failures = append(failures, fmt.Errorf("persist PATH entry %q: %w", change.Directory, err))
			}
		}
	}
	if err := addToCurrentProcess(changes); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func addToCurrentProcess(changes []Change) error {
	value := os.Getenv("PATH")
	for _, change := range changes {
		if change.Directory == "" || contains(value, change.Directory, currentOS()) {
			continue
		}
		separator := string(os.PathListSeparator)
		if value == "" {
			value = change.Directory
		} else {
			value += separator + change.Directory
		}
	}
	if err := os.Setenv("PATH", value); err != nil {
		return fmt.Errorf("update PATH for verification: %w", err)
	}
	return nil
}

func contains(pathValue, directory string, operatingSystem tool.OperatingSystem) bool {
	separator := ":"
	if operatingSystem == tool.Windows {
		separator = ";"
	}
	for _, existing := range strings.Split(pathValue, separator) {
		if existing == "" {
			continue
		}
		a, b := filepath.Clean(existing), filepath.Clean(directory)
		if operatingSystem == tool.Windows {
			if strings.EqualFold(a, b) {
				return true
			}
		} else if a == b {
			return true
		}
	}
	return false
}

func startupTarget(machine platform.Info, home string) (string, string, error) {
	switch strings.ToLower(machine.Shell) {
	case "bash", "bash.exe":
		return filepath.Join(home, ".bashrc"), "bash", nil
	case "zsh", "zsh.exe":
		return filepath.Join(home, ".zshrc"), "zsh", nil
	case "fish", "fish.exe":
		return filepath.Join(home, ".config", "fish", "conf.d", "dx-bootstrap-path.fish"), "fish", nil
	case "sh", "dash", "ash":
		return filepath.Join(home, ".profile"), "sh", nil
	default:
		return "", machine.Shell, fmt.Errorf("unsupported or unknown shell %q", machine.Shell)
	}
}

func hasPathBlock(target, directory string) (bool, error) {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("startup path is not a regular file")
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		return false, err
	}
	marker := blockMarker(directory)
	return strings.Contains(string(contents), marker+" begin") && strings.Contains(string(contents), marker+" end"), nil
}

func blockMarker(directory string) string {
	digest := sha256.Sum256([]byte(filepath.Clean(directory)))
	return "# developers-club-dx PATH " + hex.EncodeToString(digest[:8])
}

func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func unixCommand(shell, directory string) string {
	switch strings.ToLower(shell) {
	case "fish", "fish.exe":
		return "set -gx PATH " + quotePOSIX(directory) + " $PATH"
	default:
		return "export PATH=" + quotePOSIX(directory) + ":\"$PATH\""
	}
}

func powershellCommand(directory string) string {
	escaped := strings.ReplaceAll(directory, "'", "''")
	return "[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';' + '" + escaped + "', 'User')"
}

func currentOS() tool.OperatingSystem {
	switch runtime.GOOS {
	case "windows":
		return tool.Windows
	case "darwin":
		return tool.MacOS
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		return tool.BSD
	default:
		return tool.Linux
	}
}
