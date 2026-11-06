package tui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"developers-club-dx/internal/artifacts"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/display"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/planning"
	"github.com/charmbracelet/x/ansi"
)

type uiRect struct {
	x, y, width, height int
}

func (r uiRect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.width && y >= r.y && y < r.y+r.height
}

type uiLine struct {
	text       string
	action     string
	index      int
	extra      string
	extraX     int
	extraWidth int
}

type uiTheme struct {
	text, muted, accent, border, selected, success, warning, danger, surface color.Color
}

type paletteItem struct {
	title, description, action string
	index                      int
}

func (i paletteItem) FilterValue() string { return i.title + " " + i.description }
func (i paletteItem) Title() string       { return i.title }
func (i paletteItem) Description() string { return i.description }

func (m *Model) initializeComponents() {
	m.input = textinput.New()
	m.input.Prompt = "› "
	m.input.Placeholder = "Search tools..."
	m.input.CharLimit = 4096
	m.input.SetWidth(72)
	m.contentViewport = viewport.New()
	m.contentViewport.SoftWrap = true
	m.contentViewport.MouseWheelEnabled = true
	m.detailViewport = viewport.New()
	m.detailViewport.SoftWrap = true
	m.detailViewport.MouseWheelEnabled = true
	m.helpComponent = help.New()
	m.helpComponent.ShowAll = true
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	m.commandList = list.New(nil, delegate, 52, 12)
	m.commandList.SetShowTitle(false)
	m.commandList.SetShowFilter(false)
	m.commandList.SetShowHelp(false)
	m.commandList.SetShowPagination(false)
	m.commandList.SetShowStatusBar(false)
	m.commandList.SetFilteringEnabled(false)
	m.dataTable = table.New(table.WithFocused(true))
}

func (m *Model) workspaceTheme() uiTheme {
	if m.darkTheme {
		return uiTheme{
			text:     lipgloss.Color("#E7EAF0"),
			muted:    lipgloss.Color("#9AA4B2"),
			accent:   lipgloss.Color("#65A9FF"),
			border:   lipgloss.Color("#3A4350"),
			selected: lipgloss.Color("#19314B"),
			success:  lipgloss.Color("#62D69A"),
			warning:  lipgloss.Color("#F1B85B"),
			danger:   lipgloss.Color("#FF7777"),
			surface:  lipgloss.Color("#151A20"),
		}
	}
	return uiTheme{
		text:     lipgloss.Color("#1B2430"),
		muted:    lipgloss.Color("#5B6675"),
		accent:   lipgloss.Color("#075FB5"),
		border:   lipgloss.Color("#A9B3C0"),
		selected: lipgloss.Color("#DDEBFA"),
		success:  lipgloss.Color("#13733C"),
		warning:  lipgloss.Color("#8A5700"),
		danger:   lipgloss.Color("#A32626"),
		surface:  lipgloss.Color("#F3F6F9"),
	}
}

func actionID(action string, index int) string {
	return fmt.Sprintf("%s:%d", action, index)
}

func line(text string) uiLine { return uiLine{text: text, index: -1} }

func control(text, action string) uiLine {
	return uiLine{text: text, action: action, index: 0}
}

func row(text, action string, index int) uiLine {
	return uiLine{text: text, action: action, index: index}
}

func (m *Model) renderWorkspace() string {
	focusedBeforeRender := m.focusedTarget
	m.hitTargets = nil
	w := max(24, m.width)
	h := max(12, m.height)
	if w > 300 {
		w = 300
	}
	if h > 120 {
		h = 120
	}
	theme := m.workspaceTheme()
	compact := w < 80 || h < 24
	wide := w >= 120 && h >= 32

	lines := make([]string, h)
	header := m.workspaceHeader(w, theme, compact)
	for i, text := range header {
		if i < h {
			lines[i] = fitLine(text, w)
		}
	}
	bodyY := len(header)
	footerY := h - 2
	bodyHeight := max(4, footerY-bodyY)
	m.mainViewportRect = uiRect{}
	m.detailViewportRect = uiRect{}

	if m.help || m.approval || m.pathMode != "" || m.commandPalette {
		modal := m.renderWorkspaceModal(w, h, theme)
		for i := range lines {
			lines[i] = fitLine("", w)
		}
		for index, text := range modal {
			if index < h {
				lines[index] = fitLine(text, w)
			}
		}
	} else if compact {
		mainRect := uiRect{x: 0, y: bodyY, width: w, height: bodyHeight}
		m.mainViewportRect = uiRect{x: mainRect.x + 1, y: mainRect.y + 1, width: max(1, mainRect.width-2), height: max(1, mainRect.height-2)}
		content, details := m.workspaceContent(max(1, w-4), bodyHeight-2, theme)
		if w >= 56 && bodyHeight >= 12 {
			content = append(content, line(""), line("DETAILS"))
			content = append(content, details...)
		} else if len(details) > 0 {
			content = append(content, line(""), line("Selected item details:"))
			content = append(content, details...)
		}
		panel := m.renderPane(mainRect, m.screenTitle(), content, &m.contentViewport, m.pageScroll, theme, true)
		copy(lines[bodyY:footerY], panel)
	} else {
		navWidth, inspectorWidth := 21, 31
		if !wide {
			navWidth, inspectorWidth = 20, 0
		}
		gap := 1
		mainWidth := w - navWidth - inspectorWidth - gap*2
		if inspectorWidth == 0 {
			mainWidth = w - navWidth - gap
		}
		mainWidth = max(34, mainWidth)
		navRect := uiRect{x: 0, y: bodyY, width: navWidth, height: bodyHeight}
		mainRect := uiRect{x: navWidth + gap, y: bodyY, width: mainWidth, height: bodyHeight}
		navPanel := m.renderPane(navRect, "NAVIGATION", m.navigationLines(theme), nil, 0, theme, false)
		content, details := m.workspaceContent(max(1, mainWidth-4), bodyHeight-2, theme)
		mainPanel := m.renderPane(mainRect, m.screenTitle(), content, &m.contentViewport, m.pageScroll, theme, true)
		panels := [][]string{navPanel, mainPanel}
		if inspectorWidth > 0 {
			detailRect := uiRect{x: mainRect.x + mainRect.width + gap, y: bodyY, width: inspectorWidth, height: bodyHeight}
			m.detailViewportRect = uiRect{x: detailRect.x + 1, y: detailRect.y + 1, width: detailRect.width - 2, height: detailRect.height - 2}
			detailPanel := m.renderPane(detailRect, "INSPECTOR", details, &m.detailViewport, m.inspectorScroll, theme, false)
			panels = append(panels, detailPanel)
		}
		joined := joinPanels(panels, gap, bodyHeight, w)
		copy(lines[bodyY:footerY], joined)
	}
	if m.help || m.approval || m.pathMode != "" || m.commandPalette {
		lines[footerY] = fitLine(lipgloss.NewStyle().Foreground(theme.muted).Render("  Esc closes this window  ·  ↑/↓ and mouse wheel scroll"), w)
		lines[h-1] = fitLine(lipgloss.NewStyle().Foreground(theme.muted).Render("  Enter activates the focused action"), w)
	} else {
		footer := m.workspaceFooter(w, footerY, theme)
		for index, text := range footer {
			if index+footerY < h {
				lines[index+footerY] = fitLine(text, w)
			}
		}
	}
	for i := range lines {
		if asciiFallbackEnabled() {
			lines[i] = fitLine(asciiText(lines[i]), w)
		}
	}
	focusVisible := false
	for _, target := range m.hitTargets {
		if target.id == m.focusedTarget {
			focusVisible = true
			break
		}
	}
	if !focusVisible {
		m.focusedTarget = ""
	}
	if m.focusedTarget == "" && len(m.hitTargets) > 0 {
		preferred := "nav-tools"
		if m.screen == CatalogScreen {
			preferred = "search"
		} else if m.screen == ProfilesScreen {
			preferred = "apply-profiles"
		} else if m.screen == EnvironmentScreen {
			preferred = "search"
		} else if m.screen == PlanScreen {
			preferred = "build-plan"
			if m.planReady && !m.plan.Blocked {
				preferred = "review-install"
			}
		} else if m.screen == SourcesScreen {
			preferred = "source-automatic"
		} else if m.screen == ProgressScreen {
			preferred = "nav-plan"
		} else if m.screen == ResultsScreen {
			preferred = "save-report"
		}
		for _, target := range m.hitTargets {
			if target.action == preferred {
				m.focusedTarget = target.id
				break
			}
		}
		if m.focusedTarget == "" {
			m.focusedTarget = m.hitTargets[0].id
		}
	}
	if m.focusedTarget != focusedBeforeRender {
		return m.renderWorkspace()
	}
	return strings.Join(lines, "\n")
}

func fitLine(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) > width {
		suffix := "…"
		if asciiFallbackEnabled() {
			suffix = "..."
		}
		suffixWidth := ansi.StringWidth(suffix)
		if width <= suffixWidth {
			text = ansi.Truncate(text, width, "")
		} else {
			text = ansi.Truncate(text, width-suffixWidth, suffix)
		}
	}
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func (m *Model) workspaceHeader(width int, theme uiTheme, compact bool) []string {
	brand := lipgloss.NewStyle().Bold(true).Foreground(theme.accent).Render("DX") + lipgloss.NewStyle().Bold(true).Foreground(theme.text).Render("  Developer workspace")
	platform := lipgloss.NewStyle().Foreground(theme.muted).Render(m.platformLabel())
	if width >= 80 {
		left := ansi.StringWidth(ansi.Strip(brand))
		space := max(1, width-left-ansi.StringWidth(ansi.Strip(platform))-3)
		separator := "─"
		if asciiFallbackEnabled() {
			separator = "-"
		}
		return []string{fitLine("  "+brand+strings.Repeat(" ", space)+platform, width), fitLine(strings.Repeat(separator, width), width)}
	}
	short := ansi.Truncate(ansi.Strip(platform), max(8, width-ansi.StringWidth("  DX  ")), "…")
	first := fitLine("  "+brand+"  "+lipgloss.NewStyle().Foreground(theme.muted).Render(short), width)
	if compact {
		sections := lipgloss.NewStyle().Foreground(theme.accent).Bold(true)
		if m.focusedTarget == actionID("commands", 0) || m.hoveredTarget == actionID("commands", 0) {
			sections = sections.Background(theme.selected)
		}
		if !m.help && !m.approval && m.pathMode == "" && !m.commandPalette {
			m.hitTargets = append(m.hitTargets, hitTarget{x: 2, y: 1, width: min(10, max(0, width-2)), height: 1, action: "commands", id: actionID("commands", 0)})
		}
		return []string{first, fitLine("  "+sections.Render("[Sections]")+" "+lipgloss.NewStyle().Foreground(theme.muted).Render("Ctrl+P opens commands"), width)}
	}
	return []string{first, fitLine(strings.Repeat("─", width), width)}
}

func (m *Model) workspaceFooter(width, y int, theme uiTheme) []string {
	status := m.status
	if status == "" {
		status = "Ready"
	}
	if m.planReady && m.plan.Blocked {
		status = "Plan needs attention: installation is blocked until the issue is resolved."
	}
	status = lipgloss.NewStyle().Foreground(theme.muted).Render("  " + status)
	bar := strings.Builder{}
	bar.WriteString("  ")
	x := 2
	actions := []struct{ label, action string }{}
	if m.screen == CatalogScreen || m.screen == ProfilesScreen {
		actions = append(actions, struct{ label, action string }{"Review plan", "review-selection"})
	} else if m.screen == PlanScreen && m.planReady && !m.plan.Blocked {
		actions = append(actions, struct{ label, action string }{"Install", "review-install"})
	} else if m.screen == OverviewScreen {
		actions = append(actions, struct{ label, action string }{"Choose tools", "nav-tools"})
	}
	actions = append(actions, struct{ label, action string }{"Ctrl+P Commands", "commands"}, struct{ label, action string }{"? Help", "help"})
	actions = append(actions, struct{ label, action string }{"Quit", "quit"})
	for index, action := range actions {
		visibleWidth := ansi.StringWidth("[" + action.label + "]")
		if x+visibleWidth > width {
			break
		}
		style := lipgloss.NewStyle().Foreground(theme.accent).Bold(true)
		id := actionID(action.action, 0)
		if m.focusedTarget == id || m.hoveredTarget == id {
			style = style.Background(theme.selected)
		}
		styled := style.Render("[" + action.label + "]")
		bar.WriteString(styled)
		m.hitTargets = append(m.hitTargets, hitTarget{x: x, y: y + 1, width: visibleWidth, height: 1, action: action.action, id: id})
		x += visibleWidth
		if index < len(actions)-1 {
			bar.WriteString("  ")
			x += 2
		}
	}
	return []string{fitLine(status, width), fitLine(bar.String(), width)}
}

func joinPanels(panels [][]string, gap, height, width int) []string {
	joined := make([]string, height)
	for y := 0; y < height; y++ {
		var parts []string
		for _, panel := range panels {
			if y < len(panel) {
				parts = append(parts, panel[y])
			} else {
				parts = append(parts, "")
			}
		}
		line := strings.Join(parts, strings.Repeat(" ", gap))
		joined[y] = fitLine(line, width)
	}
	return joined
}

func (m *Model) renderPane(rect uiRect, title string, content []uiLine, vp *viewport.Model, scroll int, theme uiTheme, main bool) []string {
	if rect.height <= 0 || rect.width <= 0 {
		return nil
	}
	w := rect.width
	h := rect.height
	contentWidth := max(1, w-4)
	contentHeight := max(1, h-2)
	if vp == nil {
		local := viewport.New()
		vp = &local
	}
	formatted := make([]uiLine, 0, len(content))
	for _, item := range content {
		wrapped := strings.Split(lipgloss.NewStyle().Width(contentWidth).Render(item.text), "\n")
		for index, text := range wrapped {
			layoutLine := line(strings.TrimRight(text, " "))
			if index == 0 || item.action != "" {
				layoutLine.action = item.action
				layoutLine.index = item.index
			}
			if index == 0 {
				layoutLine.extra, layoutLine.extraX, layoutLine.extraWidth = item.extra, item.extraX, item.extraWidth
			}
			formatted = append(formatted, layoutLine)
		}
	}
	vp.SetWidth(contentWidth)
	vp.SetHeight(contentHeight)
	vp.SoftWrap = false
	texts := make([]string, len(formatted))
	for index := range formatted {
		texts[index] = formatted[index].text
	}
	vp.SetContent(strings.Join(texts, "\n"))
	vp.SetYOffset(max(0, scroll))
	visible := strings.Split(vp.View(), "\n")
	if len(visible) > contentHeight {
		visible = visible[:contentHeight]
	}

	borderColor := lipgloss.NewStyle().Foreground(theme.border)
	cornerTL, cornerTR, cornerBL, cornerBR, horizontal, vertical := "┌", "┐", "└", "┘", "─", "│"
	if asciiFallbackEnabled() {
		cornerTL, cornerTR, cornerBL, cornerBR, horizontal, vertical = "+", "+", "+", "+", "-", "|"
	}
	titleText := " " + strings.ToUpper(title) + " "
	top := cornerTL + ansi.Truncate(titleText, max(0, w-2), "") + strings.Repeat(horizontal, max(0, w-2-ansi.StringWidth(titleText))) + cornerTR
	result := make([]string, h)
	result[0] = borderColor.Render(fitLine(top, w))
	for y := 1; y < h-1; y++ {
		lineIndex := vp.YOffset() + y - 1
		text := ""
		if y-1 < len(visible) {
			text = visible[y-1]
		}
		if lineIndex >= 0 && lineIndex < len(formatted) {
			item := formatted[lineIndex]
			if item.action != "" {
				id := actionID(item.action, item.index)
				if id == m.focusedTarget || id == m.hoveredTarget {
					text = lipgloss.NewStyle().Foreground(theme.text).Background(theme.selected).Bold(true).Width(contentWidth).Render(ansi.Truncate(item.text, contentWidth, "…"))
				}
				target := hitTarget{x: rect.x + 2, y: rect.y + y, width: contentWidth, height: 1, action: item.action, index: item.index, id: id}
				m.hitTargets = append(m.hitTargets, target)
				if item.extra != "" && item.extraWidth > 0 {
					extraX := min(max(0, item.extraX), max(0, contentWidth-1))
					extraWidth := min(item.extraWidth, contentWidth-extraX)
					if extraWidth > 0 {
						m.hitTargets = append(m.hitTargets, hitTarget{x: rect.x + 2 + extraX, y: rect.y + y, width: extraWidth, height: 1, action: item.extra, index: item.index, id: actionID(item.extra, item.index)})
					}
				}
			}
		}
		text = ansi.Truncate(text, contentWidth, "…")
		padding := strings.Repeat(" ", max(0, contentWidth-ansi.StringWidth(text)))
		result[y] = borderColor.Render(vertical) + " " + text + padding + " " + borderColor.Render(vertical)
	}
	if h > 1 {
		bottom := cornerBL + strings.Repeat(horizontal, max(0, w-2)) + cornerBR
		result[h-1] = borderColor.Render(fitLine(bottom, w))
	}
	if main {
		m.mainViewportRect = uiRect{x: rect.x + 1, y: rect.y + 1, width: rect.width - 2, height: rect.height - 2}
	} else if vp == &m.detailViewport {
		m.detailViewportRect = uiRect{x: rect.x + 1, y: rect.y + 1, width: rect.width - 2, height: rect.height - 2}
	}
	return result
}

func (m *Model) navigationLines(theme uiTheme) []uiLine {
	selectedCount := len(m.selectedIDs())
	planBadge := ""
	if m.planReady {
		planBadge = fmt.Sprintf("  %d", len(m.plan.InstallItems()))
	}
	items := []struct {
		name, action string
		screen       Screen
	}{
		{"⌂  Home", "nav-home", OverviewScreen},
		{fmt.Sprintf("◇  Tools  (%d)", selectedCount), "nav-tools", CatalogScreen},
		{"▤  Profiles", "nav-profiles", ProfilesScreen},
		{"◉  Environment", "nav-environment", EnvironmentScreen},
		{"◎  Review plan" + planBadge, "nav-plan", PlanScreen},
		{"↗  Activity", "nav-activity", ProgressScreen},
		{"✓  Results", "nav-results", ResultsScreen},
	}
	if asciiFallbackEnabled() {
		items[0].name, items[1].name, items[2].name, items[3].name = "*  Home", ">  Tools", "+  Profiles", "o  Environment"
		items[4].name, items[5].name, items[6].name = "!  Review plan"+planBadge, ">  Activity", "x  Results"
	}
	lines := []uiLine{line(lipgloss.NewStyle().Foreground(theme.muted).Render("WORKSPACE"))}
	for _, item := range items {
		name := item.name
		if item.screen == m.screen {
			name = lipgloss.NewStyle().Foreground(theme.accent).Bold(true).Render("▌ " + strings.TrimSpace(name))
		}
		lines = append(lines, row(name, item.action, 0))
	}
	lines = append(lines, line(""), control("▾  Advanced", "advanced"))
	if m.advancedExpanded {
		lines = append(lines, row("    Source settings", "nav-sources", 0))
	}
	lines = append(lines, line(""), line(lipgloss.NewStyle().Foreground(theme.muted).Render("Ctrl+P  Command palette")))
	return lines
}

func (m *Model) workspaceContent(width, height int, theme uiTheme) ([]uiLine, []uiLine) {
	if width < 12 {
		width = 12
	}
	var content, details []uiLine
	switch m.screen {
	case OverviewScreen:
		content, details = m.homeContent(theme)
	case CatalogScreen:
		content, details = m.catalogContent(width, height, theme)
	case ProfilesScreen:
		content, details = m.profilesContent(theme)
	case EnvironmentScreen:
		content, details = m.environmentContent(width, height, theme)
	case PlanScreen:
		content, details = m.planContent(theme)
	case SourcesScreen:
		content, details = m.sourcesContent(theme)
	case ProgressScreen:
		content, details = m.activityContent(theme)
	case ResultsScreen:
		content, details = m.resultsContent(width, height, theme)
	}
	if len(content) == 0 {
		content = []uiLine{line("No information is available for this section.")}
	}
	if len(details) == 0 {
		details = []uiLine{line("Select a row to see its details here."), line(""), line("Your selections are reviewed before installation.")}
	}
	return content, details
}

func (m *Model) homeContent(theme uiTheme) ([]uiLine, []uiLine) {
	states := map[detection.State]int{}
	for _, item := range m.inventory.Tools {
		states[item.State]++
	}
	selected := len(m.selectedIDs())
	content := []uiLine{
		line(lipgloss.NewStyle().Bold(true).Foreground(theme.text).Render("Welcome to your developer workspace")),
		line("Pick tools or start from a profile. DX checks your machine and shows a plan before anything runs."),
		line(""),
		control("Browse tools  → ", "nav-tools"),
		control("Choose a developer profile  →", "nav-profiles"),
		control("View my environment  →", "nav-environment"),
		line(""),
		line(lipgloss.NewStyle().Bold(true).Render("YOUR WORKSPACE")),
		line(fmt.Sprintf("%d tools selected    %d ready    %d not installed    %d need review", selected, states[detection.Healthy], states[detection.Missing], states[detection.Unknown]+states[detection.Broken]+states[detection.Partial]+states[detection.Conflicting])),
		line(""),
	}
	if m.planReady {
		content = append(content, line(lipgloss.NewStyle().Bold(true).Render("LATEST PLAN")))
		content = append(content, line(fmt.Sprintf("%d install steps  ·  %d preserved  ·  %d issues", len(m.plan.InstallItems()), countPreserved(m.plan), len(m.plan.Issues))))
		if m.plan.Blocked {
			content = append(content, row("Resolve plan issues  →", "nav-plan", 0))
		} else {
			content = append(content, row("Review plan  →", "nav-plan", 0))
		}
	} else {
		content = append(content, line("NEXT STEP"), row("Choose tools to create your first plan  →", "nav-tools", 0))
	}
	details := []uiLine{
		line("THIS MACHINE"),
		line(strings.ToUpper(string(m.inventory.Platform.OS)) + "  ·  " + string(m.inventory.Platform.Architecture)),
		line(valueOr(m.inventory.Platform.Distribution, "Operating system detected")),
		line(""),
		line("PACKAGE MANAGERS"),
	}
	if len(m.inventory.Managers) == 0 {
		details = append(details, line("None detected"))
	} else {
		for _, manager := range m.inventory.Managers {
			details = append(details, line(string(manager.Manager)+"  ·  "+filepath.Base(manager.Path)))
		}
	}
	details = append(details, line(""), line("SAFETY"), line("No install begins until you review and approve a plan."))
	return content, details
}

func countPreserved(plan planning.Plan) int {
	count := 0
	for _, item := range plan.Items {
		if item.Action == planning.Preserve || item.Action == planning.Provided {
			count++
		}
	}
	return count
}

func (m *Model) catalogContent(width, height int, theme uiTheme) ([]uiLine, []uiLine) {
	tools := m.filteredTools()
	category := ansi.Truncate(valueOr(m.categoryFilter, "All categories"), max(3, width-16), "…")
	search := "Search tools"
	if m.searching {
		m.input.SetWidth(max(12, width-14))
		search = "Search: " + m.input.View()
	}
	content := []uiLine{
		line(lipgloss.NewStyle().Bold(true).Render("Tools and runtimes")),
		line("Choose what you need. Select rows to add them to your plan."),
		control("⌕  "+search, "search"),
		{
			text:       "Category  ‹  " + category + "  ›",
			action:     "category-previous",
			index:      0,
			extra:      "category-next",
			extraX:     ansi.StringWidth("Category  ‹  " + category + "  "),
			extraWidth: 1,
		},
		row("Installed filter  ·  "+m.stateFilter+"  (change)", "state-filter", 0),
		control(fmt.Sprintf("Selected: %d   Clear filters", len(m.selectedIDs())), "clear-filters"),
		line(""),
	}
	if m.catalog == nil || len(tools) == 0 {
		content = append(content, line("No tools match these filters."), control("Clear filters", "clear-filters"))
		return content, nil
	}
	rows := make([]table.Row, len(tools))
	for index, item := range tools {
		mark := "□"
		if m.selected[item.ID] {
			mark = "✓"
		}
		state := "Not installed"
		if detected, exists := m.inventory.Tools[item.ID]; exists {
			state = plainStateLabel(detected.State)
		}
		rows[index] = table.Row{mark, item.Name, state}
	}
	cols := []table.Column{{Title: "", Width: 3}, {Title: "TOOL", Width: max(8, width-24)}, {Title: "STATUS", Width: 18}}
	tableLines := m.tableLines(cols, rows, len(tools), m.cursor, theme)
	for rowIndex, tableLine := range tableLines {
		if rowIndex == 0 {
			content = append(content, line(tableLine))
			continue
		}
		toolIndex := rowIndex - 1
		if toolIndex >= len(tools) {
			break
		}
		content = append(content, uiLine{text: tableLine, action: "inspect-tool", index: toolIndex, extra: "toggle-selection", extraX: 0, extraWidth: 3})
	}
	content = append(content, line(""), control("Use a profile", "nav-profiles"), control("Review selected tools", "review-selection"))
	details := m.toolDetails(tools[min(max(m.cursor, 0), len(tools)-1)], theme)
	return content, details
}

func (m *Model) tableLines(columns []table.Column, rows []table.Row, visibleRows, cursor int, theme uiTheme) []string {
	styles := table.DefaultStyles()
	styles.Header = lipgloss.NewStyle().Bold(true).Foreground(theme.muted)
	styles.Cell = lipgloss.NewStyle().Foreground(theme.text)
	styles.Selected = lipgloss.NewStyle().Foreground(theme.text).Background(theme.selected).Bold(true)
	model := table.New(
		table.WithColumns(columns), table.WithRows(rows),
		table.WithHeight(max(1, len(rows)+1)), table.WithWidth(sumColumnWidths(columns)),
		table.WithFocused(true), table.WithStyles(styles),
	)
	model.SetCursor(cursor)
	m.dataTable = model
	return strings.Split(model.View(), "\n")
}

func sumColumnWidths(columns []table.Column) int {
	width := 0
	for _, column := range columns {
		width += column.Width
	}
	return width
}

func plainStateLabel(state detection.State) string {
	switch state {
	case detection.Healthy:
		return "Ready"
	case detection.Missing:
		return "Not installed"
	case detection.Broken:
		return "Needs repair"
	case detection.Partial:
		return "Partly installed"
	case detection.Conflicting:
		return "Conflicting"
	case detection.Unknown:
		return "Needs review"
	case detection.Unsupported:
		return "Not supported"
	case detection.Outdated:
		return "Update available"
	default:
		return string(state)
	}
}

func (m *Model) toolDetails(item tool.Manifest, theme uiTheme) []uiLine {
	lines := []uiLine{
		line(lipgloss.NewStyle().Bold(true).Foreground(theme.text).Render(item.Name)),
		line(item.Description),
		line(""),
		line("STATUS"),
	}
	state := detection.Missing
	if detected, ok := m.inventory.Tools[item.ID]; ok {
		state = detected.State
	}
	lines = append(lines, line(plainStateLabel(state)), line("Category: "+item.Category), line("Version channel: "+item.Version.Channel))
	if dependencies := item.DependenciesFor(m.inventory.Platform.OS); len(dependencies) > 0 {
		lines = append(lines, line("Dependencies: "+strings.Join(dependencies, ", ")))
	}
	lines = append(lines, line("Supported here: "+yesNo(item.Supports(m.inventory.Platform.OS, m.inventory.Platform.Architecture))))
	if installer, ok := item.InstallerForPlatform(m.inventory.Platform.OS, m.inventory.Platform.Architecture, m.managerOrder()); ok {
		lines = append(lines, line("Install method: "+string(installer.Manager)), line("Package: "+installer.PackageID), line("Source: "+installer.Source), line("Scope: "+string(installer.Scope)))
	} else if artifact, ok := item.ArtifactFor(m.inventory.Platform.OS, m.inventory.Platform.Architecture); ok {
		lines = append(lines, line("Install method: verified archive"), line("Version: "+artifact.Version), line("Source: "+artifact.URL), line("SHA-256: "+artifact.SHA256))
		if artifact.SizeEstimateNote != "" {
			lines = append(lines, line("Download: "+artifact.SizeEstimateNote))
		}
	} else {
		lines = append(lines, line("No installer is available for this machine."))
	}
	return lines
}

func (m *Model) profilesContent(theme uiTheme) ([]uiLine, []uiLine) {
	content := []uiLine{
		line(lipgloss.NewStyle().Bold(true).Render("Start with a profile")),
		line("Profiles are editable starting points; choose one or more and adjust tools afterward."),
		control("Apply selected profiles", "apply-profiles"),
		control("Browse individual tools", "nav-tools"),
		control("Import selection", "import-selection"),
		control("Export selection", "export-selection"),
		line(""),
	}
	if m.catalog == nil {
		return append(content, line("The tool catalog is not available.")), nil
	}
	profiles := m.catalog.Profiles()
	for index, profile := range profiles {
		check := "□"
		if m.selectedProfiles[profile.ID] {
			check = "✓"
		}
		card := fmt.Sprintf("%s  %-28s  %d tools", check, profile.Name, len(profile.Tools))
		content = append(content, row(card, "profile-toggle", index))
	}
	if len(profiles) == 0 {
		content = append(content, line("No profiles are available."))
		return content, nil
	}
	profile := profiles[min(max(m.cursor, 0), len(profiles)-1)]
	details := []uiLine{line(lipgloss.NewStyle().Bold(true).Foreground(theme.text).Render(profile.Name)), line(profile.Description), line(""), line("INCLUDED TOOLS")}
	for _, id := range profile.Tools {
		name := id
		if item, ok := m.catalog.Tool(id); ok {
			name = item.Name
		}
		details = append(details, line("• "+name))
	}
	details = append(details, line(""), line("Selecting this profile adds these tools to your editable selection."))
	return content, details
}

func (m *Model) environmentContent(width, height int, theme uiTheme) ([]uiLine, []uiLine) {
	ids := m.filteredEnvironmentIDs()
	search := "Search installed tools"
	if m.environmentSearching {
		m.input.SetWidth(max(12, width-14))
		search = "Search: " + m.input.View()
	}
	content := []uiLine{
		line(lipgloss.NewStyle().Bold(true).Render("Your environment")),
		line("See what is already installed on this machine."),
		control("⌕  "+search, "search"),
		control("Clear search", "clear-search"),
		control("Refresh inventory", "refresh"),
		line(fmt.Sprintf("%d tool records  ·  %d inventory warnings", len(ids), len(m.inventory.Warnings))),
		line(""),
	}
	if len(ids) == 0 {
		return append(content, line("No installed-tool records match this search.")), nil
	}
	rows := make([]table.Row, len(ids))
	for index, id := range ids {
		item := m.inventory.Tools[id]
		rows[index] = table.Row{item.Name, plainStateLabel(item.State), id}
	}
	cols := []table.Column{{Title: "TOOL", Width: max(10, width-34)}, {Title: "STATUS", Width: 16}, {Title: "ID", Width: 18}}
	for rowIndex, text := range m.tableLines(cols, rows, len(ids), m.cursor, theme) {
		if rowIndex == 0 {
			content = append(content, line(text))
			continue
		}
		index := rowIndex - 1
		if index < len(ids) {
			content = append(content, row(text, "inspect-row", index))
		}
	}
	if len(m.inventory.Warnings) > 0 {
		content = append(content, line(""), row(fmt.Sprintf("Inventory warnings (%d)", len(m.inventory.Warnings)), "inspect-warning", 0))
	}
	item := m.inventory.Tools[ids[min(max(m.cursor, 0), len(ids)-1)]]
	details := []uiLine{line(lipgloss.NewStyle().Bold(true).Foreground(theme.text).Render(item.Name)), line("Status: " + plainStateLabel(item.State)), line("ID: " + item.ID)}
	for _, instance := range item.Instances {
		details = append(details, line(""), line("EXECUTABLE"), line(instance.Executable), line("Version: "+valueOr(instance.Version, "unknown")), line("Path: "+instance.Path), line("Architecture: "+display.ArchitectureVerification(instance)))
		if instance.Diagnostic != "" {
			details = append(details, line("Diagnostic: "+instance.Diagnostic))
		}
	}
	for _, installed := range item.Packages {
		details = append(details, line("Package: "+display.PackageOwnership(string(installed.Manager), installed.ID, installed.Version, installed.Source)))
	}
	if len(item.Packages) > 0 && len(item.Instances) == 0 {
		details = append(details, line("Package record found; the executable was not found on PATH."))
	}
	return content, details
}

func (m *Model) planContent(theme uiTheme) ([]uiLine, []uiLine) {
	content := []uiLine{
		line(lipgloss.NewStyle().Bold(true).Render("Installation plan")),
		line("Review every operation, source, version, and setup change before approving."),
	}
	if !m.planReady {
		content = append(content, control("Build plan", "build-plan"), line("DX checks your selection and machine. This does not install anything."))
		return content, nil
	}
	installCount := len(m.plan.InstallItems())
	preserved := countPreserved(m.plan)
	content = append(content, line(fmt.Sprintf("%d installs  ·  %d already satisfied  ·  %d issues", installCount, preserved, len(m.plan.Issues))))
	content = append(content, control("Refresh plan", "build-plan"))
	if m.plan.Blocked {
		content = append(content, line("⚠ Installation is blocked until the issue is resolved."))
	} else if installCount == 0 {
		content = append(content, line("Everything selected is already present."))
	} else {
		content = append(content, control("Review and install", "review-install"))
	}
	content = append(content, line(""), line("PLAN ITEMS"))
	for index, item := range m.plan.Items {
		label := "INSTALL"
		if item.Action == planning.Preserve {
			label = "READY"
		} else if item.Action == planning.Provided {
			label = "INCLUDED"
		}
		name := item.Name
		if name == "" {
			name = item.ToolID
		}
		content = append(content, row(fmt.Sprintf("%-9s %-26s %s", label, name, item.Version), "inspect-plan", index))
	}
	for index, issue := range m.plan.Issues {
		label := "Needs attention"
		if issue.Severity == planning.Blocking {
			label = "Blocks installation"
		}
		content = append(content, row("⚠  "+label+": "+issue.ToolID, "inspect-plan", len(m.plan.Items)+index))
	}
	details := []uiLine{line("PLAN DETAILS"), line(fmt.Sprintf("%d install operations", installCount)), line(fmt.Sprintf("%d preserved or provided", preserved)), line(""), line("Existing tools are preserved and are not upgraded.")}
	if m.cursor >= len(m.plan.Items) && m.cursor-len(m.plan.Items) < len(m.plan.Issues) {
		issue := m.plan.Issues[m.cursor-len(m.plan.Items)]
		severity := "Needs attention"
		if issue.Severity == planning.Blocking {
			severity = "Blocks installation"
		}
		details = append(details, line(""), line(lipgloss.NewStyle().Bold(true).Foreground(theme.warning).Render(severity)), line(issue.ToolID), line(issue.Message))
	} else if len(m.plan.Items) > 0 {
		itemIndex := min(max(m.cursor, 0), len(m.plan.Items)-1)
		item := m.plan.Items[itemIndex]
		details = append(details, line(""), line(lipgloss.NewStyle().Bold(true).Foreground(theme.text).Render(valueOr(item.Name, item.ToolID))))
		if item.Action == planning.Preserve {
			details = append(details, line("Already present: "+stateLabel(item.ExistingState)))
		} else if item.Action == planning.Provided {
			details = append(details, line("Included with "+item.ProvidedBy))
		} else {
			details = appendPlanItemDetails(details, item)
		}
	}
	if m.plan.TransactionDisclosure != "" {
		details = append(details, line(""), line("PACKAGE MANAGER DISCLOSURE"), line(m.plan.TransactionDisclosure))
	}
	if len(m.plan.Issues) > 0 {
		details = append(details, line(""), line("ISSUES"))
		for _, issue := range m.plan.Issues {
			details = append(details, line(issue.ToolID+": "+issue.Message))
		}
	}
	return content, details
}

func appendPlanItemDetails(lines []uiLine, item planning.Item) []uiLine {
	lines = append(lines, line("Action: install"), line("Version: "+item.Version), line("Source: "+item.Source), line("Package: "+item.PackageID), line("Install path: "+item.InstallPath))
	if item.Artifact != nil {
		lines = append(lines, line("SHA-256: "+item.Artifact.SHA256), line("Download: "+display.ApproximateSize(item.ApproximateSize)))
	}
	if item.Command.Display() != "" {
		lines = append(lines, line("Command: "+item.Command.Display()))
	}
	if item.InstallationNote != "" {
		lines = append(lines, line("Setup: "+item.InstallationNote))
	}
	for _, change := range item.PathChanges {
		if change.Manual {
			lines = append(lines, line("Manual PATH setup: "+change.ManualCommand))
		} else if change.Persist {
			lines = append(lines, line("PATH change: "+change.Directory+" → "+change.Target))
		}
	}
	return lines
}

func (m *Model) sourcesContent(theme uiTheme) ([]uiLine, []uiLine) {
	content := []uiLine{
		line(lipgloss.NewStyle().Bold(true).Render("Source settings")),
		line("Choose download sources. Publisher archives remain checksum-verified."),
		line("Current choice: " + sourceModeLabel(m.sourceMode)),
		row(sourceChoiceText("Automatic", m.sourceMode == "automatic"), "source-automatic", 0),
		row(sourceChoiceText("Mainland China", m.sourceMode == "mainland-china"), "source-china", 0),
		row(sourceChoiceText("Global", m.sourceMode == "global"), "source-global", 0),
		row(sourceChoiceText("Publisher only", m.sourceMode == "official-only"), "source-official-only", 0),
		row(sourceChoiceText("Publisher first", m.sourceMode == "official-first"), "source-official-first", 0),
		line(""),
		line("CUSTOM SOURCES"),
		control("Add custom archive URL", "custom-source"),
		control("Add registry override", "registry-source"),
		control("Add local archive", "offline-archive"),
		control("Load signed offline bundle", "offline-bundle"),
		control("Clear source overrides", "clear-sources"),
		line(""),
		line("PACKAGE MANAGER PREFERENCE"),
	}
	for index, manager := range m.inventory.Managers {
		prefix := "○ "
		if manager.Manager == m.preferredManager {
			prefix = "● "
		}
		content = append(content, row(prefix+string(manager.Manager)+"  ·  "+filepath.Base(manager.Path), "prefer-manager", index))
	}
	if len(m.inventory.Managers) == 0 {
		content = append(content, line("No supported package manager was detected."))
	}
	details := []uiLine{line("REGION"), line(regionLabel(m.regionDetection.Region)), line("Policy: " + sourceModeLabel(m.sourceMode)), line(""), line("VERIFICATION"), line("Downloaded archives are checked against their catalog SHA-256 before extraction."), line(""), line("PREFERRED PACKAGE MANAGER"), line("The selected manager is preferred when it supports the tool."), control("Restore automatic choice", "reset-manager")}
	if m.offlineBundlePath != "" {
		details = append(details, line(""), line("Signed bundle: "+m.offlineBundlePath))
	}
	for ecosystem, source := range m.registrySources {
		details = append(details, line("Registry "+string(ecosystem)+": "+source))
	}
	for id, source := range m.customSources {
		details = append(details, line("Custom source "+id+": "+source))
	}
	return content, details
}

func sourceChoiceText(label string, selected bool) string {
	if selected {
		return "●  " + label + "  ·  selected"
	}
	return "○  " + label
}

func (m *Model) activityContent(theme uiTheme) ([]uiLine, []uiLine) {
	content := []uiLine{line(lipgloss.NewStyle().Bold(true).Render("Installation activity"))}
	if m.session == nil {
		content = append(content, line("No installation is running."), control("Review the plan", "nav-plan"))
		return content, []uiLine{line("The installation engine reports progress here after plan approval."), line("No commands run before explicit approval.")}
	}
	results := m.session.Report()
	content = append(content, line(fmt.Sprintf("%d of %d operations completed", len(results), len(m.plan.Items))))
	if m.preparingDownloads {
		content = append(content, line(fmt.Sprintf("Preparing %d archive download(s); up to %d run at once.", m.preparingDownloadCount, artifacts.DefaultConcurrentDownloads)))
	}
	if m.activeItem.ToolID != "" {
		content = append(content, line("Current operation: "+m.activeItem.Name))
	}
	for index, result := range results {
		content = append(content, row(result.Name+"  ·  "+resultStateLabel(result.State), "inspect-result", index))
	}
	if m.session.Complete() {
		content = append(content, line(""), control("View results", "nav-results"))
	}
	details := []uiLine{line("SELECTED ACTIVITY")}
	if len(results) > 0 {
		result := results[min(max(m.cursor, 0), len(results)-1)]
		details = append(details, line(result.Name), line(resultStateLabel(result.State)))
		if result.Verification != "" {
			details = append(details, line("Verification: "+result.Verification))
		}
		if result.Source != "" {
			details = append(details, line("Source: "+result.Source))
		}
		if result.Diagnostic != "" {
			details = append(details, line("Diagnostic: "+result.Diagnostic))
		}
	} else if m.activeItem.ToolID != "" {
		details = append(details, line(m.activeItem.Name), line("Source: "+m.activeItem.Source))
		if m.activeItem.Artifact != nil {
			details = append(details, line("SHA-256 is checked before extraction."))
			if progress, ok := m.downloadProgress[m.activeItem.ToolID]; ok {
				details = append(details, line(display.DownloadProgress(progress.BytesReceived, progress.TotalBytes)), line("Verification: "+progress.Phase))
			}
		} else if m.activeItem.Command.Display() != "" {
			details = append(details, line("Command: "+m.activeItem.Command.Display()))
		}
	} else {
		details = append(details, line(valueOr(m.status, "Waiting for approval or the next operation.")))
	}
	return content, details
}

func (m *Model) resultsContent(width, height int, theme uiTheme) ([]uiLine, []uiLine) {
	content := []uiLine{line(lipgloss.NewStyle().Bold(true).Render("Installation results")), control("Save redacted report", "save-report"), control("Inspect environment", "nav-environment")}
	if m.session == nil {
		content = append(content, line("No installation session has run."))
		return content, nil
	}
	results := m.session.Report()
	if len(results) == 0 {
		content = append(content, line("No operations were completed."))
		return content, nil
	}
	for index := 0; index < len(results); index++ {
		result := results[index]
		content = append(content, row(result.Name+"  ·  "+resultStateLabel(result.State), "inspect-result", index))
	}
	result := results[min(max(m.cursor, 0), len(results)-1)]
	details := []uiLine{line(result.Name), line(resultStateLabel(result.State)), line("")}
	if result.Verification != "" {
		details = append(details, line("Verification: "+result.Verification))
	}
	if result.Source != "" {
		details = append(details, line("Source: "+result.Source))
	}
	if result.SourceURL != "" {
		details = append(details, line("Source URL: "+result.SourceURL))
	}
	if result.InstallPath != "" {
		details = append(details, line("Installed at: "+result.InstallPath))
	}
	if result.SHA256 != "" {
		details = append(details, line("SHA-256: "+result.SHA256))
	}
	for _, change := range result.PathChanges {
		if change.Manual {
			details = append(details, line("Manual PATH setup: "+change.ManualCommand))
		} else if change.Persist {
			details = append(details, line("PATH: "+change.Directory+" → "+change.Target))
		}
	}
	if result.Diagnostic != "" {
		details = append(details, line("Diagnostic: "+result.Diagnostic))
	}
	details = append(details, line("Temporary bootstrap artifacts are removed when the application exits."))
	return content, details
}

func (m *Model) screenTitle() string {
	switch m.screen {
	case OverviewScreen:
		return "HOME"
	case CatalogScreen:
		return "TOOLS"
	case ProfilesScreen:
		return "PROFILES"
	case EnvironmentScreen:
		return "ENVIRONMENT"
	case PlanScreen:
		return "PLAN REVIEW"
	case SourcesScreen:
		return "ADVANCED / SOURCES"
	case ProgressScreen:
		return "ACTIVITY"
	case ResultsScreen:
		return "RESULTS"
	default:
		return "WORKSPACE"
	}
}

func (m *Model) renderWorkspaceModal(width, height int, theme uiTheme) []string {
	if m.help {
		return m.renderModal(width, height, "Keyboard and mouse", m.helpLines(theme), []uiLine{control("Close help", "close-help")}, theme, &m.contentViewport, &m.pageScroll)
	}
	if m.approval {
		return m.renderModal(width, height, "Confirm installation", m.approvalLines(theme), []uiLine{control("Install now  ·  y", "approve"), control("Cancel  ·  Esc", "cancel-approval")}, theme, &m.contentViewport, &m.pageScroll)
	}
	if m.pathMode != "" {
		if m.input.Value() != m.pathInput {
			m.input.SetValue(m.pathInput)
		}
		m.input.SetWidth(max(12, width-18))
		placeholder := "Enter a value"
		switch m.pathMode {
		case "export":
			placeholder = "New JSON file path"
		case "import":
			placeholder = "Selection JSON file path"
		case "diagnostic":
			placeholder = "New redacted report path"
		}
		m.input.Placeholder = placeholder
		m.input.Focus()
		lines := []uiLine{line(m.pathPromptTitle()), line(m.status), uiLine{text: m.input.View(), action: "input-focus", index: 0}, line("Enter confirms  ·  Esc cancels")}
		return m.renderModal(width, height, "Enter details", lines, []uiLine{control("Confirm", "confirm-path"), control("Cancel", "cancel-path")}, theme, nil, nil)
	}
	return m.renderCommandPalette(width, height, theme)
}

func (m *Model) renderModal(width, height int, title string, body, actions []uiLine, theme uiTheme, vp *viewport.Model, scroll *int) []string {
	modalWidth := min(width-2, max(48, width*3/4))
	if width < 50 {
		modalWidth = width
	}
	modalHeight := min(height-2, max(10, height*3/4))
	if modalHeight < 5 {
		modalHeight = height
	}
	x := max(0, (width-modalWidth)/2)
	y := max(1, (height-modalHeight)/2)
	modalRect := uiRect{x: x, y: y, width: modalWidth, height: modalHeight}
	if vp == &m.detailViewport {
		m.detailViewportRect = uiRect{x: x + 1, y: y + 1, width: max(1, modalWidth-2), height: max(1, modalHeight-2)}
	} else {
		m.mainViewportRect = uiRect{x: x + 1, y: y + 1, width: max(1, modalWidth-2), height: max(1, modalHeight-2)}
	}
	lines := make([]string, height)
	for index := range lines {
		lines[index] = strings.Repeat(" ", width)
	}
	modalLines := append([]uiLine{}, body...)
	modalLines = append(modalLines, line(""))
	modalLines = append(modalLines, actions...)
	if vp == nil {
		vp = &m.contentViewport
	}
	if scroll == nil {
		local := 0
		scroll = &local
	}
	panel := m.renderPane(modalRect, title, modalLines, vp, *scroll, theme, false)
	for index, rendered := range panel {
		if y+index < len(lines) {
			prefix := strings.Repeat(" ", x)
			lines[y+index] = prefix + rendered
		}
	}
	if m.commandPalette {
		lines[0] = fitLine("  COMMAND PALETTE  ·  type to filter  ·  Esc closes", width)
	}
	if m.pathMode != "" {
		lines[0] = fitLine("  DX Bootstrap  /  Input required", width)
	}
	if m.help {
		lines[0] = fitLine("  DX Bootstrap  /  Help", width)
	}
	if m.approval {
		lines[0] = fitLine("  DX Bootstrap  /  Approval required", width)
	}
	return lines
}

func (m *Model) pathPromptTitle() string {
	switch m.pathMode {
	case "export":
		return "Choose where to save the tool selection JSON."
	case "import":
		return "Choose a tool selection JSON file to open."
	case "diagnostic":
		return "Choose a new path for the redacted diagnostic report."
	case "custom-source":
		return "Add a custom archive URL for a catalog tool."
	case "registry":
		return "Set an ecosystem registry URL."
	case "offline-archive":
		return "Choose a local archive for a selected tool."
	case "offline-bundle":
		return "Choose a signed offline bundle directory."
	default:
		return "Enter a value for this setting."
	}
}

func (m *Model) approvalLines(theme uiTheme) []uiLine {
	packageCount, archiveCount := 0, 0
	lines := []uiLine{
		line("DX checked your machine again. Review these operations before continuing."),
		line(fmt.Sprintf("%d package-manager operations  ·  %d verified archive downloads", packageCount, archiveCount)),
	}
	for _, item := range m.plan.InstallItems() {
		if item.Artifact != nil {
			archiveCount++
		} else {
			packageCount++
		}
		lines = append(lines, line(""), line(valueOr(item.Name, item.ToolID)+"  ·  "+item.Version), line("Source: "+item.Source), line("Install path: "+item.InstallPath))
		if item.Artifact != nil {
			lines = append(lines, line("SHA-256: "+item.Artifact.SHA256), line("Approximate download: "+display.ApproximateSize(item.ApproximateSize)))
		}
		if item.Command.Display() != "" {
			lines = append(lines, line("Command: "+item.Command.Display()))
		}
	}
	// Replace the leading summary after counting all operations.
	lines[1] = line(fmt.Sprintf("%d package-manager operations  ·  %d verified archive downloads", packageCount, archiveCount))
	if m.plan.TransactionDisclosure != "" {
		lines = append(lines, line(""), line("PACKAGE MANAGER DISCLOSURE"), line(m.plan.TransactionDisclosure))
	}
	return lines
}

func (m *Model) helpLines(theme uiTheme) []uiLine {
	bindings := [][]key.Binding{
		{
			key.NewBinding(key.WithKeys("tab", "shift+tab"), key.WithHelp("Tab / Shift+Tab", "move focus")),
			key.NewBinding(key.WithKeys("↑", "↓"), key.WithHelp("↑ / ↓", "move through rows")),
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", "open or activate")),
			key.NewBinding(key.WithKeys("space"), key.WithHelp("Space", "select a tool")),
		},
		{
			key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("Ctrl+P", "open commands")),
			key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "show help")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", "close dialog")),
			key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		},
	}
	m.helpComponent.SetWidth(max(20, m.width-24))
	return []uiLine{
		line("Click any visible control. Use Tab to move focus and Enter to activate it."),
		line("Arrow keys move through rows; Space selects. The mouse wheel scrolls the pane under the pointer."),
		line(""),
		line(m.helpComponent.FullHelpView(bindings)),
		line(""),
		line("Installation always requires reviewing and approving a current plan."),
	}
}

func (m *Model) openCommandPalette() {
	m.commandPalette = true
	m.commandQuery = ""
	m.input.SetValue("")
	m.input.Placeholder = "Type a section or action..."
	m.input.Focus()
	m.updateCommandList()
}

func (m *Model) updateCommandList() {
	commands := []paletteItem{
		{title: "Home", description: "Workspace overview", action: "nav-home"},
		{title: "Tools", description: "Browse and select developer tools", action: "nav-tools"},
		{title: "Profiles", description: "Start with an editable tool set", action: "nav-profiles"},
		{title: "Environment", description: "Inspect tools already on this machine", action: "nav-environment"},
		{title: "Review plan", description: "Build or inspect the installation plan", action: "nav-plan"},
		{title: "Activity", description: "See installation progress", action: "nav-activity"},
		{title: "Results", description: "Inspect completed operations", action: "nav-results"},
		{title: "Source settings", description: "Advanced download and package manager settings", action: "nav-sources"},
		{title: "Search tools", description: "Filter the catalog", action: "search-tools"},
		{title: "Refresh inventory", description: "Check this machine again", action: "refresh"},
		{title: "Build plan", description: "Check selected tools and produce a review", action: "build-plan"},
		{title: "Help", description: "Show mouse and keyboard controls", action: "help"},
		{title: "Quit", description: "Exit the installer", action: "quit"},
	}
	query := strings.ToLower(strings.TrimSpace(m.commandQuery))
	items := make([]list.Item, 0, len(commands))
	for _, command := range commands {
		if query != "" && !strings.Contains(strings.ToLower(command.FilterValue()), query) {
			continue
		}
		items = append(items, command)
	}
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	m.commandList = list.New(items, delegate, max(24, min(m.width-12, 72)), max(3, min(m.height-8, 12)))
	m.commandList.SetShowTitle(false)
	m.commandList.SetShowFilter(false)
	m.commandList.SetShowHelp(false)
	m.commandList.SetShowPagination(false)
	m.commandList.SetShowStatusBar(false)
	m.commandList.SetFilteringEnabled(false)
}

func (m *Model) renderCommandPalette(width, height int, theme uiTheme) []string {
	if m.input.Value() != m.commandQuery {
		m.input.SetValue(m.commandQuery)
	}
	m.input.SetWidth(max(16, min(width-18, 68)))
	m.input.Placeholder = "Type a section or action..."
	items := m.commandList.Items()
	content := []uiLine{line("Jump to a section or run an action."), uiLine{text: m.input.View(), action: "input-focus", index: 0}}
	listLines := strings.Split(m.commandList.View(), "\n")
	for index, text := range listLines {
		if index >= len(items) {
			break
		}
		content = append(content, row(text, "palette-command", index))
	}
	if len(items) == 0 {
		content = append(content, line("No matching commands."))
	}
	return m.renderModal(width, height, "Command palette", content, []uiLine{control("Close  ·  Esc", "close-commands")}, theme, nil, nil)
}

func (m *Model) handleCommandPaletteKey(message tea.KeyPressMsg, keyName string) tea.Cmd {
	switch keyName {
	case "esc":
		m.commandPalette = false
		m.input.Blur()
		return nil
	case "enter":
		selected, ok := m.commandList.SelectedItem().(paletteItem)
		if !ok {
			return nil
		}
		m.commandPalette = false
		m.input.Blur()
		return m.activateTarget(hitTarget{action: selected.action, index: selected.index})
	case "up", "down", "pgup", "pgdown":
		listModel, command := m.commandList.Update(message)
		m.commandList = listModel
		return command
	default:
		input, command := m.input.Update(message)
		m.input = input
		m.commandQuery = input.Value()
		m.updateCommandList()
		return command
	}
}

func (m *Model) cycleFocus(direction int) {
	if len(m.hitTargets) == 0 {
		m.renderWorkspace()
	}
	if len(m.hitTargets) == 0 {
		return
	}
	current := -1
	for index, target := range m.hitTargets {
		if target.id == m.focusedTarget {
			current = index
			break
		}
	}
	if current < 0 {
		if direction > 0 {
			current = 0
		} else {
			current = len(m.hitTargets) - 1
		}
	} else {
		current = (current + direction + len(m.hitTargets)) % len(m.hitTargets)
	}
	target := m.hitTargets[current]
	m.focusedTarget = target.id
	if target.action == "inspect-tool" || target.action == "toggle-selection" || target.action == "profile-toggle" || target.action == "inspect-row" || target.action == "inspect-plan" || target.action == "inspect-result" {
		m.cursor = target.index
	}
}

func (m *Model) focusedControlTarget() (hitTarget, bool) {
	for _, target := range m.hitTargets {
		if target.id == m.focusedTarget {
			return target, true
		}
	}
	return hitTarget{}, false
}

func (m *Model) renderPaneForTests(width, height int) string {
	content, _ := m.workspaceContent(width-4, height-2, m.workspaceTheme())
	return strings.Join(m.renderPane(uiRect{width: width, height: height}, m.screenTitle(), content, &m.contentViewport, 0, m.workspaceTheme(), true), "\n")
}

var _ list.Item = paletteItem{}
