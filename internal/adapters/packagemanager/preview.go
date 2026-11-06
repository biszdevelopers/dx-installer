package packagemanager

import (
	"context"
	"encoding/json"
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

type PreviewOptions struct {
	Source      string
	Kind        string
	Environment map[string]string
}

type previewStep struct {
	arguments []string
	role      string
}

// InspectInstall runs bounded, non-mutating candidate and transaction queries.
// It never runs an explicit repository refresh; uncertainty remains visible when
// a manager cannot safely simulate the complete operation.
func InspectInstall(ctx context.Context, managers []Detected, manager tool.PackageManager, packageID string, options ...PreviewOptions) InstallPreview {
	if ctx == nil {
		return unavailablePreview("no preview context was provided")
	}
	var previewOptions PreviewOptions
	if len(options) > 0 {
		previewOptions = options[0]
	}
	detected, exists := detectedManager(managers, manager)
	if !exists {
		return unavailablePreview("the selected package manager is no longer available")
	}
	if manager == tool.EspressifEIM {
		return InstallPreview{
			CandidateVersion: packageID,
			Disclosure:       "The selected ESP-IDF release is pinned by the catalog. EIM does not expose a non-mutating installation transaction query; related tools and system changes remain uncertain. Review the EIM confirmation before accepting the operation.",
		}
	}
	steps, supported := previewSteps(manager, packageID, previewOptions)
	if !supported {
		return unavailablePreview("this package manager does not provide a safe candidate-version or transaction query")
	}
	previewContext, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	preview := InstallPreview{}
	for _, step := range steps {
		command := exec.CommandContext(previewContext, detected.Path, step.arguments...)
		stdout := &boundedOutput{limit: maxPreviewOutput}
		command.Stdout = stdout
		command.Stderr = &boundedOutput{limit: 16 << 10}
		environment := map[string]string{"LC_ALL": "C", "LANG": "C"}
		for name, value := range previewOptions.Environment {
			environment[name] = value
		}
		if manager == tool.Homebrew {
			environment["HOMEBREW_NO_AUTO_UPDATE"] = "1"
			environment["HOMEBREW_NO_INSTALL_CLEANUP"] = "1"
			environment["HOMEBREW_NO_INSTALL_UPGRADE"] = "1"
		}
		command.Env = mergedEnvironment(os.Environ(), environment)
		processErr := command.Run()
		if stdout.truncated {
			return unavailablePreview("the package manager exceeded the preview output limit")
		}
		mergePreview(&preview, parsePreviewStep(manager, packageID, stdout.String(), step.role))
		if previewContext.Err() != nil {
			return incompletePreview(preview, "the package-manager query timed out")
		}
		if processErr != nil {
			return incompletePreview(preview, "the package-manager query exited unsuccessfully")
		}
	}
	if preview.CandidateVersion == "" && preview.Transaction == "" {
		return unavailablePreview("the package manager returned no parseable candidate or transaction details")
	}
	if preview.Transaction == "" {
		stepDisclosure := preview.Disclosure
		preview.Disclosure = "The candidate version was inspected without starting an installation or refreshing package repositories. This package manager does not expose a reliable non-mutating transaction simulation; dependencies and other system changes remain uncertain. Review this uncertainty and any manager confirmation before accepting the operation."
		if stepDisclosure != "" {
			preview.Disclosure += " " + stepDisclosure
		}
		return preview
	}
	stepDisclosure := preview.Disclosure
	preview.Disclosure = "The preview did not start an installation or refresh package repositories. The live transaction can differ if package metadata or machine state changes before installation; review any manager confirmation before accepting it."
	if stepDisclosure != "" {
		preview.Disclosure += " " + stepDisclosure
	}
	if preview.CandidateVersion == "" {
		preview.Disclosure += " The candidate version was not identifiable in the preview output."
	}
	if preview.Transaction == "" {
		preview.Disclosure += " The package manager did not expose a complete affected-package list."
	}
	return preview
}

func previewSteps(manager tool.PackageManager, packageID string, options PreviewOptions) ([]previewStep, bool) {
	if packageID == "" {
		return nil, false
	}
	if arguments, supported := previewArguments(manager, packageID); supported {
		return []previewStep{{arguments: arguments, role: "transaction"}}, true
	}
	switch manager {
	case tool.Winget:
		arguments := []string{"show", "--id", packageID, "--exact", "--disable-interactivity", "--locale", "en-US"}
		if options.Source != "" {
			arguments = append(arguments, "--source", options.Source)
		}
		return []previewStep{{arguments: arguments, role: "candidate"}}, true
	case tool.Scoop:
		return []previewStep{
			{arguments: []string{"info", packageID}, role: "candidate"},
			{arguments: []string{"depends", packageID}, role: "dependencies"},
		}, true
	case tool.Chocolatey:
		return []previewStep{
			{arguments: []string{"info", packageID, "--exact", "--limit-output"}, role: "candidate"},
			{arguments: []string{"install", packageID, "--noop", "--limit-output", "--no-progress"}, role: "transaction"},
		}, true
	case tool.Homebrew:
		kind := "--formula"
		if options.Kind == "cask" {
			kind = "--cask"
		}
		return []previewStep{
			{arguments: []string{"info", "--json=v2", kind, packageID}, role: "candidate"},
			{arguments: []string{"install", "--dry-run", kind, packageID}, role: "transaction"},
		}, true
	case tool.MacPorts:
		return []previewStep{
			{arguments: []string{"info", packageID}, role: "candidate"},
			{arguments: []string{"rdeps", packageID}, role: "dependencies"},
		}, true
	case tool.AndroidSDK:
		return []previewStep{{arguments: []string{"--list", "--channel=0"}, role: "candidate"}}, true
	}
	return nil, false
}

func mergePreview(target *InstallPreview, source InstallPreview) {
	if source.CandidateVersion != "" {
		target.CandidateVersion = source.CandidateVersion
	}
	if source.Transaction != "" {
		target.Transaction = source.Transaction
	}
	if source.Disclosure != "" {
		if target.Disclosure != "" {
			target.Disclosure += " "
		}
		target.Disclosure += source.Disclosure
	}
}

func parsePreviewStep(manager tool.PackageManager, packageID, output, role string) InstallPreview {
	if role == "transaction" {
		switch manager {
		case tool.APT, tool.DNF, tool.YUM, tool.Pacman, tool.Zypper, tool.APK:
			return parseInstallPreview(manager, packageID, output)
		case tool.FreeBSDPkg:
			return parseFreeBSDInstallPreview(packageID, output)
		case tool.Chocolatey:
			return parseDelimitedPackageRows(output, packageID)
		case tool.Homebrew:
			return parseHomebrewDryRun(output)
		}
	}
	if role == "dependencies" {
		return parseDependencyPreview(packageID, output)
	}
	switch manager {
	case tool.Winget, tool.Scoop, tool.MacPorts:
		return InstallPreview{CandidateVersion: parseVersionField(output)}
	case tool.Chocolatey:
		return parseDelimitedCandidate(output, packageID)
	case tool.Homebrew:
		return parseHomebrewInfo(output)
	case tool.AndroidSDK:
		preview := parseAndroidSDKList(packageID, output)
		preview.Disclosure = "The sdkmanager availability query may contact configured package sources to read component metadata; it does not install or update components or accept licenses."
		return preview
	default:
		return InstallPreview{}
	}
}

func parseVersionField(output string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(fields) == 2 && strings.EqualFold(strings.TrimSpace(fields[0]), "version") {
			version := strings.TrimSpace(fields[1])
			if safePackageToken(version) {
				return version
			}
		}
	}
	return ""
}

func parseDelimitedPackageRows(output, packageID string) InstallPreview {
	preview := InstallPreview{}
	packages := make([]string, 0)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "|")
		if len(fields) < 2 {
			continue
		}
		name, version := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
		if !safePackageToken(name) || !safePackageToken(version) {
			continue
		}
		packages = appendUnique(packages, name)
		if packageMatches(name, packageID) {
			preview.CandidateVersion = version
		}
	}
	if len(packages) > 0 {
		preview.Transaction = packageTransaction(packages)
	}
	return preview
}

func parseDelimitedCandidate(output, packageID string) InstallPreview {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "|")
		if len(fields) < 2 {
			continue
		}
		name, version := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
		if packageMatches(name, packageID) && safePackageToken(version) {
			return InstallPreview{CandidateVersion: version}
		}
	}
	return InstallPreview{}
}

func parseHomebrewInfo(output string) InstallPreview {
	var result struct {
		Formulae []struct {
			Versions struct {
				Stable string `json:"stable"`
			} `json:"versions"`
		} `json:"formulae"`
		Casks []struct {
			Version string `json:"version"`
		} `json:"casks"`
	}
	if json.Unmarshal([]byte(output), &result) != nil {
		return InstallPreview{}
	}
	version := ""
	if len(result.Formulae) > 0 {
		version = result.Formulae[0].Versions.Stable
	}
	if len(result.Casks) > 0 {
		version = result.Casks[0].Version
	}
	if !safePackageToken(version) {
		version = ""
	}
	return InstallPreview{CandidateVersion: version}
}

func parseHomebrewDryRun(output string) InstallPreview {
	packages := make([]string, 0)
	collecting := false
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if homebrewInstallHeading.MatchString(line) {
			collecting = true
			continue
		}
		if line == "" || strings.HasPrefix(line, "==>") {
			collecting = false
			continue
		}
		if collecting {
			fields := strings.Fields(line)
			if len(fields) > 0 && safePackageToken(fields[0]) {
				packages = appendUnique(packages, fields[0])
			}
			continue
		}
		lower := strings.ToLower(line)
		if !strings.HasPrefix(lower, "would install ") {
			continue
		}
		packageList := line[len("Would install "):]
		if dependencyNote := strings.Index(strings.ToLower(packageList), " and its dependencies"); dependencyNote >= 0 {
			packageList = packageList[:dependencyNote]
		}
		for _, entry := range strings.Split(packageList, ",") {
			fields := strings.Fields(strings.TrimSpace(entry))
			if len(fields) == 0 {
				continue
			}
			name := strings.Trim(fields[0], ",:;[]()")
			if safePackageToken(name) {
				packages = appendUnique(packages, name)
			}
		}
	}
	return InstallPreview{
		Transaction: packageTransaction(packages),
		Disclosure:  "The Homebrew dry-run may not enumerate every dependency operation.",
	}
}

var homebrewInstallHeading = regexp.MustCompile(`(?i)^(?:==>\s*)?Would install\s+\d+\s+(?:formulas?|casks?|packages?|dependenc(?:y|ies))(?:\s+for\s+[^:]+)?:$`)

func parseDependencyPreview(packageID, output string) InstallPreview {
	packages := []string{packageID}
	collect := false
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)
		if strings.Contains(lower, "dependenc") || strings.Contains(lower, "required by") {
			collect = true
			continue
		}
		if line == "" || !collect {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := strings.Trim(fields[0], "*+-:,[]()")
		if safePackageToken(name) {
			packages = appendUnique(packages, name)
		}
	}
	return InstallPreview{
		Transaction: packageTransaction(packages),
		Disclosure:  "This dependency query is informational and may not capture upgrades, removals, or optional package operations.",
	}
}

func parseAndroidSDKList(packageID, output string) InstallPreview {
	available := false
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.Contains(strings.ToLower(line), "available packages") {
			available = true
			continue
		}
		if !available {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 2 || strings.TrimSpace(fields[0]) != packageID {
			continue
		}
		version := strings.TrimSpace(fields[1])
		if safePackageToken(version) {
			return InstallPreview{CandidateVersion: version}
		}
	}
	return InstallPreview{}
}

func parseFreeBSDInstallPreview(packageID, output string) InstallPreview {
	packages := make([]string, 0)
	preview := InstallPreview{}
	collect := false
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)
		if strings.Contains(lower, "packages to be") {
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
		fields := strings.SplitN(line, ":", 2)
		if len(fields) != 2 {
			continue
		}
		name, version := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
		if !safePackageToken(name) || !safePackageToken(version) {
			continue
		}
		packages = appendUnique(packages, name)
		if packageMatches(name, packageID) {
			preview.CandidateVersion = version
		}
	}
	preview.Transaction = packageTransaction(packages)
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
	case tool.FreeBSDPkg:
		return []string{"install", "--dry-run", "--no-repo-update", packageID}, true
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
	return fmt.Sprintf("Preview reported %d package operation(s): %s", len(packages), strings.Join(packages, ", "))
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
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._+:-~^=/@", char)) {
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

func incompletePreview(preview InstallPreview, reason string) InstallPreview {
	if preview.CandidateVersion == "" && preview.Transaction == "" {
		return unavailablePreview(reason + "; no usable details were returned")
	}
	if preview.Transaction != "" {
		preview.Transaction = strings.Replace(preview.Transaction, "Preview reported", "Incomplete preview output reported", 1)
	}
	stepDisclosure := preview.Disclosure
	preview.Disclosure = "Package-manager preview did not complete because " + reason + ". Any candidate version or package list shown may be incomplete and must not be treated as a complete transaction. DX will not refresh repositories automatically; review this uncertainty and any manager confirmation before accepting the operation."
	if stepDisclosure != "" {
		preview.Disclosure += " " + stepDisclosure
	}
	return preview
}
