package catalog

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"

	"developers-club-dx/internal/domain/tool"
	"gopkg.in/yaml.v3"
)

//go:embed data/tools/*.yaml data/profiles/*.yaml
var embedded embed.FS

type Catalog struct {
	tools      map[string]tool.Manifest
	toolIDs    []string
	profiles   map[string]tool.Profile
	profileIDs []string
}

var categoryPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)+$`)

func LoadEmbedded() (*Catalog, error) {
	return Load(embedded)
}

func Load(source fs.FS) (*Catalog, error) {
	toolFiles, err := fs.Glob(source, "data/tools/*.yaml")
	if err != nil {
		return nil, fmt.Errorf("list tool manifests: %w", err)
	}
	profileFiles, err := fs.Glob(source, "data/profiles/*.yaml")
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	if len(toolFiles) == 0 {
		return nil, errors.New("catalog contains no tool manifests")
	}

	catalog := &Catalog{
		tools:    make(map[string]tool.Manifest, len(toolFiles)),
		profiles: make(map[string]tool.Profile, len(profileFiles)),
	}
	for _, name := range toolFiles {
		var manifest tool.Manifest
		if err := decodeFile(source, name, &manifest); err != nil {
			return nil, err
		}
		if err := manifest.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if !categoryPattern.MatchString(manifest.Category) {
			return nil, fmt.Errorf("%s: category %q must use dotted lowercase names", name, manifest.Category)
		}
		if _, exists := catalog.tools[manifest.ID]; exists {
			return nil, fmt.Errorf("%s: duplicate tool id %q", name, manifest.ID)
		}
		catalog.tools[manifest.ID] = manifest
		catalog.toolIDs = append(catalog.toolIDs, manifest.ID)
	}
	for _, name := range profileFiles {
		var profile tool.Profile
		if err := decodeFile(source, name, &profile); err != nil {
			return nil, err
		}
		if err := validateProfile(profile, catalog.tools); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if _, exists := catalog.profiles[profile.ID]; exists {
			return nil, fmt.Errorf("%s: duplicate profile id %q", name, profile.ID)
		}
		catalog.profiles[profile.ID] = profile
		catalog.profileIDs = append(catalog.profileIDs, profile.ID)
	}
	if err := catalog.validateGraph(); err != nil {
		return nil, err
	}
	sort.Strings(catalog.toolIDs)
	sort.Strings(catalog.profileIDs)
	return catalog, nil
}

func decodeFile(source fs.FS, name string, target any) error {
	file, err := source.Open(name)
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode %s: multiple YAML documents are not allowed", name)
		}
		return fmt.Errorf("decode trailing content in %s: %w", name, err)
	}
	return nil
}

func validateProfile(profile tool.Profile, tools map[string]tool.Manifest) error {
	if profile.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1, got %d", profile.SchemaVersion)
	}
	if !profileIDPattern.MatchString(profile.ID) {
		return fmt.Errorf("invalid profile id %q", profile.ID)
	}
	if profile.Name == "" || profile.Description == "" || len(profile.Tools) == 0 {
		return fmt.Errorf("name, description, and at least one tool are required")
	}
	seen := make(map[string]bool, len(profile.Tools))
	for _, id := range profile.Tools {
		if _, exists := tools[id]; !exists {
			return fmt.Errorf("unknown tool %q", id)
		}
		if seen[id] {
			return fmt.Errorf("duplicate tool %q", id)
		}
		seen[id] = true
	}
	return nil
}

var profileIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func (c *Catalog) validateGraph() error {
	for _, id := range c.toolIDs {
		manifest := c.tools[id]
		for _, dependency := range manifest.Dependencies {
			if _, exists := c.tools[dependency]; !exists {
				return fmt.Errorf("tool %q references unknown dependency %q", id, dependency)
			}
		}
		for _, conflict := range manifest.Conflicts {
			if _, exists := c.tools[conflict]; !exists {
				return fmt.Errorf("tool %q references unknown conflict %q", id, conflict)
			}
		}
	}
	state := make(map[string]uint8, len(c.tools))
	var visit func(string, []string) error
	visit = func(id string, chain []string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("dependency cycle: %v -> %s", chain, id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, dependency := range c.tools[id].Dependencies {
			if err := visit(dependency, append(chain, id)); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, id := range c.toolIDs {
		if err := visit(id, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *Catalog) Tool(id string) (tool.Manifest, bool) {
	manifest, exists := c.tools[id]
	return manifest, exists
}

func (c *Catalog) Tools() []tool.Manifest {
	tools := make([]tool.Manifest, 0, len(c.toolIDs))
	for _, id := range c.toolIDs {
		tools = append(tools, c.tools[id])
	}
	return tools
}

func (c *Catalog) ToolIDs() []string {
	return append([]string(nil), c.toolIDs...)
}

func (c *Catalog) Profile(id string) (tool.Profile, bool) {
	profile, exists := c.profiles[id]
	return profile, exists
}

func (c *Catalog) Profiles() []tool.Profile {
	profiles := make([]tool.Profile, 0, len(c.profileIDs))
	for _, id := range c.profileIDs {
		profiles = append(profiles, c.profiles[id])
	}
	return profiles
}
