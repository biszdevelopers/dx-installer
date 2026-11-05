package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	registryadapter "developers-club-dx/internal/adapters/registry"
	"developers-club-dx/internal/artifacts"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/display"
	"developers-club-dx/internal/installation"
	"developers-club-dx/internal/pathconfig"
	"developers-club-dx/internal/planning"
	"developers-club-dx/internal/sources"
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
	plan, err := model.buildSelectedPlan(model.selectedIDs())
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
		model.inventory = inventory
		updated, buildErr := model.buildSelectedPlan(model.selectedIDs())
		if buildErr != nil {
			return buildErr
		}
		if updated.Blocked {
			printPlan(output, updated)
			return fmt.Errorf("refreshed plan is blocked; no commands were run")
		}
		if planSignature(updated) == planSignature(plan) {
			plan = updated
			break
		}
		plan = updated
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
		if item.Action == planning.Provided {
			actual, found := model.inventory.Tools[item.ToolID]
			if !found {
				actual = detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Unknown}
			}
			if err := session.RecordProvided(item, actual); err != nil {
				return err
			}
			continue
		}
		var operationErr error
		details := installation.ExecutionDetails{PathChanges: item.PathChanges}
		if item.Artifact != nil {
			fmt.Fprintf(output, "\nDownloading and installing %s %s from %s\n", item.Name, item.Version, item.Source)
			home, homeErr := os.UserHomeDir()
			if homeErr != nil {
				operationErr = homeErr
			} else {
				policy := sources.OfficialOnly
				if item.RegionalFallback {
					policy = sources.OfficialFirst
				}
				result, installErr := artifacts.NewArchiveInstaller().InstallWithOptions(ctx, item.ToolID, home, *item.Artifact, artifacts.InstallOptions{
					Policy:                policy,
					AllowRegionalFallback: item.RegionalFallback,
					CustomSourceURL:       item.CustomSourceURL,
					OfflineArchivePath:    item.OfflineArchivePath,
				})
				operationErr = installErr
				details.Source = result.SourceName
				details.SourceURL = result.SourceURL
				details.InstallPath = result.Path
				details.SHA256 = result.SHA256
				details.CleanupNotice = result.CleanupNotice
			}
		} else {
			fmt.Fprintf(output, "\nInstalling %s from %s with %s\n", item.Name, item.Source, item.Command.Display())
			commandSpec := item.Command
			temporaryDirectory := ""
			if item.RegistryInstaller != nil {
				var temporaryErr error
				temporaryDirectory, temporaryErr = registryadapter.NewTemporaryDirectory()
				if temporaryErr != nil {
					operationErr = temporaryErr
				} else {
					commandSpec, operationErr = registryadapter.PrepareCommand(commandSpec, temporaryDirectory)
				}
			}
			command, startErr := commandSpec.ExecContext(ctx)
			if startErr != nil {
				operationErr = startErr
			} else if operationErr == nil {
				command.Stdin = reader
				command.Stdout = output
				command.Stderr = output
				operationErr = command.Run()
			}
			if temporaryDirectory != "" {
				if cleanupErr := os.RemoveAll(temporaryDirectory); cleanupErr != nil {
					details.CleanupNotice = "temporary registry workspace cleanup failed: " + cleanupErr.Error()
					operationErr = errors.Join(operationErr, cleanupErr)
				}
			}
		}
		scanCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		inventory, scanErr := detection.ScanWithSearchPaths(scanCtx, model.catalog, model.inventory.Platform, searchDirectories(item.PathChanges))
		cancel()
		actual := inventory.Tools[item.ToolID]
		if scanErr != nil {
			actual = detection.Tool{ID: item.ToolID, Name: item.Name, State: detection.Unknown}
			if operationErr == nil {
				operationErr = fmt.Errorf("post-install verification failed: %w", scanErr)
			}
		} else {
			model.inventory = inventory
		}
		if actual.State == detection.Healthy && len(item.PathChanges) > 0 {
			if pathErr := pathconfig.Apply(item.PathChanges); pathErr != nil {
				details.PathError = pathErr.Error()
				fmt.Fprintln(output, "PATH update needs attention:", pathErr)
			}
		}
		if item.RegistryInstaller != nil && actual.State == detection.Healthy {
			details.Source = string(item.RegistryInstaller.Ecosystem) + " registry"
			details.SourceURL = item.Source
			details.InstallPath = item.InstallPath
		}
		if err := session.RecordWithDetails(item, operationErr, actual, details); err != nil {
			return err
		}
		result, exists := session.Result(item.ToolID)
		if exists {
			fmt.Fprintf(output, "Result: %s | verification: %s\n", result.State, result.Verification)
			if result.Diagnostic != "" {
				fmt.Fprintln(output, "Diagnostic: "+result.Diagnostic)
			}
			if result.InstallPath != "" {
				fmt.Fprintln(output, "Installed at:", result.InstallPath)
			}
			if result.SourceURL != "" {
				fmt.Fprintln(output, "Download URL:", result.SourceURL)
			}
			if result.SHA256 != "" {
				fmt.Fprintln(output, "SHA-256:", result.SHA256)
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
		if item.Action == planning.Provided {
			fmt.Fprintf(output, "  PROVIDED %s by dependency %s (verified after install)\n", item.Name, item.ProvidedBy)
			continue
		}
		if item.Artifact != nil {
			fmt.Fprintf(output, "  INSTALL %s %s\n    official source: %s\n    SHA-256: %s\n    approximate download: %s\n    install path: %s\n", item.Name, item.Version, item.Source, item.Artifact.SHA256, approximateSize(item.ApproximateSize), item.InstallPath)
			for _, change := range item.PathChanges {
				if change.Manual {
					fmt.Fprintf(output, "    PATH: manual update required (%s)\n    command: %s\n", change.Target, change.ManualCommand)
				} else if change.AlreadyConfigured {
					fmt.Fprintf(output, "    PATH: already configured in %s; update current session\n", change.Target)
				} else {
					fmt.Fprintf(output, "    PATH: add %s to %s\n", change.Directory, change.Target)
				}
			}
			if item.RegionalFallback && item.Artifact.RegionalURL != "" {
				fmt.Fprintf(output, "    verified regional fallback: %s (only after retryable official-source failures)\n", item.Artifact.RegionalURL)
			}
			continue
		}
		if item.RegistryInstaller != nil {
			fmt.Fprintf(output, "  INSTALL %s %s\n    registry package: %s (%s)\n    source: %s\n    install path: %s\n    command: %s\n", item.Name, item.Version, item.PackageID, item.RegistryInstaller.Ecosystem, item.Source, item.InstallPath, item.Command.Display())
			for _, change := range item.PathChanges {
				if change.Manual {
					fmt.Fprintf(output, "    PATH: manual update required (%s)\n    command: %s\n", change.Target, change.ManualCommand)
				} else if change.AlreadyConfigured {
					fmt.Fprintf(output, "    PATH: already configured in %s; update current session\n", change.Target)
				} else {
					fmt.Fprintf(output, "    PATH: add %s to %s\n", change.Directory, change.Target)
				}
			}
			continue
		}
		fmt.Fprintf(output, "  INSTALL %s\n    source: %s (%s)\n    scope: %s\n", item.Name, item.Source, item.Manager, item.Scope)
		if item.ApproximateSize > 0 {
			fmt.Fprintf(output, "    approximate download: %s\n", approximateSize(item.ApproximateSize))
		}
		fmt.Fprintf(output, "    command: %s\n", item.Command.Display())
	}
	for _, issue := range plan.Issues {
		fmt.Fprintf(output, "  %s %s: %s\n", strings.ToUpper(string(issue.Severity)), issue.ToolID, issue.Message)
	}
	if len(plan.InstallItems()) > 0 {
		fmt.Fprintf(output, "  NOTICE %s\n", plan.TransactionDisclosure)
	}
}

func approximateSize(bytes int64) string {
	if bytes <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("about %.0f MB", float64(bytes)/1_000_000)
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
