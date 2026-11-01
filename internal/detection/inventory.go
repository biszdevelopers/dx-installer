package detection

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"developers-club-dx/internal/adapters/packagemanager"
	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/platform"
)

type State string

const (
	Missing     State = "missing"
	Healthy     State = "healthy"
	Outdated    State = "outdated"
	Broken      State = "broken"
	Partial     State = "partial"
	Conflicting State = "conflicting"
	Unsupported State = "unsupported"
	Unknown     State = "unknown"
)

type Instance struct {
	Executable             string
	Path                   string
	Version                string
	State                  State
	Architecture           []tool.Architecture
	ArchitectureStatus     ArchitectureStatus
	ArchitectureDiagnostic string
	Diagnostic             string
}

type ArchitectureStatus string

const (
	ArchitectureCompatible ArchitectureStatus = "compatible"
	ArchitectureMismatch   ArchitectureStatus = "mismatch"
	ArchitectureUnverified ArchitectureStatus = "unverified"
)

type Tool struct {
	ID        string
	Name      string
	Category  string
	State     State
	Instances []Instance
	Packages  []PackageOwnership
}

type PackageOwnership struct {
	Manager tool.PackageManager
	ID      string
	Version string
	Source  string
}

type Inventory struct {
	Platform platform.Info
	Managers []packagemanager.Detected
	Tools    map[string]Tool
	Warnings []string
}

type scanResult struct {
	manifest  tool.Manifest
	instances []Instance
	packages  []PackageOwnership
	state     State
}

const maxDiscoveredInstallDirectories = 512

var installDirectoryCache sync.Map

func Scan(ctx context.Context, items *catalog.Catalog, machine platform.Info) (Inventory, error) {
	inventory := Inventory{
		Platform: machine,
		Tools:    make(map[string]Tool),
	}
	if err := platform.RefreshSearchPath(); err != nil {
		inventory.Warnings = append(inventory.Warnings, "could not refresh the process PATH after installation: "+err.Error())
	}
	inventory.Managers = packagemanager.Detect(machine.OS, machine.Distribution)
	packageInventory := packagemanager.QueryInstalled(ctx, inventory.Managers)
	inventory.Warnings = append(inventory.Warnings, packageInventory.Warnings...)
	uncertainManagers := make(map[tool.PackageManager]bool, len(packageInventory.UncertainManagers))
	for _, manager := range packageInventory.UncertainManagers {
		uncertainManagers[manager] = true
	}
	manifests := items.Tools()
	if len(manifests) == 0 {
		return inventory, fmt.Errorf("catalog contains no tools")
	}
	jobs := make(chan tool.Manifest)
	results := make(chan scanResult, len(manifests))
	workerCount := min(8, len(manifests))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for manifest := range jobs {
				instances, packages, state := scanTool(ctx, manifest, machine, inventory.Managers, packageInventory.Packages, uncertainManagers)
				results <- scanResult{manifest: manifest, instances: instances, packages: packages, state: state}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, manifest := range manifests {
			select {
			case jobs <- manifest:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	for result := range results {
		inventory.Tools[result.manifest.ID] = Tool{
			ID:        result.manifest.ID,
			Name:      result.manifest.Name,
			Category:  result.manifest.Category,
			State:     result.state,
			Instances: result.instances,
			Packages:  result.packages,
		}
	}
	if err := ctx.Err(); err != nil {
		return inventory, err
	}
	return inventory, nil
}

func scanTool(ctx context.Context, manifest tool.Manifest, machine platform.Info, managers []packagemanager.Detected, installedPackages []packagemanager.InstalledPackage, uncertainManagers map[tool.PackageManager]bool) ([]Instance, []PackageOwnership, State) {
	if !manifest.Supports(machine.OS, machine.Architecture) {
		return nil, nil, Unsupported
	}
	packages := installedPackagesFor(manifest, machine.OS, managers, installedPackages)
	paths := make(map[string]string)
	for _, executable := range manifest.Executables {
		for _, path := range findExecutables(executable) {
			paths[path] = executable
		}
	}
	if len(paths) == 0 {
		if len(packages) > 0 {
			return nil, packages, Unknown
		}
		availableManagers := make(map[tool.PackageManager]bool, len(managers))
		for _, manager := range managers {
			availableManagers[manager.Manager] = true
		}
		for _, installer := range manifest.Installers {
			if installer.Platform == machine.OS && availableManagers[installer.Manager] && uncertainManagers[installer.Manager] {
				return nil, nil, Unknown
			}
		}
		return nil, nil, Missing
	}
	instances := make([]Instance, 0, len(paths))
	for path, executable := range paths {
		instance := inspectVersion(ctx, path, executable, manifest.VersionArgs)
		architectures, recognized, architectureErr := binaryArchitectures(path)
		instance.Architecture = architectures
		instance.ArchitectureStatus = ArchitectureUnverified
		if architectureErr != nil {
			instance.ArchitectureDiagnostic = "binary architecture could not be read: " + architectureErr.Error()
		} else if recognized {
			if containsArchitecture(architectures, machine.Architecture) {
				instance.ArchitectureStatus = ArchitectureCompatible
			} else {
				instance.ArchitectureStatus = ArchitectureMismatch
				instance.State = Conflicting
				instance.ArchitectureDiagnostic = fmt.Sprintf("binary architecture %s does not match host architecture %s", joinArchitectures(architectures), machine.Architecture)
			}
		}
		instances = append(instances, instance)
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].Path < instances[j].Path })
	healthyCount := 0
	unknownCount := 0
	for _, instance := range instances {
		if instance.State == Healthy {
			healthyCount++
		}
		if instance.State == Unknown {
			unknownCount++
		}
	}
	if ctx.Err() != nil {
		return instances, packages, Unknown
	}
	if healthyCount == len(instances) {
		return instances, packages, Healthy
	}
	if healthyCount > 0 {
		return instances, packages, Partial
	}
	if unknownCount == len(instances) {
		return instances, packages, Unknown
	}
	if unknownCount > 0 {
		return instances, packages, Partial
	}
	return instances, packages, Broken
}

func findExecutables(name string) []string {
	searchPath := filepath.SplitList(os.Getenv("PATH"))
	searchPath = append(searchPath, knownExecutableDirectories()...)
	if len(searchPath) == 0 {
		return nil
	}
	candidates := []string{name}
	if runtime.GOOS == "windows" {
		switch strings.ToLower(filepath.Ext(name)) {
		case ".exe", ".com":
		default:
			candidates = []string{name + ".exe", name + ".com"}
		}
	}
	found := make(map[string]string)
	for _, directory := range searchPath {
		directory = strings.Trim(directory, `"`)
		if directory == "" {
			continue
		}
		for _, candidate := range candidates {
			path := filepath.Join(directory, candidate)
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
				continue
			}
			absolute, err := filepath.Abs(path)
			if err != nil {
				continue
			}
			key, err := filepath.EvalSymlinks(absolute)
			if err != nil {
				key = absolute
			}
			if runtime.GOOS == "windows" {
				key = strings.ToLower(key)
			}
			found[key] = absolute
		}
	}
	paths := make([]string, 0, len(found))
	for _, path := range found {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func knownExecutableDirectories() []string {
	home, _ := os.UserHomeDir()
	var directories []string
	appendHome := func(relative ...string) {
		if home != "" {
			directories = append(directories, filepath.Join(append([]string{home}, relative...)...))
		}
	}
	switch runtime.GOOS {
	case "windows":
		for _, environment := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
			root := os.Getenv(environment)
			if root != "" {
				directories = append(directories, filepath.Join(root, "Git", "cmd"), filepath.Join(root, "Git", "bin"), filepath.Join(root, "nodejs"), filepath.Join(root, "dotnet"))
			}
		}
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData != "" {
			directories = append(directories,
				filepath.Join(localAppData, "Programs", "Git", "cmd"),
				filepath.Join(localAppData, "Programs", "Git", "bin"),
				filepath.Join(localAppData, "Microsoft", "WinGet", "Links"),
			)
		}
		appendHome(".cargo", "bin")
		appendHome(".dotnet")
		appendHome("scoop", "shims")
		if scoopRoot := os.Getenv("SCOOP"); scoopRoot != "" {
			directories = append(directories, filepath.Join(scoopRoot, "shims"))
		}
	case "darwin":
		directories = append(directories, "/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin")
		appendHome(".local", "bin")
		appendHome("bin")
		appendHome(".cargo", "bin")
		appendHome("go", "bin")
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		directories = append(directories, "/usr/local/bin", "/usr/bin")
		appendHome(".local", "bin")
		appendHome("bin")
		appendHome(".cargo", "bin")
		appendHome("go", "bin")
	default:
		directories = append(directories, "/usr/local/bin", "/usr/bin", "/bin", "/snap/bin")
		appendHome(".local", "bin")
		appendHome("bin")
		appendHome(".cargo", "bin")
		appendHome("go", "bin")
	}
	directories = append(directories, discoveredInstallDirectories(home)...)
	seen := make(map[string]bool, len(directories))
	unique := make([]string, 0, len(directories))
	for _, directory := range directories {
		directory = filepath.Clean(directory)
		key := directory
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if directory == "." || seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, directory)
	}
	return unique
}

func discoveredInstallDirectories(home string) []string {
	cacheParts := []string{runtime.GOOS, home}
	for _, name := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramData", "LOCALAPPDATA", "SCOOP", "SCOOP_GLOBAL"} {
		cacheParts = append(cacheParts, os.Getenv(name))
	}
	cacheKey := strings.Join(cacheParts, "\x00")
	if cached, exists := installDirectoryCache.Load(cacheKey); exists {
		return append([]string(nil), cached.([]string)...)
	}
	directories := scanKnownInstallDirectories(home)
	actual, _ := installDirectoryCache.LoadOrStore(cacheKey, directories)
	return append([]string(nil), actual.([]string)...)
}

func scanKnownInstallDirectories(home string) []string {
	var patterns []string
	appendHomePattern := func(relative ...string) {
		if home != "" {
			patterns = append(patterns, filepath.Join(append([]string{home}, relative...)...))
		}
	}
	switch runtime.GOOS {
	case "windows":
		for _, environment := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
			root := os.Getenv(environment)
			if root == "" {
				continue
			}
			for _, vendor := range []string{"Java", filepath.Join("Eclipse Adoptium"), "Microsoft", "Zulu"} {
				patterns = append(patterns, filepath.Join(root, vendor, "jdk-*", "bin"))
			}
			patterns = append(patterns,
				filepath.Join(root, "Zulu", "zulu-*", "bin"),
				filepath.Join(root, "Amazon Corretto", "jdk*", "bin"),
				filepath.Join(root, "Python*"),
				filepath.Join(root, "Python*", "Scripts"),
			)
		}
		appendHomePattern("AppData", "Local", "Programs", "Python", "Python*")
		appendHomePattern("AppData", "Local", "Programs", "Python", "Python*", "Scripts")
		for _, root := range []string{os.Getenv("SCOOP"), os.Getenv("SCOOP_GLOBAL")} {
			if root != "" {
				patterns = append(patterns, filepath.Join(root, "apps", "*", "*"), filepath.Join(root, "apps", "*", "*", "bin"), filepath.Join(root, "apps", "*", "*", "cmd"))
			}
		}
		if os.Getenv("SCOOP") == "" && home != "" {
			patterns = append(patterns, filepath.Join(home, "scoop", "apps", "*", "*"), filepath.Join(home, "scoop", "apps", "*", "*", "bin"), filepath.Join(home, "scoop", "apps", "*", "*", "cmd"))
		}
		if os.Getenv("SCOOP_GLOBAL") == "" && os.Getenv("ProgramData") != "" {
			root := filepath.Join(os.Getenv("ProgramData"), "scoop")
			patterns = append(patterns, filepath.Join(root, "apps", "*", "*"), filepath.Join(root, "apps", "*", "*", "bin"), filepath.Join(root, "apps", "*", "*", "cmd"))
		}
	case "darwin":
		patterns = append(patterns,
			"/Library/Java/JavaVirtualMachines/*/Contents/Home/bin",
			"/System/Library/Frameworks/JavaVM.framework/Versions/Current/Commands",
		)
		appendHomePattern("Library", "Java", "JavaVirtualMachines", "*", "Contents", "Home", "bin")
		appendHomePattern(".nvm", "versions", "node", "*", "bin")
		appendHomePattern(".pyenv", "versions", "*", "bin")
		appendHomePattern(".asdf", "installs", "*", "*", "bin")
		appendHomePattern(".local", "share", "mise", "installs", "*", "*", "bin")
		patterns = append(patterns, "/usr/local/Cellar/*/*/bin", "/opt/homebrew/Cellar/*/*/bin")
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		patterns = append(patterns, "/usr/local/openjdk*/bin", "/usr/local/lib/nodejs/node-v*/bin")
		appendHomePattern(".nvm", "versions", "node", "*", "bin")
		appendHomePattern(".pyenv", "versions", "*", "bin")
		appendHomePattern(".asdf", "installs", "*", "*", "bin")
		appendHomePattern(".local", "share", "mise", "installs", "*", "*", "bin")
	default:
		patterns = append(patterns, "/usr/lib/jvm/*/bin", "/usr/lib/jvm/*/jre/bin", "/usr/local/lib/nodejs/node-v*/bin")
		appendHomePattern(".nvm", "versions", "node", "*", "bin")
		appendHomePattern(".pyenv", "versions", "*", "bin")
		appendHomePattern(".asdf", "installs", "*", "*", "bin")
		appendHomePattern(".local", "share", "mise", "installs", "*", "*", "bin")
	}

	directories := make([]string, 0, min(len(patterns), maxDiscoveredInstallDirectories))
	seen := make(map[string]bool)
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || !info.IsDir() {
				continue
			}
			match = filepath.Clean(match)
			if seen[match] {
				continue
			}
			seen[match] = true
			directories = append(directories, match)
			if len(directories) >= maxDiscoveredInstallDirectories {
				return directories
			}
		}
	}
	return directories
}

func installedPackagesFor(manifest tool.Manifest, operatingSystem tool.OperatingSystem, managers []packagemanager.Detected, installed []packagemanager.InstalledPackage) []PackageOwnership {
	available := make(map[tool.PackageManager]bool, len(managers))
	for _, manager := range managers {
		available[manager.Manager] = true
	}
	packages := make([]PackageOwnership, 0)
	seen := make(map[string]bool)
	for _, installer := range manifest.Installers {
		if installer.Platform != operatingSystem || !available[installer.Manager] {
			continue
		}
		for _, current := range installed {
			if current.Manager != installer.Manager || !strings.EqualFold(current.ID, installer.PackageID) {
				continue
			}
			key := strings.Join([]string{string(current.Manager), strings.ToLower(current.ID), current.Version, strings.ToLower(current.Source)}, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			source := installer.Source
			if current.Source != "" {
				source = current.Source
			}
			packages = append(packages, PackageOwnership{Manager: current.Manager, ID: current.ID, Version: current.Version, Source: source})
		}
	}
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Manager != packages[j].Manager {
			return packages[i].Manager < packages[j].Manager
		}
		if packages[i].ID != packages[j].ID {
			return packages[i].ID < packages[j].ID
		}
		if packages[i].Version != packages[j].Version {
			return packages[i].Version < packages[j].Version
		}
		return packages[i].Source < packages[j].Source
	})
	return packages
}

func inspectVersion(parent context.Context, path, executable string, args []string) Instance {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	stdout := &boundedBuffer{limit: 8192}
	stderr := &boundedBuffer{limit: 8192}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	output := strings.TrimSpace(stdout.String())
	if output == "" {
		output = strings.TrimSpace(stderr.String())
	}
	output = sanitizeOutput(output)
	instance := Instance{Executable: executable, Path: path, Version: output}
	if err != nil {
		instance.State = Broken
		instance.Diagnostic = sanitizeOutput(err.Error())
		if ctx.Err() == context.DeadlineExceeded {
			instance.Diagnostic = "version check timed out after 3 seconds"
		}
		return instance
	}
	if output == "" {
		instance.State = Unknown
		instance.Diagnostic = "version command succeeded without reporting a version"
		return instance
	}
	instance.State = Healthy
	return instance
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	originalLength := len(value)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.Buffer.Write(value)
	}
	return originalLength, nil
}

func sanitizeOutput(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 {
			return r
		}
		return -1
	}, value)
	if len(value) > 240 {
		return value[:240] + "…"
	}
	return value
}

func (i Inventory) ToolIDs() []string {
	ids := make([]string, 0, len(i.Tools))
	for id := range i.Tools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (i Inventory) HasManager(manager tool.PackageManager) bool {
	for _, detected := range i.Managers {
		if detected.Manager == manager {
			return true
		}
	}
	return false
}

func (i Inventory) IsPresent(id string) bool {
	item, exists := i.Tools[id]
	if !exists {
		return false
	}
	switch item.State {
	case Healthy, Outdated, Partial, Conflicting, Unknown:
		return true
	default:
		return false
	}
}

func (i Inventory) ManagerOrder() []tool.PackageManager {
	order := make([]tool.PackageManager, 0, len(i.Managers))
	for _, manager := range i.Managers {
		order = append(order, manager.Manager)
	}
	return order
}
