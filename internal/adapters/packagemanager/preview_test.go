package packagemanager

import (
	"context"
	"strings"
	"testing"

	"developers-club-dx/internal/domain/tool"
)

func TestPreviewArgumentsDoNotRefreshOrModifyPackageMetadata(t *testing.T) {
	tests := []struct {
		manager tool.PackageManager
		want    []string
	}{
		{manager: tool.APT, want: []string{"--simulate", "--no-download", "--no-install-recommends", "install", "ninja-build"}},
		{manager: tool.DNF, want: []string{"--cacheonly", "--assumeno", "--color=never", "install", "ninja-build"}},
		{manager: tool.YUM, want: []string{"--cacheonly", "--assumeno", "--color=never", "install", "ninja-build"}},
		{manager: tool.Pacman, want: []string{"-Sp", "--print-format", "%n\t%v", "--needed", "ninja-build"}},
		{manager: tool.Zypper, want: []string{"--no-refresh", "--non-interactive", "install", "--dry-run", "--no-recommends", "ninja-build"}},
		{manager: tool.APK, want: []string{"--no-network", "add", "--simulate", "ninja-build"}},
	}
	for _, test := range tests {
		t.Run(string(test.manager), func(t *testing.T) {
			arguments, supported := previewArguments(test.manager, "ninja-build")
			if !supported {
				t.Fatal("manager preview was not supported")
			}
			if strings.Join(arguments, "|") != strings.Join(test.want, "|") {
				t.Fatalf("preview arguments = %#v, want %#v", arguments, test.want)
			}
		})
	}
}

func TestPreviewParsersReportCandidateAndAffectedPackages(t *testing.T) {
	tests := []struct {
		manager tool.PackageManager
		output  string
		version string
		count   string
	}{
		{manager: tool.APT, output: "Inst ninja-build [1.10.1-1] (1.11.1-2 Debian:stable)\nInst ninja-common (1.11.1-2 Debian:stable)\nConf ninja-build (1.11.1-2 Debian:stable)\nConf ninja-common (1.11.1-2 Debian:stable)\n", version: "1.11.1-2", count: "2 package operation(s)"},
		{manager: tool.DNF, output: "Installing:\n  ninja-build x86_64 1.11.1-2.el9 appstream\nInstalling dependencies:\n  ninja-common noarch 1.11.1-2.el9 appstream\n", version: "1.11.1-2.el9", count: "2 package operation(s)"},
		{manager: tool.Pacman, output: "ninja-build 1.11.1-1\nninja-common 1.11.1-1\n", version: "1.11.1-1", count: "2 package operation(s)"},
		{manager: tool.Zypper, output: "The following 2 NEW packages are going to be installed:\n  ninja-build 1.11.1-2 x86_64\n  ninja-common 1.11.1-2 noarch\n", version: "1.11.1-2", count: "2 package operation(s)"},
		{manager: tool.APK, output: "(1/2) Installing ninja-build (1.11.1-r0)\n(2/2) Installing ninja-common (1.11.1-r0)\n", version: "1.11.1-r0", count: "2 package operation(s)"},
	}
	for _, test := range tests {
		t.Run(string(test.manager), func(t *testing.T) {
			preview := parseInstallPreview(test.manager, "ninja-build", test.output)
			if preview.CandidateVersion != test.version {
				t.Fatalf("candidate version = %q, want %q", preview.CandidateVersion, test.version)
			}
			if !strings.Contains(preview.Transaction, test.count) {
				t.Fatalf("transaction = %q, expected %q", preview.Transaction, test.count)
			}
		})
	}
}

func TestUnsupportedPreviewExplainsUncertainty(t *testing.T) {
	preview := InspectInstall(context.Background(), []Detected{{Manager: tool.Winget, Path: "winget"}}, tool.Winget, "Git.Git")
	if preview.CandidateVersion != "" || preview.Transaction != "" || !strings.Contains(preview.Disclosure, "does not provide a safe cache-only") {
		t.Fatalf("unsupported preview = %#v", preview)
	}
}
