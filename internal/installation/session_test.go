package installation

import (
	"errors"
	"strings"
	"testing"

	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/pathconfig"
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

func TestSessionReportsVerifiedArchiveAndFailedPATHPersistenceAsPartial(t *testing.T) {
	item := planning.Item{ToolID: "go", Name: "Go", Action: planning.Install, Source: "https://go.dev/dl/go1.27.3.linux-amd64.tar.gz", Artifact: &tool.Artifact{Version: "1.27.3"}}
	session, err := NewSession(planning.Plan{Items: []planning.Item{item}})
	if err != nil {
		t.Fatal(err)
	}
	active, ok := session.Next()
	if !ok {
		t.Fatal("session did not produce an operation")
	}
	details := ExecutionDetails{
		Source:      "official source",
		SourceURL:   "https://go.dev/dl/go1.27.3.linux-amd64.tar.gz",
		InstallPath: "/home/user/.local/opt/go/1.27.3",
		SHA256:      "b6f8e931e4ab497fcde6da8ae4499a58f9d258e55c8a80824a516eae5950a750",
		PathChanges: []pathconfig.Change{{Directory: "/home/user/.local/opt/go/1.27.3/bin", Target: "/home/user/.bashrc", Persist: true}},
		PathError:   "permission denied",
	}
	actual := detection.Tool{ID: "go", State: detection.Healthy, Instances: []detection.Instance{{State: detection.Healthy, Version: "go1.27.3"}}}
	if err := session.RecordWithDetails(active, nil, actual, details); err != nil {
		t.Fatal(err)
	}
	result, _ := session.Result("go")
	if result.State != PartiallyInstalled || result.Source != "official source" || result.SourceURL != details.SourceURL || result.InstallPath != details.InstallPath || result.SHA256 != details.SHA256 || result.Verification != "go1.27.3" || !strings.Contains(result.Diagnostic, "permission denied") {
		t.Fatalf("archive and PATH outcomes were not reported accurately: %#v", result)
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

func TestSessionVerifiesAProvidedComponentAfterItsDependencyInstalls(t *testing.T) {
	session, err := NewSession(planning.Plan{Items: []planning.Item{
		{ToolID: "nodejs-lts", Name: "Node.js LTS", Action: planning.Install},
		{ToolID: "npm", Name: "npm", Dependencies: []string{"nodejs-lts"}, Action: planning.Provided, ProvidedBy: "nodejs-lts"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	node, ok := session.Next()
	if !ok || node.ToolID != "nodejs-lts" {
		t.Fatalf("first operation = %#v, %v", node, ok)
	}
	if err := session.Record(node, nil, detection.Tool{ID: "nodejs-lts", State: detection.Healthy}); err != nil {
		t.Fatal(err)
	}
	npm, ok := session.Next()
	if !ok || npm.ToolID != "npm" || npm.Action != planning.Provided {
		t.Fatalf("provided component = %#v, %v", npm, ok)
	}
	if err := session.RecordProvided(npm, detection.Tool{ID: "npm", State: detection.Healthy, Instances: []detection.Instance{{State: detection.Healthy, Version: "12.2.1"}}}); err != nil {
		t.Fatal(err)
	}
	result, exists := session.Result("npm")
	if !exists || result.State != AlreadySatisfied || result.Verification != "12.2.1" || !strings.Contains(result.Diagnostic, "nodejs-lts") {
		t.Fatalf("provided component was not verified and reported: %#v, %v", result, exists)
	}
	if !session.Complete() {
		t.Fatal("session should finish after the provided component was verified")
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
