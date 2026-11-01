package installation

import (
	"context"
	"errors"
	"fmt"

	"developers-club-dx/internal/detection"
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
	ToolID       string
	Name         string
	State        ResultState
	Manager      string
	Source       string
	Command      string
	Verification string
	Diagnostic   string
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
		if item.Action != planning.Install {
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
	if s.running == "" || s.running != item.ToolID {
		return fmt.Errorf("tool %q is not the active installation", item.ToolID)
	}
	result := Result{
		ToolID:  item.ToolID,
		Name:    item.Name,
		Manager: string(item.Manager),
		Source:  item.Source,
		Command: item.Command.Display(),
	}
	result.Verification = string(actual.State)
	if len(actual.Instances) > 0 {
		result.Verification = actual.Instances[0].Version
	}
	if actual.State == detection.Healthy {
		result.State = InstalledVerified
		if commandErr != nil {
			result.Diagnostic = "package manager returned an error, but the installed tool passed verification: " + commandErr.Error()
		}
	} else if actual.State == detection.Missing {
		result.State = Failed
		if commandErr != nil {
			result.Diagnostic = commandErr.Error()
		} else {
			result.Diagnostic = "package manager completed, but the tool was not found during verification"
		}
	} else {
		result.State = PartiallyInstalled
		if commandErr != nil {
			result.Diagnostic = commandErr.Error() + "; post-install state is " + string(actual.State)
		} else {
			result.Diagnostic = "post-install verification returned " + string(actual.State)
		}
	}
	s.results[item.ToolID] = result
	s.running = ""
	return nil
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
		if item.Action == planning.Install {
			if _, done := s.results[item.ToolID]; !done {
				return false
			}
		}
	}
	return true
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
