package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	registryadapter "developers-club-dx/internal/adapters/registry"
	"developers-club-dx/internal/artifacts"
	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/installation"
	"developers-club-dx/internal/offline"
	"developers-club-dx/internal/pathconfig"
	"developers-club-dx/internal/planning"
	"developers-club-dx/internal/regional"
	"developers-club-dx/internal/reporting"
	"developers-club-dx/internal/security"
	"developers-club-dx/internal/sources"
)

type Screen int

const (
	OverviewScreen Screen = iota
	CatalogScreen
	ProfilesScreen
	EnvironmentScreen
	PlanScreen
	SourcesScreen
	ProgressScreen
	ResultsScreen
)

type Model struct {
	ctx                    context.Context
	catalog                *catalog.Catalog
	inventory              detection.Inventory
	screen                 Screen
	selected               map[string]bool
	selectedAlternatives   map[string]string
	selectedProfiles       map[string]bool
	preferredManager       tool.PackageManager
	sourceMode             string
	regionDetection        regional.Result
	archiveInstaller       *artifacts.ArchiveInstaller
	customSources          map[string]string
	registrySources        sources.RegistryURLs
	offlineArchives        map[string]string
	offlineBundlePath      string
	plan                   planning.Plan
	planReady              bool
	planning               bool
	planningCancel         context.CancelFunc
	approval               bool
	preflighting           bool
	searching              bool
	environmentSearching   bool
	pathMode               string
	pathInput              string
	help                   bool
	filter                 string
	environmentFilter      string
	categoryFilter         string
	stateFilter            string
	status                 string
	width                  int
	height                 int
	cursor                 int
	session                *installation.Session
	activeItem             planning.Item
	preparedArchives       map[string]artifacts.PrepareResult
	downloadProgress       map[string]sources.DownloadProgress
	downloadProgressCh     chan artifacts.PrepareProgress
	preparingDownloads     bool
	preparingDownloadCount int
	downloadCancel         context.CancelFunc
	installCancel          context.CancelFunc
	advancedExpanded       bool
	hitTargets             []hitTarget
	pageScroll             int
	inspectorScroll        int
	focusedTarget          string
	hoveredTarget          string
	commandPalette         bool
	commandQuery           string
	darkTheme              bool
	backgroundKnown        bool
	input                  textinput.Model
	contentViewport        viewport.Model
	detailViewport         viewport.Model
	commandList            list.Model
	dataTable              table.Model
	helpComponent          help.Model
	mainViewportRect       uiRect
	detailViewportRect     uiRect
}

type hitTarget struct {
	x, y, width, height int
	action              string
	index               int
	id                  string
}

type preflightMsg struct {
	inventory detection.Inventory
	err       error
	approval  bool
}

type planPreviewMsg struct {
	plan              planning.Plan
	previousSignature string
	approval          bool
}

type processFinishedMsg struct {
	toolID string
	err    error
}

type downloadProgressMsg struct {
	toolID   string
	progress sources.DownloadProgress
}

type downloadProgressDoneMsg struct{}

type archivesPreparedMsg struct {
	results   []artifacts.PrepareResult
	cancelled bool
}

type verificationMsg struct {
	item        planning.Item
	processErr  error
	inventory   detection.Inventory
	actual      detection.Tool
	verifyError error
}

type artifactInstallMsg struct {
	item        planning.Item
	result      artifacts.InstallResult
	installErr  error
	pathErr     error
	inventory   detection.Inventory
	actual      detection.Tool
	verifyError error
}

func searchDirectories(changes []pathconfig.Change) []string {
	directories := make([]string, 0, len(changes))
	for _, change := range changes {
		directories = append(directories, change.Directory)
	}
	return directories
}

func archivePrepareRequests(plan planning.Plan, home string) []artifacts.PrepareRequest {
	requests := make([]artifacts.PrepareRequest, 0)
	for _, item := range plan.Items {
		if item.Action != planning.Install || item.Artifact == nil {
			continue
		}
		policy := sources.OfficialOnly
		if item.RegionalFallback {
			policy = sources.OfficialFirst
		}
		requests = append(requests, artifacts.PrepareRequest{
			ToolID:   item.ToolID,
			Home:     home,
			Artifact: *item.Artifact,
			Options: artifacts.InstallOptions{
				Policy:                policy,
				AllowRegionalFallback: item.RegionalFallback,
				CustomSourceURL:       item.CustomSourceURL,
				OfflineArchivePath:    item.OfflineArchivePath,
			},
		})
	}
	return requests
}

func NewModel(ctx context.Context, items *catalog.Catalog, initial detection.Inventory, selected []string) *Model {
	selection := make(map[string]bool, len(selected))
	for _, id := range selected {
		selection[id] = true
	}
	model := &Model{
		ctx:                  ctx,
		catalog:              items,
		inventory:            initial,
		screen:               OverviewScreen,
		selected:             selection,
		selectedAlternatives: make(map[string]string),
		selectedProfiles:     make(map[string]bool),
		sourceMode:           "automatic",
		regionDetection:      regional.Detect(initial.Platform),
		archiveInstaller:     artifacts.NewArchiveInstaller(),
		customSources:        make(map[string]string),
		offlineArchives:      make(map[string]string),
		stateFilter:          "all",
		status:               "Choose tools for this machine. Nothing installs until you approve a plan.",
		width:                80,
		height:               24,
		darkTheme:            true,
	}
	model.initializeComponents()
	return model
}

func (m *Model) SetPreferredManager(manager tool.PackageManager) {
	m.preferredManager = manager
}

func (m *Model) SetSourceMode(mode string) error {
	switch mode {
	case "automatic", "mainland-china", "global", "official-only", "official-first":
		m.sourceMode = mode
		return nil
	default:
		return fmt.Errorf("unsupported source policy %q", mode)
	}
}

func (m *Model) SetArchiveInstaller(installer *artifacts.ArchiveInstaller) {
	m.archiveInstaller = installer
}

func (m *Model) SetSourceOverrides(customSources, offlineArchives map[string]string) error {
	for toolID := range customSources {
		if offlineArchives[toolID] != "" {
			return fmt.Errorf("tool %q has both a custom source and an offline archive", toolID)
		}
	}
	m.customSources = cloneStringMap(customSources)
	m.offlineArchives = cloneStringMap(offlineArchives)
	m.invalidatePlan("Custom and offline sources updated. Rebuild the plan to review the selected archives.")
	return nil
}

func (m *Model) SetRegistryOverrides(registries sources.RegistryURLs) error {
	validated, err := sources.ParseRegistryAssignments(registryAssignments(registries))
	if err != nil {
		return err
	}
	m.registrySources = validated
	m.invalidatePlan("Registry sources updated. Rebuild the plan to review the selected registry installations.")
	return nil
}

func registryAssignments(registries sources.RegistryURLs) []string {
	assignments := make([]string, 0, len(registries))
	for ecosystem, source := range registries {
		assignments = append(assignments, string(ecosystem)+"="+source)
	}
	return assignments
}

func (m *Model) LoadOfflineBundle(bundlePath string) error {
	absolute, err := filepath.Abs(strings.TrimSpace(bundlePath))
	if err != nil {
		return fmt.Errorf("resolve offline bundle path: %w", err)
	}
	publicKey, err := security.DecodePublicKey(security.PinnedEd25519PublicKey)
	if err != nil {
		return fmt.Errorf("pinned offline bundle public key is unavailable: %w", err)
	}
	bundle, err := offline.LoadBundle(absolute, m.catalog, m.inventory.Platform, publicKey)
	if err != nil {
		return err
	}
	for toolID, archivePath := range bundle.Artifacts {
		if m.customSources[toolID] != "" {
			return fmt.Errorf("tool %q already has a custom source; clear it before loading this offline bundle", toolID)
		}
		if existing := m.offlineArchives[toolID]; existing != "" && existing != archivePath {
			return fmt.Errorf("tool %q already has a different offline archive configured", toolID)
		}
		m.offlineArchives[toolID] = archivePath
	}
	m.offlineBundlePath = absolute
	m.invalidatePlan("Signed offline bundle verified. Rebuild the plan to review its catalog-pinned archives.")
	return nil
}

func cloneStringMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneRegistryURLs(source sources.RegistryURLs) sources.RegistryURLs {
	clone := make(sources.RegistryURLs, len(source))
	for ecosystem, url := range source {
		clone[ecosystem] = url
	}
	return clone
}

func (m *Model) managerOrder() []tool.PackageManager {
	return planning.PreferredManagerOrder(m.inventory.ManagerOrder(), m.preferredManager)
}

func (m *Model) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.hitTargets = nil
		return m, nil
	case tea.BackgroundColorMsg:
		m.darkTheme = msg.IsDark()
		m.backgroundKnown = true
		return m, nil
	case preflightMsg:
		return m.handlePreflight(msg)
	case planPreviewMsg:
		return m.handlePlanPreview(msg)
	case processFinishedMsg:
		if m.activeItem.ToolID != msg.toolID {
			return m, nil
		}
		m.status = "Checking the installed tool and its version."
		return m, m.verifyCommand(msg.err)
	case downloadProgressMsg:
		if !m.preparingDownloads || m.downloadProgressCh == nil {
			return m, nil
		}
		if m.downloadProgress == nil {
			m.downloadProgress = make(map[string]sources.DownloadProgress)
		}
		m.downloadProgress[msg.toolID] = msg.progress
		return m, waitForDownloadProgress(m.downloadProgressCh)
	case downloadProgressDoneMsg:
		if m.preparingDownloads {
			m.downloadProgressCh = nil
		}
		return m, nil
	case archivesPreparedMsg:
		return m.handleArchivesPrepared(msg)
	case verificationMsg:
		return m.handleVerification(msg)
	case artifactInstallMsg:
		return m.handleArtifactInstall(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseClickMsg:
		return m, m.handleMouseClick(tea.Mouse(msg))
	case tea.MouseWheelMsg:
		m.handleMouseWheel(tea.Mouse(msg))
		return m, nil
	case tea.MouseMotionMsg:
		m.handleMouseMotion(tea.Mouse(msg))
		return m, nil
	default:
		return m, nil
	}
}

func (m *Model) handleKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := message.String()
	if m.planning {
		if key != "q" && key != "ctrl+c" {
			return m, nil
		}
		if m.planningCancel != nil {
			m.planningCancel()
			m.planningCancel = nil
		}
		m.planning = false
		m.status = "Plan preview cancelled. No commands were run."
		return m, tea.Quit
	}
	if m.help {
		switch key {
		case "pgup", "up", "k":
			m.pageScroll = max(0, m.pageScroll-max(1, m.height/2))
			return m, nil
		case "pgdown", "down", "j":
			m.pageScroll += max(1, m.height/2)
			return m, nil
		case "q", "ctrl+c":
			return m, m.quit()
		}
		if key == "?" || key == "esc" || key == "enter" {
			m.help = false
			m.pageScroll = 0
		}
		return m, nil
	}
	if m.commandPalette {
		return m, m.handleCommandPaletteKey(message, key)
	}
	if m.pathMode != "" {
		return m, m.handlePathKey(message, key)
	}
	if m.searching {
		return m, m.handleSearchKey(message, key)
	}
	if m.environmentSearching {
		return m, m.handleEnvironmentSearchKey(message, key)
	}
	if m.approval {
		switch key {
		case "pgup", "up", "k":
			m.pageScroll = max(0, m.pageScroll-max(1, m.height/2))
		case "pgdown", "down", "j":
			m.pageScroll += max(1, m.height/2)
		case "y", "enter":
			return m, m.approvePlan()
		case "n", "esc":
			m.cancelApproval()
		}
		return m, nil
	}
	switch key {
	case "ctrl+c", "q":
		return m, m.quit()
	case "ctrl+p":
		m.openCommandPalette()
	case "?":
		m.help = true
		return m, nil
	case "1":
		m.navigate(OverviewScreen)
	case "2":
		m.navigate(CatalogScreen)
	case "3":
		m.navigate(ProfilesScreen)
	case "4":
		m.navigate(EnvironmentScreen)
	case "5":
		m.navigate(PlanScreen)
	case "6":
		m.navigate(SourcesScreen)
	case "7":
		m.navigate(ProgressScreen)
	case "8":
		m.navigate(ResultsScreen)
	case "tab", "right":
		m.cycleFocus(1)
	case "shift+tab", "left":
		m.cycleFocus(-1)
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		m.ensureCursorVisible(-1)
	case "down", "j":
		m.cursor++
		m.clampCursor()
		m.ensureCursorVisible(1)
	case "pgup":
		m.pageScroll = max(0, m.pageScroll-max(1, m.height/2))
	case "pgdown":
		m.pageScroll += max(1, m.height/2)
	case "home":
		m.pageScroll = 0
	case "end":
		m.pageScroll = 1 << 30
	case "f":
		m.pageScroll = 0
		if m.screen == CatalogScreen {
			return m, m.activateTarget(hitTarget{action: "search", index: 0})
		} else if m.screen == EnvironmentScreen {
			return m, m.activateTarget(hitTarget{action: "search", index: 0})
		}
	case "[":
		if m.screen == CatalogScreen {
			m.cycleCategory(-1)
			m.cursor = 0
			m.pageScroll = 0
		}
	case "]":
		if m.screen == CatalogScreen {
			m.cycleCategory(1)
			m.cursor = 0
			m.pageScroll = 0
		}
	case "s":
		if m.screen == CatalogScreen {
			m.cycleStateFilter()
			m.cursor = 0
			m.pageScroll = 0
		}
	case "a":
		if m.screen == CatalogScreen {
			m.categoryFilter = ""
			m.filter = ""
			m.input.SetValue("")
			m.stateFilter = "all"
			m.cursor = 0
			m.pageScroll = 0
		} else if m.screen == EnvironmentScreen {
			m.environmentFilter = ""
			m.input.SetValue("")
			m.cursor = 0
			m.pageScroll = 0
		} else if m.screen == SourcesScreen {
			m.sourceMode = "automatic"
			m.invalidatePlan("Automatic region-based source policy selected.")
		}
	case "space":
		if target, ok := m.focusedControlTarget(); ok {
			if target.action == "inspect-tool" {
				m.cursor = target.index
				m.toggleCurrent()
			} else {
				return m, m.activateTarget(target)
			}
		} else {
			m.toggleCurrent()
		}
		m.pageScroll = 0
	case "enter":
		if target, ok := m.focusedControlTarget(); ok {
			return m, m.activateTarget(target)
		}
		switch m.screen {
		case OverviewScreen:
			m.navigate(CatalogScreen)
		case CatalogScreen:
			m.cursor = min(m.maxCursor(), max(0, m.cursor))
		case ProfilesScreen:
			m.applyProfiles()
		case SourcesScreen:
			m.chooseCurrentManager()
		}
	case "p":
		if m.screen == PlanScreen {
			return m, m.buildPlan()
		}
	case "i":
		if m.screen == PlanScreen && m.planReady && !m.plan.Blocked {
			m.beginApproval()
		}
	case "0":
		if m.screen == SourcesScreen {
			m.sourceMode = "automatic"
			m.preferredManager = ""
			m.invalidatePlan("Automatic source selection and package-manager order restored.")
		}
	case "r":
		return m, m.refreshInventory()
	case "e":
		if m.screen == ProfilesScreen || m.screen == CatalogScreen {
			m.pathMode = "export"
			m.pathInput = ""
			m.status = "Enter a new file path for the selected-tool export, then press Enter."
		}
	case "o":
		if m.screen == SourcesScreen {
			m.sourceMode = "official-only"
			m.invalidatePlan("Official-only downloads selected. No regional mirror will be contacted.")
		} else if m.screen == ProfilesScreen || m.screen == CatalogScreen {
			m.pathMode = "import"
			m.pathInput = ""
			m.status = "Enter the selection JSON file path, then press Enter."
		}
	case "d":
		if m.screen == ResultsScreen {
			m.pathMode = "diagnostic"
			m.pathInput = ""
			m.status = "Enter a new JSON report path. The file will contain redacted diagnostics and will not overwrite an existing file."
		}
	case "m":
		if m.screen == SourcesScreen {
			m.sourceMode = "official-first"
			m.invalidatePlan("Official-first downloads selected. Verified regional mirrors may be tried after retryable official-source failures.")
		}
	case "c":
		if m.screen == SourcesScreen {
			m.sourceMode = "mainland-china"
			m.invalidatePlan("Mainland China selected manually. Official downloads remain first; verified regional mirrors may follow retryable failures.")
		}
	case "u":
		if m.screen == SourcesScreen {
			m.pathMode = "custom-source"
			m.pathInput = ""
			m.status = "Enter tool-id=https://mirror.example/archive, then press Enter. The catalog SHA-256 remains mandatory."
		}
	case "v":
		if m.screen == SourcesScreen {
			m.pathMode = "registry"
			m.pathInput = ""
			m.status = "Enter ecosystem=https://registry.example, such as npm=https://registry.npmjs.org/."
		}
	case "l":
		if m.screen == SourcesScreen {
			m.pathMode = "offline-archive"
			m.pathInput = ""
			m.status = "Enter tool-id=absolute-archive-path, then press Enter. The catalog SHA-256 remains mandatory."
		}
	case "b":
		if m.screen == SourcesScreen {
			m.pathMode = "offline-bundle"
			m.pathInput = ""
			m.status = "Enter the absolute directory path of a signed platform bundle, then press Enter."
		}
	case "x":
		if m.screen == SourcesScreen {
			m.customSources = make(map[string]string)
			m.registrySources = make(sources.RegistryURLs)
			m.offlineArchives = make(map[string]string)
			m.offlineBundlePath = ""
			m.invalidatePlan("Custom and offline source overrides cleared.")
		}
	case "g":
		if m.screen == SourcesScreen {
			m.sourceMode = "global"
			m.invalidatePlan("Global source policy selected. Regional mirrors will not be contacted.")
		}
	}
	m.clampCursor()
	return m, nil
}

func (m *Model) approvePlan() tea.Cmd {
	m.approval = false
	m.preflighting = true
	m.status = "Checking your selections again before installation."
	return m.preflight()
}

func (m *Model) beginApproval() {
	if len(m.plan.InstallItems()) == 0 {
		m.status = "Nothing needs installing. The selected tools are already present."
		return
	}
	m.approval = true
	m.pageScroll = 0
}

func (m *Model) cancelApproval() {
	m.approval = false
	m.status = "Installation cancelled. No commands were run."
}

func (m *Model) navigate(screen Screen) {
	m.screen = screen
	m.cursor = 0
	m.pageScroll = 0
	if screen == EnvironmentScreen || screen == SourcesScreen {
		m.advancedExpanded = true
	}
}

func (m *Model) maxCursor() int {
	count := 0
	switch m.screen {
	case CatalogScreen:
		count = len(m.filteredTools())
	case ProfilesScreen:
		if m.catalog != nil {
			count = len(m.catalog.Profiles())
		}
	case EnvironmentScreen:
		count = len(m.filteredEnvironmentIDs())
	case PlanScreen:
		count = len(m.plan.Items) + len(m.plan.Issues)
	case ProgressScreen:
		if m.session != nil {
			count = len(m.session.Report())
		}
	case SourcesScreen:
		count = len(m.inventory.Managers)
	case ResultsScreen:
		if m.session != nil {
			count = len(m.session.Report())
		}
	}
	return max(0, count-1)
}

func (m *Model) ensureCursorVisible(direction int) {
	if direction == 0 {
		return
	}
	action := ""
	switch m.screen {
	case CatalogScreen:
		action = "inspect-tool"
	case ProfilesScreen:
		action = "profile-toggle"
	case EnvironmentScreen:
		action = "inspect-row"
	case PlanScreen:
		action = "inspect-plan"
	case ProgressScreen, ResultsScreen:
		action = "inspect-result"
	default:
		return
	}
	m.clampCursor()
	for attempts := 0; attempts < max(1, m.height); attempts++ {
		m.renderWorkspace()
		for _, target := range m.hitTargets {
			if target.action == action && target.index == m.cursor {
				return
			}
		}
		if direction > 0 {
			m.pageScroll++
		} else if m.pageScroll > 0 {
			m.pageScroll--
		} else {
			return
		}
	}
}

func (m *Model) handleMouseClick(event tea.Mouse) tea.Cmd {
	if event.Button != tea.MouseLeft {
		return nil
	}
	var selected *hitTarget
	for _, target := range m.hitTargets {
		if event.X < target.x || event.X >= target.x+target.width || event.Y < target.y || event.Y >= target.y+max(1, target.height) {
			continue
		}
		if selected == nil || target.width*target.height <= selected.width*selected.height {
			copy := target
			selected = &copy
		}
	}
	if selected == nil {
		return nil
	}
	m.focusedTarget = selected.id
	if selected.action == "quit" {
		return m.quit()
	}
	if m.planning {
		return nil
	}
	return m.activateTarget(*selected)
}

func (m *Model) handleMouseWheel(event tea.Mouse) {
	if m.planning {
		return
	}
	step := 3
	if event.Button == tea.MouseWheelUp {
		step = -1
	} else if event.Button != tea.MouseWheelDown {
		return
	}
	if m.detailViewportRect.contains(event.X, event.Y) {
		if step < 0 {
			m.detailViewport.ScrollUp(-step)
		} else {
			m.detailViewport.ScrollDown(step)
		}
		m.inspectorScroll = m.detailViewport.YOffset()
		return
	}
	if m.mainViewportRect.contains(event.X, event.Y) {
		if step < 0 {
			m.contentViewport.ScrollUp(-step)
		} else {
			m.contentViewport.ScrollDown(step)
		}
		m.pageScroll = m.contentViewport.YOffset()
	}
}

func (m *Model) handleMouseMotion(event tea.Mouse) {
	for _, target := range m.hitTargets {
		if event.X >= target.x && event.X < target.x+target.width && event.Y >= target.y && event.Y < target.y+max(1, target.height) {
			m.hoveredTarget = target.id
			return
		}
	}
	m.hoveredTarget = ""
}

func (m *Model) activateTarget(target hitTarget) tea.Cmd {
	m.pageScroll = 0
	switch target.action {
	case "nav-home":
		m.navigate(OverviewScreen)
	case "nav-tools":
		m.navigate(CatalogScreen)
	case "nav-profiles":
		m.navigate(ProfilesScreen)
	case "nav-environment":
		m.navigate(EnvironmentScreen)
	case "nav-plan":
		m.navigate(PlanScreen)
	case "nav-activity":
		m.navigate(ProgressScreen)
	case "nav-results":
		m.navigate(ResultsScreen)
	case "nav-sources":
		m.navigate(SourcesScreen)
	case "welcome":
		m.navigate(OverviewScreen)
	case "choose":
		m.navigate(CatalogScreen)
	case "review":
		m.navigate(PlanScreen)
	case "install":
		if m.session == nil {
			m.navigate(PlanScreen)
			m.status = "Review the plan before starting installation."
		} else {
			m.navigate(ProgressScreen)
		}
	case "results":
		m.navigate(ResultsScreen)
	case "advanced":
		m.advancedExpanded = !m.advancedExpanded
	case "environment":
		m.navigate(EnvironmentScreen)
	case "sources":
		m.navigate(SourcesScreen)
	case "start-setup", "browse-tools":
		m.navigate(CatalogScreen)
	case "choose-profile":
		m.navigate(ProfilesScreen)
	case "use-profiles":
		m.navigate(ProfilesScreen)
	case "review-selection":
		m.navigate(PlanScreen)
		return m.buildPlan()
	case "apply-profiles":
		m.applyProfiles()
	case "build-plan":
		m.navigate(PlanScreen)
		return m.buildPlan()
	case "review-install":
		if m.planReady && !m.plan.Blocked {
			m.beginApproval()
		} else {
			return m.buildPlan()
		}
	case "approve":
		return m.approvePlan()
	case "cancel-approval":
		m.cancelApproval()
	case "search":
		if m.screen == EnvironmentScreen {
			m.environmentSearching = true
			m.input.SetValue(m.environmentFilter)
			m.input.Placeholder = "Search names, paths, versions..."
		} else {
			m.searching = true
			m.input.SetValue(m.filter)
			m.input.Placeholder = "Search tools..."
		}
		return m.input.Focus()
	case "input-focus":
		return m.input.Focus()
	case "search-tools":
		m.navigate(CatalogScreen)
		return m.activateTarget(hitTarget{action: "search", index: 0})
	case "category-previous":
		m.cycleCategory(-1)
		m.cursor = 0
	case "category-next":
		m.cycleCategory(1)
		m.cursor = 0
	case "state-filter":
		m.cycleStateFilter()
		m.cursor = 0
	case "clear-filters":
		m.filter, m.categoryFilter = "", ""
		m.stateFilter = "all"
		m.searching = false
		m.input.SetValue("")
		m.input.Blur()
		m.cursor = 0
	case "clear-search":
		m.environmentFilter = ""
		m.environmentSearching = false
		m.input.SetValue("")
		m.input.Blur()
		m.cursor = 0
	case "refresh":
		return m.refreshInventory()
	case "toggle-selection":
		m.cursor = target.index
		m.toggleCurrent()
	case "select-platform-alternative":
		m.selectPlatformAlternative(target.index)
	case "inspect-tool":
		m.cursor = target.index
	case "inspect-plan":
		m.cursor = target.index
	case "inspect-row":
		m.cursor = target.index
	case "inspect-result":
		m.cursor = target.index
	case "inspect-warning":
		m.cursor = 0
	case "prefer-manager":
		m.cursor = target.index
		m.chooseCurrentManager()
	case "profile-toggle":
		m.cursor = target.index
		m.toggleCurrent()
	case "source-automatic":
		m.sourceMode = "automatic"
		m.invalidatePlan("Automatic source choice selected.")
	case "source-china":
		m.sourceMode = "mainland-china"
		m.invalidatePlan("Mainland China source choice selected.")
	case "source-global":
		m.sourceMode = "global"
		m.invalidatePlan("Global source choice selected.")
	case "source-official-only":
		m.sourceMode = "official-only"
		m.invalidatePlan("Publisher-only downloads selected.")
	case "source-official-first":
		m.sourceMode = "official-first"
		m.invalidatePlan("Publisher-first downloads selected.")
	case "custom-source":
		m.pathMode, m.pathInput = "custom-source", ""
		m.input.SetValue("")
		m.input.Placeholder = "tool-id=https://mirror.example/archive"
		m.input.Focus()
		m.status = "Type tool-id=https://... and press Enter. The catalog checksum is still required."
	case "registry-source":
		m.pathMode, m.pathInput = "registry", ""
		m.input.SetValue("")
		m.input.Placeholder = "npm=https://registry.npmjs.org/"
		m.input.Focus()
		m.status = "Type an ecosystem and registry URL, then press Enter."
	case "offline-archive":
		m.pathMode, m.pathInput = "offline-archive", ""
		m.input.SetValue("")
		m.input.Placeholder = "tool-id=absolute-archive-path"
		m.input.Focus()
		m.status = "Type tool-id=absolute-archive-path and press Enter."
	case "offline-bundle":
		m.pathMode, m.pathInput = "offline-bundle", ""
		m.input.SetValue("")
		m.input.Placeholder = "Signed bundle directory path"
		m.input.Focus()
		m.status = "Type the signed bundle directory path and press Enter."
	case "clear-sources":
		m.customSources = make(map[string]string)
		m.registrySources = make(sources.RegistryURLs)
		m.offlineArchives = make(map[string]string)
		m.offlineBundlePath = ""
		m.invalidatePlan("Custom and offline source overrides cleared.")
	case "reset-manager":
		m.preferredManager = ""
		m.invalidatePlan("Automatic package-manager order restored.")
	case "apply", "save-report":
		if m.screen == ProfilesScreen {
			m.pathMode, m.pathInput = "export", ""
		} else if m.screen == ResultsScreen {
			m.pathMode, m.pathInput = "diagnostic", ""
			m.input.SetValue("")
			m.input.Placeholder = "New redacted report path"
			return m.input.Focus()
		}
	case "export-selection":
		m.pathMode, m.pathInput = "export", ""
		m.input.SetValue("")
		m.input.Placeholder = "New JSON file path"
		m.input.Focus()
	case "import-selection":
		m.pathMode, m.pathInput = "import", ""
		m.input.SetValue("")
		m.input.Placeholder = "Selection JSON file path"
		m.input.Focus()
	case "help":
		m.help = true
	case "commands":
		m.openCommandPalette()
	case "close-help":
		m.help = false
		m.pageScroll = 0
	case "close-commands":
		m.commandPalette = false
		m.input.Blur()
	case "palette-command":
		items := m.commandList.Items()
		if target.index >= 0 && target.index < len(items) {
			if selected, ok := items[target.index].(paletteItem); ok {
				m.commandPalette = false
				m.input.Blur()
				return m.activateTarget(hitTarget{action: selected.action, index: selected.index})
			}
		}
	case "quit":
		return m.quit()
	case "confirm-path":
		return m.handlePathKey(tea.KeyPressMsg{Code: tea.KeyEnter}, "enter")
	case "cancel-path":
		return m.handlePathKey(tea.KeyPressMsg{Code: tea.KeyEscape}, "esc")
	}
	return nil
}

func (m *Model) quit() tea.Cmd {
	if m.planningCancel != nil {
		m.planningCancel()
		m.planningCancel = nil
	}
	m.planning = false
	if m.session != nil && !m.session.Complete() {
		m.session.Cancel()
	}
	if m.installCancel != nil {
		m.installCancel()
		m.installCancel = nil
	}
	if m.downloadCancel != nil {
		m.downloadCancel()
	}
	if cleanupErr := m.cleanupPreparedArchives(); cleanupErr != nil {
		m.status = "Temporary archive cleanup needs attention: " + cleanupErr.Error()
	}
	return tea.Quit
}

func (m *Model) handleSearchKey(message tea.KeyPressMsg, key string) tea.Cmd {
	switch key {
	case "enter", "esc":
		m.searching = false
		m.filter = m.input.Value()
		m.input.Blur()
		m.cursor = 0
		return nil
	default:
		input, command := m.input.Update(message)
		m.input = input
		m.filter = input.Value()
		m.cursor = 0
		return command
	}
}

func (m *Model) handleEnvironmentSearchKey(message tea.KeyPressMsg, key string) tea.Cmd {
	switch key {
	case "enter", "esc":
		m.environmentSearching = false
		m.environmentFilter = m.input.Value()
		m.input.Blur()
		m.cursor = 0
		return nil
	default:
		input, command := m.input.Update(message)
		m.input = input
		m.environmentFilter = input.Value()
		m.cursor = 0
		return command
	}
}

func (m *Model) toggleCurrent() {
	switch m.screen {
	case CatalogScreen:
		tools := m.filteredTools()
		if m.cursor >= 0 && m.cursor < len(tools) {
			item := tools[m.cursor]
			id := item.ID
			if !m.selected[id] && !item.Supports(m.inventory.Platform.OS, m.inventory.Platform.Architecture) {
				if alternatives := m.availablePlatformAlternatives(item); len(alternatives) > 0 {
					m.selected[id] = true
					m.selectedAlternatives[id] = alternatives[0].ToolID
					m.invalidatePlan(fmt.Sprintf("Selected %s as the native alternative for %s. Review its limits in the details panel.", m.toolName(alternatives[0].ToolID), item.Name))
					break
				}
			}
			if m.selected[id] {
				delete(m.selected, id)
				delete(m.selectedAlternatives, id)
			} else {
				m.selected[id] = true
			}
			m.invalidatePlan("Selection changed. Rebuild the installation plan.")
		}
	case ProfilesScreen:
		profiles := m.catalog.Profiles()
		if m.cursor >= 0 && m.cursor < len(profiles) {
			id := profiles[m.cursor].ID
			m.selectedProfiles[id] = !m.selectedProfiles[id]
		}
	case SourcesScreen:
		m.chooseCurrentManager()
	}
}

func (m *Model) selectPlatformAlternative(index int) {
	tools := m.filteredTools()
	if m.catalog == nil || m.cursor < 0 || m.cursor >= len(tools) {
		return
	}
	item := tools[m.cursor]
	alternatives := m.availablePlatformAlternatives(item)
	if index < 0 || index >= len(alternatives) {
		return
	}
	selectedTool := alternatives[index].ToolID
	if m.selectedPlatformAlternative(item, selectedTool) {
		delete(m.selected, item.ID)
		delete(m.selectedAlternatives, item.ID)
		m.invalidatePlan("Native alternative removed from the selection.")
		return
	}
	m.selected[item.ID] = true
	m.selectedAlternatives[item.ID] = selectedTool
	m.invalidatePlan(fmt.Sprintf("Selected %s instead of %s. The details panel explains the compatibility limits.", m.toolName(selectedTool), item.Name))
}

func (m *Model) availablePlatformAlternatives(item tool.Manifest) []tool.PlatformAlternative {
	if m.catalog == nil {
		return nil
	}
	available := make([]tool.PlatformAlternative, 0)
	for _, alternative := range item.PlatformAlternatives[m.inventory.Platform.OS] {
		if len(alternative.Architectures) > 0 && !slices.Contains(alternative.Architectures, m.inventory.Platform.Architecture) {
			continue
		}
		candidate, exists := m.catalog.Tool(alternative.ToolID)
		if exists && candidate.Supports(m.inventory.Platform.OS, m.inventory.Platform.Architecture) {
			available = append(available, alternative)
		}
	}
	return available
}

func (m *Model) selectedPlatformAlternative(item tool.Manifest, alternativeID string) bool {
	if !m.selected[item.ID] {
		return false
	}
	selectedID := m.selectedAlternatives[item.ID]
	if selectedID == "" {
		alternatives := m.availablePlatformAlternatives(item)
		if len(alternatives) > 0 {
			selectedID = alternatives[0].ToolID
		}
	}
	return selectedID == alternativeID
}

func (m *Model) toolName(id string) string {
	if m.catalog != nil {
		if item, exists := m.catalog.Tool(id); exists {
			return item.Name
		}
	}
	return id
}

func (m *Model) applyProfiles() {
	if m.catalog == nil {
		m.status = "The tool catalog is not available, so profiles cannot be applied."
		return
	}
	selection := make(map[string]bool)
	selectedProfiles := 0
	for _, profile := range m.catalog.Profiles() {
		if !m.selectedProfiles[profile.ID] {
			continue
		}
		selectedProfiles++
		for _, id := range profile.Tools {
			selection[id] = true
		}
	}
	if selectedProfiles == 0 {
		m.status = "Select one or more profiles, then press Enter to combine them."
		return
	}
	m.selected = selection
	m.selectedAlternatives = make(map[string]string)
	m.invalidatePlan("Profiles applied. Review and edit the resulting tool selection.")
	m.screen = CatalogScreen
	m.cursor = 0
	m.pageScroll = 0
}

func (m *Model) buildPlan() tea.Cmd {
	selected := m.selectedIDs()
	if len(selected) == 0 {
		m.plan = planning.Plan{}
		m.planReady = false
		m.status = "Choose at least one tool or profile before reviewing a plan."
		return nil
	}
	plan, err := m.buildSelectedPlan(selected)
	if err != nil {
		m.plan = planning.Plan{}
		m.planReady = false
		m.status = err.Error()
		return nil
	}
	m.plan = plan
	if plan.Blocked || len(plan.InstallItems()) == 0 {
		m.planReady = true
		if plan.Blocked {
			m.status = "Resolve the blocking items below before any installation can run."
		} else {
			m.status = "Every selected tool is already present. No installation is needed."
		}
		return nil
	}
	m.planReady = false
	m.status = "Inspecting cached package metadata for safe transaction previews."
	return m.reviewPlan(plan, false, "")
}

func (m *Model) reviewPlan(plan planning.Plan, approval bool, previousSignature string) tea.Cmd {
	ctx, cancel := context.WithTimeout(m.ctx, 20*time.Second)
	m.planning = true
	m.planningCancel = cancel
	inventory := m.inventory
	return func() tea.Msg {
		defer cancel()
		return planPreviewMsg{
			plan:              planning.PreviewPackageTransactions(ctx, inventory, plan),
			previousSignature: previousSignature,
			approval:          approval,
		}
	}
}

func (m *Model) handlePlanPreview(message planPreviewMsg) (tea.Model, tea.Cmd) {
	m.planning = false
	if m.planningCancel != nil {
		m.planningCancel()
		m.planningCancel = nil
	}
	m.plan = message.plan
	m.planReady = true
	if !message.approval {
		if message.plan.Blocked {
			m.status = "Resolve the blocking items below before any installation can run."
		} else {
			m.status = fmt.Sprintf("Plan ready: %d installation step(s), %d preserved tool(s). Review every source, candidate version, transaction preview and command.", len(message.plan.InstallItems()), len(message.plan.Items)-len(message.plan.InstallItems()))
		}
		return m, nil
	}
	if message.plan.Blocked || planSignature(message.plan) != message.previousSignature {
		m.screen = PlanScreen
		if message.plan.Blocked {
			m.status = "The refreshed plan has blocking issues. Review them below; no command was run."
		} else {
			m.status = "The machine or package metadata changed since review. Inspect the refreshed plan and approve it again."
		}
		return m, nil
	}
	session, err := installation.NewSession(message.plan)
	if err != nil {
		m.screen = PlanScreen
		m.status = "Could not start the installation session: " + err.Error()
		return m, nil
	}
	m.session = session
	m.screen = ProgressScreen
	m.status = "Preparing the approved archive downloads."
	return m, m.prepareArchives()
}

func (m *Model) buildSelectedPlan(selected []string) (planning.Plan, error) {
	allowRegionalFallback := false
	switch m.sourceMode {
	case "automatic":
		allow, err := m.regionDetection.UseRegionalFallback(regional.Automatic)
		if err != nil {
			if m.missingToolsHaveExplicitSources(selected) {
				allowRegionalFallback = false
				break
			}
			return planning.Plan{
				Selected: append([]string(nil), selected...),
				Blocked:  true,
				Issues:   []planning.Issue{{Severity: planning.Blocking, Message: err.Error()}},
			}, nil
		}
		allowRegionalFallback = allow
	case "official-first", "mainland-china":
		allowRegionalFallback = true
	case "official-only", "global":
		allowRegionalFallback = false
	default:
		return planning.Plan{}, fmt.Errorf("unknown download source policy %q", m.sourceMode)
	}
	return planning.BuildWithOptions(m.catalog, m.inventory, selected, planning.Options{
		PreferredManager:      m.preferredManager,
		AllowRegionalFallback: allowRegionalFallback,
		CustomSources:         cloneStringMap(m.customSources),
		OfflineArchives:       cloneStringMap(m.offlineArchives),
		RegistrySources:       cloneRegistryURLs(m.registrySources),
		NativeAlternatives:    m.nativeAlternativeSelections(),
	})
}

func (m *Model) nativeAlternativeSelections() map[string][]string {
	selected := make(map[string][]string)
	if m.catalog == nil {
		return selected
	}
	for requestedID, isSelected := range m.selected {
		if !isSelected {
			continue
		}
		requested, exists := m.catalog.Tool(requestedID)
		if !exists || requested.Supports(m.inventory.Platform.OS, m.inventory.Platform.Architecture) {
			continue
		}
		alternativeID := m.selectedAlternatives[requestedID]
		if alternativeID == "" {
			alternatives := m.availablePlatformAlternatives(requested)
			if len(alternatives) > 0 {
				alternativeID = alternatives[0].ToolID
			}
		}
		if alternativeID != "" {
			selected[alternativeID] = append(selected[alternativeID], requestedID)
		}
	}
	for id := range selected {
		sort.Strings(selected[id])
	}
	return selected
}

func (m *Model) missingToolsHaveExplicitSources(selected []string) bool {
	for _, id := range selected {
		if m.inventory.Tools[id].State != detection.Missing {
			continue
		}
		if m.customSources[id] != "" || m.offlineArchives[id] != "" {
			continue
		}
		manifest, exists := m.catalog.Tool(id)
		if !exists {
			return false
		}
		registrySelected := false
		for _, installer := range manifest.RegistryInstallers {
			if installer.Platform == m.inventory.Platform.OS && m.registrySources[installer.Ecosystem] != "" {
				registrySelected = true
				break
			}
		}
		if !registrySelected {
			return false
		}
	}
	return true
}

func (m *Model) handlePathKey(message tea.KeyPressMsg, key string) tea.Cmd {
	switch key {
	case "?":
		m.help = true
		return nil
	case "enter":
		if m.pathInput == "" {
			m.pathInput = m.input.Value()
		}
		path := strings.TrimSpace(m.pathInput)
		if path == "" {
			m.status = "A file path is required."
			return nil
		}
		var err error
		switch m.pathMode {
		case "export":
			err = m.writeSelection(path)
		case "import":
			err = m.readSelection(path)
		case "diagnostic":
			err = m.writeDiagnostic(path)
		case "custom-source":
			err = m.setCustomSource(path)
		case "registry":
			err = m.setRegistrySource(path)
		case "offline-archive":
			err = m.setOfflineArchive(path)
		case "offline-bundle":
			err = m.LoadOfflineBundle(path)
		}
		if err != nil {
			m.status = err.Error()
		} else if m.pathMode == "export" {
			m.status = "Selection exported to " + path + "."
		} else if m.pathMode == "diagnostic" {
			m.status = "Redacted diagnostic report saved to " + path + "."
		} else if m.pathMode == "custom-source" {
			m.status = "Custom HTTPS source recorded. Rebuild the plan to review the catalog-pinned digest."
		} else if m.pathMode == "registry" {
			m.status = "Registry override recorded. Rebuild the plan to review the exact package version and source."
		} else if m.pathMode == "offline-archive" {
			m.status = "Offline archive recorded. Rebuild the plan to review its catalog-pinned digest."
		} else if m.pathMode == "offline-bundle" {
			// LoadOfflineBundle sets a more specific verification status.
		} else {
			m.status = "Selection imported. Review the catalog before planning."
			m.screen = CatalogScreen
		}
		m.pathMode = ""
		m.pathInput = ""
		m.input.Blur()
	case "esc":
		m.pathMode = ""
		m.pathInput = ""
		m.input.Blur()
		m.status = "Selection file operation cancelled."
	default:
		input, command := m.input.Update(message)
		m.input = input
		m.pathInput = input.Value()
		return command
	}
	return nil
}

func (m *Model) setCustomSource(input string) error {
	toolID, sourceURL, found := strings.Cut(input, "=")
	toolID = strings.TrimSpace(toolID)
	sourceURL = strings.TrimSpace(sourceURL)
	if !found || toolID == "" || sourceURL == "" {
		return errors.New("custom source must use tool-id=https://mirror.example/archive")
	}
	manifest, exists := m.catalog.Tool(toolID)
	if !exists {
		return fmt.Errorf("unknown catalog tool %q", toolID)
	}
	artifact, exists := manifest.ArtifactFor(m.inventory.Platform.OS, m.inventory.Platform.Architecture)
	if !exists {
		return fmt.Errorf("%s has no catalog-pinned archive for this platform", toolID)
	}
	if err := sources.ValidateRemoteSource(sources.RemoteSource{Name: toolID + " custom source", Kind: sources.Custom, URL: sourceURL, SHA256: artifact.SHA256}); err != nil {
		return fmt.Errorf("validate custom source: %w", err)
	}
	m.customSources[toolID] = sourceURL
	delete(m.offlineArchives, toolID)
	m.offlineBundlePath = ""
	m.invalidatePlan("Custom HTTPS source recorded. Rebuild the plan to review the catalog-pinned digest.")
	return nil
}

func (m *Model) setRegistrySource(input string) error {
	updated, err := sources.ParseRegistryAssignments([]string{strings.TrimSpace(input)})
	if err != nil {
		return err
	}
	if m.registrySources == nil {
		m.registrySources = make(sources.RegistryURLs)
	}
	for ecosystem, source := range updated {
		m.registrySources[ecosystem] = source
	}
	m.invalidatePlan("Registry override recorded. Rebuild the plan to review the exact package version and source.")
	return nil
}

func (m *Model) setOfflineArchive(input string) error {
	toolID, archivePath, found := strings.Cut(input, "=")
	toolID = strings.TrimSpace(toolID)
	archivePath = strings.Trim(strings.TrimSpace(archivePath), "\"")
	if !found || toolID == "" || archivePath == "" {
		return errors.New("offline archive must use tool-id=absolute-archive-path")
	}
	manifest, exists := m.catalog.Tool(toolID)
	if !exists {
		return fmt.Errorf("unknown catalog tool %q", toolID)
	}
	if _, exists := manifest.ArtifactFor(m.inventory.Platform.OS, m.inventory.Platform.Architecture); !exists {
		return fmt.Errorf("%s has no catalog-pinned archive for this platform", toolID)
	}
	absolute, err := filepath.Abs(archivePath)
	if err != nil {
		return fmt.Errorf("resolve offline archive path: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("offline archive must be an existing regular, non-symlink file: %s", absolute)
	}
	m.offlineArchives[toolID] = absolute
	delete(m.customSources, toolID)
	m.offlineBundlePath = ""
	m.invalidatePlan("Offline archive recorded. Rebuild the plan to review its catalog-pinned digest.")
	return nil
}

func (m *Model) writeSelection(path string) error {
	data, err := catalog.EncodeSelection(m.catalog, m.selectedIDs())
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create selection file: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write selection file: %w", err)
	}
	return nil
}

func (m *Model) readSelection(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read selection file: %w", err)
	}
	selected, err := catalog.DecodeSelection(m.catalog, data)
	if err != nil {
		return err
	}
	m.selected = make(map[string]bool, len(selected))
	m.selectedAlternatives = make(map[string]string)
	for _, id := range selected {
		m.selected[id] = true
	}
	m.invalidatePlan("Selection imported. Review the catalog before planning.")
	return nil
}

func (m *Model) writeDiagnostic(path string) error {
	document := reporting.New(m.inventory.Platform, m.selectedIDs(), m.inventory.Warnings, m.SessionReport())
	data, err := reporting.Encode(document)
	if err != nil {
		return fmt.Errorf("encode diagnostic report: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create diagnostic report: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write diagnostic report: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close diagnostic report: %w", err)
	}
	return nil
}

func (m *Model) invalidatePlan(message string) {
	m.planReady = false
	m.plan = planning.Plan{}
	m.approval = false
	m.status = message
}

func (m *Model) selectedIDs() []string {
	resolved := make(map[string]bool, len(m.selected))
	for id, isSelected := range m.selected {
		if !isSelected {
			continue
		}
		if m.catalog == nil {
			resolved[id] = true
			continue
		}
		manifest, exists := m.catalog.Tool(id)
		if exists && !manifest.Supports(m.inventory.Platform.OS, m.inventory.Platform.Architecture) {
			alternatives := m.availablePlatformAlternatives(manifest)
			alternativeID := m.selectedAlternatives[id]
			if alternativeID == "" && len(alternatives) > 0 {
				alternativeID = alternatives[0].ToolID
			}
			if alternativeID != "" {
				resolved[alternativeID] = true
				continue
			}
		}
		resolved[id] = true
	}
	ids := make([]string, 0, len(resolved))
	for id := range resolved {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *Model) filteredTools() []tool.Manifest {
	if m.catalog == nil {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(m.filter))
	tools := make([]tool.Manifest, 0)
	for _, manifest := range m.catalog.Tools() {
		if m.categoryFilter != "" && manifest.Category != m.categoryFilter {
			continue
		}
		state := m.inventory.Tools[manifest.ID].State
		if m.stateFilter == "missing" && state != detection.Missing {
			continue
		}
		if m.stateFilter == "installed" && state != detection.Healthy && state != detection.Outdated && state != detection.Unknown {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join(append([]string{manifest.ID, manifest.Name, manifest.Category, manifest.Description}, manifest.Tags...), " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		tools = append(tools, manifest)
	}
	return tools
}

func (m *Model) filteredEnvironmentIDs() []string {
	query := strings.ToLower(strings.TrimSpace(m.environmentFilter))
	ids := m.inventory.ToolIDs()
	if query == "" {
		return ids
	}
	filtered := make([]string, 0, len(ids))
	for _, id := range ids {
		item := m.inventory.Tools[id]
		parts := []string{id, item.Name, item.Category, string(item.State)}
		for _, instance := range item.Instances {
			parts = append(parts, instance.Executable, instance.Path, instance.Version, string(instance.State), instance.Diagnostic, instance.ArchitectureDiagnostic)
		}
		for _, installed := range item.Packages {
			parts = append(parts, string(installed.Manager), installed.ID, installed.Version, installed.Source)
		}
		if strings.Contains(strings.ToLower(strings.Join(parts, " ")), query) {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

func (m *Model) cycleCategory(direction int) {
	categories := make([]string, 0)
	seen := make(map[string]bool)
	for _, manifest := range m.catalog.Tools() {
		if !seen[manifest.Category] {
			seen[manifest.Category] = true
			categories = append(categories, manifest.Category)
		}
	}
	sort.Strings(categories)
	if len(categories) == 0 {
		return
	}
	index := -1
	for i, category := range categories {
		if category == m.categoryFilter {
			index = i
			break
		}
	}
	index = (index + direction + len(categories) + 1) % (len(categories) + 1)
	if index == len(categories) {
		m.categoryFilter = ""
	} else {
		m.categoryFilter = categories[index]
	}
}

func (m *Model) cycleStateFilter() {
	switch m.stateFilter {
	case "all":
		m.stateFilter = "missing"
	case "missing":
		m.stateFilter = "installed"
	default:
		m.stateFilter = "all"
	}
}

func (m *Model) managers() []tool.PackageManager {
	managers := make([]tool.PackageManager, 0, len(m.inventory.Managers))
	for _, item := range m.inventory.Managers {
		managers = append(managers, item.Manager)
	}
	return managers
}

func (m *Model) chooseCurrentManager() {
	managers := m.managers()
	if m.cursor < 0 || m.cursor >= len(managers) {
		return
	}
	m.preferredManager = managers[m.cursor]
	m.invalidatePlan("Package manager preference updated. Rebuild the plan to see the selected source.")
}

func (m *Model) clampCursor() {
	var count int
	switch m.screen {
	case CatalogScreen:
		count = len(m.filteredTools())
	case ProfilesScreen:
		if m.catalog != nil {
			count = len(m.catalog.Profiles())
		}
	case EnvironmentScreen:
		count = len(m.filteredEnvironmentIDs())
	case PlanScreen:
		count = len(m.plan.Items) + len(m.plan.Issues)
	case ProgressScreen:
		if m.session != nil {
			count = len(m.session.Report())
		}
	case SourcesScreen:
		count = len(m.inventory.Managers)
	case ResultsScreen:
		if m.session != nil {
			count = len(m.session.Report())
		}
	}
	if count == 0 {
		m.cursor = 0
	} else if m.cursor >= count {
		m.cursor = count - 1
	}
}

func (m *Model) preflight() tea.Cmd {
	items := m.catalog
	machine := m.inventory.Platform
	ctx := m.ctx
	return func() tea.Msg {
		scanCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		inventory, err := detection.Scan(scanCtx, items, machine)
		return preflightMsg{inventory: inventory, err: err, approval: true}
	}
}

func (m *Model) handlePreflight(message preflightMsg) (tea.Model, tea.Cmd) {
	m.preflighting = false
	if message.err != nil {
		m.screen = PlanScreen
		m.status = "Could not refresh inventory; no command was run: " + message.err.Error()
		return m, nil
	}
	if !message.approval {
		m.inventory = message.inventory
		if len(m.selected) > 0 {
			return m, m.buildPlan()
		} else {
			m.status = "Inventory refreshed."
		}
		return m, nil
	}
	previousSignature := planSignature(m.plan)
	m.inventory = message.inventory
	updated, err := m.buildSelectedPlan(m.selectedIDs())
	if err != nil {
		m.screen = PlanScreen
		m.status = "Could not rebuild the plan; no command was run: " + err.Error()
		return m, nil
	}
	m.planReady = false
	m.status = "Rechecking cached package metadata before execution."
	return m, m.reviewPlan(updated, true, previousSignature)
}

func (m *Model) prepareArchives() tea.Cmd {
	home, homeErr := os.UserHomeDir()
	requests := archivePrepareRequests(m.plan, home)
	if len(requests) == 0 {
		return m.startNext()
	}
	installer := m.archiveInstaller
	if installer == nil {
		installer = artifacts.NewArchiveInstaller()
	}
	progressChannel := make(chan artifacts.PrepareProgress, 32)
	m.downloadProgressCh = progressChannel
	m.downloadProgress = make(map[string]sources.DownloadProgress, len(requests))
	m.preparingDownloads = true
	m.preparingDownloadCount = len(requests)
	m.status = fmt.Sprintf("Preparing %d independent archives with up to %d concurrent downloads.", len(requests), installer.Concurrency())
	ctx, cancel := context.WithCancel(m.ctx)
	m.downloadCancel = cancel
	return tea.Batch(waitForDownloadProgress(progressChannel), func() tea.Msg {
		defer close(progressChannel)
		if homeErr != nil {
			results := make([]artifacts.PrepareResult, len(requests))
			for index, request := range requests {
				results[index] = artifacts.PrepareResult{ToolID: request.ToolID, Err: fmt.Errorf("resolve the user home directory: %w", homeErr)}
			}
			return archivesPreparedMsg{results: results}
		}
		results := installer.PrepareMany(ctx, requests, func(event artifacts.PrepareProgress) {
			select {
			case progressChannel <- event:
			default:
				select {
				case <-progressChannel:
				default:
				}
				select {
				case progressChannel <- event:
				default:
				}
			}
		})
		if ctx.Err() != nil {
			for index := range results {
				if results[index].Prepared != nil {
					results[index].Err = errors.Join(results[index].Err, results[index].Prepared.Cleanup())
					results[index].Prepared = nil
				}
				results[index].Err = errors.Join(results[index].Err, ctx.Err())
			}
			return archivesPreparedMsg{results: results, cancelled: true}
		}
		return archivesPreparedMsg{results: results}
	})
}

func (m *Model) handleArchivesPrepared(message archivesPreparedMsg) (tea.Model, tea.Cmd) {
	if m.downloadCancel != nil {
		m.downloadCancel()
		m.downloadCancel = nil
	}
	m.preparingDownloads = false
	m.downloadProgressCh = nil
	if message.cancelled {
		m.preparedArchives = nil
		if m.session != nil {
			m.session.Cancel()
		}
		m.status = "Archive downloads were cancelled."
		return m, nil
	}
	m.preparedArchives = make(map[string]artifacts.PrepareResult, len(message.results))
	failed := 0
	for _, result := range message.results {
		m.preparedArchives[result.ToolID] = result
		if result.Progress.BytesReceived > 0 {
			m.downloadProgress[result.ToolID] = result.Progress
		}
		if result.Err != nil {
			failed++
		}
	}
	if failed > 0 {
		m.status = fmt.Sprintf("Prepared %d of %d archives; failed downloads will be reported while unrelated tools continue.", len(message.results)-failed, len(message.results))
	} else {
		m.status = fmt.Sprintf("Prepared %d archives; installation operations will run in dependency order.", len(message.results))
	}
	return m, m.startNext()
}

func planSignature(plan planning.Plan) string {
	parts := make([]string, 0, len(plan.Items)+len(plan.Issues))
	for _, item := range plan.Items {
		parts = append(parts, strings.Join([]string{item.ToolID, string(item.Action), string(item.ExistingState), string(item.Manager), item.PackageID, item.InstallerKind, item.Source, string(item.Scope), item.Command.Display(), item.Version, item.CandidateVersion, item.TransactionPreview, item.TransactionNote, item.InstallPath, fmt.Sprint(item.ApproximateSize), item.SizeEstimateNote, item.InstallationNote, fmt.Sprint(item.RegionalFallback), item.CustomSourceURL, item.OfflineArchivePath}, "|"))
		if item.Artifact != nil {
			parts = append(parts, fmt.Sprintf("%#v", *item.Artifact))
		}
		if item.RegistryInstaller != nil {
			parts = append(parts, strings.Join([]string{string(item.RegistryInstaller.Ecosystem), item.RegistryInstaller.PackageID, item.RegistryInstaller.Version}, "|"))
		}
		for _, pathChange := range item.PathChanges {
			parts = append(parts, strings.Join([]string{pathChange.Directory, pathChange.Target, pathChange.Shell, pathChange.ManualCommand, fmt.Sprint(pathChange.Persist), fmt.Sprint(pathChange.Manual)}, "|"))
		}
		for _, installed := range item.ExistingPackages {
			parts = append(parts, strings.Join([]string{item.ToolID, string(installed.Manager), installed.ID, installed.Version, installed.Source}, "|"))
		}
	}
	for _, issue := range plan.Issues {
		parts = append(parts, strings.Join([]string{string(issue.Severity), issue.ToolID, issue.Message}, "|"))
	}
	return strings.Join(parts, "\n")
}

func (m *Model) startNext() tea.Cmd {
	if m.session == nil || m.preparingDownloads {
		return nil
	}
	item, exists := m.session.Next()
	if !exists {
		if cleanupErr := m.cleanupPreparedArchives(); cleanupErr != nil {
			m.status = "All planned operations have completed; temporary archive cleanup needs attention: " + cleanupErr.Error()
			m.screen = ResultsScreen
			return nil
		}
		m.screen = ResultsScreen
		m.status = "All planned operations have completed. Review verification results and diagnostics."
		return nil
	}
	if item.Action == planning.Provided {
		actual, found := m.inventory.Tools[item.ToolID]
		if !found {
			actual = detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Unknown}
		}
		if err := m.session.RecordProvided(item, actual); err != nil {
			m.status = err.Error()
			return nil
		}
		if actual.State == detection.Healthy {
			m.status = item.Name + " is provided and verified by " + item.ProvidedBy + "."
		} else {
			m.status = item.Name + " was not provided by " + item.ProvidedBy + "; dependent operations will be skipped."
		}
		return m.startNext()
	}
	m.activeItem = item
	if item.Artifact != nil {
		prepared, exists := m.preparedArchives[item.ToolID]
		delete(m.preparedArchives, item.ToolID)
		if !exists {
			installErr := errors.New("approved archive was not prepared")
			_ = m.session.Record(item, installErr, detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Missing})
			m.status = "Could not prepare the approved archive for " + item.Name + "."
			m.activeItem = planning.Item{}
			return m.startNext()
		}
		if prepared.Err != nil {
			_ = m.session.Record(item, prepared.Err, detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Missing})
			m.status = "Could not download " + item.Name + ": " + prepared.Err.Error()
			m.activeItem = planning.Item{}
			return m.startNext()
		}
		if prepared.Prepared == nil {
			installErr := errors.New("archive preparation returned no verified payload")
			_ = m.session.Record(item, installErr, detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Missing})
			m.status = "Could not prepare the approved archive for " + item.Name + "."
			m.activeItem = planning.Item{}
			return m.startNext()
		}
		m.status = "Installing the prepared verified archive for " + item.Name + "."
		installer := m.archiveInstaller
		if installer == nil {
			installer = artifacts.NewArchiveInstaller()
		}
		ctx, cancel := context.WithCancel(m.ctx)
		m.installCancel = cancel
		catalogItems := m.catalog
		machine := m.inventory.Platform
		installCommand := func() tea.Msg {
			home, err := os.UserHomeDir()
			if err != nil {
				_ = prepared.Prepared.Cleanup()
				return artifactInstallMsg{item: item, installErr: err, actual: detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Missing}}
			}
			result, installErr := installer.InstallPrepared(ctx, item.ToolID, home, *item.Artifact, prepared.Prepared)
			if cleanupErr := prepared.Prepared.Cleanup(); cleanupErr != nil {
				result.CleanupNotice = joinCleanupNotice(result.CleanupNotice, "temporary archive cleanup failed: "+cleanupErr.Error())
				if installErr != nil {
					installErr = errors.Join(installErr, cleanupErr)
				}
			}
			if installErr != nil {
				return artifactInstallMsg{item: item, result: result, installErr: installErr, actual: detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Missing}}
			}
			scanCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			inventory, verifyError := detection.ScanWithSearchPaths(scanCtx, catalogItems, machine, searchDirectories(item.PathChanges))
			actual, ok := inventory.Tools[item.ToolID]
			if verifyError == nil && !ok {
				verifyError = fmt.Errorf("inventory did not return %s", item.ToolID)
			}
			if verifyError != nil {
				actual = detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Unknown}
			}
			if verifyError == nil && ctx.Err() != nil {
				return artifactInstallMsg{item: item, result: result, installErr: ctx.Err(), actual: detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Unknown}}
			}
			var pathErr error
			if verifyError == nil && actual.State == detection.Healthy {
				pathErr = pathconfig.Apply(item.PathChanges)
			}
			return artifactInstallMsg{item: item, result: result, pathErr: pathErr, inventory: inventory, actual: actual, verifyError: verifyError}
		}
		return installCommand
	}
	m.status = "Running the approved package-manager operation for " + item.Name + "."
	commandSpec := item.Command
	temporaryDirectory := ""
	if item.RegistryInstaller != nil {
		m.status = "Installing the approved registry package for " + item.Name + "."
		var err error
		temporaryDirectory, err = registryadapter.NewTemporaryDirectory()
		if err != nil {
			_ = m.session.Record(item, err, detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Missing})
			m.status = "Could not create temporary registry workspace: " + err.Error()
			return m.startNext()
		}
		commandSpec, err = registryadapter.PrepareCommand(commandSpec, temporaryDirectory)
		if err != nil {
			err = errors.Join(err, os.RemoveAll(temporaryDirectory))
			_ = m.session.Record(item, err, detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Missing})
			m.status = "Could not prepare the registry installer: " + err.Error()
			return m.startNext()
		}
	}
	command, err := commandSpec.ExecContext(m.ctx)
	if err != nil {
		if temporaryDirectory != "" {
			err = errors.Join(err, os.RemoveAll(temporaryDirectory))
		}
		_ = m.session.Record(item, err, detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Missing})
		m.status = "Could not start " + item.Name + ": " + err.Error()
		return m.startNext()
	}
	return tea.ExecProcess(command, func(err error) tea.Msg {
		if temporaryDirectory != "" {
			err = errors.Join(err, os.RemoveAll(temporaryDirectory))
		}
		return processFinishedMsg{toolID: item.ToolID, err: err}
	})
}

func (m *Model) cleanupPreparedArchives() error {
	var cleanupErrors []error
	for toolID, result := range m.preparedArchives {
		if result.Prepared != nil {
			if err := result.Prepared.Cleanup(); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("%s: %w", toolID, err))
			}
		}
		delete(m.preparedArchives, toolID)
	}
	return errors.Join(cleanupErrors...)
}

func joinCleanupNotice(existing, next string) string {
	if existing == "" {
		return next
	}
	return existing + "; " + next
}

func (m *Model) handleArtifactInstall(message artifactInstallMsg) (tea.Model, tea.Cmd) {
	if m.installCancel != nil {
		m.installCancel()
		m.installCancel = nil
	}
	if m.session == nil {
		return m, nil
	}
	actual := message.actual
	operationErr := message.installErr
	if message.verifyError != nil {
		actual = detection.Tool{ID: message.item.ToolID, Name: message.item.Name, State: detection.Unknown}
		operationErr = errors.Join(operationErr, fmt.Errorf("post-install verification failed: %w", message.verifyError))
		m.status = "Verification could not finish for " + message.item.Name + ": " + message.verifyError.Error()
	} else if message.installErr == nil {
		m.inventory = message.inventory
		m.status = "Verified " + message.item.Name + " as " + string(actual.State) + "."
	}
	details := installation.ExecutionDetails{
		Source:        message.result.SourceName,
		SourceURL:     message.result.SourceURL,
		InstallPath:   message.result.Path,
		SHA256:        message.result.SHA256,
		PathChanges:   message.item.PathChanges,
		CleanupNotice: message.result.CleanupNotice,
	}
	if message.pathErr != nil {
		details.PathError = message.pathErr.Error()
		m.status = "The tool installed, but PATH configuration needs attention: " + message.pathErr.Error()
	}
	if err := m.session.RecordWithDetails(message.item, operationErr, actual, details); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.activeItem = planning.Item{}
	return m, m.startNext()
}

func waitForDownloadProgress(progress <-chan artifacts.PrepareProgress) tea.Cmd {
	return func() tea.Msg {
		value, open := <-progress
		if !open {
			return downloadProgressDoneMsg{}
		}
		return downloadProgressMsg{toolID: value.ToolID, progress: value.Progress}
	}
}

func (m *Model) verifyCommand(processErr error) tea.Cmd {
	items := m.catalog
	machine := m.inventory.Platform
	item := m.activeItem
	ctx := m.ctx
	return func() tea.Msg {
		scanCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		inventory, err := detection.ScanWithSearchPaths(scanCtx, items, machine, searchDirectories(item.PathChanges))
		if err != nil {
			return verificationMsg{item: item, processErr: processErr, verifyError: err}
		}
		actual, ok := inventory.Tools[item.ToolID]
		if !ok {
			return verificationMsg{item: item, processErr: processErr, inventory: inventory, verifyError: fmt.Errorf("inventory did not return %s", item.ToolID)}
		}
		return verificationMsg{item: item, processErr: processErr, inventory: inventory, actual: actual}
	}
}

func (m *Model) handleVerification(message verificationMsg) (tea.Model, tea.Cmd) {
	if m.session == nil {
		return m, nil
	}
	actual := message.actual
	commandErr := message.processErr
	if message.verifyError != nil {
		actual = detection.Tool{ID: message.item.ToolID, Name: message.item.Name, State: detection.Unknown}
		if commandErr == nil {
			commandErr = fmt.Errorf("post-install verification failed: %w", message.verifyError)
		}
		m.status = "Verification could not finish for " + message.item.Name + ": " + message.verifyError.Error()
	} else {
		m.inventory = message.inventory
		m.status = "Verified " + message.item.Name + " as " + string(actual.State) + "."
	}
	var recordErr error
	if message.item.RegistryInstaller != nil {
		details := installation.ExecutionDetails{
			Source:      string(message.item.RegistryInstaller.Ecosystem) + " registry",
			SourceURL:   message.item.Source,
			InstallPath: message.item.InstallPath,
			PathChanges: message.item.PathChanges,
		}
		if actual.State == detection.Healthy {
			if pathErr := pathconfig.Apply(message.item.PathChanges); pathErr != nil {
				details.PathError = pathErr.Error()
			}
		}
		recordErr = m.session.RecordWithDetails(message.item, commandErr, actual, details)
	} else {
		recordErr = m.session.Record(message.item, commandErr, actual)
	}
	if err := recordErr; err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.activeItem = planning.Item{}
	return m, m.startNext()
}

func (m *Model) refreshInventory() tea.Cmd {
	items := m.catalog
	machine := m.inventory.Platform
	ctx := m.ctx
	m.status = "Refreshing the existing tool inventory."
	return func() tea.Msg {
		scanCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		inventory, err := detection.Scan(scanCtx, items, machine)
		return preflightMsg{inventory: inventory, err: err, approval: false}
	}
}

func (m *Model) View() tea.View {
	view := tea.NewView(m.render())
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m *Model) SessionReport() []installation.Result {
	if m.session == nil {
		return nil
	}
	return m.session.Report()
}
