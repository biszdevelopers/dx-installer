package artifacts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/platform"
	"developers-club-dx/internal/sources"
)

type InstallResult struct {
	Path          string
	Version       string
	SourceName    string
	SourceURL     string
	SHA256        string
	CleanupNotice string
}

type ArchiveInstaller struct {
	Downloader *sources.Downloader
	TempRoot   string
}

var toolIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,63}$`)

func NewArchiveInstaller() *ArchiveInstaller {
	return &ArchiveInstaller{Downloader: sources.NewDownloader(sources.DownloadOptions{})}
}

func InstallationPath(machine tool.OperatingSystem, toolID, version, home string) (string, error) {
	if !toolIDPattern.MatchString(toolID) || !versionPattern.MatchString(version) || !filepath.IsAbs(home) {
		return "", errors.New("tool ID, version, and absolute home directory are required")
	}
	parent, err := installParent(machine, toolID, home)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, version), nil
}

func (i *ArchiveInstaller) Install(ctx context.Context, toolID, home string, artifact tool.Artifact, allowRegionalFallback bool) (result InstallResult, installErr error) {
	if !toolIDPattern.MatchString(toolID) {
		return result, fmt.Errorf("invalid tool identifier %q", toolID)
	}
	if !filepath.IsAbs(home) {
		return result, errors.New("an absolute user home directory is required")
	}
	machine := platform.Detect()
	if artifact.Platform != machine.OS || artifact.Architecture != machine.Architecture {
		return result, errors.New("archive platform and architecture do not match the current machine")
	}
	if !versionPattern.MatchString(artifact.Version) {
		return result, errors.New("archive version is invalid")
	}
	if artifact.Format != tool.TarGzip && artifact.Format != tool.Zip {
		return result, fmt.Errorf("unsupported archive format %q", artifact.Format)
	}
	parent, err := installParent(machine.OS, toolID, home)
	if err != nil {
		return result, err
	}
	target, err := InstallationPath(machine.OS, toolID, artifact.Version, home)
	if err != nil {
		return result, err
	}
	if _, err := os.Lstat(target); err == nil {
		return result, fmt.Errorf("installation target already exists; refusing to replace %s", target)
	} else if !os.IsNotExist(err) {
		return result, fmt.Errorf("inspect installation target: %w", err)
	}
	downloader := i.Downloader
	if downloader == nil {
		downloader = sources.NewDownloader(sources.DownloadOptions{})
	}

	workspace, err := os.MkdirTemp(i.TempRoot, "dx-tool-install-")
	if err != nil {
		return result, fmt.Errorf("create temporary tool installation workspace: %w", err)
	}
	if err := os.Chmod(workspace, 0o700); err != nil {
		_ = os.RemoveAll(workspace)
		return result, fmt.Errorf("restrict temporary tool installation workspace: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(workspace); cleanupErr != nil {
			result.CleanupNotice = "temporary artifact cleanup failed: " + cleanupErr.Error()
			if installErr != nil {
				installErr = errors.Join(installErr, cleanupErr)
			}
		}
	}()

	candidates := []sources.RemoteSource{{
		Name:                 toolID + " " + artifact.Version + " official source",
		Kind:                 sources.Official,
		URL:                  artifact.URL,
		SHA256:               artifact.SHA256,
		AllowedRedirectHosts: append([]string(nil), artifact.AllowedRedirectHosts...),
	}}
	policy := sources.OfficialOnly
	if allowRegionalFallback && artifact.RegionalURL != "" {
		candidates = append(candidates, sources.RemoteSource{
			Name:   toolID + " " + artifact.Version + " verified regional mirror",
			Kind:   sources.Regional,
			URL:    artifact.RegionalURL,
			SHA256: artifact.SHA256,
		})
		policy = sources.OfficialFirst
	}
	download, err := downloader.Download(ctx, policy, candidates, workspace)
	if err != nil {
		return result, fmt.Errorf("download %s %s: %w", toolID, artifact.Version, err)
	}

	if err := os.MkdirAll(parent, 0o700); err != nil {
		return result, fmt.Errorf("create user-level installation directory: %w", err)
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return result, fmt.Errorf("inspect user-level installation directory: %w", err)
	}
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("user-level installation directory must be a real directory")
	}
	if _, err := os.Lstat(target); err == nil {
		return result, fmt.Errorf("installation target already exists; refusing to replace %s", target)
	} else if !os.IsNotExist(err) {
		return result, fmt.Errorf("inspect installation target: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".tool-install-*")
	if err != nil {
		return result, fmt.Errorf("create temporary installation staging directory: %w", err)
	}
	defer func() {
		if staging != "" {
			if cleanupErr := os.RemoveAll(staging); cleanupErr != nil {
				result.CleanupNotice = joinNotice(result.CleanupNotice, "installation staging cleanup failed: "+cleanupErr.Error())
				if installErr != nil {
					installErr = errors.Join(installErr, cleanupErr)
				}
			}
		}
	}()
	if err := Extract(download.Path, staging, artifact.ArchiveRoot, artifact.Format, Limits{}); err != nil {
		return result, fmt.Errorf("extract %s %s: %w", toolID, artifact.Version, err)
	}
	if err := os.Rename(staging, target); err != nil {
		return result, fmt.Errorf("publish user-level installation: %w", err)
	}
	staging = ""
	return InstallResult{Path: target, Version: artifact.Version, SourceName: download.SourceName, SourceURL: download.URL, SHA256: download.SHA256}, nil
}

func installParent(machine tool.OperatingSystem, toolID, home string) (string, error) {
	if machine == tool.Windows {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		if !filepath.IsAbs(localAppData) {
			return "", errors.New("LOCALAPPDATA must be an absolute path")
		}
		return filepath.Join(localAppData, "Programs", toolID), nil
	}
	if machine == tool.MacOS || machine == tool.Linux || machine == tool.BSD {
		return filepath.Join(home, ".local", "opt", toolID), nil
	}
	return "", fmt.Errorf("user-level archive installation is not supported on %q", machine)
}

func joinNotice(existing, next string) string {
	if existing == "" {
		return next
	}
	return strings.Join([]string{existing, next}, "; ")
}
