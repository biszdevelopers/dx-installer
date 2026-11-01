package packagemanager

import (
	"path/filepath"
	"reflect"
	"testing"

	"developers-club-dx/internal/domain/tool"
)

func TestResolveInstallerPathsKeepsHomebrewEntriesUnderFormulaPrefix(t *testing.T) {
	installer := tool.Installer{Manager: tool.Homebrew, PathEntries: []string{"bin", "libexec/bin"}}
	prefix := filepath.Join(t.TempDir(), "opt", "python@3.15")
	got, err := ResolveInstallerPaths(installer, prefix)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(prefix, "bin"), filepath.Join(prefix, "libexec", "bin")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved PATH entries = %q, want %q", got, want)
	}
}

func TestResolveInstallerPathsRejectsInvalidPrefixesAndTraversal(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		installer tool.Installer
		prefix    string
	}{
		{name: "relative prefix", installer: tool.Installer{Manager: tool.Homebrew, PathEntries: []string{"bin"}}, prefix: "relative"},
		{name: "path traversal", installer: tool.Installer{Manager: tool.Homebrew, PathEntries: []string{"../bin"}}, prefix: t.TempDir()},
		{name: "absolute entry", installer: tool.Installer{Manager: tool.Homebrew, PathEntries: []string{filepath.Join(t.TempDir(), "bin")}}, prefix: t.TempDir()},
		{name: "wrong manager", installer: tool.Installer{Manager: tool.Winget, PathEntries: []string{"bin"}}, prefix: t.TempDir()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ResolveInstallerPaths(testCase.installer, testCase.prefix); err == nil {
				t.Fatal("invalid formula-relative PATH entry was accepted")
			}
		})
	}
}
