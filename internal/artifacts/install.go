package artifacts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"developers-club-dx/internal/adapters/packagemanager"
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

type InstallOptions struct {
	Policy                sources.Policy
	AllowRegionalFallback bool
	CustomSourceURL       string
	OfflineArchivePath    string
}

var toolIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func NewArchiveInstaller() *ArchiveInstaller {
	return &ArchiveInstaller{Downloader: sources.NewDownloader(sources.DownloadOptions{})}
}

func InstallationPath(machine tool.OperatingSystem, toolID, version, home string) (string, error) {
	if !toolIDPattern.MatchString(toolID) || !tool.IsValidArtifactVersion(version) || !filepath.IsAbs(home) {
		return "", errors.New("tool ID, version, and absolute home directory are required")
	}
	if toolID == "zephyr-sdk" {
		if machine == tool.Windows {
			return filepath.Join(home, "zephyr-sdk-"+version), nil
		}
		return filepath.Join(home, ".local", "opt", "zephyr-sdk-"+version), nil
	}
	parent, err := installParent(machine, toolID, home)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, version), nil
}

func (i *ArchiveInstaller) Install(ctx context.Context, toolID, home string, artifact tool.Artifact, allowRegionalFallback bool) (result InstallResult, installErr error) {
	return i.InstallWithOptions(ctx, toolID, home, artifact, InstallOptions{AllowRegionalFallback: allowRegionalFallback})
}

func (i *ArchiveInstaller) InstallWithOptions(ctx context.Context, toolID, home string, artifact tool.Artifact, options InstallOptions) (result InstallResult, installErr error) {
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
	if !tool.IsValidArtifactVersion(artifact.Version) {
		return result, errors.New("archive version is invalid")
	}
	if artifact.Format != tool.TarGzip && artifact.Format != tool.TarXZ && artifact.Format != tool.Zip && artifact.Format != tool.SevenZip {
		return result, fmt.Errorf("unsupported archive format %q", artifact.Format)
	}
	target, err := InstallationPath(machine.OS, toolID, artifact.Version, home)
	if err != nil {
		return result, err
	}
	parent := filepath.Dir(target)
	if _, err := os.Lstat(target); err == nil {
		return result, fmt.Errorf("installation target already exists; refusing to replace %s", target)
	} else if !os.IsNotExist(err) {
		return result, fmt.Errorf("inspect installation target: %w", err)
	}
	downloader := i.Downloader
	if downloader == nil {
		downloader = sources.NewDownloader(sources.DownloadOptions{})
	}
	if artifact.MaxDownloadBytes > 0 {
		downloader, err = downloader.WithArtifactLimit(artifact.MaxDownloadBytes)
		if err != nil {
			return result, fmt.Errorf("configure %s download limit: %w", toolID, err)
		}
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

	if options.OfflineArchivePath != "" && options.CustomSourceURL != "" {
		return result, errors.New("choose either an offline archive or a custom online source, not both")
	}
	if options.OfflineArchivePath != "" {
		download, err := downloader.CopyLocal(ctx, options.OfflineArchivePath, artifact.SHA256, workspace, nil, nil)
		if err != nil {
			return result, fmt.Errorf("verify offline %s %s: %w", toolID, artifact.Version, err)
		}
		return i.extractAndPublish(ctx, toolID, download, target, parent, artifact)
	}
	candidates := []sources.RemoteSource{{
		Name:                 toolID + " " + artifact.Version + " official source",
		Kind:                 sources.Official,
		URL:                  artifact.URL,
		SHA256:               artifact.SHA256,
		AllowedRedirectHosts: append([]string(nil), artifact.AllowedRedirectHosts...),
	}}
	policy := options.Policy
	if options.CustomSourceURL != "" {
		custom := sources.RemoteSource{Name: toolID + " user-configured HTTPS source", Kind: sources.Custom, URL: options.CustomSourceURL, SHA256: artifact.SHA256}
		if err := sources.ValidateRemoteSource(custom); err != nil {
			return result, fmt.Errorf("validate custom source for %s: %w", toolID, err)
		}
		candidates = []sources.RemoteSource{custom}
		policy = sources.CustomOnly
	}
	if policy == "" {
		policy = sources.OfficialOnly
	}
	if options.AllowRegionalFallback && options.CustomSourceURL == "" && artifact.RegionalURL != "" {
		candidates = append(candidates, sources.RemoteSource{
			Name:   toolID + " " + artifact.Version + " verified regional mirror",
			Kind:   sources.Regional,
			URL:    artifact.RegionalURL,
			SHA256: artifact.SHA256,
		})
		if policy == sources.OfficialOnly {
			policy = sources.OfficialFirst
		}
	}
	download, err := downloader.Download(ctx, policy, candidates, workspace)
	if err != nil {
		return result, fmt.Errorf("download %s %s: %w", toolID, artifact.Version, err)
	}
	return i.extractAndPublish(ctx, toolID, download, target, parent, artifact)
}

func (i *ArchiveInstaller) extractAndPublish(ctx context.Context, toolID string, download sources.DownloadResult, target, parent string, artifact tool.Artifact) (result InstallResult, installErr error) {
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
	if err := Extract(download.Path, staging, artifact.ArchiveRoot, artifact.Format, Limits{MaxExpandedBytes: artifact.MaxExpandedBytes}); err != nil {
		return result, fmt.Errorf("extract %s %s: %w", toolID, artifact.Version, err)
	}
	if artifact.InstallSubdirectory != "" {
		layout, err := placeInSubdirectory(staging, parent, artifact.InstallSubdirectory)
		if err != nil {
			return result, fmt.Errorf("prepare %s installation layout: %w", toolID, err)
		}
		if err := os.RemoveAll(staging); err != nil {
			_ = os.RemoveAll(layout)
			return result, fmt.Errorf("remove extracted staging directory: %w", err)
		}
		staging = layout
	}
	if err := publishDirectory(staging, target); err != nil {
		return result, fmt.Errorf("publish user-level installation: %w", err)
	}
	staging = ""
	if artifact.PostInstallAction != "" {
		if err := runPostInstallAction(ctx, target, artifact.PostInstallAction); err != nil {
			cleanupErr := os.RemoveAll(target)
			return result, errors.Join(fmt.Errorf("initialize %s after extraction: %w", toolID, err), cleanupErr)
		}
	}
	return InstallResult{Path: target, Version: artifact.Version, SourceName: download.SourceName, SourceURL: download.URL, SHA256: download.SHA256}, nil
}

func runPostInstallAction(ctx context.Context, installPath string, action tool.PostInstallAction) error {
	if action != tool.ZephyrSDKSetup {
		return fmt.Errorf("unsupported typed post-install action %q", action)
	}
	setupScript := "setup.sh"
	arguments := []string{"-t", "all", "-h"}
	if runtime.GOOS == "windows" {
		setupScript = "setup.cmd"
		arguments = []string{"//t", "all", "//h"}
	}
	scriptPath := filepath.Join(installPath, setupScript)
	info, err := os.Lstat(scriptPath)
	if err != nil {
		return fmt.Errorf("inspect bundled setup script: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("bundled setup script must be a regular non-symlink file")
	}
	setupContext, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	commandSpec := packagemanager.Command{Executable: scriptPath, Arguments: arguments}
	command, err := commandSpec.ExecContext(setupContext)
	if err != nil {
		return fmt.Errorf("prepare bundled setup command: %w", err)
	}
	var stdout, stderr limitedCommandOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("vendor setup command failed: %w%s", err, setupOutput(stdout.String(), stderr.String()))
	}
	if setupContext.Err() != nil {
		return setupContext.Err()
	}
	return nil
}

type limitedCommandOutput struct {
	buffer    bytes.Buffer
	truncated bool
}

func (output *limitedCommandOutput) Write(contents []byte) (int, error) {
	const maximum = 16 << 10
	written := len(contents)
	remaining := maximum - output.buffer.Len()
	if remaining > 0 {
		if remaining > len(contents) {
			remaining = len(contents)
		}
		_, _ = output.buffer.Write(contents[:remaining])
	}
	if written > remaining {
		output.truncated = true
	}
	return written, nil
}

func (output *limitedCommandOutput) String() string {
	text := strings.TrimSpace(output.buffer.String())
	if output.truncated {
		text += " [output truncated]"
	}
	return text
}

func setupOutput(stdout, stderr string) string {
	parts := make([]string, 0, 2)
	if stdout != "" {
		parts = append(parts, " stdout: "+stdout)
	}
	if stderr != "" {
		parts = append(parts, " stderr: "+stderr)
	}
	return strings.Join(parts, ";")
}

func placeInSubdirectory(staging, parent, subdirectory string) (string, error) {
	clean := path.Clean(subdirectory)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != subdirectory || strings.HasPrefix(subdirectory, "/") || strings.Contains(subdirectory, "\\") {
		return "", errors.New("install subdirectory must be a safe relative path")
	}
	for _, segment := range strings.Split(subdirectory, "/") {
		if hasUnsafeSegment(segment) || (runtime.GOOS == "windows" && hasWindowsDeviceName(segment)) {
			return "", errors.New("install subdirectory contains an unsafe path segment")
		}
	}
	layout, err := os.MkdirTemp(parent, ".tool-layout-*")
	if err != nil {
		return "", fmt.Errorf("create wrapped installation directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(layout)
		}
	}()
	target := filepath.Join(layout, filepath.FromSlash(subdirectory))
	if err := os.MkdirAll(target, 0o700); err != nil {
		return "", fmt.Errorf("create install subdirectory: %w", err)
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return "", fmt.Errorf("read extracted files: %w", err)
	}
	if len(entries) == 0 {
		return "", errors.New("extracted archive is empty")
	}
	for _, entry := range entries {
		from := filepath.Join(staging, entry.Name())
		to := filepath.Join(target, entry.Name())
		if err := os.Rename(from, to); err != nil {
			return "", fmt.Errorf("place extracted entry %q: %w", entry.Name(), err)
		}
	}
	keep = true
	return layout, nil
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
