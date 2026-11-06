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
		{manager: tool.FreeBSDPkg, want: []string{"install", "--dry-run", "--no-repo-update", "ninja-build"}},
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

func TestOtherManagersExposeReadOnlyVersionAndTransactionQueries(t *testing.T) {
	tests := []struct {
		name      string
		manager   tool.PackageManager
		packageID string
		options   PreviewOptions
		want      [][]string
	}{
		{
			name:      "winget metadata",
			manager:   tool.Winget,
			packageID: "Git.Git",
			options:   PreviewOptions{Source: "winget"},
			want: [][]string{{
				"show", "--id", "Git.Git", "--exact", "--disable-interactivity", "--locale", "en-US", "--source", "winget",
			}},
		},
		{
			name:      "scoop metadata and dependencies",
			manager:   tool.Scoop,
			packageID: "git",
			want:      [][]string{{"info", "git"}, {"depends", "git"}},
		},
		{
			name:      "chocolatey metadata and no-op install use the configured source",
			manager:   tool.Chocolatey,
			packageID: "ninja",
			options:   PreviewOptions{Source: "chocolatey-community"},
			want: [][]string{
				{"info", "ninja", "--exact", "--limit-output"},
				{"install", "ninja", "--noop", "--limit-output", "--no-progress"},
			},
		},
		{
			name:    "homebrew formula",
			manager: tool.Homebrew,
			want: [][]string{
				{"info", "--json=v2", "--formula", "ninja"},
				{"install", "--dry-run", "--formula", "ninja"},
			},
		},
		{
			name:      "homebrew cask",
			manager:   tool.Homebrew,
			packageID: "font-example",
			options:   PreviewOptions{Kind: "cask"},
			want: [][]string{
				{"info", "--json=v2", "--cask", "font-example"},
				{"install", "--dry-run", "--cask", "font-example"},
			},
		},
		{
			name:    "macports metadata and dependency list",
			manager: tool.MacPorts,
			want:    [][]string{{"info", "ninja"}, {"rdeps", "ninja"}},
		},
		{
			name:      "android sdk metadata",
			manager:   tool.AndroidSDK,
			packageID: "platform-tools",
			want:      [][]string{{"--list", "--channel=0"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packageID := test.packageID
			if packageID == "" {
				packageID = "ninja"
			}
			steps, ok := previewSteps(test.manager, packageID, test.options)
			if !ok || len(steps) != len(test.want) {
				t.Fatalf("preview steps = %#v, supported = %v", steps, ok)
			}
			for index, step := range steps {
				if strings.Join(step.arguments, "|") != strings.Join(test.want[index], "|") {
					t.Fatalf("step %d arguments = %#v, want %#v", index, step.arguments, test.want[index])
				}
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

func TestAdditionalManagerPreviewParsers(t *testing.T) {
	tests := []struct {
		name      string
		manager   tool.PackageManager
		packageID string
		role      string
		output    string
		version   string
		count     string
	}{
		{name: "winget", manager: tool.Winget, role: "candidate", output: "Name: Git\nVersion: 2.47.1\n", version: "2.47.1"},
		{name: "scoop", manager: tool.Scoop, role: "candidate", output: "Name: git\nVersion: 2.47.1\n", version: "2.47.1"},
		{name: "chocolatey candidate", manager: tool.Chocolatey, role: "candidate", output: "ninja|1.12.1\n", version: "1.12.1"},
		{name: "chocolatey transaction", manager: tool.Chocolatey, role: "transaction", output: "ninja|1.12.1\n", version: "1.12.1", count: "1 package operation(s)"},
		{name: "homebrew formula", manager: tool.Homebrew, role: "candidate", output: `{"formulae":[{"versions":{"stable":"1.12.1"}}]}`, version: "1.12.1"},
		{name: "homebrew cask", manager: tool.Homebrew, role: "candidate", output: `{"casks":[{"version":"2.4.0"}]}`, version: "2.4.0"},
		{name: "homebrew legacy dry-run", manager: tool.Homebrew, role: "transaction", output: "Would install ninja and its dependencies\n", count: "1 package operation(s)"},
		{name: "homebrew formula and dependency headings", manager: tool.Homebrew, role: "transaction", output: "==> Would install 1 formula:\noven-sh/bun/bun\n==> Would install 2 dependencies for oven-sh/bun/bun:\nca-certificates\nopenssl@3\n", count: "3 package operation(s)"},
		{name: "macports", manager: tool.MacPorts, role: "candidate", output: "Version: 1.12.1_0\n", version: "1.12.1_0"},
		{name: "scoop dependencies", manager: tool.Scoop, role: "dependencies", output: "Dependencies for ninja:\n  cmake\n", count: "2 package operation(s)"},
		{name: "macports dependencies", manager: tool.MacPorts, role: "dependencies", output: "The following ports are dependencies of ninja @1.12.1_0:\n  cmake @3.30.2_0\n", count: "2 package operation(s)"},
		{name: "android sdk", manager: tool.AndroidSDK, packageID: "platform-tools", role: "candidate", output: "Installed packages:\nplatform-tools | 35.0.0\nAvailable Packages:\nplatform-tools | 36.0.0 | Android SDK Platform-Tools\n", version: "36.0.0"},
		{name: "freebsd pkg", manager: tool.FreeBSDPkg, role: "transaction", output: "New packages to be INSTALLED:\n  cmake: 3.30.2\n  ninja: 1.12.1\n", version: "1.12.1", count: "2 package operation(s)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packageID := test.packageID
			if packageID == "" {
				packageID = "ninja"
			}
			preview := parsePreviewStep(test.manager, packageID, test.output, test.role)
			if preview.CandidateVersion != test.version {
				t.Fatalf("candidate version = %q, want %q", preview.CandidateVersion, test.version)
			}
			if test.count != "" && !strings.Contains(preview.Transaction, test.count) {
				t.Fatalf("transaction = %q, expected %q", preview.Transaction, test.count)
			}
		})
	}
}

func TestUnsupportedPreviewExplainsUncertainty(t *testing.T) {
	manager := tool.PackageManager("unsupported")
	preview := InspectInstall(context.Background(), []Detected{{Manager: manager, Path: "unsupported"}}, manager, "package")
	if preview.CandidateVersion != "" || preview.Transaction != "" || !strings.Contains(preview.Disclosure, "does not provide a safe candidate-version") {
		t.Fatalf("unsupported preview = %#v", preview)
	}
}

func TestEIMPreviewUsesCatalogPinnedVersionAndDisclosesTransactionLimit(t *testing.T) {
	preview := InspectInstall(context.Background(), []Detected{{Manager: tool.EspressifEIM, Path: "eim"}}, tool.EspressifEIM, "v5.3.2")
	if preview.CandidateVersion != "v5.3.2" || preview.Transaction != "" || !strings.Contains(preview.Disclosure, "pinned by the catalog") || !strings.Contains(preview.Disclosure, "system changes remain uncertain") {
		t.Fatalf("EIM preview = %#v", preview)
	}
}

func TestIncompletePreviewNeverClaimsThePartialTransactionWasResolved(t *testing.T) {
	preview := incompletePreview(InstallPreview{
		CandidateVersion: "1.2.3",
		Transaction:      packageTransaction([]string{"tool", "dependency"}),
	}, "the package manager exited unsuccessfully")
	if !strings.Contains(preview.Transaction, "Incomplete preview output reported") || strings.Contains(preview.Transaction, "resolved") || !strings.Contains(preview.Disclosure, "may be incomplete") {
		t.Fatalf("partial preview was not clearly labeled: %#v", preview)
	}
}
