package catalog

import (
	"testing"

	"testing/fstest"
)

func TestEmbeddedCatalogPassesGraphAndProfileValidation(t *testing.T) {
	items, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(items.Tools()); got == 0 {
		t.Fatal("embedded catalog contains no tools")
	}
	if got := len(items.Profiles()); got != 18 {
		t.Fatalf("embedded profile count = %d, want 18", got)
	}
	for _, id := range []string{"mobile", "embedded", "devops", "security", "api", "swift", "dotnet"} {
		if _, exists := items.Profile(id); !exists {
			t.Errorf("embedded catalog is missing the %q profile", id)
		}
	}

	complete, exists := items.Profile("complete")
	if !exists {
		t.Fatal("embedded catalog is missing the complete profile")
	}
	selected := make(map[string]bool, len(complete.Tools))
	for _, id := range complete.Tools {
		selected[id] = true
	}
	for _, id := range items.ToolIDs() {
		if !selected[id] {
			t.Errorf("complete profile is missing catalog tool %q", id)
		}
	}
	if len(complete.Tools) != len(items.Tools()) {
		t.Errorf("complete profile contains %d tools; catalog contains %d", len(complete.Tools), len(items.Tools()))
	}
}

func TestCatalogRejectsUnknownFieldsAndDependencyCycles(t *testing.T) {
	unknown := catalogFS(`
schema_version: 1
id: sample
name: Sample
category: language.general
description: Sample tool.
version:
  channel: stable
  upgrade_policy: never_automatically
executables: [sample]
version_args: [--version]
platforms:
  windows: [amd64]
installers:
  - platform: windows
    manager: winget
    package_id: Example.Sample
    scope: user
    source: official
unknown: rejected
`)
	if _, err := Load(unknown); err == nil {
		t.Fatal("unknown manifest field was accepted")
	}

	cycle := fstest.MapFS{
		"data/tools/alpha.yaml": {Data: []byte(manifestYAML("alpha", "beta"))},
		"data/tools/beta.yaml":  {Data: []byte(manifestYAML("beta", "alpha"))},
	}
	if _, err := Load(cycle); err == nil {
		t.Fatal("dependency cycle was accepted")
	}
}

func TestCatalogRejectsUnknownProfileToolAndInvalidCategory(t *testing.T) {
	unknownProfileTool := catalogFS(manifestYAML("sample", ""))
	unknownProfileTool["data/profiles/sample.yaml"] = &fstest.MapFile{Data: []byte(`
schema_version: 1
id: sample
name: Sample profile
description: Invalid reference.
tools: [not-in-catalog]
`)}
	if _, err := Load(unknownProfileTool); err == nil {
		t.Fatal("profile reference to a missing tool was accepted")
	}

	invalidCategory := manifestYAML("sample", "")
	invalidCategory = replaceOnce(invalidCategory, "category: language.general", "category: General")
	if _, err := Load(catalogFS(invalidCategory)); err == nil {
		t.Fatal("noncanonical category was accepted")
	}
}

func catalogFS(manifest string) fstest.MapFS {
	return fstest.MapFS{"data/tools/sample.yaml": {Data: []byte(manifest)}}
}

func manifestYAML(id, dependency string) string {
	manifest := `schema_version: 1
id: ` + id + `
name: ` + id + `
category: language.general
description: Sample tool.
version:
  channel: stable
  upgrade_policy: never_automatically
executables: [` + id + `]
version_args: [--version]
`
	if dependency != "" {
		manifest += "dependencies: [" + dependency + "]\n"
	}
	manifest += `platforms:
  windows: [amd64]
installers:
  - platform: windows
    manager: winget
    package_id: Example.Sample
    scope: user
    source: official
`
	return manifest
}

func replaceOnce(value, old, replacement string) string {
	for index := 0; index+len(old) <= len(value); index++ {
		if value[index:index+len(old)] == old {
			return value[:index] + replacement + value[index+len(old):]
		}
	}
	return value
}
