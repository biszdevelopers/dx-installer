package registry

import (
	"path/filepath"
	"reflect"
	"testing"

	"developers-club-dx/internal/domain/tool"
)

func TestBuildCommandCreatesIsolatedPinnedEcosystemInstall(t *testing.T) {
	tests := []struct {
		name        string
		installer   tool.RegistryInstaller
		wantCommand string
		wantArgs    func(string) []string
		wantEnvFor  func(string) map[string]string
		wantPathFor func(string) []string
	}{
		{
			name:        "npm",
			installer:   tool.RegistryInstaller{Ecosystem: tool.NPMRegistry, PackageID: "tsx", Version: "4.20.6", Source: "https://registry.npmjs.org/"},
			wantCommand: "npm",
			wantArgs: func(path string) []string {
				return []string{"install", "--prefix", path, "--no-save", "--package-lock=false", "--no-audit", "--no-fund", "--registry", "https://registry.npmjs.org/", "--", "tsx@4.20.6"}
			},
			wantEnvFor: func(path string) map[string]string {
				return map[string]string{"npm_config_cache": filepath.Join(temporaryDirectoryToken, "registry-cache"), "npm_config_update_notifier": "false", "npm_config_audit": "false", "npm_config_fund": "false"}
			},
			wantPathFor: func(path string) []string { return []string{filepath.Join(path, "node_modules", ".bin")} },
		},
		{
			name:        "PyPI",
			installer:   tool.RegistryInstaller{Ecosystem: tool.PyPIRegistry, PackageID: "ruff", Version: "0.14.3", Source: "https://pypi.org/simple"},
			wantCommand: "uv",
			wantArgs:    func(string) []string { return []string{"tool", "install", "ruff==0.14.3"} },
			wantEnvFor: func(path string) map[string]string {
				return map[string]string{"UV_DEFAULT_INDEX": "https://pypi.org/simple", "UV_TOOL_DIR": filepath.Join(path, "tools"), "UV_TOOL_BIN_DIR": filepath.Join(path, "bin"), "UV_CACHE_DIR": filepath.Join(temporaryDirectoryToken, "registry-cache"), "UV_PYTHON_DOWNLOADS": "never", "UV_NO_PROGRESS": "1"}
			},
			wantPathFor: func(path string) []string { return []string{filepath.Join(path, "bin")} },
		},
		{
			name:        "Go modules",
			installer:   tool.RegistryInstaller{Ecosystem: tool.GoModuleRegistry, PackageID: "golang.org/x/vuln/cmd/govulncheck", Version: "v1.1.4", Source: "https://proxy.golang.org"},
			wantCommand: "go",
			wantArgs:    func(string) []string { return []string{"install", "golang.org/x/vuln/cmd/govulncheck@v1.1.4"} },
			wantEnvFor: func(path string) map[string]string {
				return map[string]string{"GOBIN": filepath.Join(path, "bin"), "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org", "GOCACHE": filepath.Join(temporaryDirectoryToken, "registry-cache", "build"), "GOMODCACHE": filepath.Join(temporaryDirectoryToken, "registry-cache", "modules"), "GOTOOLCHAIN": "local"}
			},
			wantPathFor: func(path string) []string { return []string{filepath.Join(path, "bin")} },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			installPath := filepath.Join(t.TempDir(), test.installer.PackageID)
			command, pathEntries, err := BuildCommand(test.installer, installPath, "")
			if err != nil {
				t.Fatal(err)
			}
			if command.Executable != test.wantCommand || !reflect.DeepEqual(command.Arguments, test.wantArgs(installPath)) || !reflect.DeepEqual(pathEntries, test.wantPathFor(installPath)) {
				t.Fatalf("command = %#v, paths = %q", command, pathEntries)
			}
			for key, expected := range test.wantEnvFor(installPath) {
				if got := command.Environment[key]; got != expected {
					t.Errorf("environment %s = %q, want %q", key, got, expected)
				}
			}
		})
	}
}

func TestBuildCommandRejectsUnsafeRegistryAndRelativeTarget(t *testing.T) {
	installer := tool.RegistryInstaller{Ecosystem: tool.NPMRegistry, PackageID: "safe", Version: "1.0.0", Source: "https://registry.npmjs.org/"}
	if _, _, err := BuildCommand(installer, "relative/path", ""); err == nil {
		t.Fatal("relative install path was accepted")
	}
	if _, _, err := BuildCommand(installer, filepath.Join(t.TempDir(), "safe"), "http://registry.example"); err == nil {
		t.Fatal("insecure registry source was accepted")
	}
}

func TestBuildCommandPreservesScopedNPMPackageIdentity(t *testing.T) {
	command, _, err := BuildCommand(tool.RegistryInstaller{
		Ecosystem: tool.NPMRegistry,
		PackageID: "@changesets/cli",
		Version:   "3.0.3",
		Source:    "https://registry.npmjs.org/",
	}, filepath.Join(t.TempDir(), "changesets"), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := command.Arguments[len(command.Arguments)-1]; got != "@changesets/cli@3.0.3" {
		t.Fatalf("scoped package specifier = %q, want @changesets/cli@3.0.3", got)
	}
}

func TestBindTemporaryDirectoryReplacesOnlyScratchPath(t *testing.T) {
	command, _, err := BuildCommand(tool.RegistryInstaller{Ecosystem: tool.NPMRegistry, PackageID: "safe", Version: "1.0.0", Source: "https://registry.npmjs.org/"}, filepath.Join(t.TempDir(), "safe"), "")
	if err != nil {
		t.Fatal(err)
	}
	temporaryDirectory := t.TempDir()
	command = BindTemporaryDirectory(command, temporaryDirectory)
	if got := command.Environment["npm_config_cache"]; got != filepath.Join(temporaryDirectory, "registry-cache") {
		t.Fatalf("cache path = %q", got)
	}
}
