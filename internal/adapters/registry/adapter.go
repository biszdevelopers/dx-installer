package registry

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"developers-club-dx/internal/adapters/packagemanager"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/sources"
)

const temporaryDirectoryToken = "{dx-temporary-directory}"

func BuildCommand(installer tool.RegistryInstaller, installPath, source string) (packagemanager.Command, []string, error) {
	if !filepath.IsAbs(installPath) {
		return packagemanager.Command{}, nil, fmt.Errorf("registry install path must be absolute")
	}
	if source == "" {
		source = installer.Source
	}
	if err := sources.ValidateRegistryURL(installer.Ecosystem, source); err != nil {
		return packagemanager.Command{}, nil, err
	}
	cache := filepath.Join(temporaryDirectoryToken, "registry-cache")
	command := packagemanager.Command{}
	var pathEntries []string
	switch installer.Ecosystem {
	case tool.NPMRegistry:
		command.Executable = "npm"
		command.Arguments = []string{"install", "--prefix", installPath, "--no-save", "--package-lock=false", "--no-audit", "--no-fund", "--registry", source, "--", installer.PackageID + "@" + installer.Version}
		command.Environment = map[string]string{
			"npm_config_cache":           cache,
			"npm_config_update_notifier": "false",
			"npm_config_audit":           "false",
			"npm_config_fund":            "false",
		}
		pathEntries = []string{filepath.Join(installPath, "node_modules", ".bin")}
	case tool.PyPIRegistry:
		command.Executable = "uv"
		command.Arguments = []string{"tool", "install", installer.PackageID + "==" + installer.Version}
		command.Environment = map[string]string{
			"UV_DEFAULT_INDEX":    source,
			"UV_TOOL_DIR":         filepath.Join(installPath, "tools"),
			"UV_TOOL_BIN_DIR":     filepath.Join(installPath, "bin"),
			"UV_CACHE_DIR":        cache,
			"UV_PYTHON_DOWNLOADS": "never",
			"UV_NO_PROGRESS":      "1",
		}
		pathEntries = []string{filepath.Join(installPath, "bin")}
	case tool.GoModuleRegistry:
		command.Executable = "go"
		command.Arguments = []string{"install", installer.PackageID + "@" + installer.Version}
		command.Environment = map[string]string{
			"GOBIN":       filepath.Join(installPath, "bin"),
			"GOPROXY":     source,
			"GOSUMDB":     "sum.golang.org",
			"GOCACHE":     filepath.Join(cache, "build"),
			"GOMODCACHE":  filepath.Join(cache, "modules"),
			"GOTOOLCHAIN": "local",
		}
		pathEntries = []string{filepath.Join(installPath, "bin")}
	default:
		return packagemanager.Command{}, nil, fmt.Errorf("unsupported registry ecosystem %q", installer.Ecosystem)
	}
	return command, pathEntries, nil
}

func BindTemporaryDirectory(command packagemanager.Command, directory string) packagemanager.Command {
	if !filepath.IsAbs(directory) {
		return command
	}
	for key, value := range command.Environment {
		command.Environment[key] = strings.ReplaceAll(value, temporaryDirectoryToken, directory)
	}
	return command
}

func PrepareCommand(command packagemanager.Command, temporaryDirectory string) (packagemanager.Command, error) {
	if !filepath.IsAbs(temporaryDirectory) {
		return packagemanager.Command{}, fmt.Errorf("temporary registry workspace must be absolute")
	}
	command = BindTemporaryDirectory(command, temporaryDirectory)
	if runtime.GOOS != "windows" || command.Executable != "npm" {
		return command, nil
	}
	npmPath, err := exec.LookPath("npm")
	if err != nil {
		return packagemanager.Command{}, fmt.Errorf("find npm for the Node.js runtime: %w", err)
	}
	nodePath := filepath.Join(filepath.Dir(npmPath), "node.exe")
	npmCLI := filepath.Join(filepath.Dir(npmPath), "node_modules", "npm", "bin", "npm-cli.js")
	for _, path := range []string{nodePath, npmCLI} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return packagemanager.Command{}, fmt.Errorf("the npm command wrapper has no regular Node.js runtime or npm CLI at %s", path)
		}
	}
	command.Executable = nodePath
	command.Arguments = append([]string{npmCLI}, command.Arguments...)
	return command, nil
}

func NewTemporaryDirectory() (string, error) {
	directory, err := os.MkdirTemp("", "dx-registry-install-")
	if err != nil {
		return "", fmt.Errorf("create temporary registry workspace: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		_ = os.RemoveAll(directory)
		return "", fmt.Errorf("restrict temporary registry workspace: %w", err)
	}
	return directory, nil
}
