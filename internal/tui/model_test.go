package tui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"developers-club-dx/internal/adapters/packagemanager"
	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/planning"
	"developers-club-dx/internal/platform"
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
	model.Update(keyMessage('p'))
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
	if model.session != nil || !strings.Contains(output.String(), "Plan cancelled. No commands were run.") {
		t.Fatalf("fallback did not honor explicit decline: session=%v output=%q", model.session, output.String())
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
