package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/display"
	"developers-club-dx/internal/installation"
	"developers-club-dx/internal/planning"
)

func RunLineFallback(ctx context.Context, model *Model, input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	fmt.Fprintln(output, "DX Bootstrap — line-oriented terminal mode")
	for _, warning := range model.inventory.Warnings {
		fmt.Fprintln(output, "Inventory warning:", warning)
	}
	if len(model.selectedIDs()) == 0 {
		fmt.Fprintln(output, "Available tools:")
		for _, item := range model.catalog.Tools() {
			entry := model.inventory.Tools[item.ID]
			state := entry.State
			fmt.Fprintf(output, "  %-18s %-28s %s\n", item.ID, item.Name, state)
			for _, installed := range entry.Packages {
				fmt.Fprintf(output, "    package: %s\n", display.PackageOwnership(string(installed.Manager), installed.ID, installed.Version, installed.Source))
			}
		}
		fmt.Fprint(output, "Enter comma-separated tool IDs: ")
		line, err := readLine(reader)
		if err != nil {
			return err
		}
		model.selected = make(map[string]bool)
		for _, id := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			if _, exists := model.catalog.Tool(id); !exists {
				return fmt.Errorf("unknown tool %q", id)
			}
			model.selected[id] = true
		}
	}
	plan, err := planning.BuildWithOptions(model.catalog, model.inventory, model.selectedIDs(), planning.Options{PreferredManager: model.preferredManager})
	if err != nil {
		return err
	}
	if plan.Blocked {
		printPlan(output, plan)
		return fmt.Errorf("installation plan is blocked")
	}
	model.plan = plan
	model.planReady = true
	printPlan(output, plan)
	if len(plan.InstallItems()) == 0 {
		fmt.Fprintln(output, "All selected tools are already present; no installation is needed.")
		return nil
	}
	fmt.Fprint(output, "Run this plan? Type yes to approve: ")
	answer, err := readLine(reader)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(answer), "yes") {
		fmt.Fprintln(output, "Plan cancelled. No commands were run.")
		return nil
	}

	for attempt := 0; attempt < 3; attempt++ {
		scanCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		inventory, scanErr := detection.Scan(scanCtx, model.catalog, model.inventory.Platform)
		cancel()
		if scanErr != nil {
			return fmt.Errorf("could not refresh inventory before execution: %w", scanErr)
		}
		updated, buildErr := planning.BuildWithOptions(model.catalog, inventory, model.selectedIDs(), planning.Options{PreferredManager: model.preferredManager})
		if buildErr != nil {
			return buildErr
		}
		if updated.Blocked {
			printPlan(output, updated)
			return fmt.Errorf("refreshed plan is blocked; no commands were run")
		}
		if planSignature(updated) == planSignature(plan) {
			plan = updated
			model.inventory = inventory
			break
		}
		plan = updated
		model.inventory = inventory
		model.plan = plan
		model.planReady = true
		printPlan(output, plan)
		if attempt == 2 {
			return fmt.Errorf("machine inventory changed repeatedly; no commands were run")
		}
		fmt.Fprint(output, "The plan changed after inventory refresh. Type yes to approve the updated plan: ")
		answer, err = readLine(reader)
		if err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(answer), "yes") {
			fmt.Fprintln(output, "Plan cancelled. No commands were run.")
			return nil
		}
	}
	session, err := installation.NewSession(plan)
	if err != nil {
		return err
	}
	model.session = session
	for {
		item, ok := session.Next()
		if !ok {
			break
		}
		fmt.Fprintf(output, "\nInstalling %s from %s with %s\n", item.Name, item.Source, item.Command.Display())
		command, startErr := item.Command.ExecContext(ctx)
		if startErr != nil {
			_ = session.Record(item, startErr, detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Missing})
			continue
		}
		command.Stdin = reader
		command.Stdout = output
		command.Stderr = output
		commandErr := command.Run()
		scanCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		inventory, scanErr := detection.Scan(scanCtx, model.catalog, model.inventory.Platform)
		cancel()
		actual := inventory.Tools[item.ToolID]
		if scanErr != nil {
			actual = detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Unknown}
			if commandErr == nil {
				commandErr = fmt.Errorf("post-install verification failed: %w", scanErr)
			}
		} else {
			model.inventory = inventory
		}
		if err := session.Record(item, commandErr, actual); err != nil {
			return err
		}
		result, exists := session.Result(item.ToolID)
		if exists {
			fmt.Fprintf(output, "Result: %s | verification: %s\n", result.State, result.Verification)
			if result.Diagnostic != "" {
				fmt.Fprintln(output, "Diagnostic: "+result.Diagnostic)
			}
		}
	}
	fmt.Fprintln(output, "\nResults:")
	for _, result := range session.Report() {
		fmt.Fprintf(output, "  %-22s %s\n", result.Name, result.State)
		if result.Diagnostic != "" {
			fmt.Fprintln(output, "    "+result.Diagnostic)
		}
	}
	return nil
}

func printPlan(output io.Writer, plan planning.Plan) {
	fmt.Fprintln(output, "\nInstallation plan:")
	for _, item := range plan.Items {
		if item.Action == planning.Preserve {
			fmt.Fprintf(output, "  PRESERVE %s (%s)\n", item.Name, item.ExistingState)
			for _, installed := range item.ExistingPackages {
				fmt.Fprintf(output, "    package: %s\n", display.PackageOwnership(string(installed.Manager), installed.ID, installed.Version, installed.Source))
			}
			continue
		}
		fmt.Fprintf(output, "  INSTALL %s\n    source: %s (%s)\n    scope: %s\n    command: %s\n", item.Name, item.Source, item.Manager, item.Scope, item.Command.Display())
	}
	for _, issue := range plan.Issues {
		fmt.Fprintf(output, "  %s %s: %s\n", strings.ToUpper(string(issue.Severity)), issue.ToolID, issue.Message)
	}
	if len(plan.InstallItems()) > 0 {
		fmt.Fprintf(output, "  NOTICE %s\n", planning.TransactionDisclosure)
	}
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	if err == io.EOF && line == "" {
		return "", io.EOF
	}
	return strings.TrimRight(line, "\r\n"), nil
}
