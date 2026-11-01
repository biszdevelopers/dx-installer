package sources

import (
	"reflect"
	"testing"

	"developers-club-dx/internal/domain/tool"
)

func TestParseRegistryAssignmentsValidatesAndNormalizesURLs(t *testing.T) {
	got, err := ParseRegistryAssignments([]string{
		"npm=https://registry.example/npm/",
		"pypi=https://packages.example/simple",
		"go-modules=https://proxy.example",
		"cargo=https://index.example/",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := RegistryURLs{
		tool.NPMRegistry:      "https://registry.example/npm",
		tool.PyPIRegistry:     "https://packages.example/simple",
		tool.GoModuleRegistry: "https://proxy.example",
		tool.CargoRegistry:    "https://index.example",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registries = %#v, want %#v", got, want)
	}
}

func TestParseRegistryAssignmentsRejectsUnsafeOrAmbiguousValues(t *testing.T) {
	for _, entries := range [][]string{
		{"npm=http://registry.example"},
		{"npm=https://user:pass@registry.example"},
		{"npm=https://registry.example?token=value"},
		{"npm=https://registry.example#fragment"},
		{"npm=https://registry.example", "npm=https://mirror.example"},
		{"unknown=https://registry.example"},
		{"npm"},
	} {
		t.Run(entries[0], func(t *testing.T) {
			if _, err := ParseRegistryAssignments(entries); err == nil {
				t.Fatalf("unsafe registry values were accepted: %q", entries)
			}
		})
	}
}
