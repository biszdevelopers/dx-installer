package packagemanager

import (
	"context"
	"fmt"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"strings"

	"developers-club-dx/internal/domain/tool"
)

type Detected struct {
	Manager tool.PackageManager
	Path    string
}

type Command struct {
	Executable        string
	Arguments         []string
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
		return append([]Detected{{Manager: manager, Path: path}}, detectRemaining(os, distribution, manager)...)
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
			found = append(found, Detected{Manager: manager, Path: path})
		}
	}
	return found
}

func managerOrder(os tool.OperatingSystem, distribution string) []tool.PackageManager {
	switch os {
	case tool.Windows:
		return []tool.PackageManager{tool.Winget, tool.Scoop, tool.Chocolatey}
	case tool.MacOS:
		return []tool.PackageManager{tool.Homebrew, tool.MacPorts}
	case tool.Linux:
		id := strings.Fields(strings.ToLower(distribution))
		if len(id) > 0 {
			switch id[0] {
			case "debian", "ubuntu", "linuxmint", "pop", "elementary", "raspbian":
				return []tool.PackageManager{tool.APT, tool.DNF, tool.Pacman, tool.Zypper, tool.APK}
			case "fedora", "rhel", "centos", "rocky", "almalinux":
				return []tool.PackageManager{tool.DNF, tool.APT, tool.Pacman, tool.Zypper, tool.APK}
			case "arch", "manjaro", "endeavouros":
				return []tool.PackageManager{tool.Pacman, tool.APT, tool.DNF, tool.Zypper, tool.APK}
			case "opensuse", "opensuse-leap", "opensuse-tumbleweed", "sles":
				return []tool.PackageManager{tool.Zypper, tool.APT, tool.DNF, tool.Pacman, tool.APK}
			case "alpine":
				return []tool.PackageManager{tool.APK, tool.APT, tool.DNF, tool.Pacman, tool.Zypper}
			}
		}
		return []tool.PackageManager{tool.APT, tool.DNF, tool.Pacman, tool.Zypper, tool.APK}
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
	case tool.Pacman:
		return "pacman"
	case tool.Zypper:
		return "zypper"
	case tool.APK:
		return "apk"
	case tool.FreeBSDPkg:
		return "pkg"
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
		command.Arguments = []string{"install", "--id", packageID, "--exact"}
		if installer.Source != "" {
			command.Arguments = append(command.Arguments, "--source", installer.Source)
		}
		if installer.Scope == tool.UserScope {
			command.Arguments = append(command.Arguments, "--scope", "user")
		} else {
			command.RequiresPrivilege = true
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
	if c.PrivilegeWrapper != "" {
		if runtime.GOOS == "windows" {
			return nil, fmt.Errorf("Unix privilege wrapper cannot run on Windows")
		}
		return exec.CommandContext(ctx, c.PrivilegeWrapper, append([]string{c.Executable}, c.Arguments...)...), nil
	}
	return exec.CommandContext(ctx, c.Executable, c.Arguments...), nil
}
