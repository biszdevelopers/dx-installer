package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"developers-club-dx/internal/adapters/packagemanager"
	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/installation"
	"developers-club-dx/internal/planning"
	"developers-club-dx/internal/platform"
	"developers-club-dx/internal/regional"
	"developers-club-dx/internal/sources"
	"github.com/charmbracelet/x/ansi"
)

func TestApprovalDoesNotStartSessionUntilFreshInventoryMessage(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	inventory := detection.Inventory{
		Platform: platform.Info{OS: tool.Windows, Architecture: tool.AMD64},
		Managers: []packagemanager.Detected{{Manager: tool.Winget, Path: `C:\Windows\winget.exe`}},
		Tools: map[string]detection.Tool{
			"git": {ID: "git", Name: "Git", State: detection.Missing},
		},
	}
	model := NewModel(context.Background(), items, inventory, []string{"git"})
	model.screen = PlanScreen
	updateAndRunCommand(model, keyMessage('p'))
	if !model.planReady || model.plan.Blocked || len(model.plan.InstallItems()) != 1 {
		t.Fatalf("expected a reviewable install plan, got %#v", model.plan)
	}
	model.Update(keyMessage('i'))
	if !model.approval || model.session != nil {
		t.Fatal("opening the approval prompt started execution or failed to show approval")
	}
	model.Update(keyMessage('n'))
	if model.approval || model.session != nil || !strings.Contains(model.status, "cancelled") {
		t.Fatalf("declined plan did not remain unexecuted: approval=%v session=%v status=%q", model.approval, model.session, model.status)
	}
}

func TestLineFallbackRequiresExplicitApproval(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	inventory := detection.Inventory{
		Platform: platform.Info{OS: tool.Windows, Architecture: tool.AMD64},
		Managers: []packagemanager.Detected{{Manager: tool.Winget, Path: `C:\Windows\winget.exe`}},
		Tools: map[string]detection.Tool{
			"git": {ID: "git", Name: "Git", State: detection.Missing},
		},
	}
	model := NewModel(context.Background(), items, inventory, []string{"git"})
	var output bytes.Buffer
	if err := RunLineFallback(context.Background(), model, strings.NewReader("no\n"), &output); err != nil {
		t.Fatal(err)
	}
	if model.session != nil || !strings.Contains(output.String(), "Plan cancelled. No commands were run.") || !strings.Contains(output.String(), planning.TransactionDisclosure) {
		t.Fatalf("fallback did not honor explicit decline: session=%v output=%q", model.session, output.String())
	}
}

func TestPlanAndApprovalDiscloseUnresolvedPackageTransaction(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.Windows, Architecture: tool.AMD64},
		Managers: []packagemanager.Detected{{Manager: tool.Winget, Path: `C:\Windows\winget.exe`}},
		Tools:    map[string]detection.Tool{"git": {ID: "git", Name: "Git", State: detection.Missing}},
	}, []string{"git"})
	model.screen = PlanScreen
	updateAndRunCommand(model, keyMessage('p'))
	disclosure := model.plan.TransactionDisclosure
	if disclosure == "" {
		t.Fatal("install plan does not contain the package transaction disclosure")
	}
	if rendered := ansi.Strip(strings.Join(model.renderPlan(80), "\n")); !strings.Contains(rendered, disclosure) {
		t.Fatalf("plan omitted package transaction disclosure: %s", rendered)
	}
	model.Update(keyMessage('i'))
	if rendered := ansi.Strip(model.renderApproval()); !strings.Contains(rendered, disclosure) {
		t.Fatalf("approval omitted package transaction disclosure: %s", rendered)
	}
}

func TestAutomaticPolicyBlocksConflictsUntilUserChoosesRegion(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.Windows, Architecture: tool.AMD64},
		Managers: []packagemanager.Detected{{Manager: tool.Winget}},
		Tools:    map[string]detection.Tool{"git": {ID: "git", Name: "Git", State: detection.Missing}},
	}, []string{"git"})
	model.regionDetection = regional.Result{Region: regional.Conflict}
	plan, err := model.buildSelectedPlan(model.selectedIDs())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Blocked || len(plan.InstallItems()) != 0 {
		t.Fatalf("conflicting automatic signals did not block execution: %#v", plan)
	}
	model.screen = SourcesScreen
	model.Update(keyMessage('g'))
	plan, err = model.buildSelectedPlan(model.selectedIDs())
	if err != nil || plan.Blocked || len(plan.InstallItems()) != 1 {
		t.Fatalf("global override did not resolve the source conflict: %#v, %v", plan, err)
	}
	model.Update(keyMessage('c'))
	if model.sourceMode != "mainland-china" {
		t.Fatalf("manual Mainland China preference = %q", model.sourceMode)
	}
}

func TestSourceScreenShowsSignalsAndManualChoices(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{Platform: platform.Info{OS: tool.Linux, Architecture: tool.AMD64}}, nil)
	model.regionDetection = regional.Result{Region: regional.Conflict, Signals: []regional.Signal{{Source: "locale", Value: "zh_CN", Region: regional.MainlandChina}, {Source: "time-zone", Value: "UTC", Region: regional.Global}}}
	text := ansi.Strip(strings.Join(model.renderSources(30), "\n"))
	for _, expected := range []string{"conflicting signals", "zh_CN", "UTC", "Mainland China", "global", "Publisher only", "Publisher first"} {
		if !strings.Contains(text, expected) {
			t.Errorf("source screen omitted %q: %s", expected, text)
		}
	}
}

func TestSourceScreenConfiguresAndPlansCustomAndOfflineArchives(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "LocalAppData"))
	t.Setenv("PATH", "")
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.Windows, Architecture: tool.AMD64},
		Tools:    map[string]detection.Tool{"go": {ID: "go", Name: "Go", State: detection.Missing}},
	}, []string{"go"})
	model.screen = SourcesScreen
	model.regionDetection = regional.Result{Region: regional.Conflict}

	model.Update(keyMessage('u'))
	if model.pathMode != "custom-source" {
		t.Fatalf("custom source key opened mode %q", model.pathMode)
	}
	model.pathInput = "go=https://mirror.example/go.zip"
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	plan, err := model.buildSelectedPlan(model.selectedIDs())
	if err != nil || plan.Blocked || len(plan.Items) != 1 || plan.Items[0].CustomSourceURL != "https://mirror.example/go.zip" {
		t.Fatalf("custom source was not used in the reviewed plan: %#v, %v", plan, err)
	}

	archive := filepath.Join(t.TempDir(), "go.zip")
	if err := os.WriteFile(archive, []byte("offline archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	model.Update(keyMessage('l'))
	model.pathInput = "go=" + archive
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	plan, err = model.buildSelectedPlan(model.selectedIDs())
	if err != nil || plan.Blocked || len(plan.Items) != 1 || plan.Items[0].OfflineArchivePath != archive || plan.Items[0].CustomSourceURL != "" {
		t.Fatalf("offline source did not replace the custom source: %#v, %v", plan, err)
	}
	if rendered := strings.Join(model.renderSources(30), "\n"); !strings.Contains(rendered, "local archive "+archive) {
		t.Fatalf("source screen omitted the active offline archive: %s", rendered)
	}
}

func TestSourceScreenConfiguresRegistryOverrideAndPlansExactGoTool(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "LocalAppData"))
	t.Setenv("PATH", "")
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.Windows, Architecture: tool.AMD64},
		Managers: []packagemanager.Detected{{Manager: tool.Winget, Path: `C:\Windows\winget.exe`}},
		Tools: map[string]detection.Tool{
			"go":          {ID: "go", Name: "Go", State: detection.Missing},
			"govulncheck": {ID: "govulncheck", Name: "govulncheck", State: detection.Missing},
		},
	}, []string{"govulncheck"})
	model.screen = SourcesScreen
	model.regionDetection = regional.Result{Region: regional.Conflict}
	model.Update(keyMessage('v'))
	if model.pathMode != "registry" {
		t.Fatalf("registry override key opened mode %q", model.pathMode)
	}
	model.pathInput = "go-modules=https://proxy.example/goproxy"
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model.screen = PlanScreen
	updateAndRunCommand(model, keyMessage('p'))
	if !model.planReady || model.plan.Blocked || len(model.plan.Items) != 2 {
		t.Fatalf("registry source did not produce an executable plan: %#v", model.plan)
	}
	item := model.plan.Items[1]
	if item.RegistryInstaller == nil || item.Source != "https://proxy.example/goproxy" || item.Version != "v1.9.0" {
		t.Fatalf("plan omitted the explicit registry, exact version, or typed installer: %#v", item)
	}
	if rendered := ansi.Strip(strings.Join(model.renderSources(40), "\n")); !strings.Contains(rendered, "Registry override go-modules: https://proxy.example/goproxy") {
		t.Fatalf("source screen omitted the configured registry: %s", rendered)
	}
}

func TestLineFallbackDisplaysPackageOwnedUnknownTool(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.Windows, Architecture: tool.AMD64},
		Tools: map[string]detection.Tool{
			"git": {ID: "git", Name: "Git", State: detection.Unknown, Packages: []detection.PackageOwnership{{Manager: tool.Winget, ID: "Git.Git", Version: "2.47.1", Source: "winget"}}},
		},
	}, []string{"git"})
	var output bytes.Buffer
	if err := RunLineFallback(context.Background(), model, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "package: winget / Git.Git 2.47.1") || !strings.Contains(output.String(), "PRESERVE Git (unknown)") {
		t.Fatalf("line fallback omitted package ownership: %s", output.String())
	}
}

func TestEnvironmentShowsPackageOwnershipWithoutClaimingExecutableHealth(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{
		Tools: map[string]detection.Tool{
			"cmake": {
				ID:    "cmake",
				Name:  "CMake",
				State: detection.Unknown,
				Packages: []detection.PackageOwnership{{
					Manager: tool.APT,
					ID:      "cmake",
					Version: "3.30.0-1",
					Source:  "configured-apt-repositories",
				}},
			},
		},
	}, nil)
	output := strings.Join(model.renderEnvironment(10), "\n")
	if !strings.Contains(output, "Package: apt / cmake 3.30.0-1") || !strings.Contains(output, "executable was not found on PATH") {
		t.Fatalf("environment screen omitted package ownership or PATH limitation: %s", output)
	}
}

func TestEnvironmentSearchMatchesVersionsAndPathsIndependently(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{
		Tools: map[string]detection.Tool{
			"git": {
				ID: "git", Name: "Git", Category: "vcs.client", State: detection.Healthy,
				Instances: []detection.Instance{{Executable: "git", Path: "/opt/git/bin/git", Version: "git version 2.48.1", State: detection.Healthy}},
			},
			"cmake": {
				ID: "cmake", Name: "CMake", Category: "build.system", State: detection.Healthy,
				Instances: []detection.Instance{{Executable: "cmake", Path: "/usr/bin/cmake", Version: "cmake version 3.29.0", State: detection.Healthy}},
			},
		},
	}, nil)
	model.screen = EnvironmentScreen
	model.filter = "no-catalog-match"

	model.Update(keyMessage('f'))
	if !model.environmentSearching {
		t.Fatal("f should open search on the Environment screen")
	}
	for _, character := range "3.29" {
		model.Update(keyMessage(character))
	}
	if got := model.filteredEnvironmentIDs(); len(got) != 1 || got[0] != "cmake" {
		t.Fatalf("version search returned %v, want [cmake]", got)
	}
	if model.filter != "no-catalog-match" {
		t.Fatalf("environment search changed the catalog query: %q", model.filter)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.environmentSearching {
		t.Fatal("Enter should finish environment search input")
	}
	model.Update(keyMessage('a'))
	if got := model.filteredEnvironmentIDs(); len(got) != 2 {
		t.Fatalf("clearing environment search returned %v, want all records", got)
	}

	model.environmentFilter = "/opt/git/bin"
	if got := model.filteredEnvironmentIDs(); len(got) != 1 || got[0] != "git" {
		t.Fatalf("path search returned %v, want [git]", got)
	}
}

func TestEnvironmentScreenGolden(t *testing.T) {
	t.Setenv("DX_ASCII", "0")
	model := NewModel(context.Background(), nil, detection.Inventory{
		Platform: platform.Info{OS: tool.Linux, Architecture: tool.AMD64, Distribution: "ubuntu", Shell: "bash"},
		Managers: []packagemanager.Detected{{Manager: tool.APT, Path: "/usr/bin/apt"}},
		Tools: map[string]detection.Tool{
			"git": {
				ID: "git", Name: "Git", Category: "vcs.client", State: detection.Healthy,
				Instances: []detection.Instance{{Executable: "git", Path: "/usr/bin/git", Version: "git version 2.48.1", State: detection.Healthy}},
			},
		},
	}, nil)
	model.screen = EnvironmentScreen
	model.width = 72
	model.height = 18
	got := normalizedRender(model.render())
	path := filepath.Join("testdata", "environment-screen.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("environment screen differs from golden snapshot\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestScreensGolden(t *testing.T) {
	t.Setenv("DX_ASCII", "0")
	model := screenGoldenModel(t)
	var rendered strings.Builder
	for screen := OverviewScreen; screen <= ResultsScreen; screen++ {
		model.screen = screen
		fmt.Fprintf(&rendered, "=== %s ===\n%s\n", screenNames[screen], normalizedRender(model.render()))
	}
	path := filepath.Join("testdata", "screens.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(rendered.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.String() != string(want) {
		t.Fatalf("TUI screen snapshots differ\n--- got ---\n%s\n--- want ---\n%s", rendered.String(), want)
	}
}

func TestWorkspaceLayoutsGolden(t *testing.T) {
	t.Setenv("DX_ASCII", "0")
	model := screenGoldenModel(t)
	var rendered strings.Builder
	for _, sample := range []struct {
		name   string
		width  int
		height int
		setup  func()
	}{
		{name: "Wide dashboard 120x32", width: 120, height: 32, setup: func() { model.screen = OverviewScreen }},
		{name: "Wide tool catalog 120x32", width: 120, height: 32, setup: func() { model.screen = CatalogScreen; model.cursor = 0 }},
		{name: "Wide plan review 120x32", width: 120, height: 32, setup: func() { model.screen = PlanScreen; model.cursor = 0 }},
		{name: "Approval dialog 120x32", width: 120, height: 32, setup: func() {
			model.screen = PlanScreen
			model.approval = true
			model.help = false
			model.pathMode = ""
			model.commandPalette = false
			model.pageScroll = 0
		}},
		{name: "Command palette 120x32", width: 120, height: 32, setup: func() {
			model.approval = false
			model.help = false
			model.pathMode = ""
			model.commandPalette = true
			model.updateCommandList()
			model.pageScroll = 0
		}},
		{name: "Source entry dialog 120x32", width: 120, height: 32, setup: func() {
			model.commandPalette = false
			model.screen = SourcesScreen
			model.pathMode = "registry"
			model.pathInput = "npm=https://registry.npmjs.org/"
			model.input.SetValue(model.pathInput)
			model.pageScroll = 0
		}},
		{name: "Help dialog 120x32", width: 120, height: 32, setup: func() { model.pathMode = ""; model.help = true; model.pageScroll = 0 }},
		{name: "Compact dashboard 40x12", width: 40, height: 12, setup: func() {
			model.help = false
			model.pathMode = ""
			model.commandPalette = false
			model.approval = false
			model.screen = OverviewScreen
			model.pageScroll = 0
		}},
	} {
		model.Update(tea.WindowSizeMsg{Width: sample.width, Height: sample.height})
		model.focusedTarget = ""
		sample.setup()
		fmt.Fprintf(&rendered, "=== %s ===\n%s\n", sample.name, strings.TrimRight(normalizedRender(model.render()), "\n"))
	}
	path := filepath.Join("testdata", "workspace-layouts.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(rendered.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.String() != string(want) {
		t.Fatalf("workspace layout snapshots differ\n--- got ---\n%s\n--- want ---\n%s", rendered.String(), want)
	}
}

func TestEveryScreenFitsSmallAndResizedTerminals(t *testing.T) {
	t.Setenv("DX_ASCII", "0")
	model := screenGoldenModel(t)
	for _, size := range []struct{ width, height int }{{120, 32}, {96, 28}, {80, 24}, {60, 18}, {40, 12}} {
		model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		for screen := OverviewScreen; screen <= ResultsScreen; screen++ {
			model.screen = screen
			model.pageScroll = 0
			model.focusedTarget = ""
			lines := strings.Split(ansi.Strip(model.render()), "\n")
			if len(lines) > size.height {
				t.Errorf("%s rendered %d rows for %dx%d terminal", screenNames[screen], len(lines), size.width, size.height)
			}
			for index, line := range lines {
				if ansi.StringWidth(line) > size.width {
					t.Errorf("%s line %d is %d columns wide for %dx%d terminal: %q", screenNames[screen], index+1, ansi.StringWidth(line), size.width, size.height, line)
				}
			}
			for _, target := range model.hitTargets {
				if target.x < 0 || target.y < 0 || target.width <= 0 || target.height <= 0 || target.x+target.width > size.width || target.y+target.height > size.height {
					t.Errorf("%s has out-of-bounds target %+v for %dx%d terminal", screenNames[screen], target, size.width, size.height)
				}
			}
		}
	}
}

func TestMouseNavigationSelectionAndGuidedReview(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.Linux, Architecture: tool.AMD64},
	}, nil)
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	clickAction(t, model, "nav-tools", 0)
	if model.screen != CatalogScreen {
		t.Fatalf("Tools navigation opened %v, want CatalogScreen", model.screen)
	}
	clickAction(t, model, "toggle-selection", 0)
	firstID := model.filteredTools()[0].ID
	if !model.selected[firstID] {
		t.Fatalf("clicking the first catalog row did not select %q", firstID)
	}
	command := clickAction(t, model, "review-selection", 0)
	if model.screen != PlanScreen || !model.planning && !model.planReady {
		t.Fatalf("Review my selection did not produce a reviewable plan: screen=%v planning=%v planReady=%v command=%v", model.screen, model.planning, model.planReady, command != nil)
	}
}

func TestUnsupportedToolCanBeReplacedByClickableNativeAlternative(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.Linux, Architecture: tool.AMD64},
		Managers: []packagemanager.Detected{{Manager: tool.APT, Path: "/usr/bin/apt"}},
		Tools: map[string]detection.Tool{
			"msvc-build-tools": {ID: "msvc-build-tools", Name: "MSVC Build Tools", State: detection.Unsupported},
			"clang":            {ID: "clang", Name: "Clang", State: detection.Missing},
		},
	}, nil)
	model.screen = CatalogScreen
	model.filter = "msvc-build-tools"
	model.sourceMode = "official-only"
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 32})

	clickAction(t, model, "select-platform-alternative", 0)
	if !model.selected["msvc-build-tools"] || model.selectedAlternatives["msvc-build-tools"] != "clang" {
		t.Fatalf("click did not choose Clang as the MSVC alternative: selected=%v alternatives=%v", model.selected, model.selectedAlternatives)
	}
	selected := model.selectedIDs()
	if len(selected) != 1 || selected[0] != "clang" {
		t.Fatalf("installer selection should resolve to Clang: %v", selected)
	}
	plan, err := model.buildSelectedPlan(selected)
	if err != nil || plan.Blocked || len(plan.Items) != 1 || plan.Items[0].ToolID != "clang" {
		t.Fatalf("native alternative did not produce an installable plan: plan=%#v err=%v", plan, err)
	}
	if !strings.Contains(plan.Items[0].Name, "native alternative for MSVC Build Tools") || len(plan.Issues) != 1 || !strings.Contains(plan.Issues[0].Message, "does not provide the MSVC ABI") {
		t.Fatalf("plan did not disclose the alternative and its limits: %#v", plan)
	}

	output := ansi.Strip(model.render())
	if !strings.Contains(output, "Use Clang") {
		t.Fatalf("catalog did not identify the native alternative:\n%s", output)
	}
}

func TestMouseAdvancedSourceChoiceAndProfileActions(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.Linux, Architecture: tool.AMD64},
		Managers: []packagemanager.Detected{{Manager: tool.APT, Path: "/usr/bin/apt"}},
	}, []string{"git"})
	model.planReady = true
	model.plan = planning.Plan{Items: []planning.Item{{ToolID: "git", Action: planning.Install}}}
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	clickAction(t, model, "advanced", 0)
	clickAction(t, model, "nav-sources", 0)
	if model.screen != SourcesScreen {
		t.Fatalf("Advanced Sources opened %v", model.screen)
	}
	clickAction(t, model, "source-official-only", 0)
	if model.sourceMode != "official-only" || model.planReady {
		t.Fatalf("source choice was not applied or did not invalidate the plan: source=%q planReady=%v", model.sourceMode, model.planReady)
	}

	model.navigate(ProfilesScreen)
	clickAction(t, model, "profile-toggle", 0)
	profiles := items.Profiles()
	if len(profiles) == 0 || !model.selectedProfiles[profiles[0].ID] {
		t.Fatal("clicking a profile did not select it")
	}
	clickAction(t, model, "apply-profiles", 0)
	if model.screen != CatalogScreen || len(model.selectedIDs()) == 0 {
		t.Fatalf("Apply profiles did not return an editable tool selection: screen=%v selected=%v", model.screen, model.selectedIDs())
	}
}

func TestMouseCatalogFiltersAndSearchControls(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.Linux, Architecture: tool.AMD64},
	}, nil)
	model.width = 100
	model.height = 50
	model.screen = CatalogScreen
	clickAction(t, model, "category-next", 0)
	if model.categoryFilter == "" {
		t.Fatal("category button did not change the category")
	}
	clickAction(t, model, "category-previous", 0)
	if model.categoryFilter != "" {
		t.Fatalf("previous-category arrow did not return to All categories: %q", model.categoryFilter)
	}
	clickAction(t, model, "state-filter", 0)
	if model.stateFilter == "all" {
		t.Fatal("installation-state button did not change the filter")
	}
	clickAction(t, model, "clear-filters", 0)
	if model.filter != "" || model.categoryFilter != "" || model.stateFilter != "all" {
		t.Fatalf("Clear filters left an active filter: search=%q category=%q state=%q", model.filter, model.categoryFilter, model.stateFilter)
	}
	clickAction(t, model, "search", 0)
	if !model.searching {
		t.Fatal("Search button did not focus catalog search")
	}
}

func TestBlockedPlanRowUpdatesInspector(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{}, nil)
	model.width, model.height, model.screen = 120, 32, PlanScreen
	model.planReady = true
	model.plan = planning.Plan{Blocked: true, Issues: []planning.Issue{{ToolID: "missing-tool", Severity: planning.Blocking, Message: "No supported installer is available on this platform."}}}
	clickAction(t, model, "inspect-plan", 0)
	if output := ansi.Strip(model.render()); !strings.Contains(output, "No supported installer is") || !strings.Contains(output, "available on this platform.") || !strings.Contains(output, "Blocks installation") {
		t.Fatalf("selecting a blocked item did not show its explanation in the inspector:\n%s", output)
	}
}

func TestMouseSourceDialogInputAndConfirmation(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{}, nil)
	model.width, model.height, model.screen = 120, 32, SourcesScreen
	clickAction(t, model, "registry-source", 0)
	if model.pathMode != "registry" {
		t.Fatalf("registry source action opened dialog %q", model.pathMode)
	}
	clickAction(t, model, "input-focus", 0)
	for _, character := range "npm=https://registry.npmjs.org/" {
		model.Update(keyMessage(character))
	}
	clickAction(t, model, "confirm-path", 0)
	if model.pathMode != "" || len(model.registrySources) != 1 {
		t.Fatalf("clickable source dialog did not save the registry entry: mode=%q sources=%v status=%q", model.pathMode, model.registrySources, model.status)
	}
}

func TestMouseApprovalRequiresFinalConfirmation(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{}, nil)
	model.width = 100
	model.height = 50
	model.screen = PlanScreen
	model.planReady = true
	model.plan = planning.Plan{Items: []planning.Item{{ToolID: "go", Name: "Go", Action: planning.Install}}}
	model.approval = true
	command := clickAction(t, model, "approve", 0)
	if !model.preflighting || model.approval || command == nil {
		t.Fatalf("Install now did not start the fresh preflight: preflighting=%v approval=%v command=%v", model.preflighting, model.approval, command != nil)
	}

	model.approval = true
	clickAction(t, model, "cancel-approval", 0)
	if model.approval || !strings.Contains(model.status, "cancelled") {
		t.Fatalf("cancel click did not leave the approval screen safely: approval=%v status=%q", model.approval, model.status)
	}
}

func TestMouseHelpCanOpenAndCloseDuringInputPrompt(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{}, nil)
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model.Update(keyMessage('?'))
	if !model.help {
		t.Fatal("Help keyboard shortcut did not open the help dialog")
	}
	clickAction(t, model, "close-help", 0)
	if model.help {
		t.Fatal("Close help click did not close the help dialog")
	}

	model.pathMode = "import"
	model.render()
	model.Update(keyMessage('?'))
	if !model.help || model.pathMode != "import" {
		t.Fatalf("Help did not open over the input dialog: help=%v pathMode=%q", model.help, model.pathMode)
	}
	clickAction(t, model, "close-help", 0)
	if model.help || model.pathMode != "import" {
		t.Fatalf("closing Help lost the input prompt: help=%v pathMode=%q", model.help, model.pathMode)
	}
}

func TestMouseWheelAndPageKeysScrollLongDetails(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{
		Platform: platform.Info{OS: tool.Linux, Architecture: tool.AMD64},
	}, nil)
	model.height = 12
	model.render()
	main := model.mainViewportRect
	model.Update(tea.MouseWheelMsg{X: main.x + 1, Y: main.y + 1, Button: tea.MouseWheelDown})
	if model.pageScroll == 0 {
		t.Fatal("mouse wheel did not scroll the welcome content")
	}
	before := model.pageScroll
	model.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if model.pageScroll <= before {
		t.Fatal("Page Down did not move the viewport")
	}

	model.screen = PlanScreen
	model.pageScroll = 0
	model.planReady = true
	model.plan = planning.Plan{}
	for index := 0; index < 10; index++ {
		model.plan.Items = append(model.plan.Items, planning.Item{
			ToolID: fmt.Sprintf("tool-%02d", index), Name: fmt.Sprintf("Tool %02d", index),
			Action: planning.Install, Manager: tool.APT, PackageID: fmt.Sprintf("package-%02d", index),
		})
	}
	model.width = 80
	model.height = 18
	initialPlan := ansi.Strip(model.render())
	sawLastPlanItem := false
	for index := 0; index < 12; index++ {
		main := model.mainViewportRect
		model.Update(tea.MouseWheelMsg{X: main.x + 1, Y: main.y + 1, Button: tea.MouseWheelDown})
		if strings.Contains(ansi.Strip(model.render()), "Tool 09") {
			sawLastPlanItem = true
		}
	}
	if !strings.Contains(initialPlan, "Tool 00") || !sawLastPlanItem {
		t.Fatalf("mouse wheel did not expose the full installation plan (scroll=%d):\n%s", model.pageScroll, ansi.Strip(model.render()))
	}

	model.screen = EnvironmentScreen
	model.advancedExpanded = true
	model.pageScroll = 0
	model.inventory = detection.Inventory{
		Platform: platform.Info{OS: tool.Linux, Architecture: tool.AMD64},
		Tools: map[string]detection.Tool{
			"git": {ID: "git", Name: "Git", State: detection.Healthy, Instances: []detection.Instance{{Executable: "git", Path: "/usr/bin/git", Version: "2.0", State: detection.Healthy, Diagnostic: strings.Repeat("extra diagnostic detail ", 20)}}},
		},
	}
	model.regionDetection = regional.Detect(model.inventory.Platform)
	initial := ansi.Strip(model.render())
	main = model.mainViewportRect
	model.Update(tea.MouseWheelMsg{X: main.x + 1, Y: main.y + 1, Button: tea.MouseWheelDown})
	updated := ansi.Strip(model.render())
	for index := 0; index < 10 && !strings.Contains(updated, "extra diagnostic"); index++ {
		main := model.mainViewportRect
		model.Update(tea.MouseWheelMsg{X: main.x + 1, Y: main.y + 1, Button: tea.MouseWheelDown})
		updated = ansi.Strip(model.render())
	}
	if model.pageScroll == 0 || !strings.Contains(updated, "extra diagnostic") || initial == updated {
		t.Fatalf("mouse wheel did not expose clipped environment details: scroll=%d\n%s", model.pageScroll, updated)
	}
}

func TestMouseWheelScrollsWideInspectorIndependently(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{
		Platform: platform.Info{OS: tool.Linux, Architecture: tool.AMD64},
		Tools: map[string]detection.Tool{
			"git": {ID: "git", Name: "Git", State: detection.Healthy, Instances: []detection.Instance{{Executable: "git", Path: "/usr/bin/git", Version: "2.0", State: detection.Healthy, Diagnostic: "first segment " + strings.Repeat("diagnostic-tail ", 160)}}},
		},
	}, nil)
	model.width, model.height, model.screen = 120, 32, EnvironmentScreen
	model.render()
	inspector := model.detailViewportRect
	if inspector.width <= 0 || inspector.height <= 0 {
		t.Fatal("wide workspace did not lay out an independent inspector")
	}
	model.Update(tea.MouseWheelMsg{X: inspector.x + 1, Y: inspector.y + 1, Button: tea.MouseWheelDown})
	if model.inspectorScroll == 0 || model.pageScroll != 0 {
		t.Fatalf("wheel over inspector changed main scroll: inspector=%d main=%d", model.inspectorScroll, model.pageScroll)
	}
}

func TestMouseTargetsAreRecomputedAfterResize(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{}, nil)
	if mouseMode := model.View().MouseMode; mouseMode != tea.MouseModeCellMotion {
		t.Fatalf("Bubble Tea View mouse mode = %v, want cell-motion clicks and wheel events", mouseMode)
	}
	model.render()
	if len(model.hitTargets) == 0 {
		t.Fatal("initial render registered no mouse targets")
	}
	model.Update(tea.WindowSizeMsg{Width: 24, Height: 12})
	if len(model.hitTargets) != 0 {
		t.Fatal("resize left stale mouse targets active")
	}
	clickAction(t, model, "commands", 0)
	clickAction(t, model, "palette-command", 1)
	if model.screen != CatalogScreen {
		t.Fatalf("compact command navigation opened %v, want catalog", model.screen)
	}
}

func TestKeyboardFocusTraversalAndNavigation(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{}, nil)
	model.width, model.height = 120, 32
	model.render()
	initialFocus := model.focusedTarget
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if model.focusedTarget == initialFocus {
		t.Fatal("Tab did not move focus to the next visible control")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if model.focusedTarget != initialFocus {
		t.Fatalf("Shift+Tab restored focus to %q, want %q", model.focusedTarget, initialFocus)
	}
	model.focusedTarget = actionID("nav-tools", 0)
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.screen != CatalogScreen {
		t.Fatalf("Enter activated focused navigation to screen %v, want CatalogScreen", model.screen)
	}
}

func clickAction(t *testing.T, model *Model, action string, index int) tea.Cmd {
	t.Helper()
	model.render()
	for _, target := range model.hitTargets {
		if target.action != action || target.index != index {
			continue
		}
		_, command := model.Update(tea.MouseClickMsg{X: target.x, Y: target.y, Button: tea.MouseLeft})
		return command
	}
	t.Fatalf("rendered screen %v has no visible target %q at index %d", model.screen, action, index)
	return nil
}

func TestCatalogDetailsShowCurrentPlatformDependencies(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.Windows, Architecture: tool.AMD64},
		Tools:    map[string]detection.Tool{"zephyr-sdk": {ID: "zephyr-sdk", Name: "Zephyr SDK", State: detection.Missing}},
	}, nil)
	for index, item := range items.Tools() {
		if item.ID == "zephyr-sdk" {
			model.cursor = index
			break
		}
	}
	output := strings.Join(model.renderCatalog(80), "\n")
	if !strings.Contains(output, "Dependencies: cmake, seven-zip") {
		t.Fatalf("Zephyr details omitted its Windows-specific archive dependency:\n%s", output)
	}
}

func TestUnicodeToolDetailsFitTerminalWidth(t *testing.T) {
	t.Setenv("DX_ASCII", "0")
	model := NewModel(context.Background(), nil, detection.Inventory{
		Platform: platform.Info{OS: tool.Linux, Architecture: tool.AMD64},
		Tools: map[string]detection.Tool{
			"tool": {ID: "工具", Name: "工具鏈 🚀", State: detection.Healthy, Instances: []detection.Instance{{Executable: "tool", Path: "/opt/工具/bin", Version: "版本 1.2", State: detection.Healthy}}},
		},
	}, nil)
	model.screen = EnvironmentScreen
	for _, width := range []int{24, 40, 80} {
		model.width = width
		model.height = 12
		for index, line := range strings.Split(ansi.Strip(model.render()), "\n") {
			if ansi.StringWidth(line) > width {
				t.Errorf("Unicode screen line %d is %d columns wide for a %d-column terminal: %q", index+1, ansi.StringWidth(line), width, line)
			}
		}
	}
}

func TestASCIIFallbackSanitizesRenderedAndLineOutput(t *testing.T) {
	t.Setenv("DX_ASCII", "1")
	if got, want := asciiText("… → — – 工具"), "... -> -- - ??"; got != want {
		t.Fatalf("ASCII conversion = %q, want %q", got, want)
	}
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.Linux, Architecture: tool.AMD64},
		Warnings: []string{"warning from 工具 🚀"},
		Tools:    map[string]detection.Tool{"git": {ID: "git", Name: "Git", State: detection.Healthy}},
	}, []string{"git"})
	for _, rendered := range []string{model.render(), lineFallbackOutput(t, model)} {
		for _, character := range rendered {
			if character > 0x7f {
				t.Fatalf("ASCII fallback emitted non-ASCII character %q in %q", character, rendered)
			}
		}
	}
}

func lineFallbackOutput(t *testing.T, model *Model) string {
	t.Helper()
	var output bytes.Buffer
	if err := RunLineFallback(context.Background(), model, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func screenGoldenModel(t *testing.T) *Model {
	t.Helper()
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	inventory := detection.Inventory{
		Platform: platform.Info{OS: tool.Windows, Architecture: tool.AMD64},
		Managers: []packagemanager.Detected{{Manager: tool.Winget, Path: `C:\Windows\winget.exe`}},
		Tools: map[string]detection.Tool{
			"git": {ID: "git", Name: "Git", State: detection.Healthy, Instances: []detection.Instance{{Executable: "git", Path: `C:\Program Files\Git\cmd\git.exe`, Version: "git version 2.48.1", State: detection.Healthy}}},
			"go":  {ID: "go", Name: "Go", State: detection.Missing},
		},
	}
	model := NewModel(context.Background(), items, inventory, []string{"go"})
	model.regionDetection = regional.Result{Region: regional.Global}
	model.width = 80
	model.height = 24
	if command := model.buildPlan(); command != nil {
		message := command()
		model.Update(message)
	}
	session, err := installation.NewSession(model.plan)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := session.Next()
	if !ok {
		t.Fatal("golden fixture did not produce an install operation")
	}
	if err := session.Record(item, nil, detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Healthy, Instances: []detection.Instance{{Executable: "go", Path: `C:\Go\bin\go.exe`, Version: "go1.27.3", State: detection.Healthy}}}); err != nil {
		t.Fatal(err)
	}
	model.session = session
	return model
}

func TestRenderAdaptsToTerminalResize(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{
		Platform: platform.Info{OS: tool.Linux, Architecture: tool.AMD64},
		Tools: map[string]detection.Tool{
			"git": {ID: "git", Name: "Git", State: detection.Healthy, Instances: []detection.Instance{{Executable: "git", Path: "/usr/local/bin/git", Version: "git version 2.48.1", State: detection.Healthy}}},
		},
	}, nil)
	model.screen = EnvironmentScreen
	model.Update(tea.WindowSizeMsg{Width: 32, Height: 12})
	lines := strings.Split(ansi.Strip(model.render()), "\n")
	if len(lines) > 12 {
		t.Fatalf("rendered %d rows for a 12-row terminal:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for index, line := range lines {
		if utf8.RuneCountInString(line) > 32 {
			t.Errorf("rendered line %d is wider than the 32-column terminal: %q", index+1, line)
		}
	}
}

func FuzzEnvironmentSearchInput(f *testing.F) {
	f.Add("C:\\tools\\git 2.48.1", uint8(4))
	f.Add("工具版本", uint8(2))
	f.Fuzz(func(t *testing.T, input string, deleteCount uint8) {
		model := NewModel(context.Background(), nil, detection.Inventory{
			Tools: map[string]detection.Tool{"git": {ID: "git", Name: "Git", State: detection.Healthy}},
		}, nil)
		model.environmentSearching = true
		for _, character := range input {
			if unicode.IsControl(character) {
				continue
			}
			key := "text"
			if character == ' ' {
				key = "space"
			}
			model.handleEnvironmentSearchKey(tea.KeyPressMsg{Text: string(character), Code: character}, key)
		}
		for index := uint8(0); index < deleteCount && index < 100; index++ {
			model.handleEnvironmentSearchKey(tea.KeyPressMsg{}, "backspace")
		}
		if !utf8.ValidString(model.environmentFilter) {
			t.Fatalf("search input is not valid UTF-8 after editing: %q", model.environmentFilter)
		}
		_ = model.filteredEnvironmentIDs()
	})
}

func normalizedRender(rendered string) string {
	lines := strings.Split(ansi.Strip(rendered), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

func TestCatalogDetailsShowCompatibilityDependenciesConflictsAndVerification(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.MacOS, Architecture: tool.ARM64},
		Managers: []packagemanager.Detected{{Manager: tool.Homebrew, Path: "/opt/homebrew/bin/brew"}},
	}, nil)
	for index, item := range items.Tools() {
		if item.ID == "flutter" {
			model.cursor = index
			break
		}
	}
	output := strings.Join(model.renderCatalog(80), "\n")
	for _, expected := range []string{
		"Flutter SDK details",
		"Dependencies: git",
		"Conflicts: dart",
		"Supported platforms: linux/amd64; macos/amd64, arm64; windows/amd64",
		"Verification: flutter --version",
		"Proposed source: homebrew / flutter (homebrew-cask)",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("catalog details omitted %q:\n%s", expected, output)
		}
	}
}

func TestCatalogDetailsHonorPreferredManager(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), items, detection.Inventory{
		Platform: platform.Info{OS: tool.MacOS, Architecture: tool.ARM64},
		Managers: []packagemanager.Detected{
			{Manager: tool.Homebrew, Path: "/opt/homebrew/bin/brew"},
			{Manager: tool.MacPorts, Path: "/opt/local/bin/port"},
		},
	}, nil)
	model.SetPreferredManager(tool.MacPorts)
	for index, item := range items.Tools() {
		if item.ID == "git" {
			model.cursor = index
			break
		}
	}
	output := strings.Join(model.renderCatalog(80), "\n")
	if !strings.Contains(output, "Proposed source: macports / git (macports-ports)") {
		t.Fatalf("catalog details ignored the selected manager preference:\n%s", output)
	}
}

func TestPlanSignatureIncludesPreservedPackageOwnership(t *testing.T) {
	base := planning.Plan{Items: []planning.Item{{
		ToolID:        "git",
		Action:        planning.Preserve,
		ExistingState: detection.Unknown,
		ExistingPackages: []detection.PackageOwnership{{
			Manager: tool.APT,
			ID:      "git",
			Version: "2.47.0",
		}},
	}}}
	changed := base
	changed.Items = append([]planning.Item(nil), base.Items...)
	changed.Items[0].ExistingPackages = append([]detection.PackageOwnership(nil), base.Items[0].ExistingPackages...)
	changed.Items[0].ExistingPackages[0].Version = "2.48.0"
	if planSignature(base) == planSignature(changed) {
		t.Fatal("package ownership changes should invalidate the approval signature")
	}
}

func TestPlanSignatureIncludesArchiveSetupAndLimits(t *testing.T) {
	base := planning.Plan{Items: []planning.Item{{
		ToolID:           "zephyr-sdk",
		Action:           planning.Install,
		InstallationNote: "run bundled setup",
		Artifact:         &tool.Artifact{URL: "https://downloads.example/sdk.7z", SHA256: strings.Repeat("a", 64), MaxDownloadBytes: 2 << 30},
	}}}
	changed := base
	changed.Items = append([]planning.Item(nil), base.Items...)
	artifact := *base.Items[0].Artifact
	artifact.MaxExpandedBytes = 16 << 30
	changed.Items[0].Artifact = &artifact
	if planSignature(base) == planSignature(changed) {
		t.Fatal("archive extraction limits must invalidate the approval signature")
	}
}

func TestPlanSignatureIncludesPackageKind(t *testing.T) {
	base := planning.Plan{Items: []planning.Item{{ToolID: "adb", Action: planning.Install, Manager: tool.Homebrew, PackageID: "android-platform-tools", InstallerKind: "formula"}}}
	changed := base
	changed.Items = append([]planning.Item(nil), base.Items...)
	changed.Items[0].InstallerKind = "cask"
	if planSignature(base) == planSignature(changed) {
		t.Fatal("changing a Homebrew formula to a cask should invalidate the plan approval")
	}
}

func TestProgressScreenShowsKnownDownloadProgress(t *testing.T) {
	plan := planning.Plan{Items: []planning.Item{{ToolID: "go", Name: "Go", Action: planning.Install}}}
	session, err := installation.NewSession(plan)
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), nil, detection.Inventory{}, nil)
	model.plan = plan
	model.session = session
	model.activeItem = planning.Item{ToolID: "go", Name: "Go", Artifact: &tool.Artifact{}}
	model.downloadProgress = map[string]sources.DownloadProgress{
		"go": {SourceName: "publisher mirror", BytesReceived: 1_000_000, TotalBytes: 2_000_000, Phase: "verifying SHA-256 and signature"},
	}

	output := strings.Join(model.renderProgress(20), "\n")
	if !strings.Contains(output, "Prepared source: publisher mirror") || !strings.Contains(output, "Downloaded: 1.0 MB of 2.0 MB (50%)") || !strings.Contains(output, "Verification: verifying SHA-256 and signature") {
		t.Fatalf("progress screen omitted known download progress:\n%s", output)
	}
}

func TestQuitCancelsActiveArchiveInstall(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{}, nil)
	cancelled := false
	model.installCancel = func() { cancelled = true }

	updated, command := model.Update(keyMessage('q'))
	if !cancelled || model.installCancel != nil || command == nil {
		t.Fatalf("quitting did not cancel and clear the active archive install: cancelled=%v installCancel=%v command=%v", cancelled, model.installCancel != nil, command != nil)
	}
	if updated != model {
		t.Fatal("quit replaced the TUI model")
	}
}

func TestDiagnosticReportRequiresExplicitExportAndDoesNotOverwrite(t *testing.T) {
	model := NewModel(context.Background(), nil, detection.Inventory{
		Platform: platform.Info{OS: tool.Windows, Architecture: tool.AMD64},
		Warnings: []string{`failed under C:\Users\alice\AppData\Local`},
	}, []string{"git"})
	model.screen = ResultsScreen
	model.Update(keyMessage('d'))
	if model.pathMode != "diagnostic" {
		t.Fatal("diagnostic export should require the explicit Results action")
	}

	path := filepath.Join(t.TempDir(), "dx-report.json")
	model.pathInput = path
	model.handlePathKey(tea.KeyPressMsg{Code: tea.KeyEnter}, "enter")
	if model.pathMode != "" || !strings.Contains(model.status, "saved") {
		t.Fatalf("report export did not complete: mode=%q status=%q", model.pathMode, model.status)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "%USERPROFILE%") || strings.Contains(string(data), "alice") {
		t.Fatalf("saved report was not redacted: %s", data)
	}

	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := model.writeDiagnostic(path); err == nil {
		t.Fatal("expected existing path to reject replacement")
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("existing report was replaced: %q", data)
	}
}

func keyMessage(value rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: string(value), Code: value}
}

func updateAndRunCommand(model *Model, message tea.Msg) {
	_, command := model.Update(message)
	if command != nil {
		model.Update(command())
	}
}
