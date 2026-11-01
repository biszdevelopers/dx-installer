package installation

import (
	"context"
	"errors"
	"fmt"

	"developers-club-dx/internal/detection"
	"developers-club-dx/internal/pathconfig"
	"developers-club-dx/internal/planning"
)

type ResultState string

const (
	InstalledVerified  ResultState = "installed_and_verified"
	AlreadySatisfied   ResultState = "already_satisfied"
	Preserved          ResultState = "preserved"
	Skipped            ResultState = "skipped"
	Failed             ResultState = "failed"
	PartiallyInstalled ResultState = "partially_installed"
)

type Result struct {
	ToolID        string
	Name          string
	State         ResultState
	Manager       string
	Source        string
	SourceURL     string
	Command       string
	InstallPath   string
	SHA256        string
	PathChanges   []pathconfig.Change
	CleanupNotice string
	Verification  string
	Diagnostic    string
}

type ExecutionDetails struct {
	Source        string
	SourceURL     string
	InstallPath   string
	SHA256        string
	PathChanges   []pathconfig.Change
	PathError     string
	CleanupNotice string
}

type Session struct {
	plan    planning.Plan
	items   []planning.Item
	results map[string]Result
	order   []string
	next    int
	running string
}

func NewSession(plan planning.Plan) (*Session, error) {
	if plan.Blocked {
		return nil, errors.New("installation plan has blocking issues")
	}
	if len(plan.Items) == 0 {
		return nil, errors.New("installation plan has no items")
	}
	session := &Session{
		plan:    plan,
		items:   append([]planning.Item(nil), plan.Items...),
		results: make(map[string]Result, len(plan.Items)),
	}
	for _, item := range session.items {
		session.order = append(session.order, item.ToolID)
		if item.Action == planning.Preserve {
			state := Preserved
			if item.ExistingState == detection.Healthy || item.ExistingState == detection.Outdated {
				state = AlreadySatisfied
			}
			session.results[item.ToolID] = Result{
				ToolID:       item.ToolID,
				Name:         item.Name,
				State:        state,
				Verification: string(item.ExistingState),
				Diagnostic:   "existing installation was left unchanged",
			}
		}
	}
	return session, nil
}

func (s *Session) Next() (planning.Item, bool) {
	if s.running != "" {
		return planning.Item{}, false
	}
	for s.next < len(s.items) {
		item := s.items[s.next]
		s.next++
		if item.Action != planning.Install && item.Action != planning.Provided {
			continue
		}
		if failedDependency := s.failedDependency(item); failedDependency != "" {
			s.results[item.ToolID] = Result{
				ToolID:       item.ToolID,
				Name:         item.Name,
				State:        Skipped,
				Verification: "not run",
				Diagnostic:   fmt.Sprintf("dependency %q did not install successfully", failedDependency),
			}
			continue
		}
		s.running = item.ToolID
		return item, true
	}
	return planning.Item{}, false
}

func (s *Session) RecordProvided(item planning.Item, actual detection.Tool) error {
	if item.Action != planning.Provided {
		return fmt.Errorf("tool %q is not provided by a dependency", item.ToolID)
	}
	if s.running == "" || s.running != item.ToolID {
		return fmt.Errorf("tool %q is not the active provided component", item.ToolID)
	}
	result := Result{ToolID: item.ToolID, Name: item.Name, Manager: "dependency", Source: item.ProvidedBy, Verification: string(actual.State)}
	if actual.State == detection.Healthy {
		result.State = AlreadySatisfied
		result.Diagnostic = fmt.Sprintf("provided and verified by dependency %q", item.ProvidedBy)
		for _, instance := range actual.Instances {
			if instance.State == detection.Healthy && instance.Version != "" {
				result.Verification = instance.Version
				break
			}
		}
	} else if actual.State == detection.Missing {
		result.State = Failed
		result.Diagnostic = fmt.Sprintf("dependency %q was installed but did not provide the required executable", item.ProvidedBy)
	} else {
		result.State = PartiallyInstalled
		result.Diagnostic = fmt.Sprintf("dependency %q did not provide a healthy executable (state: %s)", item.ProvidedBy, actual.State)
	}
	s.results[item.ToolID] = result
	s.running = ""
	return nil
}

func (s *Session) failedDependency(item planning.Item) string {
	for _, dependency := range item.Dependencies {
		result, exists := s.results[dependency]
		if !exists {
			return dependency
		}
		if result.State != InstalledVerified && result.State != AlreadySatisfied && result.State != Preserved {
			return dependency
		}
	}
	return ""
}

func (s *Session) Record(item planning.Item, commandErr error, actual detection.Tool) error {
	return s.RecordWithDetails(item, commandErr, actual, ExecutionDetails{})
}

func (s *Session) RecordWithDetails(item planning.Item, operationErr error, actual detection.Tool, details ExecutionDetails) error {
	if s.running == "" || s.running != item.ToolID {
		return fmt.Errorf("tool %q is not the active installation", item.ToolID)
	}
	result := Result{
		ToolID:        item.ToolID,
		Name:          item.Name,
		Manager:       string(item.Manager),
		Source:        item.Source,
		Command:       item.Command.Display(),
		InstallPath:   details.InstallPath,
		SHA256:        details.SHA256,
		PathChanges:   append([]pathconfig.Change(nil), details.PathChanges...),
		CleanupNotice: details.CleanupNotice,
	}
	if details.Source != "" {
		result.Source = details.Source
	}
	if details.SourceURL != "" {
		result.SourceURL = details.SourceURL
	}
	result.Verification = string(actual.State)
	for _, instance := range actual.Instances {
		if instance.State == detection.Healthy && instance.Version != "" {
			result.Verification = instance.Version
			break
		}
	}
	if actual.State == detection.Healthy {
		if details.PathError != "" {
			result.State = PartiallyInstalled
			result.Diagnostic = "tool passed verification, but its persistent PATH update failed: " + details.PathError
		} else {
			result.State = InstalledVerified
		}
		if operationErr != nil {
			result.Diagnostic = joinDiagnostic(result.Diagnostic, "installation operation reported an error, but the installed tool passed verification: "+operationErr.Error())
		}
	} else if actual.State == detection.Missing {
		result.State = Failed
		if operationErr != nil {
			result.Diagnostic = operationErr.Error()
		} else {
			result.Diagnostic = "installation completed, but the tool was not found during verification"
		}
	} else {
		result.State = PartiallyInstalled
		if operationErr != nil {
			result.Diagnostic = operationErr.Error() + "; post-install state is " + string(actual.State)
		} else {
			result.Diagnostic = "post-install verification returned " + string(actual.State)
		}
	}
	for _, change := range details.PathChanges {
		if change.Manual && change.ManualCommand != "" {
			result.Diagnostic = joinDiagnostic(result.Diagnostic, "PATH requires a manual update: "+change.ManualCommand)
		}
	}
	if details.CleanupNotice != "" {
		result.Diagnostic = joinDiagnostic(result.Diagnostic, details.CleanupNotice)
	}
	s.results[item.ToolID] = result
	s.running = ""
	return nil
}

func joinDiagnostic(existing, next string) string {
	if existing == "" {
		return next
	}
	return existing + "; " + next
}

func (s *Session) Current() (planning.Item, bool) {
	if s.running == "" {
		return planning.Item{}, false
	}
	for _, item := range s.items {
		if item.ToolID == s.running {
			return item, true
		}
	}
	return planning.Item{}, false
}

func (s *Session) Complete() bool {
	if s.running != "" {
		return false
	}
	for _, item := range s.items {
		if item.Action == planning.Install || item.Action == planning.Provided {
			if _, done := s.results[item.ToolID]; !done {
				return false
			}
		}
	}
	return true
}

func (s *Session) Cancel() {
	for _, item := range s.items {
		if item.Action != planning.Install && item.Action != planning.Provided {
			continue
		}
		if _, exists := s.results[item.ToolID]; exists {
			continue
		}
		s.results[item.ToolID] = Result{
			ToolID:       item.ToolID,
			Name:         item.Name,
			State:        Skipped,
			Verification: "not run",
			Diagnostic:   "installation cancelled by the user",
		}
	}
	s.running = ""
}

func (s *Session) Report() []Result {
	report := make([]Result, 0, len(s.results))
	for _, id := range s.order {
		if result, exists := s.results[id]; exists {
			report = append(report, result)
		}
	}
	return report
}

func (s *Session) Result(toolID string) (Result, bool) {
	result, exists := s.results[toolID]
	return result, exists
}

type Scanner func(context.Context) (detection.Inventory, error)

func VerifyAfterInstall(ctx context.Context, scanner Scanner, toolID string) (detection.Tool, error) {
	if scanner == nil {
		return detection.Tool{}, errors.New("verification scanner is required")
	}
	current, err := scanner(ctx)
	if err != nil {
		return detection.Tool{}, fmt.Errorf("rescan environment after %s: %w", toolID, err)
	}
	entry, exists := current.Tools[toolID]
	if !exists {
		return detection.Tool{}, fmt.Errorf("verification scan did not return %s", toolID)
	}
	return entry, nil
}
