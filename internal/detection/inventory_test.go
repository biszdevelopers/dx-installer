package detection

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"developers-club-dx/internal/adapters/packagemanager"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/platform"
)

func TestInstalledPackageWithoutExecutableIsPreservedAsUnknown(t *testing.T) {
	manifest := tool.Manifest{
		ID:          "inventory-test",
		Executables: []string{"dx-inventory-test-missing-executable"},
		Platforms:   map[tool.OperatingSystem][]tool.Architecture{tool.Linux: {tool.AMD64}},
		Installers: []tool.Installer{{
			Platform:  tool.Linux,
			Manager:   tool.APT,
			PackageID: "dx-inventory-package",
			Scope:     tool.SystemScope,
			Source:    "configured-apt-repositories",
		}},
	}
	managers := []packagemanager.Detected{{Manager: tool.APT, Path: "/usr/bin/apt-get"}}
	installed := []packagemanager.InstalledPackage{{Manager: tool.APT, ID: "dx-inventory-package", Version: "1.2.3-1"}}
	instances, packages, state := scanTool(context.Background(), manifest, platform.Info{OS: tool.Linux, Architecture: tool.AMD64}, managers, installed)
	if state != Unknown || len(instances) != 0 {
		t.Fatalf("scan state = %s, instances = %#v", state, instances)
	}
	if len(packages) != 1 || packages[0].Manager != tool.APT || packages[0].Version != "1.2.3-1" || packages[0].Source != "configured-apt-repositories" {
		t.Fatalf("package ownership = %#v", packages)
	}
}

func TestInstalledPackageMatchingUsesDeclaredPlatformManagerAndIdentifier(t *testing.T) {
	manifest := tool.Manifest{
		Installers: []tool.Installer{
			{Platform: tool.Linux, Manager: tool.APT, PackageID: "cmake", Source: "apt"},
			{Platform: tool.MacOS, Manager: tool.Homebrew, PackageID: "cmake", Source: "brew"},
		},
	}
	managers := []packagemanager.Detected{{Manager: tool.APT}}
	installed := []packagemanager.InstalledPackage{
		{Manager: tool.APT, ID: "CMAKE", Version: "3.30"},
		{Manager: tool.Pacman, ID: "cmake", Version: "3.31"},
		{Manager: tool.APT, ID: "cmake-extra", Version: "9.0"},
	}
	got := installedPackagesFor(manifest, tool.Linux, managers, installed)
	if len(got) != 1 || got[0].ID != "CMAKE" || got[0].Version != "3.30" {
		t.Fatalf("matched package ownership = %#v", got)
	}
}

func TestFindExecutablesReturnsEachVersionOnPATH(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	name := "dx-inventory-path-test"
	filename := name
	if runtime.GOOS == "windows" {
		filename += ".exe"
	}
	for _, directory := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(directory, filename), []byte("test executable"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	searchPath := first + string(filepath.ListSeparator) + second
	t.Setenv("PATH", searchPath)
	paths := findExecutables(name)
	if len(paths) != 2 {
		t.Fatalf("found %d executable paths, want two: %#v", len(paths), paths)
	}
	if paths[0] == paths[1] || filepath.Dir(paths[0]) == filepath.Dir(paths[1]) {
		t.Fatalf("PATH entries did not remain distinct: %#v", paths)
	}
}

func TestFindExecutablesSkipsNonExecutableUnixFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use POSIX executable permission bits")
	}
	directory := t.TempDir()
	name := "dx-inventory-non-executable-test"
	if err := os.WriteFile(filepath.Join(directory, name), []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	if paths := findExecutables(name); len(paths) != 0 {
		t.Fatalf("found non-executable file on PATH: %#v", paths)
	}
}
