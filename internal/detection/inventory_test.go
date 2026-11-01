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
	instances, packages, state := scanTool(context.Background(), manifest, platform.Info{OS: tool.Linux, Architecture: tool.AMD64}, managers, installed, nil)
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

func TestWingetPackageOwnershipRetainsSameIDFromEachSource(t *testing.T) {
	manifest := tool.Manifest{Installers: []tool.Installer{{
		Platform:  tool.Windows,
		Manager:   tool.Winget,
		PackageID: "Git.Git",
		Scope:     tool.UserScope,
		Source:    "winget",
	}}}
	managers := []packagemanager.Detected{{Manager: tool.Winget}}
	installed := []packagemanager.InstalledPackage{
		{Manager: tool.Winget, ID: "Git.Git", Version: "2.47.0", Source: "msstore"},
		{Manager: tool.Winget, ID: "Git.Git", Version: "2.47.0", Source: "winget"},
	}
	got := installedPackagesFor(manifest, tool.Windows, managers, installed)
	if len(got) != 2 || got[0].Source != "msstore" || got[1].Source != "winget" {
		t.Fatalf("matched WinGet ownership = %#v, want both exact-ID source records", got)
	}
}

func TestFailedManagerInventoryKeepsUnseenToolsUnknown(t *testing.T) {
	manifest := tool.Manifest{
		ID:          "inventory-query-failure",
		Executables: []string{"dx-inventory-query-failure-missing"},
		Platforms:   map[tool.OperatingSystem][]tool.Architecture{tool.Windows: {tool.AMD64}},
		Installers: []tool.Installer{{
			Platform: tool.Windows, Manager: tool.Winget, PackageID: "Example.Tool", Scope: tool.UserScope, Source: "winget",
		}},
	}
	managers := []packagemanager.Detected{{Manager: tool.Winget}}
	_, packages, state := scanTool(context.Background(), manifest, platform.Info{OS: tool.Windows, Architecture: tool.AMD64}, managers, nil, map[tool.PackageManager]bool{tool.Winget: true})
	if state != Unknown || len(packages) != 0 {
		t.Fatalf("scan state = %s, packages = %#v, want unknown after failed package query", state, packages)
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

func TestFindExecutablesDiscoversMultipleVersionsOutsidePATH(t *testing.T) {
	home := t.TempDir()
	searchPath := t.TempDir()
	filename := "dx-versioned-runtime-test"
	var directories []string
	if runtime.GOOS == "windows" {
		filename += ".exe"
		t.Setenv("USERPROFILE", home)
		t.Setenv("ProgramFiles", home)
		t.Setenv("ProgramFiles(x86)", filepath.Join(home, "Program Files (x86)"))
		t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
		t.Setenv("ProgramData", filepath.Join(home, "ProgramData"))
		scoop := filepath.Join(home, "scoop")
		t.Setenv("SCOOP", scoop)
		t.Setenv("SCOOP_GLOBAL", "")
		directories = []string{
			filepath.Join(scoop, "apps", "nodejs", "20.11.0", "bin"),
			filepath.Join(scoop, "apps", "nodejs", "22.0.0", "bin"),
		}
	} else {
		t.Setenv("HOME", home)
		directories = []string{
			filepath.Join(home, ".nvm", "versions", "node", "v20.11.0", "bin"),
			filepath.Join(home, ".nvm", "versions", "node", "v22.0.0", "bin"),
		}
	}
	for _, directory := range directories {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, filename), []byte("test executable"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", searchPath)

	paths := findExecutables("dx-versioned-runtime-test")
	if len(paths) != 2 {
		t.Fatalf("found %d versioned executable paths outside PATH, want two: %#v", len(paths), paths)
	}
	foundDirectories := make(map[string]bool, len(paths))
	for _, path := range paths {
		foundDirectories[filepath.Clean(filepath.Dir(path))] = true
	}
	for _, directory := range directories {
		if !foundDirectories[filepath.Clean(directory)] {
			t.Fatalf("versioned executable directories = %#v, want both %#v", foundDirectories, directories)
		}
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

func TestFindExecutablesSearchesKnownUserToolDirectoryOutsidePath(t *testing.T) {
	home := t.TempDir()
	searchPath := t.TempDir()
	var directory string
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
		directory = filepath.Join(home, "scoop", "shims")
		t.Setenv("SCOOP", filepath.Join(home, "scoop"))
	} else {
		t.Setenv("HOME", home)
		directory = filepath.Join(home, ".local", "bin")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "dx-inventory-known-directory-test"
	filename := name
	if runtime.GOOS == "windows" {
		filename += ".exe"
	}
	executable := filepath.Join(directory, filename)
	if err := os.WriteFile(executable, []byte("test executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(executable, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", searchPath)

	paths := findExecutables(name)
	if len(paths) != 1 || filepath.Clean(paths[0]) != filepath.Clean(executable) {
		t.Fatalf("known-directory executable paths = %#v, want %q", paths, executable)
	}
}
