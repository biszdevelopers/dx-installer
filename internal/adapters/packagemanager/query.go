package packagemanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"developers-club-dx/internal/domain/tool"
)

const maxPackageInventoryOutput = 8 << 20

type InstalledPackage struct {
	Manager tool.PackageManager
	ID      string
	Version string
	Source  string
}

type PackageInventory struct {
	Packages          []InstalledPackage
	Warnings          []string
	UncertainManagers []tool.PackageManager
}

type packageRecord struct {
	ID      string
	Version string
	Source  string
}

type boundedOutput struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedOutput) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
			b.truncated = true
		}
		_, _ = b.Buffer.Write(value)
	} else if len(value) > 0 {
		b.truncated = true
	}
	return original, nil
}

// QueryInstalled reads local package databases for managers with a stable,
// noninteractive inventory command. A failed query is a warning, never proof
// that a package is absent.
func QueryInstalled(parent context.Context, managers []Detected) PackageInventory {
	if len(managers) == 0 {
		return PackageInventory{}
	}
	type queryResult struct {
		index    int
		packages []packageRecord
		err      error
	}
	results := make([]queryResult, len(managers))
	jobs := make(chan int)
	completed := make(chan queryResult, len(managers))
	workerCount := min(4, len(managers))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				queryContext, cancel := context.WithTimeout(parent, 15*time.Second)
				packages, err := queryManager(queryContext, managers[index])
				cancel()
				completed <- queryResult{index: index, packages: packages, err: err}
			}
		}()
	}
	go func() {
		for index := range managers {
			jobs <- index
		}
		close(jobs)
		workers.Wait()
		close(completed)
	}()
	for item := range completed {
		results[item.index] = item
	}
	result := PackageInventory{}
	for index, item := range results {
		manager := managers[index].Manager
		if item.err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("could not inspect the local %s package database: %s", manager, safeQueryError(item.err)))
			result.UncertainManagers = append(result.UncertainManagers, manager)
			continue
		}
		for _, entry := range item.packages {
			if entry.ID == "" {
				continue
			}
			result.Packages = append(result.Packages, InstalledPackage{Manager: manager, ID: entry.ID, Version: entry.Version, Source: entry.Source})
		}
	}
	return result
}

func queryManager(ctx context.Context, detected Detected) ([]packageRecord, error) {
	if detected.Manager == tool.Winget {
		return queryWingetExport(ctx, detected.Path)
	}
	if detected.Manager == tool.Scoop || detected.Manager == tool.Chocolatey {
		return queryWindowsPackageManager(detected.Manager, detected.Path)
	}
	if detected.Manager == tool.Homebrew {
		formulae, err := runInventoryCommand(ctx, "brew", []string{"list", "--json=v1", "--formula"}, tool.Homebrew, false)
		if err != nil {
			return nil, err
		}
		casks, err := runInventoryCommand(ctx, "brew", []string{"list", "--json=v1", "--cask"}, tool.Homebrew, true)
		if err != nil {
			return nil, err
		}
		return append(formulae, casks...), nil
	}
	command, args, err := inventoryCommand(detected.Manager)
	if err != nil {
		return nil, err
	}
	return runInventoryCommand(ctx, command, args, detected.Manager, false)
}

func runInventoryCommand(ctx context.Context, command string, args []string, manager tool.PackageManager, cask bool) ([]packageRecord, error) {
	path, err := exec.LookPath(command)
	if err != nil {
		return nil, fmt.Errorf("%s is unavailable", command)
	}
	process := exec.CommandContext(ctx, path, args...)
	output := &boundedOutput{limit: maxPackageInventoryOutput}
	process.Stdout = output
	process.Stderr = &boundedOutput{limit: 16 << 10}
	if err := process.Run(); err != nil {
		return nil, fmt.Errorf("%s inventory command failed", command)
	}
	if output.truncated {
		return nil, errors.New("package inventory exceeded the 8 MiB safety limit")
	}
	return parsePackageInventory(manager, output.Bytes(), cask)
}

func inventoryCommand(manager tool.PackageManager) (string, []string, error) {
	command, ok := inventoryCommands[manager]
	if !ok {
		return "", nil, fmt.Errorf("%s does not expose a supported local inventory query", manager)
	}
	return command.executable, append([]string(nil), command.arguments...), nil
}

func parsePackageInventory(manager tool.PackageManager, output []byte, cask bool) ([]packageRecord, error) {
	parser, ok := inventoryParsers[manager]
	if !ok {
		return nil, fmt.Errorf("%s inventory output is unsupported", manager)
	}
	return parser(output, cask)
}

type inventoryCommandSpec struct {
	executable string
	arguments  []string
}

type inventoryParser func([]byte, bool) ([]packageRecord, error)

var inventoryCommands = map[tool.PackageManager]inventoryCommandSpec{
	tool.APT:        {executable: "dpkg-query", arguments: []string{"-W", "-f=${db:Status-Abbrev}\\t${binary:Package}\\t${Version}\\n"}},
	tool.DNF:        {executable: "rpm", arguments: []string{"-qa", "--qf=%{NAME}\\t%{VERSION}-%{RELEASE}\\n"}},
	tool.Zypper:     {executable: "rpm", arguments: []string{"-qa", "--qf=%{NAME}\\t%{VERSION}-%{RELEASE}\\n"}},
	tool.Pacman:     {executable: "pacman", arguments: []string{"-Q"}},
	tool.APK:        {executable: "apk", arguments: []string{"info", "-v"}},
	tool.MacPorts:   {executable: "port", arguments: []string{"-q", "installed"}},
	tool.FreeBSDPkg: {executable: "pkg", arguments: []string{"query", "%n\\t%v\\n"}},
}

var inventoryParsers = map[tool.PackageManager]inventoryParser{
	tool.APT:        parseAPTInventory,
	tool.DNF:        parseTwoFieldInventory,
	tool.Zypper:     parseTwoFieldInventory,
	tool.Pacman:     parseTwoFieldInventory,
	tool.APK:        parseAPKInventory,
	tool.MacPorts:   parseMacPortsInventory,
	tool.FreeBSDPkg: parseTwoFieldInventory,
	tool.Homebrew:   parseHomebrewInventory,
}

func parseAPTInventory(output []byte, _ bool) ([]packageRecord, error) {
	lines := strings.Split(string(output), "\n")
	packages := make([]packageRecord, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, errors.New("malformed dpkg-query package inventory row")
		}
		if len(fields[0]) < 2 || !strings.ContainsRune("iUHFWt", rune(fields[0][1])) {
			continue
		}
		packages = append(packages, packageRecord{ID: strings.SplitN(fields[1], ":", 2)[0], Version: fields[2]})
	}
	return packages, nil
}

func parseTwoFieldInventory(output []byte, _ bool) ([]packageRecord, error) {
	lines := strings.Split(string(output), "\n")
	packages := make([]packageRecord, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(strings.TrimSuffix(line, "\r")))
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 2 {
			return nil, errors.New("malformed two-field package inventory row")
		}
		packages = append(packages, packageRecord{ID: fields[0], Version: fields[1]})
	}
	return packages, nil
}

func parseAPKInventory(output []byte, _ bool) ([]packageRecord, error) {
	lines := strings.Split(string(output), "\n")
	packages := make([]packageRecord, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		id, version := splitAPKPackage(line)
		if id == "" {
			return nil, errors.New("malformed apk package inventory row")
		}
		packages = append(packages, packageRecord{ID: id, Version: version})
	}
	return packages, nil
}

func parseMacPortsInventory(output []byte, _ bool) ([]packageRecord, error) {
	lines := strings.Split(string(output), "\n")
	packages := make([]packageRecord, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "The following ports are currently installed:") || line == "No ports are installed." {
			continue
		}
		id, version := parseMacPortsRow(line)
		if id == "" {
			return nil, errors.New("malformed MacPorts package inventory row")
		}
		packages = append(packages, packageRecord{ID: id, Version: version})
	}
	return packages, nil
}

type homebrewFormula struct {
	Name      string `json:"name"`
	FullName  string `json:"full_name"`
	Token     string `json:"token"`
	Version   string `json:"version"`
	Installed []struct {
		Version string `json:"version"`
	} `json:"installed"`
}

func parseHomebrewInventory(output []byte, cask bool) ([]packageRecord, error) {
	var entries []homebrewFormula
	if err := json.Unmarshal(output, &entries); err != nil {
		return nil, fmt.Errorf("decode Homebrew inventory: %w", err)
	}
	packages := make([]packageRecord, 0, len(entries))
	for _, entry := range entries {
		id := entry.FullName
		if cask {
			id = entry.Token
		}
		if id == "" {
			id = entry.Name
		}
		if id == "" {
			return nil, errors.New("Homebrew inventory contains an unnamed formula")
		}
		if cask {
			packages = append(packages, packageRecord{ID: id, Version: entry.Version})
			continue
		}
		versions := []string{""}
		if len(entry.Installed) > 0 {
			versions = make([]string, 0, len(entry.Installed))
			for _, installed := range entry.Installed {
				versions = append(versions, installed.Version)
			}
		}
		identifiers := []string{id}
		if entry.Name != "" && entry.Name != id {
			identifiers = append(identifiers, entry.Name)
		}
		for _, identifier := range identifiers {
			for _, version := range versions {
				packages = append(packages, packageRecord{ID: identifier, Version: version})
			}
		}
	}
	return packages, nil
}

var (
	apkVersionSuffix = regexp.MustCompile(`^(.+)-([0-9][A-Za-z0-9.+_~-]*)$`)
	macPortsRow      = regexp.MustCompile(`^([A-Za-z0-9._+-]+) @([^ ]+)(?: \(.*\))?$`)
)

func splitAPKPackage(line string) (string, string) {
	parts := apkVersionSuffix.FindStringSubmatch(line)
	if len(parts) != 3 {
		return "", ""
	}
	return parts[1], parts[2]
}

func parseMacPortsRow(line string) (string, string) {
	parts := macPortsRow.FindStringSubmatch(line)
	if len(parts) != 3 {
		return "", ""
	}
	return parts[1], parts[2]
}

func safeQueryError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "query timed out after 15 seconds"
	case errors.Is(err, context.Canceled):
		return "query was cancelled"
	default:
		return err.Error()
	}
}
