package platform

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"developers-club-dx/internal/domain/tool"
)

type Info struct {
	OS           tool.OperatingSystem
	Architecture tool.Architecture
	Distribution string
	Shell        string
}

func Detect() Info {
	info := Info{
		OS:           operatingSystem(runtime.GOOS),
		Architecture: architecture(runtime.GOARCH),
		Shell:        detectShell(),
	}
	info.Distribution = operatingSystemDistribution(runtime.GOOS, info.OS)
	return info
}

func operatingSystemDistribution(goos string, operatingSystem tool.OperatingSystem) string {
	switch operatingSystem {
	case tool.Linux:
		return linuxDistribution()
	case tool.BSD:
		return strings.ToLower(goos)
	default:
		return ""
	}
}

func operatingSystem(goos string) tool.OperatingSystem {
	switch goos {
	case "windows":
		return tool.Windows
	case "darwin":
		return tool.MacOS
	case "linux":
		return tool.Linux
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		return tool.BSD
	default:
		return tool.OperatingSystem(goos)
	}
}

func architecture(goarch string) tool.Architecture {
	switch goarch {
	case "amd64":
		return tool.AMD64
	case "arm64":
		return tool.ARM64
	case "386":
		return tool.Architecture("386")
	case "arm":
		return tool.ARM
	case "riscv64":
		return tool.RISCV64
	default:
		return tool.Architecture(goarch)
	}
}

func detectShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return filepath.Base(shell)
	}
	if shell := os.Getenv("ComSpec"); shell != "" {
		return filepath.Base(shell)
	}
	if runtime.GOOS == "windows" {
		if os.Getenv("PSModulePath") != "" {
			return "PowerShell"
		}
		return "unknown"
	}
	return "unknown"
}

func linuxDistribution() string {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return "unknown Linux distribution"
	}
	defer file.Close()

	values := make(map[string]string, 2)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok || (key != "ID" && key != "VERSION_ID") {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	if err := scanner.Err(); err != nil {
		return "unknown Linux distribution"
	}
	id := strings.ToLower(values["ID"])
	if id == "" {
		return "unknown Linux distribution"
	}
	if version := values["VERSION_ID"]; version != "" {
		return id + " " + version
	}
	return id
}
