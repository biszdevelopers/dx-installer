package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"developers-club-dx/internal/adapters/packagemanager"
	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/display"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/installation"
	"developers-club-dx/internal/offline"
	"developers-club-dx/internal/planning"
	"developers-club-dx/internal/platform"
	"developers-club-dx/internal/regional"
	"developers-club-dx/internal/security"
	"developers-club-dx/internal/sources"
	"developers-club-dx/internal/temporary"
	"developers-club-dx/internal/tui"
)

type stringValues []string

func (values *stringValues) String() string { return strings.Join(*values, ",") }

func (values *stringValues) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dx-bootstrap:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	items, err := catalog.LoadEmbedded()
	if err != nil {
		return fmt.Errorf("load tool catalog: %w", err)
	}
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			printUsage(os.Stdout)
			return nil
		case "catalog":
			return listCatalog(items, args[1:])
		case "inventory":
			return showInventory(ctx, items, args[1:])
		case "plan":
			return showPlan(ctx, items, args[1:])
		case "verify-release":
			return verifyRelease(args[1:])
		case "install", "tui":
			return launchInteractive(ctx, items, args[1:])
		default:
			return fmt.Errorf("unknown command %q; use --help for usage", args[0])
		}
	}
	return launchInteractive(ctx, items, nil)
}

func listCatalog(items *catalog.Catalog, args []string) error {
	flags := flag.NewFlagSet("catalog", flag.ContinueOnError)
	jsonOutput := flags.Bool("json", false, "write catalog as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	tools := items.Tools()
	if *jsonOutput {
		return writeJSON(os.Stdout, tools)
	}
	fmt.Fprintf(os.Stdout, "%-18s %-28s %-24s %s\n", "ID", "NAME", "CATEGORY", "VERSION CHANNEL")
	for _, item := range tools {
		fmt.Fprintf(os.Stdout, "%-18s %-28s %-24s %s\n", item.ID, item.Name, item.Category, item.Version.Channel)
	}
	fmt.Fprintf(os.Stdout, "\n%d embedded tools  |  %d profiles\n", len(tools), len(items.Profiles()))
	return nil
}

func showInventory(ctx context.Context, items *catalog.Catalog, args []string) error {
	flags := flag.NewFlagSet("inventory", flag.ContinueOnError)
	jsonOutput := flags.Bool("json", false, "write inventory as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	inventory, err := detection.Scan(ctx, items, platform.Detect())
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(os.Stdout, inventory)
	}
	fmt.Fprintf(os.Stdout, "%s / %s", inventory.Platform.OS, inventory.Platform.Architecture)
	if inventory.Platform.Distribution != "" {
		fmt.Fprintf(os.Stdout, " / %s", inventory.Platform.Distribution)
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "Package managers: %s\n\n", managerNames(inventory))
	for _, id := range inventory.ToolIDs() {
		item := inventory.Tools[id]
		fmt.Fprintf(os.Stdout, "%-18s %-28s %s\n", item.ID, item.Name, item.State)
		for _, instance := range item.Instances {
			fmt.Fprintf(os.Stdout, "  %s  %s\n", instance.Version, instance.Path)
			fmt.Fprintf(os.Stdout, "  architecture: %s\n", display.ArchitectureVerification(instance))
			if instance.ArchitectureDiagnostic != "" {
				fmt.Fprintf(os.Stdout, "  architecture diagnostic: %s\n", instance.ArchitectureDiagnostic)
			}
			if instance.Diagnostic != "" {
				fmt.Fprintf(os.Stdout, "  diagnostic: %s\n", instance.Diagnostic)
			}
		}
		for _, installed := range item.Packages {
			fmt.Fprintf(os.Stdout, "  package: %s\n", display.PackageOwnership(string(installed.Manager), installed.ID, installed.Version, installed.Source))
		}
	}
	for _, warning := range inventory.Warnings {
		fmt.Fprintln(os.Stdout, "warning:", warning)
	}
	return nil
}

func showPlan(ctx context.Context, items *catalog.Catalog, args []string) error {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	toolsFlag := flags.String("tools", "", "comma-separated tool IDs to include")
	managerFlag := flags.String("manager", "", "prefer a detected package manager")
	sourcePolicyFlag := flags.String("source-policy", string(regional.Automatic), "automatic, official-only, official-first, mainland-china, or global")
	var customSourceFlags, offlineArchiveFlags, registryFlags stringValues
	flags.Var(&customSourceFlags, "custom-source", "catalog-pinned custom HTTPS source as tool-id=https://URL (repeatable)")
	flags.Var(&offlineArchiveFlags, "offline-archive", "catalog-pinned local archive as tool-id=absolute-path (repeatable)")
	flags.Var(&registryFlags, "registry", "HTTPS registry override as ecosystem=https://URL (repeatable)")
	offlineBundleFlag := flags.String("offline-bundle", "", "signed platform-specific offline bundle directory")
	jsonOutput := flags.Bool("json", false, "write the plan as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	selected := parseIDs(*toolsFlag)
	if len(selected) == 0 {
		return errors.New("plan requires --tools with one or more catalog IDs")
	}
	machine := platform.Detect()
	managerPreference, err := detectedManagerPreference(packagemanager.Detect(machine.OS, machine.Distribution), *managerFlag)
	if err != nil {
		return err
	}
	inventory, err := detection.Scan(ctx, items, machine)
	if err != nil {
		return err
	}
	customSources, err := parseSourceAssignments(customSourceFlags, "custom source")
	if err != nil {
		return err
	}
	offlineArchives, err := parseSourceAssignments(offlineArchiveFlags, "offline archive")
	if err != nil {
		return err
	}
	registrySources, err := sources.ParseRegistryAssignments(registryFlags)
	if err != nil {
		return err
	}
	offlineArchives, err = addBundleArchives(*offlineBundleFlag, items, inventory.Platform, customSources, offlineArchives)
	if err != nil {
		return err
	}
	allowRegionalFallback, err := sourceFallbackForSelection(inventory.Platform, *sourcePolicyFlag, items, inventory, selected, customSources, offlineArchives, registrySources)
	if err != nil {
		return err
	}
	plan, err := planning.BuildWithOptions(items, inventory, selected, planning.Options{
		PreferredManager:      managerPreference,
		AllowRegionalFallback: allowRegionalFallback,
		CustomSources:         customSources,
		OfflineArchives:       offlineArchives,
		RegistrySources:       registrySources,
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		if err := writeJSON(os.Stdout, plan); err != nil {
			return err
		}
	} else {
		printPlan(os.Stdout, plan)
	}
	if plan.Blocked {
		return errors.New("plan contains blocking issues; no commands were run")
	}
	return nil
}

func launchInteractive(ctx context.Context, items *catalog.Catalog, args []string) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	toolsFlag := flags.String("tools", "", "preselect comma-separated tool IDs")
	managerFlag := flags.String("manager", "", "prefer a detected package manager")
	sourcePolicyFlag := flags.String("source-policy", string(regional.Automatic), "automatic, official-only, official-first, mainland-china, or global")
	var customSourceFlags, offlineArchiveFlags, registryFlags stringValues
	flags.Var(&customSourceFlags, "custom-source", "catalog-pinned custom HTTPS source as tool-id=https://URL (repeatable)")
	flags.Var(&offlineArchiveFlags, "offline-archive", "catalog-pinned local archive as tool-id=absolute-path (repeatable)")
	flags.Var(&registryFlags, "registry", "HTTPS registry override as ecosystem=https://URL (repeatable)")
	offlineBundleFlag := flags.String("offline-bundle", "", "signed platform-specific offline bundle directory")
	approveFlag := flags.Bool("yes", false, "approve and execute the preselected plan in line mode")
	if err := flags.Parse(args); err != nil {
		return err
	}
	selected := parseIDs(*toolsFlag)
	if *approveFlag && len(selected) == 0 {
		return errors.New("--yes requires --tools with one or more catalog IDs")
	}
	for _, id := range selected {
		if _, exists := items.Tool(id); !exists {
			return fmt.Errorf("unknown tool %q", id)
		}
	}
	if !*approveFlag && (!isTerminal(os.Stdin) || !isTerminal(os.Stdout)) {
		return errors.New("interactive approval requires a terminal; catalog, inventory, and plan commands are available without one")
	}
	machine := platform.Detect()
	preferredManager, err := detectedManagerPreference(packagemanager.Detect(machine.OS, machine.Distribution), *managerFlag)
	if err != nil {
		return err
	}
	workspace, err := temporary.NewWorkspace()
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := workspace.Close(); cleanupErr != nil {
			fmt.Fprintln(os.Stderr, "temporary cleanup failed:", cleanupErr)
		} else {
			fmt.Fprintln(os.Stdout, "Temporary bootstrap workspace removed.")
		}
	}()

	inventory, err := detection.Scan(ctx, items, machine)
	if err != nil {
		return err
	}
	customSources, err := parseSourceAssignments(customSourceFlags, "custom source")
	if err != nil {
		return err
	}
	offlineArchives, err := parseSourceAssignments(offlineArchiveFlags, "offline archive")
	if err != nil {
		return err
	}
	registrySources, err := sources.ParseRegistryAssignments(registryFlags)
	if err != nil {
		return err
	}
	offlineArchives, err = addBundleArchives(*offlineBundleFlag, items, inventory.Platform, customSources, offlineArchives)
	if err != nil {
		return err
	}
	model := tui.NewModel(ctx, items, inventory, selected)
	model.SetPreferredManager(preferredManager)
	if err := model.SetSourceOverrides(customSources, offlineArchives); err != nil {
		return err
	}
	if err := model.SetRegistryOverrides(registrySources); err != nil {
		return err
	}
	if strings.TrimSpace(*offlineBundleFlag) != "" {
		if err := model.LoadOfflineBundle(*offlineBundleFlag); err != nil {
			return err
		}
	}
	if err := model.SetSourceMode(*sourcePolicyFlag); err != nil {
		return err
	}
	if *approveFlag {
		lineErr := tui.RunLineFallback(ctx, model, io.MultiReader(strings.NewReader("yes\n"), os.Stdin), os.Stdout)
		printSummary(model.SessionReport())
		return lineErr
	}
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	finalModel, runErr := program.Run()
	if current, ok := finalModel.(*tui.Model); ok {
		model = current
	}
	if runErr != nil {
		if errors.Is(runErr, tea.ErrInterrupted) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fmt.Fprintf(os.Stderr, "Interactive renderer unavailable: %v\n", runErr)
		fmt.Fprintln(os.Stderr, "Continuing in line-oriented terminal mode.")
		if err := tui.RunLineFallback(ctx, model, os.Stdin, os.Stdout); err != nil {
			return err
		}
	}
	printSummary(model.SessionReport())
	return nil
}

func detectedManagerPreference(managers []packagemanager.Detected, requested string) (tool.PackageManager, error) {
	if requested == "" {
		return "", nil
	}
	manager := tool.PackageManager(requested)
	for _, detected := range managers {
		if detected.Manager == manager {
			return manager, nil
		}
	}
	return "", fmt.Errorf("package manager %q is not detected on this machine", requested)
}

func parseSourceAssignments(entries []string, label string) (map[string]string, error) {
	assignments := make(map[string]string, len(entries))
	for _, entry := range entries {
		toolID, value, found := strings.Cut(entry, "=")
		toolID = strings.TrimSpace(toolID)
		value = strings.TrimSpace(value)
		if !found || toolID == "" || value == "" {
			return nil, fmt.Errorf("%s must use tool-id=value", label)
		}
		if _, duplicate := assignments[toolID]; duplicate {
			return nil, fmt.Errorf("%s for %q was supplied more than once", label, toolID)
		}
		assignments[toolID] = value
	}
	return assignments, nil
}

func addBundleArchives(bundlePath string, items *catalog.Catalog, machine platform.Info, customSources, offlineArchives map[string]string) (map[string]string, error) {
	merged := cloneAssignments(offlineArchives)
	bundlePath = strings.TrimSpace(bundlePath)
	if bundlePath == "" {
		return merged, nil
	}
	absPath, err := filepath.Abs(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("resolve offline bundle path: %w", err)
	}
	publicKey, err := security.DecodePublicKey(security.PinnedEd25519PublicKey)
	if err != nil {
		return nil, fmt.Errorf("pinned offline bundle public key is unavailable: %w", err)
	}
	bundle, err := offline.LoadBundle(absPath, items, machine, publicKey)
	if err != nil {
		return nil, err
	}
	for toolID, archivePath := range bundle.Artifacts {
		if customSources[toolID] != "" {
			return nil, fmt.Errorf("tool %q has both a custom source and an offline bundle archive", toolID)
		}
		if existing := merged[toolID]; existing != "" && existing != archivePath {
			return nil, fmt.Errorf("tool %q has multiple offline archives configured", toolID)
		}
		merged[toolID] = archivePath
	}
	return merged, nil
}

func cloneAssignments(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func sourceFallback(machine platform.Info, requested string) (bool, error) {
	preference := regional.Preference(requested)
	if preference != regional.Automatic && preference != regional.China && preference != regional.Worldwide && preference != regional.OfficialOnly && preference != regional.OfficialFirst {
		return false, fmt.Errorf("unsupported source policy %q", requested)
	}
	return regional.Detect(machine).UseRegionalFallback(preference)
}

func sourceFallbackForSelection(machine platform.Info, requested string, items *catalog.Catalog, inventory detection.Inventory, selected []string, customSources, offlineArchives map[string]string, registrySources sources.RegistryURLs) (bool, error) {
	allow, err := sourceFallback(machine, requested)
	if err == nil || requested != string(regional.Automatic) {
		return allow, err
	}
	for _, id := range selected {
		if inventory.Tools[id].State != detection.Missing {
			continue
		}
		manifest, exists := items.Tool(id)
		if !exists {
			return false, fmt.Errorf("unknown tool %q", id)
		}
		if customSources[id] == "" && offlineArchives[id] == "" {
			registrySelected := false
			for _, installer := range manifest.RegistryInstallers {
				if installer.Platform == machine.OS && registrySources[installer.Ecosystem] != "" {
					registrySelected = true
					break
				}
			}
			if registrySelected {
				continue
			}
			return false, err
		}
		if _, hasArchive := manifest.ArtifactFor(machine.OS, machine.Architecture); !hasArchive {
			return false, fmt.Errorf("%s has no catalog-pinned archive for this platform", id)
		}
	}
	return false, nil
}

func printSummary(results []installation.Result) {
	if len(results) == 0 {
		return
	}
	counts := make(map[installation.ResultState]int)
	for _, result := range results {
		counts[result.State]++
	}
	fmt.Fprintf(os.Stdout, "\nCompleted: %d verified, %d already satisfied, %d skipped, %d failed or partial.\n",
		counts[installation.InstalledVerified], counts[installation.AlreadySatisfied]+counts[installation.Preserved], counts[installation.Skipped], counts[installation.Failed]+counts[installation.PartiallyInstalled])
}

func printPlan(output *os.File, plan planning.Plan) {
	fmt.Fprintln(output, "Installation plan:")
	for _, item := range plan.Items {
		if item.Action == planning.Preserve {
			fmt.Fprintf(output, "  PRESERVE %s (%s)\n", item.Name, item.ExistingState)
			for _, installed := range item.ExistingPackages {
				fmt.Fprintf(output, "    package: %s\n", display.PackageOwnership(string(installed.Manager), installed.ID, installed.Version, installed.Source))
			}
			continue
		}
		privilege := "user scope"
		if item.RequiresPrivilege || item.Scope == "system" {
			privilege = "system scope; privilege may be required"
		}
		if item.Artifact != nil {
			sizeEstimate := approxBytes(item.ApproximateSize)
			if item.SizeEstimateNote != "" {
				sizeEstimate = item.SizeEstimateNote
			}
			fmt.Fprintf(output, "  INSTALL %s %s\n    source: %s\n    SHA-256: %s\n    approximate download: %s\n    install path: %s\n",
				item.Name, item.Version, item.Source, item.Artifact.SHA256, sizeEstimate, item.InstallPath)
			if item.InstallationNote != "" {
				fmt.Fprintf(output, "    setup: %s\n", item.InstallationNote)
			}
			for _, change := range item.PathChanges {
				if change.Manual {
					fmt.Fprintf(output, "    PATH: manual setup (%s)\n    command: %s\n", change.Target, change.ManualCommand)
				} else if change.Persist {
					fmt.Fprintf(output, "    PATH: add %s to %s\n", change.Directory, change.Target)
				}
			}
			if item.RegionalFallback && item.Artifact.RegionalURL != "" {
				fmt.Fprintf(output, "    verified regional fallback: %s (after retryable official-source failures)\n", item.Artifact.RegionalURL)
			}
			continue
		}
		if item.RegistryInstaller != nil {
			fmt.Fprintf(output, "  INSTALL %s %s\n    package: %s (%s registry)\n    source: %s\n    scope: user\n    install path: %s\n    command: %s\n",
				item.Name, item.Version, item.PackageID, item.RegistryInstaller.Ecosystem, item.Source, item.InstallPath, item.Command.Display())
			for _, change := range item.PathChanges {
				if change.Manual {
					fmt.Fprintf(output, "    PATH: manual setup (%s)\n    command: %s\n", change.Target, change.ManualCommand)
				} else if change.Persist {
					fmt.Fprintf(output, "    PATH: add %s to %s\n", change.Directory, change.Target)
				}
			}
			continue
		}
		fmt.Fprintf(output, "  INSTALL %s\n    package: %s (%s)\n    source: %s\n    scope: %s\n    command: %s\n",
			item.Name, item.PackageID, item.Manager, item.Source, privilege, item.Command.Display())
	}
	for _, issue := range plan.Issues {
		fmt.Fprintf(output, "  %s %s: %s\n", strings.ToUpper(string(issue.Severity)), issue.ToolID, issue.Message)
	}
	if len(plan.InstallItems()) > 0 {
		fmt.Fprintf(output, "  NOTICE %s\n", plan.TransactionDisclosure)
	}
}

func approxBytes(bytes int64) string {
	if bytes <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("about %.0f MB", float64(bytes)/1_000_000)
}

func parseIDs(value string) []string {
	values := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	seen := make(map[string]bool, len(values))
	ids := make([]string, 0, len(values))
	for _, id := range values {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func managerNames(inventory detection.Inventory) string {
	if len(inventory.Managers) == 0 {
		return "none detected"
	}
	managers := make([]string, 0, len(inventory.Managers))
	for _, manager := range inventory.Managers {
		managers = append(managers, string(manager.Manager))
	}
	return strings.Join(managers, ", ")
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func writeJSON(output *os.File, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printUsage(output *os.File) {
	fmt.Fprintln(output, `Developers' Club DX Bootstrap

Usage:
  dx-bootstrap                         Open the interactive application
  dx-bootstrap catalog [--json]        List embedded tools
  dx-bootstrap inventory [--json]      Inspect the current environment
  dx-bootstrap plan --tools git,go     Preview an installation plan
  dx-bootstrap plan --tools git,go --manager homebrew
  dx-bootstrap plan --tools go --offline-archive go=C:\archives\go.zip
  dx-bootstrap plan --tools go --custom-source go=https://mirror.example/go.zip
  dx-bootstrap plan --tools typescript --registry npm=https://registry.example/npm/
  dx-bootstrap plan --tools go --offline-bundle C:\archives\dx-bundle
  dx-bootstrap install --tools git,go --yes [--manager apt]
                                      Approve the displayed plan without a TTY
  dx-bootstrap verify-release --directory PATH
                                      Verify release checksums with this build's pinned key
  dx-bootstrap install [--tools ids]   Open the interactive application

The install and TUI commands require an attached terminal and explicit plan approval.`)
}
