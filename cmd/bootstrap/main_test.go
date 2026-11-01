package main

import (
	"strings"
	"testing"

	"developers-club-dx/internal/platform"
)

func TestNonTerminalInstallStopsBeforeInventoryOrExecution(t *testing.T) {
	err := run([]string{"install", "--tools", "git"})
	if err == nil || !strings.Contains(err.Error(), "interactive approval requires a terminal") {
		t.Fatalf("non-terminal installation was not refused: %v", err)
	}
}

func TestSourcePolicyRejectsUnknownOverride(t *testing.T) {
	if _, err := sourceFallback(platform.Info{}, "not-a-source-policy"); err == nil || !strings.Contains(err.Error(), "unsupported source policy") {
		t.Fatalf("unknown source policy was accepted: %v", err)
	}
}

func TestNonInteractiveApprovalRequiresExplicitTools(t *testing.T) {
	err := run([]string{"install", "--yes"})
	if err == nil || !strings.Contains(err.Error(), "--yes requires --tools") {
		t.Fatalf("--yes without a selected tool list was not refused: %v", err)
	}
}

func TestPlanRejectsUndetectedManagerPreference(t *testing.T) {
	err := run([]string{"plan", "--tools", "git", "--manager", "not-a-package-manager"})
	if err == nil || !strings.Contains(err.Error(), `package manager "not-a-package-manager" is not detected`) {
		t.Fatalf("undetected package-manager preference was not rejected: %v", err)
	}
}

func TestParseIDsDeduplicatesAndSorts(t *testing.T) {
	ids := parseIDs("go, git go\tuv")
	want := []string{"git", "go", "uv"}
	if len(ids) != len(want) {
		t.Fatalf("IDs = %#v", ids)
	}
	for index := range want {
		if ids[index] != want[index] {
			t.Fatalf("IDs = %#v, want %#v", ids, want)
		}
	}
}
