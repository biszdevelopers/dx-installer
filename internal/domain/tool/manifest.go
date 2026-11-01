package tool

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
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

type ArchiveFormat string

const (
	TarGzip ArchiveFormat = "tar.gz"
	Zip     ArchiveFormat = "zip"
)

type Artifact struct {
	Platform             OperatingSystem `yaml:"platform"`
	Architecture         Architecture    `yaml:"architecture"`
	Version              string          `yaml:"version"`
	URL                  string          `yaml:"url"`
	RegionalURL          string          `yaml:"regional_url,omitempty"`
	SHA256               string          `yaml:"sha256"`
	ApproximateSize      int64           `yaml:"approximate_size,omitempty"`
	Format               ArchiveFormat   `yaml:"format"`
	ArchiveRoot          string          `yaml:"archive_root"`
	PathEntries          []string        `yaml:"path_entries,omitempty"`
	AllowedRedirectHosts []string        `yaml:"allowed_redirect_hosts,omitempty"`
}

type RegistryEcosystem string

const (
	NPMRegistry      RegistryEcosystem = "npm"
	PyPIRegistry     RegistryEcosystem = "pypi"
	GoModuleRegistry RegistryEcosystem = "go-modules"
)

type RegistryInstaller struct {
	Platform  OperatingSystem   `yaml:"platform"`
	Ecosystem RegistryEcosystem `yaml:"ecosystem"`
	PackageID string            `yaml:"package_id"`
	Version   string            `yaml:"version"`
	Scope     Scope             `yaml:"scope"`
	Source    string            `yaml:"source"`
}

type VersionPolicy struct {
	Channel       string `yaml:"channel"`
	UpgradePolicy string `yaml:"upgrade_policy"`
}

type Manifest struct {
	SchemaVersion      int                                `yaml:"schema_version"`
	ID                 string                             `yaml:"id"`
	Name               string                             `yaml:"name"`
	Category           string                             `yaml:"category"`
	Description        string                             `yaml:"description"`
	Tags               []string                           `yaml:"tags"`
	Version            VersionPolicy                      `yaml:"version"`
	Executables        []string                           `yaml:"executables"`
	VersionArgs        []string                           `yaml:"version_args"`
	Dependencies       []string                           `yaml:"dependencies"`
	ProvidedBy         string                             `yaml:"provided_by,omitempty"`
	Conflicts          []string                           `yaml:"conflicts"`
	Platforms          map[OperatingSystem][]Architecture `yaml:"platforms"`
	Installers         []Installer                        `yaml:"installers"`
	RegistryInstallers []RegistryInstaller                `yaml:"registry_installers,omitempty"`
	Artifacts          []Artifact                         `yaml:"artifacts,omitempty"`
}

type Profile struct {
	SchemaVersion int      `yaml:"schema_version"`
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	Tools         []string `yaml:"tools"`
}

var (
	identifierPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	executablePattern  = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)
	packageIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+@/-]{0,127}$`)
	versionArgPattern  = regexp.MustCompile(`^[A-Za-z0-9-][A-Za-z0-9._=:+/-]*$`)
	npmPackagePattern  = regexp.MustCompile(`^(?:@[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?/)?[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$`)
	pypiPackagePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,127})$`)
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

func (m Manifest) ArtifactFor(os OperatingSystem, architecture Architecture) (Artifact, bool) {
	for _, artifact := range m.Artifacts {
		if artifact.Platform == os && artifact.Architecture == architecture {
			return artifact, true
		}
	}
	return Artifact{}, false
}

func (m Manifest) RegistryInstallerFor(os OperatingSystem, ecosystem RegistryEcosystem) (RegistryInstaller, bool) {
	for _, installer := range m.RegistryInstallers {
		if installer.Platform == os && installer.Ecosystem == ecosystem {
			return installer, true
		}
	}
	return RegistryInstaller{}, false
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
		if !versionArgPattern.MatchString(arg) || dangerousVersionArgument(arg) {
			return fmt.Errorf("version argument %q is empty or can execute code", arg)
		}
	}
	if len(m.Platforms) == 0 {
		return fmt.Errorf("at least one supported platform is required")
	}
	if m.ProvidedBy != "" && (!identifierPattern.MatchString(m.ProvidedBy) || m.ProvidedBy == m.ID) {
		return fmt.Errorf("provided_by must name a different catalog tool")
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
	if len(m.Installers) == 0 && len(m.Artifacts) == 0 && m.ProvidedBy == "" {
		if len(m.RegistryInstallers) == 0 {
			return fmt.Errorf("at least one package-manager installer, registry installer, or verified archive source is required")
		}
	}
	seenRegistryInstallers := make(map[string]bool, len(m.RegistryInstallers))
	for _, installer := range m.RegistryInstallers {
		if !validPlatform(installer.Platform) || !hasPlatform(m.Platforms, installer.Platform) {
			return fmt.Errorf("registry installer platform %q is invalid or not declared", installer.Platform)
		}
		if installer.Scope != UserScope {
			return fmt.Errorf("registry installer %q must use user scope", installer.Ecosystem)
		}
		if !validRegistryEcosystem(installer.Ecosystem) {
			return fmt.Errorf("registry installer has unsupported ecosystem %q", installer.Ecosystem)
		}
		if !validRegistryPackageID(installer.Ecosystem, installer.PackageID) {
			return fmt.Errorf("registry installer package identifier %q is invalid", installer.PackageID)
		}
		if !artifactVersionPattern.MatchString(installer.Version) {
			return fmt.Errorf("registry installer for %q must declare a safe exact version", installer.PackageID)
		}
		if err := validateHTTPSURL(installer.Source); err != nil {
			return fmt.Errorf("registry installer for %q has an invalid source: %w", installer.PackageID, err)
		}
		key := string(installer.Platform) + "/" + string(installer.Ecosystem)
		if seenRegistryInstallers[key] {
			return fmt.Errorf("duplicate registry installer for %s", key)
		}
		seenRegistryInstallers[key] = true
	}
	seenArtifacts := make(map[string]bool, len(m.Artifacts))
	for _, artifact := range m.Artifacts {
		key := string(artifact.Platform) + "/" + string(artifact.Architecture)
		if !validPlatform(artifact.Platform) || !validArchitecture(artifact.Architecture) || !m.Supports(artifact.Platform, artifact.Architecture) {
			return fmt.Errorf("artifact %q has an invalid or undeclared platform and architecture", key)
		}
		if seenArtifacts[key] {
			return fmt.Errorf("duplicate artifact for %s", key)
		}
		seenArtifacts[key] = true
		if artifact.Version == "" || !artifactVersionPattern.MatchString(artifact.Version) {
			return fmt.Errorf("artifact %s must declare a safe exact version", key)
		}
		if err := validateHTTPSURL(artifact.URL); err != nil {
			return fmt.Errorf("artifact %s has an invalid official URL: %w", key, err)
		}
		if artifact.RegionalURL != "" {
			if err := validateHTTPSURL(artifact.RegionalURL); err != nil {
				return fmt.Errorf("artifact %s has an invalid regional URL: %w", key, err)
			}
		}
		if len(artifact.SHA256) != 64 {
			return fmt.Errorf("artifact %s must declare a SHA-256 digest", key)
		}
		if _, err := hex.DecodeString(artifact.SHA256); err != nil {
			return fmt.Errorf("artifact %s has an invalid SHA-256 digest", key)
		}
		if artifact.ApproximateSize < 0 || artifact.ApproximateSize > 1<<30 {
			return fmt.Errorf("artifact %s approximate size must be between 0 and 1 GiB", key)
		}
		if artifact.Format != TarGzip && artifact.Format != Zip {
			return fmt.Errorf("artifact %s has unsupported archive format %q", key, artifact.Format)
		}
		if artifact.ArchiveRoot == "" || path.Clean(artifact.ArchiveRoot) != artifact.ArchiveRoot || strings.HasPrefix(artifact.ArchiveRoot, "/") || strings.Contains(artifact.ArchiveRoot, "\\") || artifact.ArchiveRoot == "." || artifact.ArchiveRoot == ".." || strings.HasPrefix(artifact.ArchiveRoot, "../") {
			return fmt.Errorf("artifact %s must declare a safe archive root", key)
		}
		seenPathEntries := make(map[string]bool, len(artifact.PathEntries))
		for _, entry := range artifact.PathEntries {
			if entry == "" || path.Clean(entry) != entry || strings.HasPrefix(entry, "/") || strings.Contains(entry, "\\") || entry == "." || entry == ".." || strings.HasPrefix(entry, "../") {
				return fmt.Errorf("artifact %s has an unsafe PATH entry %q", key, entry)
			}
			if seenPathEntries[entry] {
				return fmt.Errorf("artifact %s has duplicate PATH entry %q", key, entry)
			}
			seenPathEntries[entry] = true
		}
		for _, host := range artifact.AllowedRedirectHosts {
			if !validRedirectHost(host) {
				return fmt.Errorf("artifact %s has an invalid redirect host %q", key, host)
			}
		}
	}
	return nil
}

func validRegistryEcosystem(ecosystem RegistryEcosystem) bool {
	switch ecosystem {
	case NPMRegistry, PyPIRegistry, GoModuleRegistry:
		return true
	default:
		return false
	}
}

func validRegistryPackageID(ecosystem RegistryEcosystem, packageID string) bool {
	switch ecosystem {
	case NPMRegistry:
		return npmPackagePattern.MatchString(packageID)
	case PyPIRegistry:
		return pypiPackagePattern.MatchString(packageID)
	case GoModuleRegistry:
		return packageIDPattern.MatchString(packageID) && !strings.Contains(packageID, "@")
	default:
		return false
	}
}

func RegistryRuntimeTool(ecosystem RegistryEcosystem) string {
	switch ecosystem {
	case NPMRegistry:
		return "nodejs-lts"
	case PyPIRegistry:
		return "uv"
	case GoModuleRegistry:
		return "go"
	default:
		return ""
	}
}

var artifactVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,63}$`)

func validateHTTPSURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("URL must use HTTPS and contain no credentials or fragment")
	}
	return nil
}

func validRedirectHost(host string) bool {
	return host != "" && strings.TrimSpace(host) == host && !strings.ContainsAny(host, "/:@?#*")
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
	case Homebrew:
		return os == MacOS || os == Linux
	case MacPorts:
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
