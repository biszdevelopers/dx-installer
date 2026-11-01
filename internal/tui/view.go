package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/display"
	"developers-club-dx/internal/domain/tool"
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
)

var screenNames = []string{"Overview", "Catalog", "Profiles", "Environment", "Plan", "Sources", "Progress", "Results"}

func (m *Model) render() string {
	width := min(max(m.width, 24), 140)
	height := min(max(m.height, 12), 60)
	brand := "Developers' Club DX  /  Bootstrap"
	status := m.status
	help := m.keyHelp()
	statusStyle := mutedStyle
	if m.planReady && m.plan.Blocked {
		statusStyle = warningStyle
	}
	if width < 48 {
		brand = "DX Bootstrap"
		status = ansi.Truncate(status, width-2, "…")
	}
	if width < 36 {
		help = "? help  |  j/k move  |  q quit"
		if m.screen == CatalogScreen || m.screen == EnvironmentScreen {
			help = "f search  |  a clear  |  q quit"
		}
	}
	header := []string{brandStyle.Render(brand), mutedStyle.Render(m.platformLabel()), m.renderNavigation(width), strings.Repeat("-", width)}
	header = wrapLayout(header, width)
	footer := wrapLayout([]string{strings.Repeat("-", width), statusStyle.Render(status), mutedStyle.Render(help)}, width)
	availableRows := max(0, height-len(header)-len(footer))
	var body []string
	switch {
	case m.pathMode != "":
		body = strings.Split(m.renderPathPrompt(), "\n")
	case m.help:
		body = strings.Split(renderHelp(), "\n")
	case m.approval:
		body = strings.Split(m.renderApproval(), "\n")
	default:
		body = m.renderScreen(max(1, availableRows))
	}
	body = wrapLayout(body, width)
	if len(body) > availableRows {
		body = body[:availableRows]
	}
	return strings.Join(append(append(header, body...), footer...), "\n")
}

func wrapLayout(lines []string, width int) []string {
	if len(lines) == 0 {
		return nil
	}
	wrapped := lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	return strings.Split(wrapped, "\n")
}

func (m *Model) renderNavigation(width int) string {
	if width < 36 {
		return activeStyle.Render("DX  /  " + screenNames[m.screen])
	}
	if width < 70 {
		return activeStyle.Render(fmt.Sprintf("%d %s", m.screen+1, screenNames[m.screen])) + mutedStyle.Render("  |  Tab to navigate")
	}
	parts := make([]string, 0, len(screenNames))
	for index, name := range screenNames {
		label := fmt.Sprintf("%d %s", index+1, name)
		if Screen(index) == m.screen {
			parts = append(parts, activeStyle.Render("["+label+"]"))
		} else {
			parts = append(parts, mutedStyle.Render(label))
		}
	}
	return strings.Join(parts, "   ")
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
		strongStyle.Render("Machine overview"),
		fmt.Sprintf("Operating system  %s", m.inventory.Platform.OS),
		fmt.Sprintf("Architecture      %s", m.inventory.Platform.Architecture),
		fmt.Sprintf("Distribution      %s", valueOr(m.inventory.Platform.Distribution, "not applicable")),
		fmt.Sprintf("Shell             %s", valueOr(m.inventory.Platform.Shell, "unknown")),
		fmt.Sprintf("Package managers  %s", strings.Join(managers, ", ")),
		fmt.Sprintf("Region signals    %s", regionLabel(m.regionDetection.Region)),
		fmt.Sprintf("Source policy     %s", m.sourceMode),
		fmt.Sprintf("Existing tools    %d healthy  %d missing  %d needing review", states[detection.Healthy], states[detection.Missing], states[detection.Unknown]+states[detection.Broken]+states[detection.Partial]+states[detection.Conflicting]),
		fmt.Sprintf("Selected tools    %d", len(m.selectedIDs())),
		"",
		mutedStyle.Render("No machine health score is calculated. Existing installations are preserved unless a reviewed plan says otherwise."),
	}
}

func (m *Model) renderCatalog(maxRows int) []string {
	tools := m.filteredTools()
	if len(tools) == 0 {
		return []string{"No catalog entries match these filters.", "Press a to clear search and filters."}
	}
	start, end := visibleWindow(len(tools), m.cursor, maxRows)
	lines := []string{strongStyle.Render("Tool catalog"), mutedStyle.Render(fmt.Sprintf("Search: %s   Category: %s   State: %s", valueOr(m.filter, "all"), valueOr(m.categoryFilter, "all"), m.stateFilter))}
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
	if len(item.Dependencies) > 0 {
		lines = append(lines, "Dependencies: "+strings.Join(item.Dependencies, ", "))
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
	if installer, ok := item.InstallerFor(m.inventory.Platform.OS, m.managerOrder()); ok {
		lines = append(lines, fmt.Sprintf("Proposed source: %s / %s (%s)", installer.Manager, installer.PackageID, installer.Source))
	} else {
		if artifact, exists := item.ArtifactFor(m.inventory.Platform.OS, m.inventory.Platform.Architecture); exists {
			lines = append(lines, "Proposed method: verified official archive", "Artifact version: "+artifact.Version, "Official source: "+artifact.URL, "SHA-256: "+artifact.SHA256)
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
	profiles := m.catalog.Profiles()
	start, end := visibleWindow(len(profiles), m.cursor, maxRows/2)
	lines := []string{strongStyle.Render("Profiles"), "Select multiple profiles to combine them, then press Enter to apply."}
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
	start, end := visibleWindow(len(ids), m.cursor, maxRows)
	lines := []string{strongStyle.Render("Existing environment"), "Search: " + valueOr(m.environmentFilter, "all"), "Each detected executable is checked with its declared version command."}
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

func (m *Model) renderPlan(maxRows int) []string {
	lines := []string{strongStyle.Render("Installation plan"), "Every install is explicit. Existing tools are preserved and no upgrades are planned."}
	if !m.planReady {
		return append(lines, "No plan yet. Select tools, then press p to create a dry run.")
	}
	if m.plan.Blocked {
		lines = append(lines, warningStyle.Render("Execution is blocked until every blocking issue is resolved."))
	}
	rows := make([]string, 0, len(m.plan.Items)+len(m.plan.Issues))
	for _, item := range m.plan.Items {
		if item.Action == planning.Preserve {
			rows = append(rows, fmt.Sprintf("PRESERVE  %-18s %s", item.ToolID, item.ExistingState))
			for _, installed := range item.ExistingPackages {
				rows = append(rows, "          package: "+display.PackageOwnership(string(installed.Manager), installed.ID, installed.Version, installed.Source))
			}
			continue
		}
		if item.Action == planning.Provided {
			rows = append(rows, fmt.Sprintf("PROVIDED  %-18s by %s; verify after dependency install", item.ToolID, item.ProvidedBy))
			continue
		}
		privilege := "user scope"
		if item.RequiresPrivilege || item.Scope == "system" {
			privilege = "system scope / privilege may be required"
		}
		if item.Artifact != nil {
			rows = append(rows, fmt.Sprintf("INSTALL   %-18s %s  %s", item.ToolID, item.Version, privilege),
				"          source: "+item.Source,
				"          SHA-256: "+item.Artifact.SHA256,
				"          approximate download: "+approximateSize(item.ApproximateSize),
				"          install path: "+item.InstallPath)
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
			rows = append(rows, fmt.Sprintf("INSTALL   %-18s %s  %s  %s", item.ToolID, item.Manager, item.PackageID, privilege), "          source: "+item.Source, "          command: "+item.Command.Display())
		}
	}
	for _, issue := range m.plan.Issues {
		rows = append(rows, fmt.Sprintf("%s  %s: %s", strings.ToUpper(string(issue.Severity)), issue.ToolID, issue.Message))
	}
	if len(rows) == 0 {
		rows = append(rows, "No package-manager operations are required.")
	}
	start, end := visibleWindow(len(rows), m.cursor, maxRows-3)
	lines = append(lines, rows[start:end]...)
	if !m.plan.Blocked && len(m.plan.InstallItems()) > 0 {
		lines = append(lines, warningStyle.Render(m.plan.TransactionDisclosure))
	}
	return lines
}

func (m *Model) renderSources(maxRows int) []string {
	managers := m.inventory.Managers
	lines := []string{strongStyle.Render("Download sources"), "Package managers already on this machine are preferred when they support the selected tool.", "Direct archives use pinned publisher digests; regional mirrors must match that digest.", "Region signals: " + regionLabel(m.regionDetection.Region), "Source policy: " + m.sourceMode, "Keys: a automatic  |  c Mainland China  |  g global  |  o official-only  |  m official-first  |  u custom archive source  |  v registry override  |  l local archive  |  b signed bundle  |  x clear overrides  |  Space/Enter manager  |  0 reset", "A preferred detected package manager is selected when it has a compatible catalog entry.", ""}
	if m.offlineBundlePath != "" {
		lines = append(lines, "Verified signed offline bundle: "+m.offlineBundlePath)
	}
	for _, signal := range m.regionDetection.Signals {
		lines = append(lines, fmt.Sprintf("  %s: %s (%s)", signal.Source, signal.Value, signal.Region))
	}
	for _, ecosystem := range []tool.RegistryEcosystem{tool.NPMRegistry, tool.PyPIRegistry, tool.GoModuleRegistry} {
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
			label := string(manager.Manager)
			if manager.Manager == m.preferredManager {
				label += "  (preferred)"
			}
			row := fmt.Sprintf("%-22s %s", label, filepath.Base(manager.Path))
			if index == m.cursor {
				row = activeStyle.Render("> ") + row
			} else {
				row = "  " + row
			}
			lines = append(lines, row)
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

func (m *Model) renderProgress(maxRows int) []string {
	lines := []string{strongStyle.Render("Installation progress")}
	if m.session == nil {
		return append(lines, "Approve a reviewed plan to begin installation.")
	}
	results := m.session.Report()
	lines = append(lines, fmt.Sprintf("Completed tasks: %d of %d", len(results), len(m.plan.Items)))
	if m.activeItem.ToolID != "" {
		lines = append(lines, "Current operation: "+m.activeItem.Name)
		if m.activeItem.Artifact != nil {
			lines = append(lines, "Download source: "+m.activeItem.Source, "Verifying SHA-256 and extracting the archive.")
		} else {
			lines = append(lines, "Source: "+m.activeItem.Source, "Command: "+m.activeItem.Command.Display())
		}
	}
	for _, result := range results {
		lines = append(lines, fmt.Sprintf("%-22s %s", result.Name, result.State))
	}
	if len(lines) > maxRows {
		lines = lines[:maxRows]
	}
	return lines
}

func (m *Model) renderResults(maxRows int) []string {
	lines := []string{strongStyle.Render("Results and diagnostics")}
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
		row := fmt.Sprintf("%-22s %s", result.Name, result.State)
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
	lines := []string{strongStyle.Render("Approve installation plan?"), fmt.Sprintf("This will run %d package-manager command(s) and install %d verified archive(s).", packageCount, archiveCount), "Review each source, version, integrity digest, install path, and persistent PATH change before continuing."}
	if m.plan.TransactionDisclosure != "" {
		lines = append(lines, warningStyle.Render(m.plan.TransactionDisclosure))
	}
	for _, item := range m.plan.InstallItems() {
		if item.Artifact != nil {
			lines = append(lines, fmt.Sprintf("%s  version %s  SHA-256 %s  %s", item.ToolID, item.Version, item.Artifact.SHA256, item.InstallPath))
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
	}
	lines = append(lines, warningStyle.Render("Press y to refresh inventory and continue. Any plan change requires approval again."), mutedStyle.Render("Press n or Esc to cancel without changes."))
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
	return strings.Join([]string{strongStyle.Render(label), "Path: " + m.pathInput + "_", mutedStyle.Render("Enter confirms  |  Esc cancels")}, "\n")
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
		return "y approve  |  n cancel"
	}
	if m.searching {
		return "Type to search  |  Enter finish  |  Esc keep current search"
	}
	if m.environmentSearching {
		return "Type to search installed tools, versions, and paths  |  Enter finish  |  Esc keep current search"
	}
	switch m.screen {
	case CatalogScreen:
		return "Space select  |  f search  |  [ ] category  |  s state filter  |  p plan"
	case EnvironmentScreen:
		return "f search inventory  |  a clear search  |  r refresh  |  j/k inspect records"
	case ProfilesScreen:
		return "Space combine profiles  |  Enter apply  |  e export  |  o import"
	case PlanScreen:
		return "p rebuild  |  i review approval  |  r refresh inventory"
	case SourcesScreen:
		return "a/c/g region  |  o official-only  |  m official-first  |  u custom URL  |  l local archive  |  b signed bundle  |  Space/Enter manager  |  x clear sources"
	case ResultsScreen:
		return "j/k inspect results  |  d save redacted report  |  4 environment  |  q exit"
	default:
		return "Tab navigate  |  j/k move  |  r refresh  |  ? help  |  q exit"
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

func renderHelp() string {
	return strings.Join([]string{
		strongStyle.Render("Keyboard help"),
		"1-8       Open Overview, Catalog, Profiles, Environment, Plan, Sources, Progress, Results",
		"Tab       Move to the next section",
		"j / k     Move through lists",
		"Space     Select a tool, profile, or source manager",
		"f         Search the catalog",
		"f         Search installed tools, versions, and paths from Environment",
		"[ / ]     Change catalog category; s cycles the installation-state filter; a clears the active search",
		"p         Generate a dry-run plan",
		"i         Review and approve a plan",
		"r         Refresh inventory",
		"Sources: a automatic; c Mainland China; g global; o official-only; m official-first; v registry override",
		"e / o     Export or import an explicit selection file",
		"d         Save a redacted JSON diagnostic report from Results",
		"q         Exit",
		"",
		mutedStyle.Render("No installation runs until the complete plan is approved. Package-manager prompts remain visible."),
	}, "\n")
}

func stateLabel(state detection.State) string {
	switch state {
	case detection.Healthy:
		return goodStyle.Render(string(state))
	case detection.Broken, detection.Partial, detection.Conflicting:
		return errorStyle.Render(string(state))
	case detection.Missing:
		return mutedStyle.Render(string(state))
	case detection.Unknown, detection.Unsupported:
		return warningStyle.Render(string(state))
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
