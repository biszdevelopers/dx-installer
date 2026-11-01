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
	if got := len(items.Tools()); got != 44 {
		t.Fatalf("embedded tool count = %d, want 44", got)
	}
	if got := len(items.Profiles()); got != 10 {
		t.Fatalf("embedded profile count = %d, want 10", got)
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
