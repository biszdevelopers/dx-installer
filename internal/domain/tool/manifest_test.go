package tool

import "testing"

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
