package reporting

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/installation"
	"developers-club-dx/internal/platform"
)

func TestNewRedactsSecretsAndPersonalPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	results := []installation.Result{{
		ToolID:       "git",
		Name:         "Git",
		State:        installation.Failed,
		Verification: "could not inspect " + home + "/Downloads/token.txt",
		Diagnostic:   "request https://alice:password123@example.test/install?access_token=abc123 failed; Authorization: Bearer xyz987",
	}}
	document := New(
		platform.Info{OS: tool.Windows, Architecture: tool.AMD64, Distribution: "windows"},
		[]string{"git"},
		[]string{"failed at C:\\Users\\alice\\AppData\\Local\\secret.txt"},
		results,
	)

	encoded, err := Encode(document)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"password123", "abc123", "xyz987", "alice", "AppData"} {
		if strings.Contains(text, secret) {
			t.Errorf("report contains sensitive value %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[REDACTED]") || !strings.Contains(text, "%USERPROFILE%") {
		t.Fatalf("report did not show redaction markers: %s", text)
	}
	if strings.Contains(text, home) || strings.Contains(text, "command") {
		t.Fatalf("report contains a local path or raw command: %s", text)
	}
	var decoded Document
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("report is not valid JSON: %v", err)
	}
	if decoded.SchemaVersion != 1 || decoded.Platform.OS != tool.Windows || len(decoded.Results) != 1 {
		t.Fatalf("unexpected decoded report: %#v", decoded)
	}
}

func TestSanitizeBoundsLongDiagnostic(t *testing.T) {
	value := strings.Repeat("x", 5000)
	got := sanitize(value)
	if len([]rune(got)) > 4097 {
		t.Fatalf("sanitized diagnostic length = %d runes, want at most 4097", len([]rune(got)))
	}
}
