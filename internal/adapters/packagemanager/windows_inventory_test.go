package packagemanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanScoopRootReportsInstalledVersionsAndUnknownCurrentApps(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{
		filepath.Join(root, "apps", "git", "2.50.0"),
		filepath.Join(root, "apps", "git", "current"),
		filepath.Join(root, "apps", "nodejs", "current"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	packages, err := scanScoopRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"git": "2.50.0", "nodejs": ""}
	if len(packages) != len(want) {
		t.Fatalf("Scoop packages = %#v", packages)
	}
	for _, packageRecord := range packages {
		if want[packageRecord.ID] != packageRecord.Version {
			t.Fatalf("Scoop package = %#v, want version %q", packageRecord, want[packageRecord.ID])
		}
		delete(want, packageRecord.ID)
	}
	if len(want) != 0 {
		t.Fatalf("Scoop packages were not reported: %#v", want)
	}
}

func TestScanChocolateyRootReadsInstalledNuspecMetadata(t *testing.T) {
	root := t.TempDir()
	gitDirectory := filepath.Join(root, "lib", "git")
	withoutMetadata := filepath.Join(root, "lib", "unknown-package")
	if err := os.MkdirAll(gitDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(withoutMetadata, 0o755); err != nil {
		t.Fatal(err)
	}
	nuspec := `<?xml version="1.0"?><package><metadata><id>Git.Git</id><version>2.50.1</version></metadata></package>`
	if err := os.WriteFile(filepath.Join(gitDirectory, "git.nuspec"), []byte(nuspec), 0o600); err != nil {
		t.Fatal(err)
	}
	packages, err := scanChocolateyRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 2 {
		t.Fatalf("Chocolatey packages = %#v", packages)
	}
	if packages[0] != (packageRecord{ID: "Git.Git", Version: "2.50.1"}) || packages[1] != (packageRecord{ID: "unknown-package"}) {
		t.Fatalf("Chocolatey package metadata = %#v", packages)
	}
}

func TestParseChocolateyNuspecRejectsOversizedAndMissingIdentifiers(t *testing.T) {
	if _, err := parseChocolateyNuspec(strings.NewReader(`<package><metadata><version>1.0</version></metadata></package>`)); err == nil {
		t.Fatal("nuspec without a package id was accepted")
	}
	if _, err := parseChocolateyNuspec(strings.NewReader(strings.Repeat("x", maxNuspecBytes+1))); err == nil {
		t.Fatal("oversized nuspec was accepted")
	}
}
