package planning

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestPlanUsesExplicitGoModuleRegistryWithAnExactCatalogVersion(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "LocalAppData"))
	t.Setenv("PATH", "")
	current := windowsInventory(
		[]packagemanager.Detected{{Manager: tool.Winget, Path: `C:\Windows\winget.exe`}},
		detection.Tool{ID: "go", Name: "Go", State: detection.Missing},
		detection.Tool{ID: "govulncheck", Name: "govulncheck", State: detection.Missing},
	)
	plan, err := BuildWithOptions(items, current, []string{"govulncheck"}, Options{
		RegistrySources: map[tool.RegistryEcosystem]string{tool.GoModuleRegistry: "https://proxy.example/goproxy"},
	})
	if err != nil || plan.Blocked || len(plan.Items) != 2 {
		t.Fatalf("registry installation plan = %#v, %v", plan, err)
	}
	item := plan.Items[1]
	if item.ToolID != "govulncheck" || item.RegistryInstaller == nil || item.Version != "v1.9.0" || item.Source != "https://proxy.example/goproxy" {
		t.Fatalf("registry selection omitted its pinned identity or source: %#v", item)
	}
	if item.Command.Executable != "go" || strings.Join(item.Command.Arguments, " ") != "install golang.org/x/vuln/cmd/govulncheck@v1.9.0" {
		t.Fatalf("registry operation is not a typed, exact-version Go command: %#v", item.Command)
	}
	if item.Command.Environment["GOPROXY"] != item.Source || item.Command.Environment["GOSUMDB"] != "sum.golang.org" {
		t.Fatalf("registry command did not preserve Go checksum database verification: %#v", item.Command.Environment)
	}
	if item.InstallPath == "" || len(item.PathChanges) != 1 || item.PathChanges[0].Directory != filepath.Join(item.InstallPath, "bin") {
		t.Fatalf("registry install target or PATH changes are missing: %#v", item)
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

func TestPlanUsesVerifiedArchiveWhenNoPackageManagerIsAvailable(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	localAppData := filepath.Join(home, "LocalAppData")
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("PATH", "")
	current := windowsInventory(nil, detection.Tool{ID: "go", Name: "Go", State: detection.Missing})
	plan, err := Build(items, current, []string{"go"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked || len(plan.Items) != 1 {
		t.Fatalf("archive plan is blocked or has unexpected items: %#v", plan)
	}
	item := plan.Items[0]
	if item.Artifact == nil || item.Version != "1.27.3" || item.ApproximateSize != 75_000_000 || item.Source != "https://go.dev/dl/go1.27.3.windows-amd64.zip" {
		t.Fatalf("plan omitted verified artifact details: %#v", item)
	}
	wantTarget := filepath.Join(localAppData, "Programs", "go", "1.27.3")
	if item.InstallPath != wantTarget || len(item.PathChanges) != 1 || item.PathChanges[0].Directory != filepath.Join(wantTarget, "bin") {
		t.Fatalf("plan target/PATH changes = %q / %#v, want %q / bin", item.InstallPath, item.PathChanges, wantTarget)
	}
	if item.Command.Display() != "" || item.Manager != "" || plan.TransactionDisclosure != "" {
		t.Fatalf("direct archive install was represented as a package-manager transaction: %#v", item)
	}
}

func TestPlanDisclosesZephyrSDKSizeAndVendorSetupBeforeApproval(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	current := windowsInventory(nil,
		detection.Tool{ID: "cmake", Name: "CMake", State: detection.Healthy},
		detection.Tool{ID: "zephyr-sdk", Name: "Zephyr SDK", State: detection.Missing},
	)
	plan, err := Build(items, current, []string{"zephyr-sdk"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked || len(plan.Items) != 2 {
		t.Fatalf("Zephyr SDK plan is blocked or has unexpected items: %#v", plan)
	}
	var item Item
	for _, planned := range plan.Items {
		if planned.ToolID == "zephyr-sdk" {
			item = planned
			break
		}
	}
	if item.ToolID != "zephyr-sdk" || item.Artifact == nil || item.SizeEstimateNote == "" || !strings.Contains(item.SizeEstimateNote, "1.87 GB") {
		t.Fatalf("plan omitted the SDK download estimate: %#v", item)
	}
	if item.InstallationNote == "" || !strings.Contains(item.InstallationNote, "setup script") || item.InstallPath != filepath.Join(home, "zephyr-sdk-1.0.1") || len(item.PathChanges) != 0 {
		t.Fatalf("plan omitted setup behavior or declared an unnecessary PATH change: %#v", item)
	}
}

func TestPlanPrefersAnExistingPackageManagerOverTheStandaloneArchive(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	current := windowsInventory([]packagemanager.Detected{{Manager: tool.Winget}}, detection.Tool{ID: "go", Name: "Go", State: detection.Missing})
	plan, err := Build(items, current, []string{"go"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked || len(plan.Items) != 1 || plan.Items[0].Artifact != nil || plan.Items[0].Manager != tool.Winget {
		t.Fatalf("available package manager was not preferred: %#v", plan)
	}
}

func TestPlanUsesCatalogPinnedCustomAndOfflineArchivesBeforePackageManagers(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "LocalAppData"))
	t.Setenv("PATH", "")
	current := windowsInventory([]packagemanager.Detected{{Manager: tool.Winget}}, detection.Tool{ID: "go", Name: "Go", State: detection.Missing})

	custom, err := BuildWithOptions(items, current, []string{"go"}, Options{CustomSources: map[string]string{"go": "https://mirror.example/go.zip"}})
	if err != nil || custom.Blocked || len(custom.Items) != 1 {
		t.Fatalf("catalog-pinned custom source plan = %#v, %v", custom, err)
	}
	if custom.Items[0].CustomSourceURL != "https://mirror.example/go.zip" || custom.Items[0].Artifact == nil || custom.Items[0].RegionalFallback || custom.Items[0].Manager != "" {
		t.Fatalf("custom source did not take precedence over package managers or fallback: %#v", custom.Items[0])
	}

	archive := filepath.Join(t.TempDir(), "go.zip")
	if err := os.WriteFile(archive, []byte("offline archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	offlinePlan, err := BuildWithOptions(items, current, []string{"go"}, Options{OfflineArchives: map[string]string{"go": archive}})
	if err != nil || offlinePlan.Blocked || len(offlinePlan.Items) != 1 {
		t.Fatalf("offline archive plan = %#v, %v", offlinePlan, err)
	}
	if offlinePlan.Items[0].OfflineArchivePath != archive || offlinePlan.Items[0].Artifact == nil || offlinePlan.Items[0].Manager != "" {
		t.Fatalf("offline archive did not bypass package managers: %#v", offlinePlan.Items[0])
	}
}

func TestPlanBlocksConflictingOrInvalidExplicitSources(t *testing.T) {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "LocalAppData"))
	current := windowsInventory([]packagemanager.Detected{{Manager: tool.Winget}}, detection.Tool{ID: "go", Name: "Go", State: detection.Missing})
	archive := filepath.Join(t.TempDir(), "go.zip")
	if err := os.WriteFile(archive, []byte("offline archive"), 0o600); err != nil {
		t.Fatal(err)
	}

	conflicting, err := BuildWithOptions(items, current, []string{"go"}, Options{
		CustomSources:   map[string]string{"go": "https://mirror.example/go.zip"},
		OfflineArchives: map[string]string{"go": archive},
	})
	if err != nil || !conflicting.Blocked {
		t.Fatalf("conflicting source plan = %#v, %v", conflicting, err)
	}
	invalid, err := BuildWithOptions(items, current, []string{"go"}, Options{CustomSources: map[string]string{"go": "http://mirror.example/go.zip"}})
	if err != nil || !invalid.Blocked {
		t.Fatalf("unencrypted custom source plan = %#v, %v", invalid, err)
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
