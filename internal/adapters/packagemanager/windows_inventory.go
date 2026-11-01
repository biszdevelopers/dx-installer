package packagemanager

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"developers-club-dx/internal/domain/tool"
)

const maxWindowsPackageRecords = 50000
const maxNuspecBytes = 1 << 20
const maxWingetExportBytes = 8 << 20

type wingetExport struct {
	Sources []struct {
		Details struct {
			Name string `json:"Name"`
		} `json:"SourceDetails"`
		Packages []struct {
			Identifier string `json:"PackageIdentifier"`
			Version    string `json:"Version"`
		} `json:"Packages"`
	} `json:"Sources"`
}

func queryWingetExport(ctx context.Context, executablePath string) (packages []packageRecord, resultErr error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("WinGet local inventory is available only on Windows")
	}
	if executablePath == "" {
		return nil, fmt.Errorf("WinGet executable path is unavailable")
	}
	directory, err := os.MkdirTemp("", "dx-winget-inventory-")
	if err != nil {
		return nil, fmt.Errorf("create temporary WinGet inventory workspace: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(directory); err != nil {
			packages = nil
			resultErr = errors.Join(resultErr, fmt.Errorf("remove temporary WinGet inventory workspace: %w", err))
		}
	}()

	outputPath := filepath.Join(directory, "packages.json")
	command := exec.CommandContext(ctx, executablePath,
		"export", "--output", outputPath, "--include-versions", "--disable-interactivity")
	command.Stdout = &boundedOutput{limit: 16 << 10}
	command.Stderr = &boundedOutput{limit: 16 << 10}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("WinGet export failed or requires an unaccepted source agreement")
	}
	info, err := os.Lstat(outputPath)
	if err != nil {
		return nil, fmt.Errorf("read temporary WinGet export: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("WinGet export did not create a regular JSON file")
	}
	if info.Size() > maxWingetExportBytes {
		return nil, fmt.Errorf("WinGet export exceeds the %d byte safety limit", maxWingetExportBytes)
	}
	file, err := os.Open(outputPath)
	if err != nil {
		return nil, fmt.Errorf("open temporary WinGet export: %w", err)
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, maxWingetExportBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read temporary WinGet export: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close temporary WinGet export: %w", closeErr)
	}
	if len(contents) > maxWingetExportBytes {
		return nil, fmt.Errorf("WinGet export exceeds the %d byte safety limit", maxWingetExportBytes)
	}
	return parseWingetExport(contents)
}

func parseWingetExport(contents []byte) ([]packageRecord, error) {
	var exported wingetExport
	if err := json.Unmarshal(contents, &exported); err != nil {
		return nil, fmt.Errorf("decode WinGet export: %w", err)
	}
	if len(exported.Sources) == 0 {
		return nil, fmt.Errorf("WinGet export contains no sources")
	}
	packages := make([]packageRecord, 0)
	for _, source := range exported.Sources {
		for _, entry := range source.Packages {
			identifier := strings.TrimSpace(entry.Identifier)
			if identifier == "" {
				return nil, fmt.Errorf("WinGet export contains a package without an identifier")
			}
			sourceName := strings.TrimSpace(source.Details.Name)
			if sourceName == "" {
				return nil, fmt.Errorf("WinGet export contains a package without a source name")
			}
			packages = append(packages, packageRecord{ID: identifier, Version: strings.TrimSpace(entry.Version), Source: sourceName})
			if len(packages) > maxWindowsPackageRecords {
				return nil, fmt.Errorf("WinGet inventory exceeds the %d package limit", maxWindowsPackageRecords)
			}
		}
	}
	return packages, nil
}

type nuspecMetadata struct {
	XMLName  xml.Name `xml:"package"`
	Metadata struct {
		ID      string `xml:"id"`
		Version string `xml:"version"`
	} `xml:"metadata"`
}

func queryWindowsPackageManager(manager tool.PackageManager, executablePath string) ([]packageRecord, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("%s local inventory is available only on Windows", manager)
	}
	switch manager {
	case tool.Scoop:
		for _, configuredRoot := range []string{os.Getenv("SCOOP"), os.Getenv("SCOOP_GLOBAL")} {
			if configuredRoot == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(configuredRoot, "apps")); err != nil {
				return nil, fmt.Errorf("configured Scoop apps directory is unavailable: %w", err)
			}
		}
		roots := scoopRoots(executablePath)
		packages := make([]packageRecord, 0)
		databaseFound := false
		for _, root := range roots {
			if _, err := os.Stat(filepath.Join(root, "apps")); err == nil {
				databaseFound = true
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("inspect Scoop apps directory: %w", err)
			}
			entries, err := scanScoopRoot(root)
			if err != nil {
				return nil, err
			}
			packages = append(packages, entries...)
			if len(packages) > maxWindowsPackageRecords {
				return nil, fmt.Errorf("Scoop inventory exceeds the %d package limit", maxWindowsPackageRecords)
			}
		}
		if !databaseFound {
			return nil, fmt.Errorf("Scoop apps directory could not be found in its configured roots")
		}
		return packages, nil
	case tool.Chocolatey:
		return scanChocolateyRoot(chocolateyRoot(executablePath))
	default:
		return nil, fmt.Errorf("%s does not expose a supported local inventory query", manager)
	}
}

func scoopRoots(executablePath string) []string {
	var roots []string
	if root := os.Getenv("SCOOP"); root != "" {
		roots = append(roots, root)
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, "scoop"))
	}
	if root := os.Getenv("SCOOP_GLOBAL"); root != "" {
		roots = append(roots, root)
	} else if programData := os.Getenv("ProgramData"); programData != "" {
		roots = append(roots, filepath.Join(programData, "scoop"))
	}
	if executablePath != "" {
		root := filepath.Dir(filepath.Dir(executablePath))
		if strings.EqualFold(filepath.Base(filepath.Dir(executablePath)), "shims") {
			roots = append(roots, root)
		}
	}
	unique := make([]string, 0, len(roots))
	seen := make(map[string]bool, len(roots))
	for _, root := range roots {
		if root == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(root))
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, filepath.Clean(root))
	}
	return unique
}

func chocolateyRoot(executablePath string) string {
	if root := os.Getenv("ChocolateyInstall"); root != "" {
		return root
	}
	if executablePath != "" {
		binDirectory := filepath.Dir(executablePath)
		if strings.EqualFold(filepath.Base(binDirectory), "bin") {
			return filepath.Dir(binDirectory)
		}
	}
	if programData := os.Getenv("ProgramData"); programData != "" {
		return filepath.Join(programData, "chocolatey")
	}
	return ""
}

func scanScoopRoot(root string) ([]packageRecord, error) {
	appsDirectory := filepath.Join(root, "apps")
	apps, err := os.ReadDir(appsDirectory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Scoop apps directory: %w", err)
	}
	if len(apps) > maxWindowsPackageRecords {
		return nil, fmt.Errorf("Scoop inventory exceeds the %d package limit", maxWindowsPackageRecords)
	}
	packages := make([]packageRecord, 0, len(apps))
	for _, app := range apps {
		appInfo, err := os.Lstat(filepath.Join(appsDirectory, app.Name()))
		if err != nil || !appInfo.IsDir() || appInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(appsDirectory, app.Name()))
		if err != nil {
			return nil, fmt.Errorf("read Scoop app %q: %w", app.Name(), err)
		}
		if len(versions) > maxWindowsPackageRecords {
			return nil, fmt.Errorf("Scoop app %q exceeds the version limit", app.Name())
		}
		addedVersion := false
		for _, version := range versions {
			if strings.EqualFold(version.Name(), "current") {
				continue
			}
			versionInfo, err := os.Lstat(filepath.Join(appsDirectory, app.Name(), version.Name()))
			if err != nil || !versionInfo.IsDir() || versionInfo.Mode()&os.ModeSymlink != 0 {
				continue
			}
			packages = append(packages, packageRecord{ID: app.Name(), Version: version.Name()})
			addedVersion = true
		}
		if !addedVersion && len(versions) > 0 {
			packages = append(packages, packageRecord{ID: app.Name()})
		}
		if len(packages) > maxWindowsPackageRecords {
			return nil, fmt.Errorf("Scoop inventory exceeds the %d package limit", maxWindowsPackageRecords)
		}
	}
	return packages, nil
}

func scanChocolateyRoot(root string) ([]packageRecord, error) {
	if root == "" {
		return nil, fmt.Errorf("Chocolatey installation root could not be determined")
	}
	libDirectory := filepath.Join(root, "lib")
	packages, err := os.ReadDir(libDirectory)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("Chocolatey lib directory could not be found")
	}
	if err != nil {
		return nil, fmt.Errorf("read Chocolatey lib directory: %w", err)
	}
	if len(packages) > maxWindowsPackageRecords {
		return nil, fmt.Errorf("Chocolatey inventory exceeds the %d package limit", maxWindowsPackageRecords)
	}
	installed := make([]packageRecord, 0, len(packages))
	for _, packageDirectory := range packages {
		packagePath := filepath.Join(libDirectory, packageDirectory.Name())
		info, err := os.Lstat(packagePath)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		entry := packageRecord{ID: packageDirectory.Name()}
		files, err := os.ReadDir(packagePath)
		if err != nil {
			return nil, fmt.Errorf("read Chocolatey package %q: %w", packageDirectory.Name(), err)
		}
		if len(files) > maxWindowsPackageRecords {
			return nil, fmt.Errorf("Chocolatey package %q exceeds the file limit", packageDirectory.Name())
		}
		for _, file := range files {
			if !strings.EqualFold(filepath.Ext(file.Name()), ".nuspec") {
				continue
			}
			nuspecPath := filepath.Join(packagePath, file.Name())
			nuspecInfo, err := os.Lstat(nuspecPath)
			if err != nil || !nuspecInfo.Mode().IsRegular() || nuspecInfo.Size() > maxNuspecBytes {
				continue
			}
			nuspecFile, err := os.Open(nuspecPath)
			if err != nil {
				continue
			}
			metadata, parseErr := parseChocolateyNuspec(nuspecFile)
			_ = nuspecFile.Close()
			if parseErr == nil {
				entry = metadata
				break
			}
		}
		installed = append(installed, entry)
	}
	return installed, nil
}

func parseChocolateyNuspec(reader io.Reader) (packageRecord, error) {
	limited := io.LimitReader(reader, maxNuspecBytes+1)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return packageRecord{}, err
	}
	if len(contents) > maxNuspecBytes {
		return packageRecord{}, fmt.Errorf("nuspec exceeds the %d byte safety limit", maxNuspecBytes)
	}
	var metadata nuspecMetadata
	if err := xml.Unmarshal(contents, &metadata); err != nil {
		return packageRecord{}, fmt.Errorf("parse Chocolatey nuspec: %w", err)
	}
	if metadata.Metadata.ID == "" {
		return packageRecord{}, fmt.Errorf("Chocolatey nuspec has no package id")
	}
	return packageRecord{ID: metadata.Metadata.ID, Version: metadata.Metadata.Version}, nil
}
