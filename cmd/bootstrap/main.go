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
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/display"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/installation"
	"developers-club-dx/internal/planning"
	"developers-club-dx/internal/platform"
	"developers-club-dx/internal/temporary"
	"developers-club-dx/internal/tui"
)

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
	jsonOutput := flags.Bool("json", false, "write the plan as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	selected := parseIDs(*toolsFlag)
	if len(selected) == 0 {
		return errors.New("plan requires --tools with one or more catalog IDs")
	}
	inventory, err := detection.Scan(ctx, items, platform.Detect())
	if err != nil {
		return err
	}
	preferredManager, err := detectedManagerPreference(inventory, *managerFlag)
	if err != nil {
		return err
	}
	plan, err := planning.BuildWithOptions(items, inventory, selected, planning.Options{PreferredManager: preferredManager})
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

	machine := platform.Detect()
	inventory, err := detection.Scan(ctx, items, machine)
	if err != nil {
		return err
	}
	preferredManager, err := detectedManagerPreference(inventory, *managerFlag)
	if err != nil {
		return err
	}
	model := tui.NewModel(ctx, items, inventory, selected)
	model.SetPreferredManager(preferredManager)
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

func detectedManagerPreference(inventory detection.Inventory, requested string) (tool.PackageManager, error) {
	if requested == "" {
		return "", nil
	}
	manager := tool.PackageManager(requested)
	for _, detected := range inventory.Managers {
		if detected.Manager == manager {
			return manager, nil
		}
	}
	return "", fmt.Errorf("package manager %q is not detected on this machine", requested)
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
		fmt.Fprintf(output, "  INSTALL %s\n    package: %s (%s)\n    source: %s\n    scope: %s\n    command: %s\n",
			item.Name, item.PackageID, item.Manager, item.Source, privilege, item.Command.Display())
	}
	for _, issue := range plan.Issues {
		fmt.Fprintf(output, "  %s %s: %s\n", strings.ToUpper(string(issue.Severity)), issue.ToolID, issue.Message)
	}
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
  dx-bootstrap install --tools git,go --yes [--manager apt]
                                      Approve the displayed plan without a TTY
  dx-bootstrap verify-release --directory PATH
                                      Verify release checksums with this build's pinned key
  dx-bootstrap install [--tools ids]   Open the interactive application

The install and TUI commands require an attached terminal and explicit plan approval.`)
}
