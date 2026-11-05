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
	for _, expected := range []string{"conflicting signals", "zh_CN", "UTC", "Mainland China", "global", "official-only", "official-first"} {
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

func TestEveryScreenFitsSmallAndResizedTerminals(t *testing.T) {
	t.Setenv("DX_ASCII", "0")
	model := screenGoldenModel(t)
	for _, size := range []struct{ width, height int }{{24, 12}, {40, 16}, {80, 24}} {
		model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		for screen := OverviewScreen; screen <= ResultsScreen; screen++ {
			model.screen = screen
			lines := strings.Split(ansi.Strip(model.render()), "\n")
			if len(lines) > size.height {
				t.Errorf("%s rendered %d rows for %dx%d terminal", screenNames[screen], len(lines), size.width, size.height)
			}
			for index, line := range lines {
				if ansi.StringWidth(line) > size.width {
					t.Errorf("%s line %d is %d columns wide for %dx%d terminal: %q", screenNames[screen], index+1, ansi.StringWidth(line), size.width, size.height, line)
				}
			}
		}
	}
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
		"Supported platforms: macos/amd64, arm64",
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
		"go": {SourceName: "publisher mirror", BytesReceived: 1_000_000, TotalBytes: 2_000_000},
	}

	output := strings.Join(model.renderProgress(20), "\n")
	if !strings.Contains(output, "Prepared source: publisher mirror") || !strings.Contains(output, "Downloaded: 1.0 MB of 2.0 MB (50%)") {
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
