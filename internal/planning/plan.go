package planning

import (
	"fmt"
	"sort"

	"developers-club-dx/internal/adapters/packagemanager"
	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/domain/tool"
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
	Manager           tool.PackageManager
	PackageID         string
	Source            string
	Scope             tool.Scope
	Command           packagemanager.Command
	RequiresPrivilege bool
}

type Plan struct {
	Selected []string
	Items    []Item
	Issues   []Issue
	Blocked  bool
}

func Build(items *catalog.Catalog, current detection.Inventory, selected []string) (Plan, error) {
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
			plan.Items = append(plan.Items, Item{ToolID: id, Name: manifest.Name, Dependencies: append([]string(nil), manifest.Dependencies...), Action: Preserve, ExistingState: entry.State})
		case detection.Unknown:
			plan.Items = append(plan.Items, Item{ToolID: id, Name: manifest.Name, Dependencies: append([]string(nil), manifest.Dependencies...), Action: Preserve, ExistingState: entry.State})
			plan.add(Warning, id, "an existing installation could not be verified and will be preserved")
		case detection.Broken, detection.Partial, detection.Conflicting:
			plan.Items = append(plan.Items, Item{ToolID: id, Name: manifest.Name, Dependencies: append([]string(nil), manifest.Dependencies...), Action: Preserve, ExistingState: entry.State})
			plan.add(Blocking, id, fmt.Sprintf("an existing %s installation needs manual review; DX will not replace it", entry.State))
		case detection.Missing:
			installer, exists := manifest.InstallerFor(current.Platform.OS, current.ManagerOrder())
			if !exists {
				plan.add(Blocking, id, "no supported installer is available through the detected package managers")
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
	return plan, nil
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
