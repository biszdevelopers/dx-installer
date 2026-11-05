package packagemanager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"developers-club-dx/internal/domain/tool"
)

const maxPreviewOutput = 256 << 10

type InstallPreview struct {
	CandidateVersion string
	Transaction      string
	Disclosure       string
}

// InspectInstall runs only cache-backed, non-mutating transaction previews.
// Managers without a reliable no-refresh preview return an explicit disclosure.
func InspectInstall(ctx context.Context, managers []Detected, manager tool.PackageManager, packageID string) InstallPreview {
	if ctx == nil {
		return unavailablePreview("no preview context was provided")
	}
	detected, exists := detectedManager(managers, manager)
	if !exists {
		return unavailablePreview("the selected package manager is no longer available")
	}
	arguments, supported := previewArguments(manager, packageID)
	if !supported {
		return unavailablePreview("this package manager does not provide a safe cache-only transaction preview")
	}
	previewContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(previewContext, detected.Path, arguments...)
	stdout := &boundedOutput{limit: maxPreviewOutput}
	stderr := &boundedOutput{limit: 16 << 10}
	command.Stdout = stdout
	command.Stderr = stderr
	command.Env = mergedEnvironment(os.Environ(), map[string]string{"LC_ALL": "C", "LANG": "C"})
	processErr := command.Run()
	if stdout.truncated {
		return unavailablePreview("the package manager exceeded the preview output limit")
	}
	preview := parseInstallPreview(manager, packageID, stdout.String())
	if preview.CandidateVersion == "" && preview.Transaction == "" {
		if previewContext.Err() != nil {
			return unavailablePreview("the cache-only preview timed out")
		}
		if processErr != nil {
			return unavailablePreview("the package manager could not resolve a transaction from its current local metadata")
		}
		return unavailablePreview("the package manager returned no parseable candidate or transaction details")
	}
	preview.Disclosure = "Preview used cached package metadata without refreshing repositories. The live transaction can differ if package metadata or machine state changes before installation; review any manager confirmation before accepting it."
	if preview.CandidateVersion == "" {
		preview.Disclosure += " The candidate version was not identifiable in the preview output."
	}
	if preview.Transaction == "" {
		preview.Disclosure += " The package manager did not expose a complete affected-package list."
	}
	return preview
}

func detectedManager(managers []Detected, manager tool.PackageManager) (Detected, bool) {
	for _, detected := range managers {
		if detected.Manager == manager && detected.Path != "" {
			return detected, true
		}
	}
	return Detected{}, false
}

func previewArguments(manager tool.PackageManager, packageID string) ([]string, bool) {
	if packageID == "" {
		return nil, false
	}
	switch manager {
	case tool.APT:
		return []string{"--simulate", "--no-download", "--no-install-recommends", "install", packageID}, true
	case tool.DNF, tool.YUM:
		return []string{"--cacheonly", "--assumeno", "--color=never", "install", packageID}, true
	case tool.Pacman:
		return []string{"-Sp", "--print-format", "%n\t%v", "--needed", packageID}, true
	case tool.Zypper:
		return []string{"--no-refresh", "--non-interactive", "install", "--dry-run", "--no-recommends", packageID}, true
	case tool.APK:
		return []string{"--no-network", "add", "--simulate", packageID}, true
	default:
		return nil, false
	}
}

func parseInstallPreview(manager tool.PackageManager, packageID, output string) InstallPreview {
	switch manager {
	case tool.APT:
		return parseAPTInstallPreview(packageID, output)
	case tool.DNF, tool.YUM:
		return parseRPMInstallPreview(packageID, output)
	case tool.Pacman:
		return parsePacmanInstallPreview(packageID, output)
	case tool.Zypper:
		return parseZypperInstallPreview(packageID, output)
	case tool.APK:
		return parseAPKInstallPreview(packageID, output)
	default:
		return InstallPreview{}
	}
}

func parseAPTInstallPreview(packageID, output string) InstallPreview {
	preview := InstallPreview{}
	packages := make([]string, 0)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !safePackageToken(fields[1]) || (fields[0] != "Inst" && fields[0] != "Remv" && fields[0] != "Conf") {
			continue
		}
		if fields[0] != "Conf" {
			packages = appendUnique(packages, fields[1])
		}
		if fields[0] == "Inst" && packageMatches(fields[1], packageID) {
			version := versionFromParentheses(line)
			if safePackageToken(version) {
				preview.CandidateVersion = version
			}
		}
	}
	preview.Transaction = packageTransaction(packages)
	return preview
}

func parseRPMInstallPreview(packageID, output string) InstallPreview {
	preview := InstallPreview{}
	packages := make([]string, 0)
	collect := false
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "Installing:" || line == "Installing Dependencies:" || line == "Installing dependencies:" || line == "Upgrading:" || line == "Upgrading dependencies:" || line == "Removing:" || line == "Removing dependent packages:" {
			collect = true
			continue
		}
		if line == "" {
			collect = false
			continue
		}
		if !collect {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !safePackageToken(fields[0]) {
			continue
		}
		name := stripArchitecture(fields[0])
		packages = appendUnique(packages, name)
		if packageMatches(name, packageID) {
			versionIndex := 1
			if (fields[1] == "x86_64" || fields[1] == "aarch64" || fields[1] == "noarch" || fields[1] == "i686") && len(fields) > 2 {
				versionIndex = 2
			}
			if safePackageToken(fields[versionIndex]) {
				preview.CandidateVersion = fields[versionIndex]
			}
		}
	}
	preview.Transaction = packageTransaction(packages)
	return preview
}

func parsePacmanInstallPreview(packageID, output string) InstallPreview {
	preview := InstallPreview{}
	packages := make([]string, 0)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !safePackageToken(fields[0]) {
			continue
		}
		packages = appendUnique(packages, fields[0])
		if packageMatches(fields[0], packageID) && safePackageToken(fields[1]) {
			preview.CandidateVersion = fields[1]
		}
	}
	preview.Transaction = packageTransaction(packages)
	return preview
}

func parseZypperInstallPreview(packageID, output string) InstallPreview {
	preview := InstallPreview{}
	packages := make([]string, 0)
	collect := false
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.Contains(line, "following") && (strings.Contains(line, "package") || strings.Contains(line, "packages")) && (strings.Contains(line, "installed") || strings.Contains(line, "upgraded") || strings.Contains(line, "removed") || strings.Contains(line, "downgraded")) {
			collect = true
			continue
		}
		if line == "" {
			collect = false
			continue
		}
		if !collect {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !safePackageToken(fields[0]) {
			continue
		}
		packages = appendUnique(packages, fields[0])
		if packageMatches(fields[0], packageID) && safePackageToken(fields[1]) {
			preview.CandidateVersion = fields[1]
		}
	}
	preview.Transaction = packageTransaction(packages)
	return preview
}

var apkInstallPattern = regexp.MustCompile(`^\([0-9]+/[0-9]+\) Installing ([^ ]+) \(([^)]+)\)`)

func parseAPKInstallPreview(packageID, output string) InstallPreview {
	preview := InstallPreview{}
	packages := make([]string, 0)
	for _, line := range strings.Split(output, "\n") {
		match := apkInstallPattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) != 3 {
			continue
		}
		packages = appendUnique(packages, match[1])
		if packageMatches(match[1], packageID) && safePackageToken(match[2]) {
			preview.CandidateVersion = match[2]
		}
	}
	preview.Transaction = packageTransaction(packages)
	return preview
}

func versionFromParentheses(line string) string {
	start := strings.IndexByte(line, '(')
	if start < 0 {
		return ""
	}
	fields := strings.Fields(line[start+1:])
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimRight(fields[0], ")")
}

func packageTransaction(packages []string) string {
	if len(packages) == 0 {
		return ""
	}
	return fmt.Sprintf("Cached dry-run resolved %d package operation(s): %s", len(packages), strings.Join(packages, ", "))
}

func packageMatches(actual, expected string) bool {
	actual = strings.SplitN(actual, ":", 2)[0]
	expected = strings.SplitN(expected, ":", 2)[0]
	return strings.EqualFold(actual, expected) || strings.EqualFold(stripArchitecture(actual), expected)
}

func stripArchitecture(packageID string) string {
	if index := strings.LastIndexByte(packageID, '.'); index > 0 {
		architecture := packageID[index+1:]
		if architecture == "x86_64" || architecture == "aarch64" || architecture == "noarch" || architecture == "i686" {
			return packageID[:index]
		}
	}
	return packageID
}

func safePackageToken(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._+:-~^=", char)) {
			return false
		}
	}
	return true
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func unavailablePreview(reason string) InstallPreview {
	return InstallPreview{Disclosure: "Package-manager preview unavailable: " + reason + ". DX will not refresh repositories automatically; dependencies or other system changes may be unresolved. Review this uncertainty and any manager confirmation before accepting the operation."}
}
