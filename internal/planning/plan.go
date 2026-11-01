package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"developers-club-dx/internal/adapters/packagemanager"
	registryadapter "developers-club-dx/internal/adapters/registry"
	"developers-club-dx/internal/artifacts"
	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/pathconfig"
	"developers-club-dx/internal/sources"
)

type Action string

const (
	Install  Action = "install"
	Preserve Action = "preserve"
	Skip     Action = "skip"
)

type Severity string

const (
	Warning  Severity = "warning"
	Blocking Severity = "blocking"
)

type Issue struct {
	Severity Severity
	ToolID   string
	Message  string
}

type Item struct {
	ToolID             string
	Name               string
	Dependencies       []string
	Action             Action
	ExistingState      detection.State
	ExistingPackages   []detection.PackageOwnership
	Manager            tool.PackageManager
	PackageID          string
	Source             string
	Scope              tool.Scope
	Command            packagemanager.Command
	RequiresPrivilege  bool
	Artifact           *tool.Artifact
	Version            string
	ApproximateSize    int64
	InstallPath        string
	PathChanges        []pathconfig.Change
	RegionalFallback   bool
	CustomSourceURL    string
	OfflineArchivePath string
	RegistryInstaller  *tool.RegistryInstaller
}

type Plan struct {
	Selected              []string
	Items                 []Item
	Issues                []Issue
	Blocked               bool
	TransactionDisclosure string `json:"transaction_disclosure,omitempty"`
}

const TransactionDisclosure = "DX has not resolved a candidate package version or simulated the package-manager transaction. Dependencies or other affected packages may be added; review any manager confirmation before accepting."

func Build(items *catalog.Catalog, current detection.Inventory, selected []string) (Plan, error) {
	return BuildWithOptions(items, current, selected, Options{})
}

type Options struct {
	PreferredManager      tool.PackageManager
	AllowRegionalFallback bool
	CustomSources         map[string]string
	OfflineArchives       map[string]string
	RegistrySources       map[tool.RegistryEcosystem]string
}

func BuildWithOptions(items *catalog.Catalog, current detection.Inventory, selected []string, options Options) (Plan, error) {
	plan := Plan{}
	if len(selected) == 0 {
		return plan, fmt.Errorf("select at least one tool")
	}
	selectedSet := make(map[string]bool, len(selected))
	for _, id := range selected {
		if _, exists := items.Tool(id); !exists {
			return plan, fmt.Errorf("unknown tool %q", id)
		}
		selectedSet[id] = true
	}
	closure, err := dependencyOrder(items, selectedSet)
	if err != nil {
		return plan, err
	}
	plan.Selected = append([]string(nil), selected...)
	sort.Strings(plan.Selected)
	closureSet := make(map[string]bool, len(closure))
	for _, id := range closure {
		closureSet[id] = true
	}
	managerOrder := current.ManagerOrder()
	if options.PreferredManager != "" {
		managerOrder = PreferredManagerOrder(managerOrder, options.PreferredManager)
	}
	plan.checkConflicts(items, current, closure, closureSet)
	for _, id := range closure {
		manifest, _ := items.Tool(id)
		entry, found := current.Tools[id]
		if !found {
			plan.add(Blocking, id, "inventory has no result for this catalog entry")
			continue
		}
		if !manifest.Supports(current.Platform.OS, current.Platform.Architecture) || entry.State == detection.Unsupported {
			plan.add(Blocking, id, "the current operating system and architecture are not supported by this tool")
			continue
		}
		switch entry.State {
		case detection.Healthy, detection.Outdated:
			plan.Items = append(plan.Items, Item{ToolID: id, Name: manifest.Name, Dependencies: append([]string(nil), manifest.Dependencies...), Action: Preserve, ExistingState: entry.State, ExistingPackages: append([]detection.PackageOwnership(nil), entry.Packages...)})
		case detection.Unknown:
			plan.Items = append(plan.Items, Item{ToolID: id, Name: manifest.Name, Dependencies: append([]string(nil), manifest.Dependencies...), Action: Preserve, ExistingState: entry.State, ExistingPackages: append([]detection.PackageOwnership(nil), entry.Packages...)})
			plan.add(Warning, id, "an existing installation could not be verified and will be preserved")
		case detection.Broken, detection.Partial, detection.Conflicting:
			plan.Items = append(plan.Items, Item{ToolID: id, Name: manifest.Name, Dependencies: append([]string(nil), manifest.Dependencies...), Action: Preserve, ExistingState: entry.State})
			plan.add(Blocking, id, fmt.Sprintf("an existing %s installation needs manual review; DX will not replace it", entry.State))
		case detection.Missing:
			customURL := strings.TrimSpace(options.CustomSources[id])
			offlinePath := strings.TrimSpace(options.OfflineArchives[id])
			if customURL != "" || offlinePath != "" {
				artifact, exists := manifest.ArtifactFor(current.Platform.OS, current.Platform.Architecture)
				if !exists {
					plan.add(Blocking, id, "custom and offline sources require a platform-specific artifact pinned in the catalog")
					continue
				}
				plan.addArtifact(current, id, manifest, entry, artifact, false, customURL, offlinePath)
				continue
			}
			registryInstaller, registrySource, registryRequested := requestedRegistry(manifest, current.Platform.OS, options.RegistrySources)
			if registryRequested {
				if !registryInstallerAvailableInClosure(*registryInstaller, closureSet) {
					plan.add(Blocking, id, "the selected registry ecosystem is not configured for this tool")
					continue
				}
				plan.addRegistry(current, id, manifest, entry, *registryInstaller, registrySource)
				continue
			}
			installer, managerAvailable := manifest.InstallerFor(current.Platform.OS, managerOrder)
			if !managerAvailable {
				if artifact, exists := manifest.ArtifactFor(current.Platform.OS, current.Platform.Architecture); exists {
					plan.addArtifact(current, id, manifest, entry, artifact, options.AllowRegionalFallback, "", "")
					continue
				}
				if registryInstaller, exists := firstRegistryInstaller(manifest, current.Platform.OS); exists {
					source := registryInstaller.Source
					if override := options.RegistrySources[registryInstaller.Ecosystem]; override != "" {
						source = override
					}
					plan.addRegistry(current, id, manifest, entry, registryInstaller, source)
					continue
				}
				plan.add(Blocking, id, "no supported installer is available through detected package managers and no verified archive exists")
				continue
			}
			command, err := packagemanager.BuildCommand(installer, current.Platform.OS)
			if err != nil {
				plan.add(Blocking, id, err.Error())
				continue
			}
			plan.Items = append(plan.Items, Item{
				ToolID:            id,
				Name:              manifest.Name,
				Dependencies:      append([]string(nil), manifest.Dependencies...),
				Action:            Install,
				ExistingState:     entry.State,
				Manager:           installer.Manager,
				PackageID:         installer.PackageID,
				Source:            installer.Source,
				Scope:             installer.Scope,
				Command:           command,
				RequiresPrivilege: command.RequiresPrivilege,
			})
		default:
			plan.add(Blocking, id, fmt.Sprintf("installation state %q is not safe to modify", entry.State))
		}
	}
	registryEcosystems := make([]string, 0, len(options.RegistrySources))
	for ecosystem := range options.RegistrySources {
		registryEcosystems = append(registryEcosystems, string(ecosystem))
	}
	sort.Strings(registryEcosystems)
	for _, ecosystemText := range registryEcosystems {
		ecosystem := tool.RegistryEcosystem(ecosystemText)
		used := false
		for _, id := range closure {
			manifest, _ := items.Tool(id)
			entry := current.Tools[id]
			if entry.State == detection.Missing && options.CustomSources[id] == "" && options.OfflineArchives[id] == "" {
				if _, exists := manifest.RegistryInstallerFor(current.Platform.OS, ecosystem); exists {
					used = true
					break
				}
			}
		}
		if !used {
			plan.add(Warning, "", fmt.Sprintf("registry override for %q is unused because no missing selected tool uses it", ecosystem))
		}
	}
	for _, item := range plan.Items {
		if item.Action == Install {
			if item.Artifact == nil {
				plan.TransactionDisclosure = TransactionDisclosure
				break
			}
		}
	}
	return plan, nil
}

func requestedRegistry(manifest tool.Manifest, operatingSystem tool.OperatingSystem, sources map[tool.RegistryEcosystem]string) (*tool.RegistryInstaller, string, bool) {
	for _, installer := range manifest.RegistryInstallers {
		if installer.Platform == operatingSystem {
			if source, exists := sources[installer.Ecosystem]; exists {
				installerCopy := installer
				return &installerCopy, source, true
			}
		}
	}
	return nil, "", false
}

func registryInstallerAvailableInClosure(installer tool.RegistryInstaller, closure map[string]bool) bool {
	return closure[tool.RegistryRuntimeTool(installer.Ecosystem)]
}

func firstRegistryInstaller(manifest tool.Manifest, operatingSystem tool.OperatingSystem) (tool.RegistryInstaller, bool) {
	for _, ecosystem := range []tool.RegistryEcosystem{tool.NPMRegistry, tool.PyPIRegistry, tool.GoModuleRegistry} {
		if installer, exists := manifest.RegistryInstallerFor(operatingSystem, ecosystem); exists {
			return installer, true
		}
	}
	return tool.RegistryInstaller{}, false
}

func (p *Plan) addRegistry(current detection.Inventory, id string, manifest tool.Manifest, entry detection.Tool, installer tool.RegistryInstaller, source string) {
	if source == "" {
		source = installer.Source
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		p.add(Blocking, id, "could not determine an absolute user home directory for the registry installation")
		return
	}
	installPath, err := artifacts.InstallationPath(current.Platform.OS, id, installer.Version, home)
	if err != nil {
		p.add(Blocking, id, err.Error())
		return
	}
	if _, err := os.Lstat(installPath); err == nil {
		p.add(Blocking, id, "the versioned user-level installation target already exists and will not be replaced")
		return
	} else if !os.IsNotExist(err) {
		p.add(Blocking, id, "the versioned user-level installation target could not be inspected: "+err.Error())
		return
	}
	command, pathEntries, err := registryadapter.BuildCommand(installer, installPath, source)
	if err != nil {
		p.add(Blocking, id, err.Error())
		return
	}
	pathChanges, err := pathconfig.Plan(current.Platform, home, pathEntries, os.Getenv("PATH"))
	if err != nil {
		p.add(Blocking, id, "could not safely plan PATH updates: "+err.Error())
		return
	}
	installerCopy := installer
	p.Items = append(p.Items, Item{
		ToolID:            id,
		Name:              manifest.Name,
		Dependencies:      append([]string(nil), manifest.Dependencies...),
		Action:            Install,
		ExistingState:     entry.State,
		PackageID:         installer.PackageID,
		Source:            source,
		Scope:             tool.UserScope,
		Command:           command,
		RegistryInstaller: &installerCopy,
		Version:           installer.Version,
		InstallPath:       installPath,
		PathChanges:       pathChanges,
	})
}

func (p *Plan) addArtifact(current detection.Inventory, id string, manifest tool.Manifest, entry detection.Tool, artifact tool.Artifact, allowRegionalFallback bool, customURL, offlinePath string) {
	if customURL != "" && offlinePath != "" {
		p.add(Blocking, id, "choose either a custom HTTPS source or an offline archive, not both")
		return
	}
	if customURL != "" {
		if err := sources.ValidateRemoteSource(sources.RemoteSource{Name: id + " custom source", Kind: sources.Custom, URL: customURL, SHA256: artifact.SHA256}); err != nil {
			p.add(Blocking, id, "custom source is invalid: "+err.Error())
			return
		}
	}
	if offlinePath != "" {
		if !filepath.IsAbs(offlinePath) {
			p.add(Blocking, id, "offline archive path must be absolute")
			return
		}
		info, err := os.Lstat(offlinePath)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			p.add(Blocking, id, "offline archive must be an existing regular, non-symlink file")
			return
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		p.add(Blocking, id, "could not determine an absolute user home directory for the user-level archive installation")
		return
	}
	installPath, err := artifacts.InstallationPath(current.Platform.OS, id, artifact.Version, home)
	if err != nil {
		p.add(Blocking, id, err.Error())
		return
	}
	if _, err := os.Lstat(installPath); err == nil {
		p.add(Blocking, id, "the versioned user-level installation target already exists and will not be replaced")
		return
	} else if !os.IsNotExist(err) {
		p.add(Blocking, id, "the versioned user-level installation target could not be inspected: "+err.Error())
		return
	}
	manifestCopy := artifact
	source := artifact.URL
	if customURL != "" {
		source = customURL
	} else if offlinePath != "" {
		source = "offline local archive"
	}
	pathDirectories := make([]string, 0, len(artifact.PathEntries))
	for _, relative := range artifact.PathEntries {
		pathDirectories = append(pathDirectories, filepath.Join(installPath, filepath.FromSlash(relative)))
	}
	pathChanges, err := pathconfig.Plan(current.Platform, home, pathDirectories, os.Getenv("PATH"))
	if err != nil {
		p.add(Blocking, id, "could not safely plan PATH updates: "+err.Error())
		return
	}
	p.Items = append(p.Items, Item{
		ToolID:             id,
		Name:               manifest.Name,
		Dependencies:       append([]string(nil), manifest.Dependencies...),
		Action:             Install,
		ExistingState:      entry.State,
		Source:             source,
		Artifact:           &manifestCopy,
		Version:            artifact.Version,
		ApproximateSize:    artifact.ApproximateSize,
		InstallPath:        installPath,
		PathChanges:        pathChanges,
		RegionalFallback:   allowRegionalFallback && artifact.RegionalURL != "" && customURL == "" && offlinePath == "",
		CustomSourceURL:    customURL,
		OfflineArchivePath: offlinePath,
	})
}

func PreferredManagerOrder(managers []tool.PackageManager, preferred tool.PackageManager) []tool.PackageManager {
	ordered := append([]tool.PackageManager(nil), managers...)
	if preferred == "" {
		return ordered
	}
	for index, manager := range ordered {
		if manager == preferred {
			prioritized := make([]tool.PackageManager, 0, len(ordered))
			prioritized = append(prioritized, preferred)
			prioritized = append(prioritized, ordered[:index]...)
			prioritized = append(prioritized, ordered[index+1:]...)
			return prioritized
		}
	}
	return ordered
}

func dependencyOrder(items *catalog.Catalog, selected map[string]bool) ([]string, error) {
	state := make(map[string]uint8)
	order := make([]string, 0)
	var visit func(string, []string) error
	visit = func(id string, chain []string) error {
		if state[id] == 1 {
			return fmt.Errorf("dependency cycle while planning: %v -> %s", chain, id)
		}
		if state[id] == 2 {
			return nil
		}
		manifest, exists := items.Tool(id)
		if !exists {
			return fmt.Errorf("tool %q references a missing dependency", id)
		}
		state[id] = 1
		dependencies := append([]string(nil), manifest.Dependencies...)
		sort.Strings(dependencies)
		for _, dependency := range dependencies {
			if err := visit(dependency, append(chain, id)); err != nil {
				return err
			}
		}
		state[id] = 2
		order = append(order, id)
		return nil
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := visit(id, nil); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func (p *Plan) checkConflicts(items *catalog.Catalog, current detection.Inventory, closure []string, closureSet map[string]bool) {
	seen := make(map[string]bool)
	for _, id := range closure {
		manifest, _ := items.Tool(id)
		for _, conflict := range manifest.Conflicts {
			if closureSet[conflict] {
				key := orderedPair(id, conflict)
				if !seen[key] {
					p.add(Blocking, id, fmt.Sprintf("selected tool %q conflicts with selected tool %q", id, conflict))
					seen[key] = true
				}
			}
			if existing, exists := current.Tools[conflict]; exists && existing.State != detection.Missing && existing.State != detection.Unsupported {
				key := orderedPair(id, conflict)
				if !seen[key] {
					p.add(Blocking, id, fmt.Sprintf("existing tool %q conflicts with this selection and will be preserved", conflict))
					seen[key] = true
				}
			}
		}
	}
}

func orderedPair(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "/" + b
}

func (p *Plan) add(severity Severity, id, message string) {
	p.Issues = append(p.Issues, Issue{Severity: severity, ToolID: id, Message: message})
	if severity == Blocking {
		p.Blocked = true
	}
}

func (p Plan) InstallItems() []Item {
	items := make([]Item, 0, len(p.Items))
	for _, item := range p.Items {
		if item.Action == Install {
			items = append(items, item)
		}
	}
	return items
}
