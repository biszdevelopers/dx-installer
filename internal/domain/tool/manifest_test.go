package tool

import (
	"strings"
	"testing"
)

func TestManifestAcceptsTypedWindowsInstaller(t *testing.T) {
	manifest := validManifest()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if !manifest.Supports(Windows, AMD64) || manifest.Supports(MacOS, AMD64) {
		t.Fatal("platform support does not match the manifest")
	}
	installer, exists := manifest.InstallerFor(Windows, []PackageManager{Scoop, Winget})
	if !exists || installer.Manager != Scoop {
		t.Fatalf("installer selection did not respect manager order: %#v, %v", installer, exists)
	}
}

func TestInstallerSelectionRespectsArchitectureAvailability(t *testing.T) {
	manifest := validManifest()
	manifest.Platforms = map[OperatingSystem][]Architecture{MacOS: {AMD64, ARM64}}
	manifest.Installers = []Installer{
		{Platform: MacOS, Manager: Homebrew, PackageID: "sample", Scope: UserScope, Source: "homebrew-core", Architectures: []Architecture{ARM64}},
		{Platform: MacOS, Manager: MacPorts, PackageID: "sample", Scope: SystemScope, Source: "macports", Architectures: []Architecture{AMD64}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if installer, exists := manifest.InstallerForPlatform(MacOS, AMD64, []PackageManager{Homebrew}); exists {
		t.Fatalf("Intel Mac selected an ARM-only Homebrew installer: %#v", installer)
	}
	if installer, exists := manifest.InstallerForPlatform(MacOS, AMD64, []PackageManager{MacPorts, Homebrew}); !exists || installer.Manager != MacPorts {
		t.Fatalf("Intel Mac did not select MacPorts: %#v, %v", installer, exists)
	}
	if installer, exists := manifest.InstallerForPlatform(MacOS, ARM64, []PackageManager{MacPorts, Homebrew}); !exists || installer.Manager != Homebrew {
		t.Fatalf("Apple Silicon did not select Homebrew: %#v, %v", installer, exists)
	}
}

func TestManifestValidatesHealthPattern(t *testing.T) {
	manifest := validManifest()
	manifest.HealthPattern = `(?m)^Microsoft\.NETCore\.App\s+(10\.[0-9.]+)`
	if err := manifest.Validate(); err != nil {
		t.Fatalf("valid health pattern was rejected: %v", err)
	}
	manifest.HealthPattern = "["
	if err := manifest.Validate(); err == nil {
		t.Fatal("invalid health pattern was accepted")
	}
}

func TestManifestAcceptsScopedNPMRegistryPackages(t *testing.T) {
	manifest := Manifest{
		SchemaVersion: 1,
		ID:            "changesets-cli",
		Name:          "Changesets CLI",
		Category:      "javascript.release",
		Description:   "Version and changelog management for monorepositories.",
		Version:       VersionPolicy{Channel: "stable", UpgradePolicy: "never_automatically"},
		Executables:   []string{"changeset"},
		VersionArgs:   []string{"--version"},
		Dependencies:  []string{"nodejs-lts"},
		Platforms:     map[OperatingSystem][]Architecture{Windows: {AMD64}},
		RegistryInstallers: []RegistryInstaller{{
			Platform: Windows, Ecosystem: NPMRegistry, PackageID: "@changesets/cli", Version: "3.0.3", Scope: UserScope, Source: "https://registry.npmjs.org/",
		}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("scoped npm package manifest was rejected: %v", err)
	}
	manifest.RegistryInstallers[0].PackageID = "@scope/../escape"
	if err := manifest.Validate(); err == nil {
		t.Fatal("unsafe scoped npm package path was accepted")
	}
}

func TestManifestAcceptsSafePyPIExtras(t *testing.T) {
	manifest := Manifest{
		SchemaVersion: 1,
		ID:            "hypothesis",
		Name:          "Hypothesis",
		Category:      "python.testing",
		Description:   "Property-based testing for Python.",
		Version:       VersionPolicy{Channel: "stable", UpgradePolicy: "never_automatically"},
		Executables:   []string{"hypothesis"},
		VersionArgs:   []string{"--version"},
		Dependencies:  []string{"python", "uv"},
		Platforms:     map[OperatingSystem][]Architecture{Windows: {AMD64}},
		RegistryInstallers: []RegistryInstaller{{
			Platform: Windows, Ecosystem: PyPIRegistry, PackageID: "hypothesis[cli]", Version: "6.170.14", Scope: UserScope, Source: "https://pypi.org/simple/",
		}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("safe PyPI extra was rejected: %v", err)
	}
	manifest.RegistryInstallers[0].PackageID = "hypothesis[cli;Remove-Item]"
	if err := manifest.Validate(); err == nil {
		t.Fatal("unsafe PyPI extra was accepted")
	}
}

func TestManifestAcceptsCargoCrateRegistryInstallers(t *testing.T) {
	manifest := Manifest{
		SchemaVersion: 1,
		ID:            "cargo-nextest",
		Name:          "cargo-nextest",
		Category:      "rust.testing",
		Description:   "Next-generation Rust test runner.",
		Version:       VersionPolicy{Channel: "stable", UpgradePolicy: "never_automatically"},
		Executables:   []string{"cargo"},
		VersionArgs:   []string{"nextest", "--version"},
		Dependencies:  []string{"rustup"},
		Platforms:     map[OperatingSystem][]Architecture{Windows: {AMD64}},
		RegistryInstallers: []RegistryInstaller{{
			Platform: Windows, Ecosystem: CargoRegistry, PackageID: "cargo-nextest", Version: "0.9.131", Scope: UserScope, Source: "https://index.crates.io/",
		}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("cargo crate manifest was rejected: %v", err)
	}
	manifest.RegistryInstallers[0].PackageID = "../cargo-nextest"
	if err := manifest.Validate(); err == nil {
		t.Fatal("unsafe cargo crate package ID was accepted")
	}
}

func TestManifestRejectsExecutableCodeAndUnmanagedUpgradePolicy(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Manifest)
	}{
		{"command argument", func(manifest *Manifest) { manifest.VersionArgs = []string{"-Command", "Start-Process"} }},
		{"automatic upgrade", func(manifest *Manifest) { manifest.Version.UpgradePolicy = "latest" }},
		{"unsafe executable path", func(manifest *Manifest) { manifest.Executables = []string{"../sample"} }},
		{"malformed package ID", func(manifest *Manifest) { manifest.Installers[0].PackageID = "Vendor;Remove-Item" }},
		{"scope not supported by manager", func(manifest *Manifest) { manifest.Installers[1].Scope = SystemScope }},
		{"manager and platform mismatch", func(manifest *Manifest) { manifest.Installers[0].Manager = APT }},
		{"missing package source", func(manifest *Manifest) { manifest.Installers[0].Source = "" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			manifest := validManifest()
			test.change(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}
}

func TestManifestRequiresInstallerPlatformToBeDeclared(t *testing.T) {
	manifest := validManifest()
	manifest.Installers[0].Platform = MacOS
	if err := manifest.Validate(); err == nil {
		t.Fatal("installer for an undeclared platform was accepted")
	}
}

func TestManifestAcceptsVerifiedArchiveInstallersAndLooksUpExactPlatform(t *testing.T) {
	manifest := validManifest()
	manifest.Artifacts = []Artifact{{
		Platform: Windows, Architecture: AMD64, Version: "1.2.3",
		URL: "https://downloads.example/sample.zip", SHA256: "4c5a4f9afca56f9c04b1f6c93a7f70947aa41a279c1d128f0136f726c1ddaf4d",
		ApproximateSize: 128, Format: Zip, ArchiveRoot: "sample", PathEntries: []string{"bin"},
		AllowedRedirectHosts: []string{"cdn.example"},
	}}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	got, exists := manifest.ArtifactFor(Windows, AMD64)
	if !exists || got.Version != "1.2.3" {
		t.Fatalf("artifact lookup = %#v, %v", got, exists)
	}
	if _, exists := manifest.ArtifactFor(Windows, ARM64); exists {
		t.Fatal("artifact was returned for an undeclared architecture")
	}
}

func TestManifestAcceptsZephyrSDKArchiveMetadataLargerThanOneGiB(t *testing.T) {
	manifest := Manifest{
		SchemaVersion: 1,
		ID:            "zephyr-sdk",
		Name:          "Zephyr SDK",
		Category:      "embedded.tooling",
		Description:   "Zephyr SDK GNU toolchains and host tools.",
		Version:       VersionPolicy{Channel: "stable", UpgradePolicy: "never_automatically"},
		Executables:   []string{"arm-zephyr-eabi-gcc"},
		VersionArgs:   []string{"--version"},
		Dependencies:  []string{"cmake"},
		Platforms:     map[OperatingSystem][]Architecture{Linux: {AMD64}},
		Artifacts: []Artifact{{
			Platform: Linux, Architecture: AMD64, Version: "1.0.1",
			URL:             "https://github.com/zephyrproject-rtos/sdk-ng/releases/download/v1.0.1/zephyr-sdk-1.0.1_linux-x86_64_gnu.tar.xz",
			SHA256:          "37a8c5b5569c2b482d39099c65fe3ddac4e43524996c731907e1e9f5818cbba7",
			ApproximateSize: 1_990_000_000, MaxDownloadBytes: 2_500_000_000, MaxExpandedBytes: 16 << 30,
			Format: TarXZ, ArchiveRoot: "zephyr-sdk-1.0.1", VersionFile: "sdk_version",
			PostInstallAction: ZephyrSDKSetup, InstallationNote: "Runs the bundled setup script for all GNU toolchains and host tools.",
		}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("large Zephyr SDK artifact was rejected: %v", err)
	}
	manifest.Artifacts[0].Format = SevenZip
	if err := manifest.Validate(); err != nil {
		t.Fatalf("7z SDK archive was rejected: %v", err)
	}
}

func TestManifestRejectsUnsafeArchiveSources(t *testing.T) {
	base := Artifact{
		Platform: Windows, Architecture: AMD64, Version: "1.2.3",
		URL: "https://downloads.example/sample.zip", SHA256: "4c5a4f9afca56f9c04b1f6c93a7f70947aa41a279c1d128f0136f726c1ddaf4d",
		ApproximateSize: 128, Format: Zip, ArchiveRoot: "sample",
	}
	for _, testCase := range []struct {
		name   string
		change func(*Artifact)
	}{
		{"plaintext URL", func(item *Artifact) { item.URL = "http://downloads.example/sample.zip" }},
		{"URL credentials", func(item *Artifact) { item.URL = "https://user:pass@downloads.example/sample.zip" }},
		{"missing checksum", func(item *Artifact) { item.SHA256 = "" }},
		{"malformed checksum", func(item *Artifact) { item.SHA256 = strings.Repeat("z", 64) }},
		{"negative size", func(item *Artifact) { item.ApproximateSize = -1 }},
		{"download limit below estimate", func(item *Artifact) { item.MaxDownloadBytes = 127 }},
		{"download limit over maximum", func(item *Artifact) { item.MaxDownloadBytes = 9 << 30 }},
		{"expanded limit over maximum", func(item *Artifact) { item.MaxExpandedBytes = 33 << 30 }},
		{"unsafe archive root", func(item *Artifact) { item.ArchiveRoot = "../outside" }},
		{"unsupported archive", func(item *Artifact) { item.Format = "exe" }},
		{"invalid redirect host", func(item *Artifact) { item.AllowedRedirectHosts = []string{"*.example"} }},
		{"regional URL is plaintext", func(item *Artifact) { item.RegionalURL = "http://mirror.example/sample.zip" }},
		{"unsafe PATH entry", func(item *Artifact) { item.PathEntries = []string{"../../outside"} }},
		{"unsafe version file", func(item *Artifact) { item.VersionFile = "../../outside" }},
		{"version file uses alternate data stream syntax", func(item *Artifact) { item.VersionFile = "sdk_version:stream" }},
		{"unknown install action", func(item *Artifact) { item.PostInstallAction = "arbitrary-shell" }},
		{"duplicate PATH entry", func(item *Artifact) { item.PathEntries = []string{"bin", "bin"} }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := validManifest()
			artifact := base
			testCase.change(&artifact)
			manifest.Artifacts = []Artifact{artifact}
			if err := manifest.Validate(); err == nil {
				t.Fatal("unsafe archive source was accepted")
			}
		})
	}
}

func TestManagerSelectionPrefersUserScopeOverSystemScope(t *testing.T) {
	manifest := validManifest()
	manifest.Installers = append(manifest.Installers, Installer{
		Platform: Windows, Manager: Winget, PackageID: "Example.Sample", Scope: SystemScope, Source: "winget",
	})
	installer, exists := manifest.InstallerFor(Windows, []PackageManager{Winget})
	if !exists || installer.Scope != UserScope {
		t.Fatalf("system scope was selected when user scope was available: %#v, %v", installer, exists)
	}
}

func TestManagerPreferenceTakesPriorityOverScope(t *testing.T) {
	manifest := validManifest()
	manifest.Platforms = map[OperatingSystem][]Architecture{MacOS: {AMD64}}
	manifest.Installers = []Installer{
		{Platform: MacOS, Manager: Homebrew, PackageID: "sample", Scope: UserScope, Source: "homebrew-core"},
		{Platform: MacOS, Manager: MacPorts, PackageID: "sample", Scope: SystemScope, Source: "macports-ports"},
	}
	installer, exists := manifest.InstallerFor(MacOS, []PackageManager{MacPorts, Homebrew})
	if !exists || installer.Manager != MacPorts || installer.Scope != SystemScope {
		t.Fatalf("requested package manager was not preferred over another manager's user scope: %#v, %v", installer, exists)
	}
}

func TestManifestAcceptsLinuxHomebrewInstallers(t *testing.T) {
	manifest := validManifest()
	manifest.Platforms[Linux] = []Architecture{AMD64, ARM64}
	manifest.Installers = append(manifest.Installers,
		Installer{Platform: Linux, Manager: Homebrew, PackageID: "sample", Scope: UserScope, Source: "homebrew-core"},
	)
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if installer, exists := manifest.InstallerFor(Linux, []PackageManager{Homebrew}); !exists || installer.Platform != Linux {
		t.Fatalf("Linux Homebrew installer = %#v, %v", installer, exists)
	}
}

func TestManifestValidatesHomebrewFormulaRelativePATHEntries(t *testing.T) {
	manifest := validManifest()
	manifest.Platforms = map[OperatingSystem][]Architecture{MacOS: {ARM64}}
	manifest.Installers = []Installer{{
		Platform: MacOS, Manager: Homebrew, PackageID: "python@3.15", Scope: UserScope, Source: "homebrew-core", PathEntries: []string{"bin", "libexec/bin"},
	}}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("valid formula-relative PATH entries were rejected: %v", err)
	}
	manifest.Installers[0].PathEntries = []string{"../outside"}
	if err := manifest.Validate(); err == nil {
		t.Fatal("formula-relative PATH traversal was accepted")
	}
	manifest.Installers[0].PathEntries = []string{"bin"}
	manifest.Installers[0].Manager = Winget
	if err := manifest.Validate(); err == nil {
		t.Fatal("formula-relative PATH entry was accepted for a non-Homebrew manager")
	}
}

func validManifest() Manifest {
	return Manifest{
		SchemaVersion: 1,
		ID:            "sample",
		Name:          "Sample",
		Category:      "language.general",
		Description:   "A test manifest.",
		Version:       VersionPolicy{Channel: "stable", UpgradePolicy: "never_automatically"},
		Executables:   []string{"sample"},
		VersionArgs:   []string{"--version"},
		Platforms:     map[OperatingSystem][]Architecture{Windows: {AMD64}},
		Installers: []Installer{
			{Platform: Windows, Manager: Winget, PackageID: "Example.Sample", Scope: UserScope, Source: "winget"},
			{Platform: Windows, Manager: Scoop, PackageID: "sample", Scope: UserScope, Source: "scoop-bucket"},
		},
	}
}
