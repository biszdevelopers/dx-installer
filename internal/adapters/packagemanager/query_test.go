package packagemanager

import (
	"context"
	"strings"
	"testing"

	"developers-club-dx/internal/domain/tool"
)

func TestParsePackageInventory(t *testing.T) {
	tests := []struct {
		name    string
		manager tool.PackageManager
		output  string
		want    []packageRecord
	}{
		{name: "dpkg filters removed packages keeps partial installs and strips multiarch suffix", manager: tool.APT, output: "ii\tcmake:amd64\t3.28.3-1\niU\tpartial-tool\t2.0-1\nrc\tremoved-tool\t1.0-1\n", want: []packageRecord{{ID: "cmake", Version: "3.28.3-1"}, {ID: "partial-tool", Version: "2.0-1"}}},
		{name: "rpm format", manager: tool.DNF, output: "gcc\t14.2.1-2.fc42\n", want: []packageRecord{{ID: "gcc", Version: "14.2.1-2.fc42"}}},
		{name: "pacman format", manager: tool.Pacman, output: "cmake 3.30.0-1\n", want: []packageRecord{{ID: "cmake", Version: "3.30.0-1"}}},
		{name: "apk format", manager: tool.APK, output: "py3-pip-24.3.1-r0\n", want: []packageRecord{{ID: "py3-pip", Version: "24.3.1-r0"}}},
		{name: "macports ignores header", manager: tool.MacPorts, output: "The following ports are currently installed:\n  git @2.47.0_0 (active)\n", want: []packageRecord{{ID: "git", Version: "2.47.0_0"}}},
		{name: "freebsd pkg query format", manager: tool.FreeBSDPkg, output: "git\t2.47.0\n", want: []packageRecord{{ID: "git", Version: "2.47.0"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parsePackageInventory(test.manager, []byte(test.output), false)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("records = %#v, want %#v", got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("record %d = %#v, want %#v", index, got[index], test.want[index])
				}
			}
		})
	}
}

func TestParseHomebrewInventory(t *testing.T) {
	formulae, err := parseHomebrewInventory([]byte(`[{"name":"bun","full_name":"oven-sh/bun/bun","installed":[{"version":"1.2.3"}]}]`), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(formulae) != 2 || formulae[0] != (packageRecord{ID: "oven-sh/bun/bun", Version: "1.2.3"}) || formulae[1] != (packageRecord{ID: "bun", Version: "1.2.3"}) {
		t.Fatalf("formula inventory = %#v", formulae)
	}
	casks, err := parseHomebrewInventory([]byte(`[{"token":"temurin@25","version":"25.0.1"}]`), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(casks) != 1 || casks[0] != (packageRecord{ID: "temurin@25", Version: "25.0.1"}) {
		t.Fatalf("cask inventory = %#v", casks)
	}
	if _, err := parseHomebrewInventory([]byte("not json"), false); err == nil {
		t.Fatal("malformed Homebrew JSON was accepted")
	}
}

func TestParseWingetExport(t *testing.T) {
	got, err := parseWingetExport([]byte(`{
		"Sources": [
			{"SourceDetails": {"Name": "winget"}, "Packages": [
				{"PackageIdentifier": "Git.Git", "Version": "2.47.0"},
				{"PackageIdentifier": "Microsoft.PowerShell", "Version": "7.5.0"}
			]},
			{"SourceDetails": {"Name": "msstore"}, "Packages": [{"PackageIdentifier": "Example.Tool"}]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []packageRecord{
		{ID: "Git.Git", Version: "2.47.0", Source: "winget"},
		{ID: "Microsoft.PowerShell", Version: "7.5.0", Source: "winget"},
		{ID: "Example.Tool", Source: "msstore"},
	}
	if len(got) != len(want) {
		t.Fatalf("WinGet export records = %#v, want %#v", got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("WinGet export record %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestParseWingetExportRejectsInvalidData(t *testing.T) {
	for _, output := range []string{
		"not json",
		`{"Sources": []}`,
		`{"Sources": [{"SourceDetails": {"Name": "winget"}, "Packages": [{"PackageIdentifier": ""}]}]}`,
		`{"Sources": [{"Packages": [{"PackageIdentifier": "Git.Git"}]}]}`,
	} {
		if _, err := parseWingetExport([]byte(output)); err == nil {
			t.Errorf("invalid WinGet export was accepted: %s", output)
		}
	}
}

func TestUnsupportedInventoryIsAWarningRatherThanAbsence(t *testing.T) {
	result := QueryInstalled(context.Background(), []Detected{{Manager: tool.Winget}})
	if len(result.Packages) != 0 || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "could not inspect") {
		t.Fatalf("unexpected package inventory result: %#v", result)
	}
	if len(result.UncertainManagers) != 1 || result.UncertainManagers[0] != tool.Winget {
		t.Fatalf("uncertain managers = %#v, want WinGet", result.UncertainManagers)
	}
}

func TestBoundedOutputReportsTruncation(t *testing.T) {
	output := &boundedOutput{limit: 3}
	if written, err := output.Write([]byte("abcdef")); err != nil || written != 6 {
		t.Fatalf("write returned %d, %v", written, err)
	}
	if output.String() != "abc" || !output.truncated {
		t.Fatalf("bounded output = %q (truncated %t)", output.String(), output.truncated)
	}
}
