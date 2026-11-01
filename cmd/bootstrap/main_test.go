package main

import (
	"strings"
	"testing"
)

func TestNonTerminalInstallStopsBeforeInventoryOrExecution(t *testing.T) {
	err := run([]string{"install", "--tools", "git"})
	if err == nil || !strings.Contains(err.Error(), "interactive approval requires a terminal") {
		t.Fatalf("non-terminal installation was not refused: %v", err)
	}
}

func TestNonInteractiveApprovalRequiresExplicitTools(t *testing.T) {
	err := run([]string{"install", "--yes"})
	if err == nil || !strings.Contains(err.Error(), "--yes requires --tools") {
		t.Fatalf("--yes without a selected tool list was not refused: %v", err)
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
