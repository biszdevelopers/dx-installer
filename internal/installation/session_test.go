package installation

import (
	"errors"
	"testing"

	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/planning"
)

func TestSessionSkipsOnlyDependentsOfFailedInstall(t *testing.T) {
	session, err := NewSession(planning.Plan{Items: []planning.Item{
		{ToolID: "git", Name: "Git", Action: planning.Install},
		{ToolID: "github-cli", Name: "GitHub CLI", Dependencies: []string{"git"}, Action: planning.Install},
		{ToolID: "go", Name: "Go", Action: planning.Install},
	}})
	if err != nil {
		t.Fatal(err)
	}
	dependency, ok := session.Next()
	if !ok || dependency.ToolID != "git" {
		t.Fatalf("first operation = %#v, %v", dependency, ok)
	}
	if err := session.Record(dependency, errors.New("manager failed"), detection.Tool{ID: "git", State: detection.Missing}); err != nil {
		t.Fatal(err)
	}
	unrelated, ok := session.Next()
	if !ok || unrelated.ToolID != "go" {
		t.Fatalf("unrelated operation should continue after dependency failure: %#v, %v", unrelated, ok)
	}
	if skipped, exists := session.Result("github-cli"); !exists || skipped.State != Skipped {
		t.Fatalf("dependent operation was not skipped: %#v, %v", skipped, exists)
	}
	if err := session.Record(unrelated, nil, detection.Tool{ID: "go", State: detection.Healthy, Instances: []detection.Instance{{State: detection.Healthy, Version: "go1.26.0"}}}); err != nil {
		t.Fatal(err)
	}
	if !session.Complete() {
		t.Fatal("session should be complete after unrelated work finished")
	}
	if result, _ := session.Result("go"); result.State != InstalledVerified || result.Verification != "go1.26.0" {
		t.Fatalf("healthy tool was not recorded as verified: %#v", result)
	}
}

func TestSessionAcceptsVerificationAfterNonzeroManagerExit(t *testing.T) {
	item := planning.Item{ToolID: "git", Name: "Git", Action: planning.Install, Manager: tool.Winget, Source: "winget"}
	session, err := NewSession(planning.Plan{Items: []planning.Item{item}})
	if err != nil {
		t.Fatal(err)
	}
	active, ok := session.Next()
	if !ok {
		t.Fatal("session did not produce an operation")
	}
	if err := session.Record(active, errors.New("installer exited 1"), detection.Tool{ID: "git", State: detection.Healthy, Instances: []detection.Instance{{State: detection.Healthy, Version: "git version 2.55.0"}}}); err != nil {
		t.Fatal(err)
	}
	result, _ := session.Result("git")
	if result.State != InstalledVerified || result.Diagnostic == "" {
		t.Fatalf("verified installation did not preserve the manager diagnostic: %#v", result)
	}
}

func TestSessionRejectsBlockedPlanAndCancellationIsReported(t *testing.T) {
	if _, err := NewSession(planning.Plan{Blocked: true, Items: []planning.Item{{ToolID: "git", Action: planning.Install}}}); err == nil {
		t.Fatal("blocked plan created a session")
	}
	session, err := NewSession(planning.Plan{Items: []planning.Item{{ToolID: "git", Name: "Git", Action: planning.Install}}})
	if err != nil {
		t.Fatal(err)
	}
	session.Cancel()
	if !session.Complete() {
		t.Fatal("cancelled session is not complete")
	}
	if result, exists := session.Result("git"); !exists || result.State != Skipped {
		t.Fatalf("cancelled operation is not reported: %#v, %v", result, exists)
	}
}
