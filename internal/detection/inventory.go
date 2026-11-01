package detection

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
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
	Executable string
	Path       string
	Version    string
	State      State
	Diagnostic string
}

type Tool struct {
	ID        string
	Name      string
	Category  string
	State     State
	Instances []Instance
}

type Inventory struct {
	Platform platform.Info
	Managers []packagemanager.Detected
	Tools    map[string]Tool
}

type scanResult struct {
	manifest  tool.Manifest
	instances []Instance
	state     State
}

func Scan(ctx context.Context, items *catalog.Catalog, machine platform.Info) (Inventory, error) {
	inventory := Inventory{
		Platform: machine,
		Managers: packagemanager.Detect(machine.OS, machine.Distribution),
		Tools:    make(map[string]Tool),
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
				instances, state := scanTool(ctx, manifest, machine)
				results <- scanResult{manifest: manifest, instances: instances, state: state}
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
		}
	}
	if err := ctx.Err(); err != nil {
		return inventory, err
	}
	return inventory, nil
}

func scanTool(ctx context.Context, manifest tool.Manifest, machine platform.Info) ([]Instance, State) {
	if !manifest.Supports(machine.OS, machine.Architecture) {
		return nil, Unsupported
	}
	paths := make(map[string]string)
	for _, executable := range manifest.Executables {
		path, err := exec.LookPath(executable)
		if err != nil {
			continue
		}
		if cleanPath, pathErr := filepath.Abs(path); pathErr == nil {
			path = cleanPath
		}
		paths[path] = executable
	}
	if len(paths) == 0 {
		return nil, Missing
	}
	instances := make([]Instance, 0, len(paths))
	for path, executable := range paths {
		instances = append(instances, inspectVersion(ctx, path, executable, manifest.VersionArgs))
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].Path < instances[j].Path })
	for _, instance := range instances {
		if instance.State == Healthy {
			return instances, Healthy
		}
	}
	if ctx.Err() != nil {
		return instances, Unknown
	}
	return instances, Broken
}

func inspectVersion(parent context.Context, path, executable string, args []string) Instance {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
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
