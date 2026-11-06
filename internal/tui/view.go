package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"developers-club-dx/internal/artifacts"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/display"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/installation"
	"developers-club-dx/internal/planning"
	"developers-club-dx/internal/regional"
	"github.com/charmbracelet/x/ansi"
)

var (
	brandStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	activeStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	warningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	goodStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	strongStyle   = lipgloss.NewStyle().Bold(true)
	buttonStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	asciiSymbols  = strings.NewReplacer("…", "...", "→", "->", "—", "--", "–", "-")
)

var screenNames = []string{"Overview", "Catalog", "Profiles", "Environment", "Plan", "Sources", "Progress", "Results"}

func (m *Model) render() string {
	return m.renderWorkspace()
}

func wrapLayout(lines []string, width int) []string {
	if len(lines) == 0 {
		return nil
	}
	if asciiFallbackEnabled() {
		for index := range lines {
			lines[index] = asciiText(lines[index])
		}
	}
	wrapped := lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	result := strings.Split(wrapped, "\n")
	for index := range result {
		result[index] = ansi.Truncate(result[index], width, "")
	}
	return result
}

func buttonRows(width int, labels ...string) []string {
	width = max(24, width)
	rows := make([]string, 0, len(labels))
	var current []string
	currentWidth := 0
	for _, label := range labels {
		button := "[" + label + "]"
		buttonWidth := ansi.StringWidth(button)
		if len(current) > 0 && currentWidth+2+buttonWidth > width {
			rows = append(rows, buttonStyle.Render(strings.Join(current, "  ")))
			current = nil
			currentWidth = 0
		}
		current = append(current, button)
		if currentWidth > 0 {
			currentWidth += 2
		}
		currentWidth += buttonWidth
	}
	if len(current) > 0 {
		rows = append(rows, buttonStyle.Render(strings.Join(current, "  ")))
	}
	return rows
}

func asciiFallbackEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DX_ASCII"))) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") || platformRequiresASCII() {
		return true
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		locale := strings.ToLower(os.Getenv(name))
		if locale == "c" || locale == "posix" || strings.Contains(locale, "ascii") {
			return true
		}
		if strings.Contains(locale, "utf-8") || strings.Contains(locale, "utf8") {
			return false
		}
	}
	return false
}

func asciiText(value string) string {
	value = asciiSymbols.Replace(value)
	return strings.Map(func(character rune) rune {
		if character > 0x7f {
			return '?'
		}
		return character
	}, value)
}

func (m *Model) renderScreen(maxRows int) []string {
	switch m.screen {
	case OverviewScreen:
		return m.renderOverview()
	case CatalogScreen:
		return m.renderCatalog(maxRows)
	case ProfilesScreen:
		return m.renderProfiles(maxRows)
	case EnvironmentScreen:
		return m.renderEnvironment(maxRows)
	case PlanScreen:
		return m.renderPlan(maxRows)
	case SourcesScreen:
		return m.renderSources(maxRows)
	case ProgressScreen:
		return m.renderProgress(maxRows)
	case ResultsScreen:
		return m.renderResults(maxRows)
	default:
		return []string{"Unknown screen."}
	}
}

func (m *Model) renderOverview() []string {
	states := make(map[detection.State]int)
	for _, item := range m.inventory.Tools {
		states[item.State]++
	}
	managers := make([]string, 0, len(m.inventory.Managers))
	for _, manager := range m.inventory.Managers {
		managers = append(managers, string(manager.Manager))
	}
	if len(managers) == 0 {
		managers = append(managers, "none detected")
	}
	return []string{
		strongStyle.Render("Welcome to DX Bootstrap"),
		"Choose the developer tools you need, review the plan, then approve it to install.",
		"You stay in control: nothing is installed until you approve the complete plan.",
		"",
		buttonStyle.Render("[Start setup]"),
		"",
		strongStyle.Render("This machine"),
		fmt.Sprintf("System: %s / %s", m.inventory.Platform.OS, m.inventory.Platform.Architecture),
		fmt.Sprintf("Package managers found: %s", strings.Join(managers, ", ")),
		fmt.Sprintf("Tools selected: %d", len(m.selectedIDs())),
		fmt.Sprintf("Already installed: %d  |  Not found: %d  |  Needs review: %d", states[detection.Healthy], states[detection.Missing], states[detection.Unknown]+states[detection.Broken]+states[detection.Partial]+states[detection.Conflicting]),
	}
}

func (m *Model) renderCatalog(maxRows int) []string {
	tools := m.filteredTools()
	if len(tools) == 0 {
		lines := []string{strongStyle.Render("Choose tools")}
		lines = append(lines, buttonRows(m.width, "Search", "Category -", "Category +", "Installation state", "Clear filters", "Use profiles")...)
		lines = append(lines, "No catalog entries match these filters.")
		return lines
	}
	start, end := visibleWindow(len(tools), m.cursor, max(1, maxRows-7))
	lines := []string{
		strongStyle.Render("Choose tools"),
		"Select tools one at a time, or choose a profile to start with a set of tools.",
	}
	lines = append(lines, buttonRows(m.width, "Review my selection", "Use profiles", "Search", "Category -", "Category +", "Installation state", "Clear filters", "Import selection", "Export selection")...)
	searchSummary := valueOr(m.filter, "all")
	if m.searching {
		searchSummary = m.filter + "_"
		lines = append(lines, mutedStyle.Render("Type to search. Enter finishes; Esc keeps the current search."))
	}
	lines = append(lines, mutedStyle.Render(fmt.Sprintf("Selected: %d  |  Search: %s  |  Category: %s  |  Installed filter: %s", len(m.selectedIDs()), searchSummary, valueOr(m.categoryFilter, "all"), m.stateFilter)))
	for index := start; index < end; index++ {
		item := tools[index]
		selection := "[ ]"
		if m.selected[item.ID] {
			selection = selectedStyle.Render("[x]")
		}
		row := fmt.Sprintf("%s %-18s %-24s %s", selection, item.ID, item.Name, stateLabel(m.inventory.Tools[item.ID].State))
		if index == m.cursor {
			row = activeStyle.Render("> ") + row
		} else {
			row = "  " + row
		}
		lines = append(lines, row)
	}
	item := tools[min(max(m.cursor, 0), len(tools)-1)]
	lines = append(lines, "", strongStyle.Render(item.Name+" details"), item.Description, "Category: "+item.Category)
	lines = append(lines, "Version channel: "+item.Version.Channel)
	if dependencies := item.DependenciesFor(m.inventory.Platform.OS); len(dependencies) > 0 {
		lines = append(lines, "Dependencies: "+strings.Join(dependencies, ", "))
	}
	if len(item.Conflicts) > 0 {
		lines = append(lines, "Conflicts: "+strings.Join(item.Conflicts, ", "))
	}
	platforms := make([]string, 0, len(item.Platforms))
	for operatingSystem, architectures := range item.Platforms {
		architecturesText := make([]string, len(architectures))
		for index, architecture := range architectures {
			architecturesText[index] = string(architecture)
		}
		platforms = append(platforms, string(operatingSystem)+"/"+strings.Join(architecturesText, ", "))
	}
	sort.Strings(platforms)
	lines = append(lines, "Supported platforms: "+strings.Join(platforms, "; "), "Supported here: "+yesNo(item.Supports(m.inventory.Platform.OS, m.inventory.Platform.Architecture)))
	verification := make([]string, len(item.Executables))
	for index, executable := range item.Executables {
		verification[index] = executable + " " + strings.Join(item.VersionArgs, " ")
	}
	lines = append(lines, "Verification: "+strings.Join(verification, "; "))
	if detected, ok := m.inventory.Tools[item.ID]; ok && len(detected.Instances) > 0 {
		for _, instance := range detected.Instances {
			lines = append(lines, "Existing: "+valueOr(instance.Version, string(instance.State))+" at "+instance.Path+" ["+display.ArchitectureVerification(instance)+"]")
		}
	}
	if detected, ok := m.inventory.Tools[item.ID]; ok {
		for _, installed := range detected.Packages {
			lines = append(lines, "Package record: "+display.PackageOwnership(string(installed.Manager), installed.ID, installed.Version, installed.Source))
		}
	}
	if installer, ok := item.InstallerForPlatform(m.inventory.Platform.OS, m.inventory.Platform.Architecture, m.managerOrder()); ok {
		lines = append(lines, fmt.Sprintf("Proposed source: %s / %s (%s)", installer.Manager, installer.PackageID, installer.Source))
	} else {
		if artifact, exists := item.ArtifactFor(m.inventory.Platform.OS, m.inventory.Platform.Architecture); exists {
			lines = append(lines, "Proposed method: verified official archive", "Artifact version: "+artifact.Version, "Official source: "+artifact.URL, "SHA-256: "+artifact.SHA256)
			if artifact.SizeEstimateNote != "" {
				lines = append(lines, "Download estimate: "+artifact.SizeEstimateNote)
			}
			if artifact.InstallationNote != "" {
				lines = append(lines, "Setup: "+artifact.InstallationNote)
			}
		} else {
			lines = append(lines, warningStyle.Render("No installer or verified archive is available here."))
		}
	}
	for _, installer := range item.Installers {
		lines = append(lines, fmt.Sprintf("Installer: %s / %s / %s / %s / %s", installer.Platform, installer.Manager, installer.PackageID, installer.Scope, installer.Source))
	}
	return lines
}

func (m *Model) renderProfiles(maxRows int) []string {
	if m.catalog == nil {
		return []string{strongStyle.Render("Choose a profile"), "The tool catalog is not available."}
	}
	profiles := m.catalog.Profiles()
	start, end := visibleWindow(len(profiles), m.cursor, max(1, (maxRows-4)/2))
	lines := []string{
		strongStyle.Render("Start with a profile"),
		"Profiles are editable starting points. You can remove tools before reviewing.",
	}
	lines = append(lines, buttonRows(m.width, "Browse tools", "Apply profiles", "Import selection", "Export selection")...)
	for index := start; index < end; index++ {
		profile := profiles[index]
		selection := "[ ]"
		if m.selectedProfiles[profile.ID] {
			selection = selectedStyle.Render("[x]")
		}
		row := fmt.Sprintf("%s %-24s %d tools", selection, profile.Name, len(profile.Tools))
		if index == m.cursor {
			row = activeStyle.Render("> ") + row
		} else {
			row = "  " + row
		}
		lines = append(lines, row, "    "+mutedStyle.Render(profile.Description))
	}
	return lines
}

func (m *Model) renderEnvironment(maxRows int) []string {
	ids := m.filteredEnvironmentIDs()
	start, end := visibleWindow(len(ids), m.cursor, max(1, maxRows-5))
	searchSummary := valueOr(m.environmentFilter, "all")
	lines := []string{
		strongStyle.Render("Your existing tools"),
		"Healthy means the tool runs. Needs review means DX could not verify it fully.",
	}
	if m.environmentSearching {
		searchSummary = m.environmentFilter + "_"
		lines = append(lines, mutedStyle.Render("Type to search tool names, paths, and versions. Enter finishes."))
	}
	lines = append(lines, buttonRows(m.width, "Search", "Clear search", "Refresh inventory")...)
	lines = append(lines, "Search: "+searchSummary)
	warningCount := min(len(m.inventory.Warnings), 3)
	for _, warning := range m.inventory.Warnings[:warningCount] {
		lines = append(lines, warningStyle.Render("Inventory warning: "+warning))
	}
	if len(m.inventory.Warnings) > warningCount {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("%d additional inventory warning(s) omitted", len(m.inventory.Warnings)-warningCount)))
	}
	if len(ids) == 0 {
		lines = append(lines, "No installed-tool records match this search.", "Press a to clear the search.")
		return lines
	}
	for index := start; index < end; index++ {
		item := m.inventory.Tools[ids[index]]
		row := fmt.Sprintf("%-18s %-28s %s", item.ID, item.Name, stateLabel(item.State))
		if index == m.cursor {
			row = activeStyle.Render("> ") + row
		} else {
			row = "  " + row
		}
		lines = append(lines, row)
	}
	if len(ids) > 0 {
		item := m.inventory.Tools[ids[min(max(m.cursor, 0), len(ids)-1)]]
		for _, instance := range item.Instances {
			lines = append(lines, "Path: "+instance.Path, "Version: "+valueOr(instance.Version, "unknown"))
			lines = append(lines, "Architecture: "+display.ArchitectureVerification(instance))
			if instance.ArchitectureDiagnostic != "" {
				lines = append(lines, warningStyle.Render("Architecture diagnostic: "+instance.ArchitectureDiagnostic))
			}
			if instance.Diagnostic != "" {
				lines = append(lines, warningStyle.Render("Diagnostic: "+instance.Diagnostic))
			}
		}
		for _, installed := range item.Packages {
			lines = append(lines, "Package: "+display.PackageOwnership(string(installed.Manager), installed.ID, installed.Version, installed.Source))
		}
		if len(item.Packages) > 0 && len(item.Instances) == 0 {
			lines = append(lines, warningStyle.Render("Package database record found, but its executable was not found on PATH; it will be preserved."))
		}
	}
	return lines
}

func (m *Model) renderPlan(_ int) []string {
	lines := []string{
		strongStyle.Render("Review your plan"),
		"Check what DX will install, where it comes from, and any setup changes.",
	}
	if !m.planReady {
		return append(lines, buttonStyle.Render("[Build plan]"), "Your selection is checked first. This step does not install anything.")
	}
	if m.plan.Blocked {
		lines = append(lines, warningStyle.Render("This plan has an issue that must be fixed before installation."), buttonStyle.Render("[Refresh inventory]"))
	} else if len(m.plan.InstallItems()) == 0 {
		lines = append(lines, goodStyle.Render("Nothing needs installing. All selected tools are already present."))
	} else {
		lines = append(lines, buttonStyle.Render("[Review and install]"), mutedStyle.Render("The next step shows a final confirmation before any command runs."))
	}
	lines = append(lines, mutedStyle.Render("Every install is explicit. Existing tools are preserved and not upgraded."))
	if m.plan.Blocked {
		lines = append(lines, warningStyle.Render("Execution is blocked until every blocking issue is resolved."))
	}
	rows := make([]string, 0, len(m.plan.Items)+len(m.plan.Issues))
	for _, item := range m.plan.Items {
		if item.Action == planning.Preserve {
			rows = append(rows, fmt.Sprintf("Already present  %-18s %s", item.ToolID, stateLabel(item.ExistingState)))
			for _, installed := range item.ExistingPackages {
				rows = append(rows, "          package: "+display.PackageOwnership(string(installed.Manager), installed.ID, installed.Version, installed.Source))
			}
			continue
		}
		if item.Action == planning.Provided {
			rows = append(rows, fmt.Sprintf("Included with %-18s %s; verify after setup", item.ProvidedBy, item.ToolID))
			continue
		}
		privilege := "user scope"
		if item.RequiresPrivilege || item.Scope == "system" {
			privilege = "system scope / privilege may be required"
		}
		if item.Artifact != nil {
			sizeEstimate := display.ApproximateSize(item.ApproximateSize)
			if item.SizeEstimateNote != "" {
				sizeEstimate = item.SizeEstimateNote
			}
			rows = append(rows, fmt.Sprintf("INSTALL   %-18s %s  %s", item.ToolID, item.Version, privilege),
				"          source: "+item.Source,
				"          SHA-256: "+item.Artifact.SHA256,
				"          approximate download: "+sizeEstimate,
				"          install path: "+item.InstallPath)
			if item.InstallationNote != "" {
				rows = append(rows, "          setup: "+item.InstallationNote)
			}
			if item.OfflineArchivePath != "" {
				rows = append(rows, "          verified local archive: "+item.OfflineArchivePath)
			} else if item.CustomSourceURL != "" {
				rows = append(rows, "          custom HTTPS source; catalog SHA-256 required: "+item.CustomSourceURL)
			}
			if item.RegionalFallback && item.Artifact.RegionalURL != "" {
				rows = append(rows, "          verified regional fallback after retryable official failures: "+item.Artifact.RegionalURL)
			}
			for _, change := range item.PathChanges {
				if change.Manual {
					rows = append(rows, "          PATH requires manual setup: "+change.Target, "          manual command: "+change.ManualCommand)
				} else if change.AlreadyConfigured {
					rows = append(rows, "          PATH already configured in "+change.Target+"; add to this session")
				} else {
					rows = append(rows, "          persistent PATH change: add "+change.Directory+" to "+change.Target)
				}
			}
		} else if item.RegistryInstaller != nil {
			rows = append(rows, fmt.Sprintf("INSTALL   %-18s %s  %s  user scope", item.ToolID, item.Version, item.RegistryInstaller.Ecosystem),
				"          package: "+item.PackageID,
				"          registry: "+item.Source,
				"          install path: "+item.InstallPath,
				"          command: "+item.Command.Display())
			for _, change := range item.PathChanges {
				if change.Manual {
					rows = append(rows, "          PATH requires manual setup: "+change.Target, "          manual command: "+change.ManualCommand)
				} else if change.AlreadyConfigured {
					rows = append(rows, "          PATH already configured in "+change.Target+"; add to this session")
				} else {
					rows = append(rows, "          persistent PATH change: add "+change.Directory+" to "+change.Target)
				}
			}
		} else {
			rows = append(rows, fmt.Sprintf("INSTALL   %-18s %s  %s  %s", item.ToolID, item.Manager, item.PackageID, privilege), "          source: "+item.Source)
			if item.InstallerKind != "" {
				rows = append(rows, "          package kind: "+item.InstallerKind)
			}
			if item.CandidateVersion != "" {
				rows = append(rows, "          candidate version: "+item.CandidateVersion)
			}
			if item.ApproximateSize > 0 {
				rows = append(rows, "          approximate download: "+display.ApproximateSize(item.ApproximateSize))
			} else if item.SizeEstimateNote != "" {
				rows = append(rows, "          approximate download: "+item.SizeEstimateNote)
			}
			rows = append(rows, "          command: "+item.Command.Display())
			if item.TransactionPreview != "" {
				rows = append(rows, "          transaction preview: "+item.TransactionPreview)
			}
			if item.TransactionNote != "" {
				rows = append(rows, warningStyle.Render("          preview note: "+item.TransactionNote))
			}
		}
	}
	for _, issue := range m.plan.Issues {
		label := "Needs attention"
		if issue.Severity == planning.Blocking {
			label = "Blocks installation"
		}
		rows = append(rows, fmt.Sprintf("%s  %s: %s", label, issue.ToolID, issue.Message))
	}
	if len(rows) == 0 {
		rows = append(rows, "No package-manager operations are required.")
	}
	lines = append(lines, rows...)
	if !m.plan.Blocked && len(m.plan.InstallItems()) > 0 {
		lines = append(lines, warningStyle.Render(m.plan.TransactionDisclosure))
	}
	return lines
}

func (m *Model) renderSources(maxRows int) []string {
	managers := m.inventory.Managers
	lines := []string{
		strongStyle.Render("Advanced source settings"),
		"Choose where downloads come from. Publisher archives remain checksum-verified.",
		"Detected region: " + regionLabel(m.regionDetection.Region),
		"Current choice: " + sourceModeLabel(m.sourceMode),
	}
	lines = append(lines, buttonRows(m.width, "Automatic sources", "Mainland China", "Global sources", "Publisher only", "Publisher first", "Custom archive", "Registry", "Local archive", "Signed bundle", "Clear source overrides", "Reset package manager")...)
	lines = append(lines, strongStyle.Render("Package manager"), "DX prefers a detected manager when it supports a selected tool.")
	if m.offlineBundlePath != "" {
		lines = append(lines, "Verified signed offline bundle: "+m.offlineBundlePath)
	}
	for _, signal := range m.regionDetection.Signals {
		lines = append(lines, fmt.Sprintf("  %s: %s (%s)", signal.Source, signal.Value, signal.Region))
	}
	for _, ecosystem := range []tool.RegistryEcosystem{tool.NPMRegistry, tool.PyPIRegistry, tool.GoModuleRegistry, tool.CargoRegistry} {
		if source := m.registrySources[ecosystem]; source != "" {
			lines = append(lines, "Registry override "+string(ecosystem)+": "+source)
		}
	}
	if len(managers) == 0 {
		lines = append(lines, warningStyle.Render("No supported package manager was detected."))
	} else {
		start, end := visibleWindow(len(managers), m.cursor, maxRows-len(lines)-3)
		for index := start; index < end; index++ {
			manager := managers[index]
			prefix := "  "
			if manager.Manager == m.preferredManager {
				prefix = "✓ "
			}
			label := "Prefer " + string(manager.Manager)
			lines = append(lines, prefix+buttonStyle.Render("["+label+"]")+"  "+filepath.Base(manager.Path))
		}
	}
	for _, id := range m.selectedIDs() {
		if sourceURL := m.customSources[id]; sourceURL != "" {
			lines = append(lines, id+": custom HTTPS source "+sourceURL)
			continue
		}
		if archivePath := m.offlineArchives[id]; archivePath != "" {
			lines = append(lines, id+": local archive "+archivePath)
			continue
		}
		manifest, exists := m.catalog.Tool(id)
		if !exists {
			continue
		}
		registryShown := false
		for _, installer := range manifest.RegistryInstallers {
			if installer.Platform == m.inventory.Platform.OS {
				source := m.registrySources[installer.Ecosystem]
				if source == "" {
					source = installer.Source
				}
				lines = append(lines, fmt.Sprintf("%s: %s registry %s; exact version %s", id, installer.Ecosystem, source, installer.Version))
				registryShown = true
				break
			}
		}
		if registryShown {
			continue
		}
		artifact, exists := manifest.ArtifactFor(m.inventory.Platform.OS, m.inventory.Platform.Architecture)
		if !exists {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: official %s  SHA-256 %s", id, artifact.URL, artifact.SHA256))
		if artifact.RegionalURL != "" {
			lines = append(lines, "  verified regional mirror: "+artifact.RegionalURL)
		}
	}
	return lines
}

func (m *Model) renderProgress(_ int) []string {
	lines := []string{strongStyle.Render("Installation progress")}
	if m.session == nil {
		return append(lines, "Review and approve your plan to begin installation.", buttonStyle.Render("[Review plan]"))
	}
	results := m.session.Report()
	if m.session.Complete() {
		lines = append(lines, buttonRows(m.width, "Results")...)
	}
	lines = append(lines, fmt.Sprintf("Completed tasks: %d of %d", len(results), len(m.plan.Items)))
	if m.preparingDownloads {
		concurrency := artifacts.DefaultConcurrentDownloads
		if m.archiveInstaller != nil {
			concurrency = m.archiveInstaller.Concurrency()
		}
		lines = append(lines, fmt.Sprintf("Current operation: preparing %d independent archive download(s)", m.preparingDownloadCount), fmt.Sprintf("Concurrent download limit: %d", concurrency))
		toolIDs := make([]string, 0, len(m.downloadProgress))
		for toolID := range m.downloadProgress {
			toolIDs = append(toolIDs, toolID)
		}
		sort.Strings(toolIDs)
		for _, toolID := range toolIDs {
			progress := m.downloadProgress[toolID]
			lines = append(lines, fmt.Sprintf("%s: %s", toolID, display.DownloadProgress(progress.BytesReceived, progress.TotalBytes)))
			if progress.Phase != "" {
				lines = append(lines, fmt.Sprintf("%s verification: %s", toolID, progress.Phase))
			}
		}
	}
	if m.activeItem.ToolID != "" {
		lines = append(lines, "Current operation: "+m.activeItem.Name)
		if m.activeItem.Artifact != nil {
			lines = append(lines, "Download source: "+m.activeItem.Source, "SHA-256 verification runs before extraction.")
			if progress, exists := m.downloadProgress[m.activeItem.ToolID]; exists && progress.BytesReceived > 0 {
				if progress.SourceName != "" {
					lines = append(lines, "Prepared source: "+progress.SourceName)
				}
				lines = append(lines, "Downloaded: "+display.DownloadProgress(progress.BytesReceived, progress.TotalBytes))
				if progress.Phase != "" {
					lines = append(lines, "Verification: "+progress.Phase)
				}
			}
		} else {
			lines = append(lines, "Source: "+m.activeItem.Source, "Command: "+m.activeItem.Command.Display())
		}
	}
	for _, result := range results {
		lines = append(lines, fmt.Sprintf("%-22s %s", result.Name, resultStateLabel(result.State)))
	}
	return lines
}

func (m *Model) renderResults(maxRows int) []string {
	lines := []string{strongStyle.Render("Installation results")}
	lines = append(lines, buttonRows(m.width, "Save report", "Environment")...)
	if m.session == nil {
		return append(lines, "No installation session has run.")
	}
	results := m.session.Report()
	if len(results) == 0 {
		return append(lines, "No operations were completed.")
	}
	start, end := visibleWindow(len(results), m.cursor, maxRows-2)
	for index := start; index < end; index++ {
		result := results[index]
		row := fmt.Sprintf("%-22s %s", result.Name, resultStateLabel(result.State))
		if index == m.cursor {
			row = activeStyle.Render("> ") + row
		} else {
			row = "  " + row
		}
		lines = append(lines, row)
	}
	result := results[min(max(m.cursor, 0), len(results)-1)]
	if result.Verification != "" {
		lines = append(lines, "Verification: "+result.Verification)
	}
	if result.Source != "" {
		lines = append(lines, "Source: "+result.Source)
	}
	if result.SourceURL != "" {
		lines = append(lines, "Download URL: "+result.SourceURL)
	}
	if result.InstallPath != "" {
		lines = append(lines, "Installed at: "+result.InstallPath)
	}
	if result.SHA256 != "" {
		lines = append(lines, "SHA-256: "+result.SHA256)
	}
	for _, change := range result.PathChanges {
		if change.Manual {
			lines = append(lines, warningStyle.Render("Manual PATH setup: "+change.ManualCommand))
		} else if change.Persist {
			lines = append(lines, "PATH change: "+change.Directory+" → "+change.Target)
		}
	}
	if result.CleanupNotice != "" {
		lines = append(lines, warningStyle.Render("Temporary cleanup: "+result.CleanupNotice))
	}
	if result.Diagnostic != "" {
		lines = append(lines, warningStyle.Render("Diagnostic: "+result.Diagnostic))
	}
	lines = append(lines, mutedStyle.Render("Temporary bootstrap artifacts are removed when the application exits."))
	return lines
}

func (m *Model) renderApproval() string {
	packageCount, archiveCount := 0, 0
	for _, item := range m.plan.InstallItems() {
		if item.Artifact == nil {
			packageCount++
		} else {
			archiveCount++
		}
	}
	lines := []string{
		strongStyle.Render("Confirm installation"),
		fmt.Sprintf("DX will run %d package-manager operation(s) and install %d checksum-verified archive(s).", packageCount, archiveCount),
		"Review sources, versions, integrity checks, install locations, and PATH changes before continuing.",
	}
	if m.plan.TransactionDisclosure != "" {
		lines = append(lines, warningStyle.Render(m.plan.TransactionDisclosure))
	}
	for _, item := range m.plan.InstallItems() {
		if item.Artifact != nil {
			lines = append(lines, fmt.Sprintf("%s  version %s  SHA-256 %s  %s", item.ToolID, item.Version, item.Artifact.SHA256, item.InstallPath))
			if item.InstallationNote != "" {
				lines = append(lines, "  setup: "+item.InstallationNote)
			}
			for _, change := range item.PathChanges {
				if change.Manual {
					lines = append(lines, "  PATH manual command: "+change.ManualCommand)
				} else if change.Persist {
					lines = append(lines, "  PATH change: add "+change.Directory+" to "+change.Target)
				}
			}
			continue
		}
		if item.RegistryInstaller != nil {
			lines = append(lines, fmt.Sprintf("%s  %s registry  %s %s  %s", item.ToolID, item.RegistryInstaller.Ecosystem, item.PackageID, item.Version, item.Source))
			if item.TransactionNote != "" {
				lines = append(lines, warningStyle.Render("  preview note: "+item.TransactionNote))
			}
			for _, change := range item.PathChanges {
				if change.Manual {
					lines = append(lines, "  PATH manual command: "+change.ManualCommand)
				} else if change.Persist {
					lines = append(lines, "  PATH change: add "+change.Directory+" to "+change.Target)
				}
			}
			continue
		}
		lines = append(lines, fmt.Sprintf("%s  %s  %s  %s", item.ToolID, item.Manager, item.PackageID, item.Scope))
		if item.InstallerKind != "" {
			lines = append(lines, "  package kind: "+item.InstallerKind)
		}
		if item.CandidateVersion != "" {
			lines = append(lines, "  candidate version: "+item.CandidateVersion)
		}
		if item.TransactionPreview != "" {
			lines = append(lines, "  transaction preview: "+item.TransactionPreview)
		}
		if item.TransactionNote != "" {
			lines = append(lines, warningStyle.Render("  preview note: "+item.TransactionNote))
		}
	}
	lines = append(lines,
		warningStyle.Render("DX will refresh the machine state first. If the plan changes, it will ask you to review again."),
		mutedStyle.Render("Keyboard: y installs after a fresh check; n or Esc cancels."),
	)
	lines = append(lines, buttonRows(m.width, "Install now", "Cancel installation")...)
	return strings.Join(lines, "\n")
}

func (m *Model) renderPathPrompt() string {
	label := "Export selected tools to a new JSON file"
	if m.pathMode == "import" {
		label = "Import selected tools from a JSON file"
	} else if m.pathMode == "diagnostic" {
		label = "Save a redacted diagnostic report to a new JSON file"
	} else if m.pathMode == "custom-source" {
		label = "Custom HTTPS source (tool-id=https://...)"
	} else if m.pathMode == "registry" {
		label = "Registry override (npm, pypi, or go-modules=https://...)"
	} else if m.pathMode == "offline-archive" {
		label = "Offline archive (tool-id=absolute-path)"
	} else if m.pathMode == "offline-bundle" {
		label = "Signed offline bundle directory"
	}
	lines := []string{strongStyle.Render(label), "Path: " + m.pathInput + "_"}
	lines = append(lines, buttonRows(m.width, "Confirm", "Cancel input")...)
	lines = append(lines, mutedStyle.Render("Type the value above, then press Enter. Esc cancels."))
	return strings.Join(lines, "\n")
}

func (m *Model) platformLabel() string {
	parts := []string{string(m.inventory.Platform.OS), string(m.inventory.Platform.Architecture)}
	if m.inventory.Platform.Distribution != "" {
		parts = append(parts, m.inventory.Platform.Distribution)
	}
	if m.inventory.Platform.Shell != "" {
		parts = append(parts, m.inventory.Platform.Shell)
	}
	return strings.Join(parts, "  /  ")
}

func (m *Model) keyHelp() string {
	if m.pathMode != "" {
		return "Enter confirm  |  Esc cancel"
	}
	if m.approval {
		return "y approve  |  n cancel  |  Page Up/Down or wheel to review"
	}
	if m.searching {
		return "Type to search  |  Enter finish  |  Esc keep current search"
	}
	if m.environmentSearching {
		return "Type to search installed tools, versions, and paths  |  Enter finish  |  Esc keep current search"
	}
	switch m.screen {
	case CatalogScreen:
		return "Tab next  |  arrows move  |  Space select  |  f search  |  e export  |  o import  |  wheel scroll"
	case ProfilesScreen:
		return "Space select  |  Enter apply  |  e export  |  o import  |  click Browse tools to edit"
	case EnvironmentScreen:
		return "arrows inspect  |  f search  |  a clear search  |  r refresh  |  wheel scroll"
	case PlanScreen:
		return "p rebuild plan  |  i review and install  |  arrows/Page Up/Page Down/wheel scroll"
	case SourcesScreen:
		return "a/c/g/o/m source  |  u/v/l/b add source  |  x clear  |  0 reset manager"
	case ResultsScreen:
		return "arrows inspect results  |  d save report  |  click Environment for details"
	default:
		return "Tab next  |  Enter continue  |  arrows/wheel scroll"
	}
}

func regionLabel(region regional.Region) string {
	switch region {
	case regional.MainlandChina:
		return "Mainland China"
	case regional.Global:
		return "Global"
	case regional.Conflict:
		return warningStyle.Render("conflicting signals; choose c or g")
	default:
		return "unknown; automatic policy remains official-only"
	}
}

func sourceModeLabel(mode string) string {
	switch mode {
	case "automatic":
		return "Automatic"
	case "mainland-china":
		return "Mainland China"
	case "global":
		return "Global"
	case "official-only":
		return "Publisher only"
	case "official-first":
		return "Publisher first"
	default:
		return "Unknown"
	}
}

func renderHelp() string {
	return strings.Join([]string{
		strongStyle.Render("Using this screen"),
		"Click the labeled controls, or use the keyboard shortcuts shown beside them.",
		buttonStyle.Render("[Close help]"),
		"Tab / Shift+Tab    Move through setup steps",
		"Up / Down          Move through lists; Space selects an item",
		"Page Up / Down     Move faster through long lists",
		"Mouse wheel        Scroll lists and details",
		"Enter              Continue or activate the current choice",
		"f                  Search tools or installed software",
		"[ / ] and s        Change tool filters; a clears them",
		"p                  Rebuild the review plan",
		"i, then y          Review, refresh, and approve installation",
		"r                  Refresh the machine inventory",
		"Sources: a/c/g/o/m choose policy; u/v/l/b configure a source; x clears overrides",
		"Sources: Space/Enter chooses a package manager; 0 restores automatic choice",
		"Catalog/Profiles: e exports and o imports a tool selection; Results: d saves a report",
		"?                  Close help; q exits",
		"",
		mutedStyle.Render("Nothing installs until you review and approve the complete plan."),
	}, "\n")
}

func stateLabel(state detection.State) string {
	switch state {
	case detection.Healthy:
		return goodStyle.Render("Ready")
	case detection.Missing:
		return mutedStyle.Render("Not installed")
	case detection.Broken:
		return errorStyle.Render("Needs repair")
	case detection.Partial:
		return errorStyle.Render("Partly installed")
	case detection.Conflicting:
		return errorStyle.Render("Conflicting installs")
	case detection.Unknown:
		return warningStyle.Render("Needs review")
	case detection.Unsupported:
		return warningStyle.Render("Not supported here")
	case detection.Outdated:
		return warningStyle.Render("Update available")
	default:
		return string(state)
	}
}

func resultStateLabel(state installation.ResultState) string {
	switch state {
	case installation.InstalledVerified:
		return goodStyle.Render("Installed and verified")
	case installation.AlreadySatisfied:
		return goodStyle.Render("Already installed")
	case installation.Preserved:
		return mutedStyle.Render("Kept existing tool")
	case installation.Skipped:
		return mutedStyle.Render("Skipped")
	case installation.PartiallyInstalled:
		return warningStyle.Render("Partly installed; needs attention")
	case installation.Failed:
		return errorStyle.Render("Failed")
	default:
		return string(state)
	}
}

func visibleWindow(length, cursor, maxRows int) (int, int) {
	if maxRows < 1 {
		maxRows = 1
	}
	if length <= maxRows {
		return 0, length
	}
	start := max(0, cursor-maxRows/2)
	if start+maxRows > length {
		start = length - maxRows
	}
	return start, start + maxRows
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
