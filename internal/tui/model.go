package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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
	ctx                  context.Context
	catalog              *catalog.Catalog
	inventory            detection.Inventory
	screen               Screen
	selected             map[string]bool
	selectedProfiles     map[string]bool
	preferredManager     tool.PackageManager
	sourceMode           string
	regionDetection      regional.Result
	archiveInstaller     *artifacts.ArchiveInstaller
	customSources        map[string]string
	registrySources      sources.RegistryURLs
	offlineArchives      map[string]string
	offlineBundlePath    string
	plan                 planning.Plan
	planReady            bool
	approval             bool
	preflighting         bool
	searching            bool
	environmentSearching bool
	pathMode             string
	pathInput            string
	help                 bool
	filter               string
	environmentFilter    string
	categoryFilter       string
	stateFilter          string
	status               string
	width                int
	height               int
	cursor               int
	session              *installation.Session
	activeItem           planning.Item
}

type preflightMsg struct {
	inventory detection.Inventory
	err       error
	approval  bool
}

type processFinishedMsg struct {
	toolID string
	err    error
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

func NewModel(ctx context.Context, items *catalog.Catalog, initial detection.Inventory, selected []string) *Model {
	selection := make(map[string]bool, len(selected))
	for _, id := range selected {
		selection[id] = true
	}
	return &Model{
		ctx:              ctx,
		catalog:          items,
		inventory:        initial,
		screen:           OverviewScreen,
		selected:         selection,
		selectedProfiles: make(map[string]bool),
		sourceMode:       "automatic",
		regionDetection:  regional.Detect(initial.Platform),
		archiveInstaller: artifacts.NewArchiveInstaller(),
		customSources:    make(map[string]string),
		offlineArchives:  make(map[string]string),
		stateFilter:      "all",
		status:           "Review the machine overview, then select tools from the catalog.",
		width:            80,
		height:           24,
	}
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

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case preflightMsg:
		return m.handlePreflight(msg)
	case processFinishedMsg:
		if m.activeItem.ToolID != msg.toolID {
			return m, nil
		}
		m.status = "Checking the installed tool and its version."
		return m, m.verifyCommand(msg.err)
	case verificationMsg:
		return m.handleVerification(msg)
	case artifactInstallMsg:
		return m.handleArtifactInstall(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	default:
		return m, nil
	}
}

func (m *Model) handleKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := message.String()
	if m.pathMode != "" {
		m.handlePathKey(message, key)
		return m, nil
	}
	if m.searching {
		m.handleSearchKey(message, key)
		return m, nil
	}
	if m.environmentSearching {
		m.handleEnvironmentSearchKey(message, key)
		return m, nil
	}
	if m.help {
		if key == "?" || key == "esc" || key == "enter" {
			m.help = false
		}
		return m, nil
	}
	if m.approval {
		switch key {
		case "y":
			m.approval = false
			m.preflighting = true
			m.status = "Rechecking selected tools before execution."
			return m, m.preflight()
		case "n", "esc":
			m.approval = false
			m.status = "Plan approval cancelled. No commands were run."
		}
		return m, nil
	}
	switch key {
	case "ctrl+c", "q":
		if m.session != nil && !m.session.Complete() {
			m.session.Cancel()
		}
		return m, tea.Quit
	case "?":
		m.help = true
		return m, nil
	case "1":
		m.screen = OverviewScreen
		m.cursor = 0
	case "2":
		m.screen = CatalogScreen
		m.cursor = 0
	case "3":
		m.screen = ProfilesScreen
		m.cursor = 0
	case "4":
		m.screen = EnvironmentScreen
		m.cursor = 0
	case "5":
		m.screen = PlanScreen
		m.cursor = 0
	case "6":
		m.screen = SourcesScreen
		m.cursor = 0
	case "7":
		m.screen = ProgressScreen
		m.cursor = 0
	case "8":
		m.screen = ResultsScreen
		m.cursor = 0
	case "tab", "right":
		m.screen = (m.screen + 1) % 8
		m.cursor = 0
	case "shift+tab", "left":
		if m.screen == OverviewScreen {
			m.screen = ResultsScreen
		} else {
			m.screen--
		}
		m.cursor = 0
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		m.cursor++
	case "f":
		if m.screen == CatalogScreen {
			m.searching = true
		} else if m.screen == EnvironmentScreen {
			m.environmentSearching = true
		}
	case "[":
		if m.screen == CatalogScreen {
			m.cycleCategory(-1)
			m.cursor = 0
		}
	case "]":
		if m.screen == CatalogScreen {
			m.cycleCategory(1)
			m.cursor = 0
		}
	case "s":
		if m.screen == CatalogScreen {
			m.cycleStateFilter()
			m.cursor = 0
		}
	case "a":
		if m.screen == CatalogScreen {
			m.categoryFilter = ""
			m.filter = ""
			m.stateFilter = "all"
			m.cursor = 0
		} else if m.screen == EnvironmentScreen {
			m.environmentFilter = ""
			m.cursor = 0
		} else if m.screen == SourcesScreen {
			m.sourceMode = "automatic"
			m.invalidatePlan("Automatic region-based source policy selected.")
		}
	case "space":
		m.toggleCurrent()
	case "enter":
		if m.screen == ProfilesScreen {
			m.applyProfiles()
		} else if m.screen == SourcesScreen {
			m.chooseCurrentManager()
		}
	case "p":
		if m.screen == PlanScreen {
			m.buildPlan()
		}
	case "i":
		if m.screen == PlanScreen && m.planReady && !m.plan.Blocked {
			if len(m.plan.InstallItems()) == 0 {
				m.status = "Every selected tool is already present. No installation is needed."
				return m, nil
			}
			m.approval = true
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

func (m *Model) handleSearchKey(message tea.KeyPressMsg, key string) {
	switch key {
	case "enter", "esc":
		m.searching = false
	case "backspace":
		if m.filter != "" {
			_, size := utf8.DecodeLastRuneInString(m.filter)
			m.filter = m.filter[:len(m.filter)-size]
		}
	case "space":
		m.filter += " "
	default:
		if text := message.Key().Text; text != "" {
			m.filter += text
		}
	}
	m.cursor = 0
}

func (m *Model) handleEnvironmentSearchKey(message tea.KeyPressMsg, key string) {
	switch key {
	case "enter", "esc":
		m.environmentSearching = false
	case "backspace":
		if m.environmentFilter != "" {
			_, size := utf8.DecodeLastRuneInString(m.environmentFilter)
			m.environmentFilter = m.environmentFilter[:len(m.environmentFilter)-size]
		}
	case "space":
		m.environmentFilter += " "
	default:
		if text := message.Key().Text; text != "" {
			m.environmentFilter += text
		}
	}
	m.cursor = 0
}

func (m *Model) toggleCurrent() {
	switch m.screen {
	case CatalogScreen:
		tools := m.filteredTools()
		if m.cursor >= 0 && m.cursor < len(tools) {
			id := tools[m.cursor].ID
			m.selected[id] = !m.selected[id]
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

func (m *Model) applyProfiles() {
	selection := make(map[string]bool)
	for _, profile := range m.catalog.Profiles() {
		if !m.selectedProfiles[profile.ID] {
			continue
		}
		for _, id := range profile.Tools {
			selection[id] = true
		}
	}
	if len(m.selectedProfiles) == 0 {
		m.status = "Select one or more profiles, then press Enter to combine them."
		return
	}
	m.selected = selection
	m.invalidatePlan("Profiles applied. Review and edit the resulting tool selection.")
	m.screen = CatalogScreen
	m.cursor = 0
}

func (m *Model) buildPlan() {
	selected := m.selectedIDs()
	plan, err := m.buildSelectedPlan(selected)
	if err != nil {
		m.plan = planning.Plan{}
		m.planReady = false
		m.status = err.Error()
		return
	}
	m.plan = plan
	m.planReady = true
	if plan.Blocked {
		m.status = "Resolve the blocking items below before any installation can run."
	} else {
		m.status = fmt.Sprintf("Plan ready: %d installation step(s), %d preserved tool(s). Review every source and command.", len(plan.InstallItems()), len(plan.Items)-len(plan.InstallItems()))
	}
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
	})
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

func (m *Model) handlePathKey(message tea.KeyPressMsg, key string) {
	switch key {
	case "enter":
		path := strings.TrimSpace(m.pathInput)
		if path == "" {
			m.status = "A file path is required."
			return
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
	case "esc":
		m.pathMode = ""
		m.pathInput = ""
		m.status = "Selection file operation cancelled."
	case "backspace":
		if m.pathInput != "" {
			_, size := utf8.DecodeLastRuneInString(m.pathInput)
			m.pathInput = m.pathInput[:len(m.pathInput)-size]
		}
	case "space":
		m.pathInput += " "
	default:
		if text := message.Key().Text; text != "" {
			m.pathInput += text
		}
	}
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
	ids := make([]string, 0, len(m.selected))
	for id, selected := range m.selected {
		if selected {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (m *Model) filteredTools() []tool.Manifest {
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
		count = len(m.catalog.Profiles())
	case EnvironmentScreen:
		count = len(m.filteredEnvironmentIDs())
	case PlanScreen:
		count = len(m.plan.Items) + len(m.plan.Issues)
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
			m.buildPlan()
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
	m.plan = updated
	m.planReady = true
	if updated.Blocked || planSignature(updated) != previousSignature {
		m.screen = PlanScreen
		if updated.Blocked {
			m.status = "The refreshed plan has blocking issues. Review them below; no command was run."
		} else {
			m.status = "The machine changed since review. Inspect the refreshed plan and approve it again."
		}
		return m, nil
	}
	session, err := installation.NewSession(updated)
	if err != nil {
		m.screen = PlanScreen
		m.status = "Could not start the installation session: " + err.Error()
		return m, nil
	}
	m.session = session
	m.screen = ProgressScreen
	m.status = "Starting the approved installation plan."
	return m, m.startNext()
}

func planSignature(plan planning.Plan) string {
	parts := make([]string, 0, len(plan.Items)+len(plan.Issues))
	for _, item := range plan.Items {
		parts = append(parts, strings.Join([]string{item.ToolID, string(item.Action), string(item.ExistingState), string(item.Manager), item.PackageID, item.Source, string(item.Scope), item.Command.Display(), item.Version, item.InstallPath, fmt.Sprint(item.ApproximateSize), fmt.Sprint(item.RegionalFallback), item.CustomSourceURL, item.OfflineArchivePath}, "|"))
		if item.Artifact != nil {
			parts = append(parts, strings.Join([]string{item.Artifact.URL, item.Artifact.RegionalURL, item.Artifact.SHA256, string(item.Artifact.Format), item.Artifact.ArchiveRoot}, "|"))
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
	if m.session == nil {
		return nil
	}
	item, exists := m.session.Next()
	if !exists {
		m.screen = ResultsScreen
		m.status = "All planned operations have completed. Review verification results and diagnostics."
		return nil
	}
	m.activeItem = item
	if item.Artifact != nil {
		m.status = "Downloading and installing the approved verified archive for " + item.Name + "."
		installer := m.archiveInstaller
		if installer == nil {
			installer = artifacts.NewArchiveInstaller()
		}
		ctx := m.ctx
		catalogItems := m.catalog
		machine := m.inventory.Platform
		return func() tea.Msg {
			home, err := os.UserHomeDir()
			if err != nil {
				return artifactInstallMsg{item: item, installErr: err, actual: detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Missing}}
			}
			policy := sources.OfficialOnly
			if item.RegionalFallback {
				policy = sources.OfficialFirst
			}
			result, installErr := installer.InstallWithOptions(ctx, item.ToolID, home, *item.Artifact, artifacts.InstallOptions{
				Policy:                policy,
				AllowRegionalFallback: item.RegionalFallback,
				CustomSourceURL:       item.CustomSourceURL,
				OfflineArchivePath:    item.OfflineArchivePath,
			})
			if installErr != nil {
				return artifactInstallMsg{item: item, result: result, installErr: installErr, actual: detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Missing}}
			}
			pathErr := pathconfig.Apply(item.PathChanges)
			scanCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			inventory, verifyError := detection.Scan(scanCtx, catalogItems, machine)
			actual, ok := inventory.Tools[item.ToolID]
			if verifyError == nil && !ok {
				verifyError = fmt.Errorf("inventory did not return %s", item.ToolID)
			}
			if verifyError != nil {
				actual = detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Unknown}
			}
			return artifactInstallMsg{item: item, result: result, pathErr: pathErr, inventory: inventory, actual: actual, verifyError: verifyError}
		}
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

func (m *Model) handleArtifactInstall(message artifactInstallMsg) (tea.Model, tea.Cmd) {
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

func (m *Model) verifyCommand(processErr error) tea.Cmd {
	items := m.catalog
	machine := m.inventory.Platform
	item := m.activeItem
	ctx := m.ctx
	return func() tea.Msg {
		scanCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		inventory, err := detection.Scan(scanCtx, items, machine)
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

func (m *Model) View() tea.View { return tea.NewView(m.render()) }

func (m *Model) SessionReport() []installation.Result {
	if m.session == nil {
		return nil
	}
	return m.session.Report()
}
