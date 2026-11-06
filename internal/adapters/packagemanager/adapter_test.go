package packagemanager

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"developers-club-dx/internal/domain/tool"
)

func TestBuildCommandUsesTypedArgumentsWithoutSilentAcceptance(t *testing.T) {
	cases := []struct {
		name        string
		installer   tool.Installer
		os          tool.OperatingSystem
		want        []string
		environment map[string]string
		privilege   bool
	}{
		{
			name:      "winget user scope",
			installer: tool.Installer{Manager: tool.Winget, PackageID: "Git.Git", Platform: tool.Windows, Scope: tool.UserScope, Source: "winget"},
			os:        tool.Windows,
			want:      []string{"install", "--id", "Git.Git", "--exact", "--no-upgrade", "--source", "winget", "--scope", "user"},
		},
		{
			name:      "scoop user scope",
			installer: tool.Installer{Manager: tool.Scoop, PackageID: "git", Platform: tool.Windows, Scope: tool.UserScope},
			os:        tool.Windows,
			want:      []string{"install", "git"},
		},
		{
			name:        "homebrew cask",
			installer:   tool.Installer{Manager: tool.Homebrew, PackageID: "visual-studio-code", Platform: tool.MacOS, Scope: tool.UserScope, Kind: "cask"},
			os:          tool.MacOS,
			want:        []string{"install", "--cask", "visual-studio-code"},
			environment: map[string]string{"HOMEBREW_NO_INSTALL_UPGRADE": "1"},
		},
		{
			name:      "apt system scope",
			installer: tool.Installer{Manager: tool.APT, PackageID: "git", Platform: tool.Linux, Scope: tool.SystemScope},
			os:        tool.Linux,
			want:      []string{"install", "--no-install-recommends", "git"},
			privilege: true,
		},
		{
			name:      "yum system scope",
			installer: tool.Installer{Manager: tool.YUM, PackageID: "git", Platform: tool.Linux, Scope: tool.SystemScope},
			os:        tool.Linux,
			want:      []string{"install", "git"},
			privilege: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			command, err := BuildCommand(test.installer, test.os)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(command.Arguments, test.want) {
				t.Fatalf("arguments = %#v, want %#v", command.Arguments, test.want)
			}
			if command.RequiresPrivilege != test.privilege {
				t.Fatalf("RequiresPrivilege = %v, want %v", command.RequiresPrivilege, test.privilege)
			}
			if !reflect.DeepEqual(command.Environment, test.environment) {
				t.Fatalf("environment = %#v, want %#v", command.Environment, test.environment)
			}
			for _, arg := range command.Arguments {
				if strings.Contains(strings.ToLower(arg), "accept") || strings.Contains(strings.ToLower(arg), "agree") {
					t.Fatalf("command silently accepts a prompt or license: %#v", command.Arguments)
				}
			}
		})
	}
}

func TestBuildCommandRejectsUnsupportedScopeAndManager(t *testing.T) {
	if _, err := BuildCommand(tool.Installer{Manager: tool.Scoop, PackageID: "git", Scope: tool.SystemScope}, tool.Windows); err == nil {
		t.Fatal("system-scoped Scoop installation was accepted")
	}
	if _, err := BuildCommand(tool.Installer{Manager: "unknown", PackageID: "bad"}, tool.Linux); err == nil {
		t.Fatal("unknown package manager was accepted")
	}
}

func TestBSDManagerOrderOnlyEnablesFreeBSDPkgOnFreeBSD(t *testing.T) {
	if got := managerOrder(tool.BSD, "freebsd"); !reflect.DeepEqual(got, []tool.PackageManager{tool.FreeBSDPkg}) {
		t.Fatalf("FreeBSD manager order = %#v", got)
	}
	for _, distribution := range []string{"openbsd", "netbsd", "dragonfly", "unknown"} {
		if got := managerOrder(tool.BSD, distribution); len(got) != 0 {
			t.Errorf("%s manager order = %#v, want no unsupported package manager", distribution, got)
		}
	}
}

func TestLinuxManagerOrderChecksExistingHomebrewAfterNativeManagers(t *testing.T) {
	for _, distribution := range []string{"ubuntu", "fedora", "arch", "opensuse", "alpine", "unknown"} {
		order := managerOrder(tool.Linux, distribution)
		if len(order) == 0 || order[len(order)-1] != tool.Homebrew {
			t.Errorf("%s manager order = %#v, want Homebrew as the last fallback", distribution, order)
		}
	}
}

func TestYUMIsDetectedAfterDNFOnRPMDistributions(t *testing.T) {
	order := managerOrder(tool.Linux, "centos")
	dnfIndex, yumIndex := -1, -1
	for index, manager := range order {
		if manager == tool.DNF {
			dnfIndex = index
		}
		if manager == tool.YUM {
			yumIndex = index
		}
	}
	if dnfIndex < 0 || yumIndex <= dnfIndex || managerExecutable(tool.YUM) != "yum" {
		t.Fatalf("RPM manager order/executable = %#v / %q, want DNF then yum", order, managerExecutable(tool.YUM))
	}
}

func TestCommandDisplayQuotesArgumentsAndArgvDoesNotUseShell(t *testing.T) {
	command := Command{Executable: "winget", Arguments: []string{"install", "Vendor.Product with spaces"}}
	argv := command.Argv()
	if !reflect.DeepEqual(argv, []string{"winget", "install", "Vendor.Product with spaces"}) {
		t.Fatalf("argv = %#v", argv)
	}
	if !strings.Contains(command.Display(), `"Vendor.Product with spaces"`) {
		t.Fatalf("display did not quote the package identifier: %s", command.Display())
	}
}

func TestExecContextAppliesRegistryEnvironmentWithoutDuplicateKeys(t *testing.T) {
	const key = "DX_TEST_REGISTRY_SOURCE"
	t.Setenv(key, "inherited")
	command, err := (Command{Executable: "not-started", Environment: map[string]string{key: "https://registry.example", "DX_TEST_REGISTRY_CACHE": "temporary"}}).ExecContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var values []string
	for _, entry := range command.Env {
		name, value, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(name, key) {
			values = append(values, value)
		}
	}
	if !reflect.DeepEqual(values, []string{"https://registry.example"}) {
		t.Fatalf("registry environment overlay = %#v, want exactly one override", values)
	}
}
