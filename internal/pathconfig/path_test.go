package pathconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/platform"
)

func TestPlanUsesCurrentUserWindowsPathAndAvoidsDuplicates(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "Programs", "go", "bin")
	machine := platform.Info{OS: tool.Windows, Shell: "PowerShell"}
	changes, err := Plan(machine, home, []string{directory, directory}, "C:\\Windows\\System32")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || !changes[0].Persist || changes[0].Manual || changes[0].Target != "Current-user PATH (Windows environment registry)" {
		t.Fatalf("Windows PATH plan = %#v", changes)
	}
	if strings.Contains(changes[0].ManualCommand, "\n") || !strings.Contains(changes[0].ManualCommand, directory) {
		t.Fatalf("manual Windows command is not a single-line command for the expected path: %q", changes[0].ManualCommand)
	}
}

func TestPlanRecognizesAnExistingProcessPathEntry(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "tool", "bin")
	separator := string(os.PathListSeparator)
	changes, err := Plan(platform.Info{OS: nativeOS(), Shell: "bash"}, home, []string{directory}, "other"+separator+directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("existing PATH entry was planned again: %#v", changes)
	}
}

func TestUnixApplyPreservesStartupFileAndIsIdempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell startup configuration does not apply on Windows")
	}
	home := t.TempDir()
	target := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(target, []byte("# existing user configuration\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(home, "Development Tools", "go", "bin")
	change := Change{Directory: directory, Target: target, Shell: "bash", Persist: true}
	t.Setenv("PATH", "")
	if err := Apply([]Change{change}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(contents), "# existing user configuration\n") || strings.Count(string(contents), blockMarker(directory)+" begin") != 1 {
		t.Fatalf("startup content was not preserved or marker was duplicated: %q", contents)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("existing startup-file permissions changed to %o", info.Mode().Perm())
	}
	if err := Apply([]Change{change}); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(contents), blockMarker(directory)+" begin") != 1 {
		t.Fatalf("second PATH update was not idempotent: %q", contents)
	}
	if !contains(os.Getenv("PATH"), directory, tool.Linux) {
		t.Fatalf("PATH was not updated for the current process: %q", os.Getenv("PATH"))
	}
}

func TestPlanFallsBackToManualInstructionsForUnknownShell(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "tool", "bin")
	changes, err := Plan(platform.Info{OS: tool.Linux, Shell: "unknown"}, home, []string{directory}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || !changes[0].Manual || changes[0].Persist || changes[0].ManualCommand == "" {
		t.Fatalf("unknown shell did not receive safe manual PATH instructions: %#v", changes)
	}
}

func nativeOS() tool.OperatingSystem {
	switch runtime.GOOS {
	case "windows":
		return tool.Windows
	case "darwin":
		return tool.MacOS
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		return tool.BSD
	default:
		return tool.Linux
	}
}
