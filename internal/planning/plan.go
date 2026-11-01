package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"developers-club-dx/internal/adapters/packagemanager"
	"developers-club-dx/internal/artifacts"
	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/pathconfig"
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
	ToolID            string
	Name              string
	Dependencies      []string
	Action            Action
	ExistingState     detection.State
	ExistingPackages  []detection.PackageOwnership
	Manager           tool.PackageManager
	PackageID         string
	Source            string
	Scope             tool.Scope
	Command           packagemanager.Command
	RequiresPrivilege bool
	Artifact          *tool.Artifact
	Version           string
	ApproximateSize   int64
	InstallPath       string
	PathChanges       []pathconfig.Change
	RegionalFallback  bool
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
			installer, managerAvailable := manifest.InstallerFor(current.Platform.OS, managerOrder)
			if !managerAvailable {
				if artifact, exists := manifest.ArtifactFor(current.Platform.OS, current.Platform.Architecture); exists {
					plan.addArtifact(current, id, manifest, entry, artifact, options.AllowRegionalFallback)
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

func (p *Plan) addArtifact(current detection.Inventory, id string, manifest tool.Manifest, entry detection.Tool, artifact tool.Artifact, allowRegionalFallback bool) {
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
		ToolID:           id,
		Name:             manifest.Name,
		Dependencies:     append([]string(nil), manifest.Dependencies...),
		Action:           Install,
		ExistingState:    entry.State,
		Source:           artifact.URL,
		Artifact:         &manifestCopy,
		Version:          artifact.Version,
		ApproximateSize:  artifact.ApproximateSize,
		InstallPath:      installPath,
		PathChanges:      pathChanges,
		RegionalFallback: allowRegionalFallback && artifact.RegionalURL != "",
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
