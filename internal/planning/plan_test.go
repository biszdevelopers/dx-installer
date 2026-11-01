package planning

import (
	"encoding/json"
	"strings"
	"testing"

	"developers-club-dx/internal/adapters/packagemanager"
	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/platform"
)

func TestPlanAddsDependenciesInInstallOrderAndPreservesExistingTools(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	current := windowsInventory(
		[]packagemanager.Detected{{Manager: tool.Winget, Path: `C:\Windows\winget.exe`}, {Manager: tool.Scoop, Path: `C:\scoop\shims\scoop.exe`}},
		detection.Tool{ID: "git", Name: "Git", State: detection.Healthy},
		detection.Tool{ID: "github-cli", Name: "GitHub CLI", State: detection.Missing},
	)
	plan, err := Build(items, current, []string{"github-cli"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked {
		t.Fatalf("plan should be executable: %#v", plan.Issues)
	}
	if len(plan.Items) != 2 || plan.Items[0].ToolID != "git" || plan.Items[0].Action != Preserve || plan.Items[1].ToolID != "github-cli" || plan.Items[1].Action != Install {
		t.Fatalf("dependency and preservation order is wrong: %#v", plan.Items)
	}
	if plan.Items[1].Manager != tool.Winget || plan.Items[1].PackageID != "GitHub.cli" {
		t.Fatalf("unexpected selected installer: %#v", plan.Items[1])
	}
}

func TestPlanPrefersTheRequestedDetectedManager(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	current := windowsInventory(
		[]packagemanager.Detected{{Manager: tool.Winget}, {Manager: tool.Scoop}},
		detection.Tool{ID: "git", Name: "Git", State: detection.Missing},
	)
	plan, err := BuildWithOptions(items, current, []string{"git"}, Options{PreferredManager: tool.Scoop})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked || len(plan.Items) != 1 || plan.Items[0].Manager != tool.Scoop {
		t.Fatalf("plan did not honor the detected manager preference: %#v", plan)
	}
}

func TestPlanDisclosesUnresolvedPackageTransactionInJSON(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	current := windowsInventory([]packagemanager.Detected{{Manager: tool.Winget}}, detection.Tool{ID: "git", Name: "Git", State: detection.Missing})
	plan, err := Build(items, current, []string{"git"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.TransactionDisclosure != TransactionDisclosure {
		t.Fatalf("transaction disclosure = %q, want %q", plan.TransactionDisclosure, TransactionDisclosure)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"transaction_disclosure"`) || !strings.Contains(string(encoded), TransactionDisclosure) {
		t.Fatalf("JSON plan omitted transaction uncertainty: %s", encoded)
	}
}

func TestPreferredManagerOrderMovesOnlyDetectedPreferenceToFront(t *testing.T) {
	managers := []tool.PackageManager{tool.Winget, tool.Scoop, tool.Chocolatey}
	ordered := PreferredManagerOrder(managers, tool.Scoop)
	want := []tool.PackageManager{tool.Scoop, tool.Winget, tool.Chocolatey}
	if len(ordered) != len(want) {
		t.Fatalf("ordered managers = %#v, want %#v", ordered, want)
	}
	for index := range want {
		if ordered[index] != want[index] {
			t.Fatalf("ordered managers = %#v, want %#v", ordered, want)
		}
	}
	if managers[0] != tool.Winget {
		t.Fatalf("original manager order was mutated: %#v", managers)
	}
	missing := PreferredManagerOrder(managers, tool.Homebrew)
	if len(missing) != len(managers) || missing[0] != tool.Winget {
		t.Fatalf("undetected preference changed manager order: %#v", missing)
	}
}

func TestPlanBlocksMissingManagerAndPreservesUnverifiedExistingTool(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	current := windowsInventory(nil,
		detection.Tool{ID: "git", Name: "Git", State: detection.Unknown, Packages: []detection.PackageOwnership{{Manager: tool.Winget, ID: "Git.Git", Version: "2.47.1", Source: "winget"}}},
	)
	plan, err := Build(items, current, []string{"git"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked || len(plan.Items) != 1 || plan.Items[0].Action != Preserve {
		t.Fatalf("unknown existing tool should be preserved: %#v", plan)
	}
	if len(plan.Items[0].ExistingPackages) != 1 || plan.Items[0].ExistingPackages[0].ID != "Git.Git" {
		t.Fatalf("plan did not retain package ownership details: %#v", plan.Items[0].ExistingPackages)
	}

	current.Tools["git"] = detection.Tool{ID: "git", Name: "Git", State: detection.Missing}
	plan, err = Build(items, current, []string{"git"})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Blocked {
		t.Fatal("plan without a detected package manager should be blocked")
	}
}

func TestPlanRejectsBrokenInstallationWithoutReplacingIt(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	current := windowsInventory([]packagemanager.Detected{{Manager: tool.Winget}},
		detection.Tool{ID: "git", Name: "Git", State: detection.Broken},
	)
	plan, err := Build(items, current, []string{"git"})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Blocked || len(plan.InstallItems()) != 0 || len(plan.Items) != 1 || plan.Items[0].Action != Preserve {
		t.Fatalf("broken installation was not safely blocked: %#v", plan)
	}
}

func windowsInventory(managers []packagemanager.Detected, tools ...detection.Tool) detection.Inventory {
	inventory := detection.Inventory{
		Platform: platform.Info{OS: tool.Windows, Architecture: tool.AMD64},
		Managers: managers,
		Tools:    make(map[string]detection.Tool, len(tools)),
	}
	for _, entry := range tools {
		inventory.Tools[entry.ID] = entry
	}
	if _, exists := inventory.Tools["git"]; !exists {
		inventory.Tools["git"] = detection.Tool{ID: "git", Name: "Git", State: detection.Missing}
	}
	if _, exists := inventory.Tools["github-cli"]; !exists {
		inventory.Tools["github-cli"] = detection.Tool{ID: "github-cli", Name: "GitHub CLI", State: detection.Missing}
	}
	return inventory
}
