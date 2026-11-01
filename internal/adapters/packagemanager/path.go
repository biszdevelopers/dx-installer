package packagemanager

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"developers-club-dx/internal/domain/tool"
)

func HomebrewFormulaPrefix(ctx context.Context, packageID string) (string, error) {
	command := exec.CommandContext(ctx, "brew", "--prefix", packageID)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve Homebrew formula prefix for %q: %w: %s", packageID, err, strings.TrimSpace(string(output)))
	}
	prefix := strings.TrimSpace(string(output))
	if !filepath.IsAbs(prefix) || strings.ContainsAny(prefix, "\r\n\x00") {
		return "", fmt.Errorf("Homebrew returned an invalid formula prefix for %q", packageID)
	}
	return filepath.Clean(prefix), nil
}

func ResolveInstallerPaths(installer tool.Installer, formulaPrefix string) ([]string, error) {
	if len(installer.PathEntries) == 0 {
		return nil, nil
	}
	if installer.Manager != tool.Homebrew {
		return nil, fmt.Errorf("installer %q/%q cannot resolve formula-relative PATH entries", installer.Platform, installer.Manager)
	}
	if !filepath.IsAbs(formulaPrefix) || strings.ContainsAny(formulaPrefix, "\r\n\x00") {
		return nil, fmt.Errorf("Homebrew formula prefix must be an absolute path")
	}
	prefix := filepath.Clean(formulaPrefix)
	paths := make([]string, 0, len(installer.PathEntries))
	for _, entry := range installer.PathEntries {
		if entry == "" || filepath.IsAbs(entry) || strings.ContainsAny(entry, "\\\r\n\x00") || filepath.Clean(filepath.FromSlash(entry)) != filepath.FromSlash(entry) || entry == "." || entry == ".." || strings.HasPrefix(entry, "../") {
			return nil, fmt.Errorf("installer has an unsafe formula-relative PATH entry %q", entry)
		}
		resolved := filepath.Join(prefix, filepath.FromSlash(entry))
		relative, err := filepath.Rel(prefix, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("installer PATH entry %q escapes the formula prefix", entry)
		}
		paths = append(paths, resolved)
	}
	return paths, nil
}
