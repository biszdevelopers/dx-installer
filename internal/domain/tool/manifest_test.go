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
		Size: 128, Format: Zip, ArchiveRoot: "sample",
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

func TestManifestRejectsUnsafeArchiveSources(t *testing.T) {
	base := Artifact{
		Platform: Windows, Architecture: AMD64, Version: "1.2.3",
		URL: "https://downloads.example/sample.zip", SHA256: "4c5a4f9afca56f9c04b1f6c93a7f70947aa41a279c1d128f0136f726c1ddaf4d",
		Size: 128, Format: Zip, ArchiveRoot: "sample",
	}
	for _, testCase := range []struct {
		name   string
		change func(*Artifact)
	}{
		{"plaintext URL", func(item *Artifact) { item.URL = "http://downloads.example/sample.zip" }},
		{"URL credentials", func(item *Artifact) { item.URL = "https://user:pass@downloads.example/sample.zip" }},
		{"missing checksum", func(item *Artifact) { item.SHA256 = "" }},
		{"malformed checksum", func(item *Artifact) { item.SHA256 = strings.Repeat("z", 64) }},
		{"negative size", func(item *Artifact) { item.Size = -1 }},
		{"unsafe archive root", func(item *Artifact) { item.ArchiveRoot = "../outside" }},
		{"unsupported archive", func(item *Artifact) { item.Format = "exe" }},
		{"invalid redirect host", func(item *Artifact) { item.AllowedRedirectHosts = []string{"*.example"} }},
		{"regional URL is plaintext", func(item *Artifact) { item.RegionalURL = "http://mirror.example/sample.zip" }},
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
