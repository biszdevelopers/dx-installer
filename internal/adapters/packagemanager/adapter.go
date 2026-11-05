package packagemanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"developers-club-dx/internal/domain/tool"
)

type Detected struct {
	Manager         tool.PackageManager
	Path            string
	OperatingSystem tool.OperatingSystem
}

type Command struct {
	Executable        string
	Arguments         []string
	Environment       map[string]string
	RequiresPrivilege bool
	PrivilegeWrapper  string
}

func Detect(os tool.OperatingSystem, distribution string) []Detected {
	for _, manager := range managerOrder(os, distribution) {
		binary := managerExecutable(manager)
		path, err := exec.LookPath(binary)
		if err != nil {
			continue
		}
		return append([]Detected{{Manager: manager, Path: path, OperatingSystem: os}}, detectRemaining(os, distribution, manager)...)
	}
	return nil
}

func detectRemaining(os tool.OperatingSystem, distribution string, first tool.PackageManager) []Detected {
	var found []Detected
	for _, manager := range managerOrder(os, distribution) {
		if manager == first {
			continue
		}
		path, err := exec.LookPath(managerExecutable(manager))
		if err == nil {
			found = append(found, Detected{Manager: manager, Path: path, OperatingSystem: os})
		}
	}
	return found
}

func managerOrder(os tool.OperatingSystem, distribution string) []tool.PackageManager {
	switch os {
	case tool.Windows:
		return []tool.PackageManager{tool.Winget, tool.Scoop, tool.Chocolatey, tool.AndroidSDK, tool.EspressifEIM}
	case tool.MacOS:
		return []tool.PackageManager{tool.Homebrew, tool.MacPorts, tool.AndroidSDK, tool.EspressifEIM}
	case tool.Linux:
		id := strings.Fields(strings.ToLower(distribution))
		if len(id) > 0 {
			switch id[0] {
			case "debian", "ubuntu", "linuxmint", "pop", "elementary", "raspbian":
				return []tool.PackageManager{tool.APT, tool.DNF, tool.YUM, tool.Pacman, tool.Zypper, tool.APK, tool.AndroidSDK, tool.EspressifEIM, tool.Homebrew}
			case "fedora", "rhel", "centos", "rocky", "almalinux":
				return []tool.PackageManager{tool.DNF, tool.YUM, tool.APT, tool.Pacman, tool.Zypper, tool.APK, tool.AndroidSDK, tool.EspressifEIM, tool.Homebrew}
			case "arch", "manjaro", "endeavouros":
				return []tool.PackageManager{tool.Pacman, tool.APT, tool.DNF, tool.YUM, tool.Zypper, tool.APK, tool.AndroidSDK, tool.EspressifEIM, tool.Homebrew}
			case "opensuse", "opensuse-leap", "opensuse-tumbleweed", "sles":
				return []tool.PackageManager{tool.Zypper, tool.APT, tool.DNF, tool.YUM, tool.Pacman, tool.APK, tool.AndroidSDK, tool.EspressifEIM, tool.Homebrew}
			case "alpine":
				return []tool.PackageManager{tool.APK, tool.APT, tool.DNF, tool.YUM, tool.Pacman, tool.Zypper, tool.AndroidSDK, tool.EspressifEIM, tool.Homebrew}
			}
		}
		return []tool.PackageManager{tool.APT, tool.DNF, tool.YUM, tool.Pacman, tool.Zypper, tool.APK, tool.AndroidSDK, tool.EspressifEIM, tool.Homebrew}
	case tool.BSD:
		if strings.EqualFold(strings.TrimSpace(distribution), "freebsd") {
			return []tool.PackageManager{tool.FreeBSDPkg}
		}
		return nil
	default:
		return nil
	}
}

func managerExecutable(manager tool.PackageManager) string {
	switch manager {
	case tool.Winget:
		return "winget"
	case tool.Scoop:
		return "scoop"
	case tool.Chocolatey:
		return "choco"
	case tool.Homebrew:
		return "brew"
	case tool.MacPorts:
		return "port"
	case tool.APT:
		return "apt-get"
	case tool.DNF:
		return "dnf"
	case tool.YUM:
		return "yum"
	case tool.Pacman:
		return "pacman"
	case tool.Zypper:
		return "zypper"
	case tool.APK:
		return "apk"
	case tool.FreeBSDPkg:
		return "pkg"
	case tool.AndroidSDK:
		return "sdkmanager"
	case tool.EspressifEIM:
		return "eim"
	default:
		return ""
	}
}

func BuildCommand(installer tool.Installer, currentOS tool.OperatingSystem) (Command, error) {
	command := Command{Executable: managerExecutable(installer.Manager)}
	if command.Executable == "" {
		return Command{}, fmt.Errorf("unsupported package manager %q", installer.Manager)
	}
	packageID := installer.PackageID
	switch installer.Manager {
	case tool.Winget:
		if len(installer.Workloads) > 0 && (!strings.EqualFold(packageID, "Microsoft.VisualStudio.BuildTools") || len(installer.Workloads) != 1 || installer.Workloads[0] != "Microsoft.VisualStudio.Workload.VCTools") {
			return Command{}, fmt.Errorf("unsupported WinGet workload selection for package %q", packageID)
		}
		if strings.EqualFold(packageID, "Microsoft.VisualStudio.BuildTools") && (len(installer.Workloads) != 1 || installer.Workloads[0] != "Microsoft.VisualStudio.Workload.VCTools") {
			return Command{}, errors.New("Visual Studio Build Tools requires the declared C++ workload")
		}
		command.Arguments = []string{"install", "--id", packageID, "--exact", "--no-upgrade"}
		if installer.Source != "" {
			command.Arguments = append(command.Arguments, "--source", installer.Source)
		}
		if installer.Scope == tool.UserScope {
			command.Arguments = append(command.Arguments, "--scope", "user")
		} else {
			command.Arguments = append(command.Arguments, "--scope", "machine")
			command.RequiresPrivilege = true
		}
		if len(installer.Workloads) == 1 {
			override := "--passive --wait --norestart --nocache --add " + installer.Workloads[0] + " --includeRecommended"
			command.Arguments = append(command.Arguments, "--override", override)
		}
	case tool.Scoop:
		if installer.Scope != tool.UserScope {
			return Command{}, fmt.Errorf("Scoop only supports user-scoped installs")
		}
		command.Arguments = []string{"install", packageID}
	case tool.Chocolatey:
		command.Arguments = []string{"install", packageID, "--no-progress"}
		command.RequiresPrivilege = true
	case tool.Homebrew:
		command.Arguments = []string{"install"}
		if installer.Kind == "cask" {
			command.Arguments = append(command.Arguments, "--cask")
		}
		command.Arguments = append(command.Arguments, packageID)
	case tool.MacPorts:
		command.Arguments = []string{"install", packageID}
		command.RequiresPrivilege = true
	case tool.APT:
		command.Arguments = []string{"install", "--no-install-recommends", packageID}
		command.RequiresPrivilege = true
	case tool.DNF:
		command.Arguments = []string{"install", packageID}
		command.RequiresPrivilege = true
	case tool.YUM:
		command.Arguments = []string{"install", packageID}
		command.RequiresPrivilege = true
	case tool.Pacman:
		command.Arguments = []string{"-S", "--needed", packageID}
		command.RequiresPrivilege = true
	case tool.Zypper:
		command.Arguments = []string{"install", "--no-recommends", packageID}
		command.RequiresPrivilege = true
	case tool.APK:
		command.Arguments = []string{"add", packageID}
		command.RequiresPrivilege = true
	case tool.FreeBSDPkg:
		command.Arguments = []string{"install", packageID}
		command.RequiresPrivilege = true
	case tool.AndroidSDK:
		if !tool.ValidAndroidSDKPackageID(packageID) {
			return Command{}, fmt.Errorf("unsupported Android SDK package identifier %q", packageID)
		}
		command.Arguments = []string{packageID}
	case tool.EspressifEIM:
		if !tool.ValidEspressifEIMVersion(packageID) {
			return Command{}, fmt.Errorf("unsupported ESP-IDF version identifier %q", packageID)
		}
		if installer.Scope != tool.UserScope {
			return Command{}, errors.New("Espressif EIM only supports user-scoped installs")
		}
		home, err := os.UserHomeDir()
		if err != nil || !filepath.IsAbs(home) {
			return Command{}, errors.New("could not determine an absolute user home directory for ESP-IDF")
		}
		root := filepath.Join(home, ".espressif")
		toolsDirectory := filepath.Join(root, "tools")
		command.Arguments = []string{
			"--do-not-track", "true", "--log-file", os.DevNull,
			"install", "--idf-versions", packageID, "--path", root, "--cleanup", "true",
			"--tool-install-folder-name", toolsDirectory,
			"--tool-download-folder-name", filepath.Join(root, "dist"),
			"--activation-script-path-override", toolsDirectory,
			"--esp-idf-json-path", toolsDirectory,
		}
	}
	if command.RequiresPrivilege && currentOS != tool.Windows && !isRoot() {
		command.PrivilegeWrapper = "sudo"
	}
	return command, nil
}

func (c Command) Argv() []string {
	if c.PrivilegeWrapper == "" {
		return append([]string{c.Executable}, c.Arguments...)
	}
	return append([]string{c.PrivilegeWrapper, c.Executable}, c.Arguments...)
}

func (c Command) Display() string {
	parts := make([]string, 0, len(c.Arguments)+2)
	if c.PrivilegeWrapper != "" {
		parts = append(parts, c.PrivilegeWrapper)
	}
	parts = append(parts, c.Executable)
	for _, arg := range c.Arguments {
		parts = append(parts, strconv.Quote(arg))
	}
	return strings.Join(parts, " ")
}

func isRoot() bool {
	current, err := user.Current()
	return err == nil && current.Uid == "0"
}

func (c Command) Exec() (*exec.Cmd, error) {
	return c.ExecContext(context.Background())
}

func (c Command) ExecContext(ctx context.Context) (*exec.Cmd, error) {
	if runtime.GOOS == "windows" && (strings.EqualFold(filepath.Ext(c.Executable), ".bat") || strings.EqualFold(filepath.Ext(c.Executable), ".cmd")) {
		return execBatchCommand(ctx, c)
	}
	var command *exec.Cmd
	if c.PrivilegeWrapper != "" {
		if runtime.GOOS == "windows" {
			return nil, fmt.Errorf("Unix privilege wrapper cannot run on Windows")
		}
		command = exec.CommandContext(ctx, c.PrivilegeWrapper, append([]string{c.Executable}, c.Arguments...)...)
	} else {
		command = exec.CommandContext(ctx, c.Executable, c.Arguments...)
	}
	if len(c.Environment) > 0 {
		command.Env = mergedEnvironment(os.Environ(), c.Environment)
	}
	return command, nil
}

func mergedEnvironment(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	normalize := func(key string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(key)
		}
		return key
	}
	for _, entry := range base {
		key, value, found := strings.Cut(entry, "=")
		if found {
			values[normalize(key)] = key + "=" + value
		}
	}
	for key, value := range overrides {
		values[normalize(key)] = key + "=" + value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, values[key])
	}
	return result
}
