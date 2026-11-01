package reporting

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/installation"
	"developers-club-dx/internal/platform"
)

type Document struct {
	SchemaVersion int         `json:"schema_version"`
	GeneratedAt   time.Time   `json:"generated_at"`
	Platform      Host        `json:"platform"`
	SelectedTools []string    `json:"selected_tools"`
	Warnings      []string    `json:"warnings,omitempty"`
	Results       []ToolState `json:"results"`
}

type Host struct {
	OS           tool.OperatingSystem `json:"os"`
	Architecture tool.Architecture    `json:"architecture"`
	Distribution string               `json:"distribution,omitempty"`
}

type ToolState struct {
	ID           string                   `json:"id"`
	Name         string                   `json:"name"`
	State        installation.ResultState `json:"state"`
	Manager      string                   `json:"manager,omitempty"`
	Source       string                   `json:"source,omitempty"`
	Verification string                   `json:"verification,omitempty"`
	Diagnostic   string                   `json:"diagnostic,omitempty"`
}

var (
	secretAssignment = regexp.MustCompile(`(?i)(token|secret|password|passwd|api[_-]?key|authorization|credential)(["']?\s*[:=]\s*["']?)[^\s,"']+`)
	bearerToken      = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	uriCredentials   = regexp.MustCompile(`(?i)(https?://)[^/@\s]+@`)
	querySecret      = regexp.MustCompile(`(?i)([?&](?:token|secret|password|api[_-]?key|access_token|authorization)=)[^&\s]+`)
	windowsUserPath  = regexp.MustCompile(`(?i)[a-z]:\\Users\\[^\r\n]+`)
	unixUserPath     = regexp.MustCompile(`(?i)(?:/home|/Users)/[^\r\n]+`)
	userHomePath     = regexp.MustCompile(`USER_HOME_MARKER[^\r\n]*`)
)

func New(machine platform.Info, selected []string, warnings []string, results []installation.Result) Document {
	selection := append([]string(nil), selected...)
	sort.Strings(selection)
	document := Document{
		SchemaVersion: 1,
		GeneratedAt:   time.Now().UTC(),
		Platform: Host{
			OS:           machine.OS,
			Architecture: machine.Architecture,
			Distribution: machine.Distribution,
		},
		SelectedTools: selection,
		Results:       make([]ToolState, 0, len(results)),
	}
	for _, warning := range warnings {
		document.Warnings = append(document.Warnings, sanitize(warning))
	}
	for _, result := range results {
		document.Results = append(document.Results, ToolState{
			ID:           result.ToolID,
			Name:         result.Name,
			State:        result.State,
			Manager:      result.Manager,
			Source:       result.Source,
			Verification: sanitize(result.Verification),
			Diagnostic:   sanitize(result.Diagnostic),
		})
	}
	return document
}

func Encode(document Document) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func sanitize(value string) string {
	if value == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		expression := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(home))
		value = expression.ReplaceAllString(value, "USER_HOME_MARKER")
	}
	value = windowsUserPath.ReplaceAllString(value, "USER_HOME_MARKER")
	value = unixUserPath.ReplaceAllString(value, "USER_HOME_MARKER")
	value = userHomePath.ReplaceAllString(value, "%USERPROFILE%")
	value = bearerToken.ReplaceAllString(value, "Bearer [REDACTED]")
	value = uriCredentials.ReplaceAllString(value, `${1}[REDACTED]@`)
	value = querySecret.ReplaceAllString(value, `${1}[REDACTED]`)
	value = secretAssignment.ReplaceAllString(value, `${1}${2}[REDACTED]`)
	if len(value) > 4096 {
		value = string([]rune(value)[:4096]) + "…"
	}
	return strings.TrimSpace(value)
}
