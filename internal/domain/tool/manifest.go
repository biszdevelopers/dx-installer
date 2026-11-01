package tool

import (
	"fmt"
	"regexp"
)

type OperatingSystem string

const (
	Windows OperatingSystem = "windows"
	MacOS   OperatingSystem = "macos"
	Linux   OperatingSystem = "linux"
	BSD     OperatingSystem = "bsd"
)

type Architecture string

const (
	AMD64   Architecture = "amd64"
	ARM64   Architecture = "arm64"
	I386    Architecture = "386"
	ARM     Architecture = "arm"
	RISCV64 Architecture = "riscv64"
)

type PackageManager string

const (
	Winget     PackageManager = "winget"
	Scoop      PackageManager = "scoop"
	Chocolatey PackageManager = "chocolatey"
	Homebrew   PackageManager = "homebrew"
	MacPorts   PackageManager = "macports"
	APT        PackageManager = "apt"
	DNF        PackageManager = "dnf"
	Pacman     PackageManager = "pacman"
	Zypper     PackageManager = "zypper"
	APK        PackageManager = "apk"
	FreeBSDPkg PackageManager = "freebsd-pkg"
)

type Scope string

const (
	UserScope   Scope = "user"
	SystemScope Scope = "system"
)

type Installer struct {
	Platform  OperatingSystem `yaml:"platform"`
	Manager   PackageManager  `yaml:"manager"`
	PackageID string          `yaml:"package_id"`
	Kind      string          `yaml:"kind"`
	Scope     Scope           `yaml:"scope"`
	Source    string          `yaml:"source"`
}

type VersionPolicy struct {
	Channel       string `yaml:"channel"`
	UpgradePolicy string `yaml:"upgrade_policy"`
}

type Manifest struct {
	SchemaVersion int                                `yaml:"schema_version"`
	ID            string                             `yaml:"id"`
	Name          string                             `yaml:"name"`
	Category      string                             `yaml:"category"`
	Description   string                             `yaml:"description"`
	Tags          []string                           `yaml:"tags"`
	Version       VersionPolicy                      `yaml:"version"`
	Executables   []string                           `yaml:"executables"`
	VersionArgs   []string                           `yaml:"version_args"`
	Dependencies  []string                           `yaml:"dependencies"`
	Conflicts     []string                           `yaml:"conflicts"`
	Platforms     map[OperatingSystem][]Architecture `yaml:"platforms"`
	Installers    []Installer                        `yaml:"installers"`
}

type Profile struct {
	SchemaVersion int      `yaml:"schema_version"`
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	Tools         []string `yaml:"tools"`
}

var (
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	executablePattern = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)
	packageIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+@/-]{0,127}$`)
)

func (m Manifest) Supports(os OperatingSystem, architecture Architecture) bool {
	for _, supported := range m.Platforms[os] {
		if supported == architecture {
			return true
		}
	}
	return false
}

func (m Manifest) InstallerFor(os OperatingSystem, managers []PackageManager) (Installer, bool) {
	for _, manager := range managers {
		for _, preferredScope := range []Scope{UserScope, SystemScope} {
			for _, installer := range m.Installers {
				if installer.Platform == os && installer.Manager == manager && installer.Scope == preferredScope {
					return installer, true
				}
			}
		}
	}
	return Installer{}, false
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1, got %d", m.SchemaVersion)
	}
	if !identifierPattern.MatchString(m.ID) {
		return fmt.Errorf("id %q must be a lowercase identifier", m.ID)
	}
	if m.Name == "" || m.Category == "" || m.Description == "" {
		return fmt.Errorf("name, category, and description are required")
	}
	if m.Version.Channel == "" || m.Version.UpgradePolicy != "never_automatically" {
		return fmt.Errorf("version.channel is required and upgrade_policy must be never_automatically")
	}
	if len(m.Executables) == 0 || len(m.VersionArgs) == 0 {
		return fmt.Errorf("at least one executable and version argument are required")
	}
	for _, executable := range m.Executables {
		if !executablePattern.MatchString(executable) || executable == "." || executable == ".." {
			return fmt.Errorf("executable %q is not a plain executable name", executable)
		}
	}
	for _, arg := range m.VersionArgs {
		if arg == "" || dangerousVersionArgument(arg) {
			return fmt.Errorf("version argument %q is empty or can execute code", arg)
		}
	}
	if len(m.Platforms) == 0 {
		return fmt.Errorf("at least one supported platform is required")
	}
	for os, architectures := range m.Platforms {
		if !validPlatform(os) || len(architectures) == 0 {
			return fmt.Errorf("platform %q is unknown or has no architectures", os)
		}
		seen := make(map[Architecture]bool, len(architectures))
		for _, architecture := range architectures {
			if !validArchitecture(architecture) || seen[architecture] {
				return fmt.Errorf("platform %q has unknown or duplicate architecture %q", os, architecture)
			}
			seen[architecture] = true
		}
	}
	seenInstallers := make(map[string]bool, len(m.Installers))
	for _, installer := range m.Installers {
		if !validPlatform(installer.Platform) || !validManager(installer.Manager) || !packageIDPattern.MatchString(installer.PackageID) {
			return fmt.Errorf("installer %q/%q has an invalid platform, manager, or package identifier", installer.Platform, installer.Manager)
		}
		if installer.Scope != UserScope && installer.Scope != SystemScope {
			return fmt.Errorf("installer %q/%q must declare user or system scope", installer.Platform, installer.Manager)
		}
		if !managerSupportsScope(installer.Manager, installer.Scope) {
			return fmt.Errorf("manager %q cannot use %q scope", installer.Manager, installer.Scope)
		}
		if installer.Source == "" {
			return fmt.Errorf("installer %q/%q must identify its package source", installer.Platform, installer.Manager)
		}
		if installer.Kind != "" && installer.Kind != "package" && !(installer.Manager == Homebrew && installer.Kind == "cask") {
			return fmt.Errorf("installer %q/%q has an unsupported package kind %q", installer.Platform, installer.Manager, installer.Kind)
		}
		if !hasPlatform(m.Platforms, installer.Platform) {
			return fmt.Errorf("installer platform %q is not declared in platforms", installer.Platform)
		}
		if !managerSupportsPlatform(installer.Manager, installer.Platform) {
			return fmt.Errorf("manager %q cannot install on platform %q", installer.Manager, installer.Platform)
		}
		key := string(installer.Platform) + "/" + string(installer.Manager)
		if seenInstallers[key] {
			return fmt.Errorf("duplicate installer for %s", key)
		}
		seenInstallers[key] = true
	}
	if len(m.Installers) == 0 {
		return fmt.Errorf("at least one trusted package-manager installer is required")
	}
	return nil
}

func dangerousVersionArgument(arg string) bool {
	switch arg {
	case "-c", "--command", "--eval", "-e", "/c", "-Command", "-EncodedCommand":
		return true
	default:
		return false
	}
}

func validPlatform(os OperatingSystem) bool {
	switch os {
	case Windows, MacOS, Linux, BSD:
		return true
	default:
		return false
	}
}

func validArchitecture(architecture Architecture) bool {
	switch architecture {
	case AMD64, ARM64, I386, ARM, RISCV64:
		return true
	default:
		return false
	}
}

func validManager(manager PackageManager) bool {
	switch manager {
	case Winget, Scoop, Chocolatey, Homebrew, MacPorts, APT, DNF, Pacman, Zypper, APK, FreeBSDPkg:
		return true
	default:
		return false
	}
}

func managerSupportsPlatform(manager PackageManager, os OperatingSystem) bool {
	switch manager {
	case Winget, Scoop, Chocolatey:
		return os == Windows
	case Homebrew, MacPorts:
		return os == MacOS
	case APT, DNF, Pacman, Zypper, APK:
		return os == Linux
	case FreeBSDPkg:
		return os == BSD
	default:
		return false
	}
}

func managerSupportsScope(manager PackageManager, scope Scope) bool {
	switch manager {
	case Winget:
		return scope == UserScope || scope == SystemScope
	case Scoop, Homebrew:
		return scope == UserScope
	case Chocolatey, MacPorts, APT, DNF, Pacman, Zypper, APK, FreeBSDPkg:
		return scope == SystemScope
	default:
		return false
	}
}

func hasPlatform(platforms map[OperatingSystem][]Architecture, os OperatingSystem) bool {
	_, exists := platforms[os]
	return exists
}
