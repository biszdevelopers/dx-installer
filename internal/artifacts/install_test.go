package artifacts

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/platform"
	"developers-club-dx/internal/sources"
)

func TestArchiveInstallerPlacesToolInVersionedUserLocationAndCleansTemporaryFiles(t *testing.T) {
	home := t.TempDir()
	tempRoot := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", filepath.Join(home, "LocalAppData"))
	}
	artifactBytes, binaryPath, archiveFormat := sampleGoArchive(t)
	digest := sha256.Sum256(artifactBytes)
	responseCount := 0
	installer := &ArchiveInstaller{
		TempRoot: tempRoot,
		Downloader: sources.NewDownloader(sources.DownloadOptions{
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				responseCount++
				if request.URL.String() != "https://downloads.example/go1.27.3" {
					t.Fatalf("requested URL = %q", request.URL)
				}
				return &http.Response{
					StatusCode:    http.StatusOK,
					Header:        make(http.Header),
					Body:          io.NopCloser(bytes.NewReader(artifactBytes)),
					Request:       request,
					ContentLength: int64(len(artifactBytes)),
				}, nil
			}),
		}),
	}
	manifestArtifact := tool.Artifact{
		Platform: platform.Detect().OS, Architecture: platform.Detect().Architecture,
		Version: "1.27.3", URL: "https://downloads.example/go1.27.3", SHA256: hex.EncodeToString(digest[:]),
		MaxDownloadBytes: 2 << 30, Format: archiveFormat, ArchiveRoot: "go",
	}

	result, err := installer.Install(context.Background(), "go", home, manifestArtifact, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "1.27.3" || result.SourceName == "" || result.SourceURL != "https://downloads.example/go1.27.3" || responseCount != 1 {
		t.Fatalf("install result = %#v, request count = %d", result, responseCount)
	}
	if _, err := os.Stat(filepath.Join(result.Path, binaryPath)); err != nil {
		t.Fatalf("installed binary does not exist: %v", err)
	}
	if result.CleanupNotice != "" {
		t.Fatalf("temporary cleanup notice = %q", result.CleanupNotice)
	}
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary install files remain: %#v", entries)
	}
	if runtime.GOOS == "windows" {
		if !filepath.IsAbs(result.Path) || filepath.Ext(filepath.Join(result.Path, binaryPath)) != ".exe" {
			t.Fatalf("Windows user-level target = %q", result.Path)
		}
	} else if filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(result.Path)))) != ".local" {
		t.Fatalf("Unix tool was installed outside ~/.local/opt: %q", result.Path)
	}
}

func TestPrepareManyRunsIndependentDownloadsWithBoundedConcurrency(t *testing.T) {
	contents := []byte("verified archive bytes")
	digest := sha256.Sum256(contents)
	var active atomic.Int32
	var maximum atomic.Int32
	var firstBatch atomic.Int32
	var releaseOnce sync.Once
	releaseFirstBatch := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		for observed := maximum.Load(); current > observed && !maximum.CompareAndSwap(observed, current); observed = maximum.Load() {
		}
		if firstBatch.Add(1) == DefaultConcurrentDownloads {
			releaseOnce.Do(func() { close(releaseFirstBatch) })
		}
		select {
		case <-releaseFirstBatch:
		case <-time.After(3 * time.Second):
		}
		_, _ = output.Write(contents)
		active.Add(-1)
	}))
	defer server.Close()
	machine := platform.Detect()
	artifact := tool.Artifact{
		Platform: machine.OS, Architecture: machine.Architecture, Version: "1.0.0",
		URL: server.URL, SHA256: hex.EncodeToString(digest[:]), Format: tool.Zip,
	}
	requests := make([]PrepareRequest, 5)
	for index, id := range []string{"go", "cmake", "ninja", "clang", "make"} {
		requests[index] = PrepareRequest{ToolID: id, Home: t.TempDir(), Artifact: artifact}
	}
	installer := &ArchiveInstaller{
		Downloader: sources.NewDownloader(sources.DownloadOptions{Transport: server.Client().Transport}),
		TempRoot:   t.TempDir(),
	}
	results := installer.PrepareMany(context.Background(), requests, nil)
	if len(results) != len(requests) {
		t.Fatalf("prepared %d archives, want %d", len(results), len(requests))
	}
	if maximum.Load() != DefaultConcurrentDownloads {
		t.Fatalf("maximum concurrent downloads = %d, want default %d", maximum.Load(), DefaultConcurrentDownloads)
	}
	for _, result := range results {
		if result.Err != nil || result.Prepared == nil || result.Progress.BytesReceived != int64(len(contents)) {
			t.Errorf("prepared result = %#v", result)
		}
		if result.Prepared != nil {
			if err := result.Prepared.Cleanup(); err != nil {
				t.Errorf("cleanup %s archive: %v", result.ToolID, err)
			}
		}
	}
}

func TestArchiveInstallerOptionsBoundConcurrencyAndTimeout(t *testing.T) {
	timeout := 2 * time.Minute
	installer, err := NewArchiveInstallerWithOptions(ArchiveInstallerOptions{
		MaxConcurrentDownloads: 5,
		DownloadTimeout:        timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if installer.Concurrency() != 5 || installer.DownloadTimeout != timeout {
		t.Fatalf("installer options were not applied: concurrency=%d timeout=%s", installer.Concurrency(), installer.DownloadTimeout)
	}
	for _, concurrency := range []int{-1, MaxConcurrentDownloads + 1} {
		if _, err := NewArchiveInstallerWithOptions(ArchiveInstallerOptions{MaxConcurrentDownloads: concurrency}); err == nil {
			t.Errorf("accepted concurrency %d outside the supported range", concurrency)
		}
	}
	if _, err := NewArchiveInstallerWithOptions(ArchiveInstallerOptions{DownloadTimeout: MaxDownloadTimeout + time.Second}); err == nil {
		t.Error("accepted an excessive download timeout")
	}
}

func TestExtractWithCanceledContextDoesNotWriteArchiveEntries(t *testing.T) {
	archiveBytes, _, format := sampleGoArchive(t)
	root := t.TempDir()
	archivePath := filepath.Join(root, "tool.archive")
	if err := os.WriteFile(archivePath, archiveBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "extract")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ExtractWithContext(ctx, archivePath, destination, "go", format, Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled extraction error = %v", err)
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("canceled extraction wrote entries: %#v", entries)
	}
}

func TestZephyrSDKInstallPathUsesVendorDefaultLocation(t *testing.T) {
	home := t.TempDir()
	got, err := InstallationPath(platform.Detect().OS, "zephyr-sdk", "1.0.1", home)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "opt", "zephyr-sdk-1.0.1")
	if runtime.GOOS == "windows" {
		want = filepath.Join(home, "zephyr-sdk-1.0.1")
	}
	if got != want {
		t.Fatalf("Zephyr SDK install path = %q, want %q", got, want)
	}
}

func TestPostInstallRejectsUnknownAction(t *testing.T) {
	if err := runPostInstallAction(context.Background(), t.TempDir(), "run-arbitrary-command"); err == nil {
		t.Fatal("unknown post-install action was accepted")
	}
}

func TestPostInstallRunsOnlyTypedVendorSetupAction(t *testing.T) {
	root := t.TempDir()
	if runtime.GOOS == "windows" {
		if err := os.WriteFile(filepath.Join(root, "7z.exe"), []byte("test 7-Zip command"), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	if err := os.MkdirAll(filepath.Join(root, "gnu", "arm-zephyr-eabi"), 0o700); err != nil {
		t.Fatal(err)
	}
	scriptName := "setup.sh"
	script := []byte("#!/bin/sh\n[ \"$1\" = \"-t\" ] && [ \"$2\" = \"arm-zephyr-eabi\" ] && [ \"$3\" = \"-h\" ]\n")
	if runtime.GOOS == "windows" {
		scriptName = "setup.cmd"
		script = []byte("@echo off\r\nif /i not \"%~1\"==\"/t\" exit /b 11\r\nif /i not \"%~2\"==\"arm-zephyr-eabi\" exit /b 12\r\nexit /b 0\r\n")
	}
	if err := os.WriteFile(filepath.Join(root, scriptName), script, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runPostInstallAction(context.Background(), root, tool.ZephyrSDKSetup); err != nil {
		t.Fatalf("typed Zephyr setup action failed to launch: %v", err)
	}
}

func TestPrependExecutableDirectoryPreservesOtherEnvironment(t *testing.T) {
	environment := []string{"HOME=example", "Path=original", "OTHER=value"}
	got := prependExecutableDirectory(environment, "tool-bin")
	if len(got) != 3 || got[1] != "Path=tool-bin"+string(os.PathListSeparator)+"original" || got[0] != environment[0] || got[2] != environment[2] {
		t.Fatalf("updated environment = %#v", got)
	}
	if environment[1] != "Path=original" {
		t.Fatalf("input environment was mutated: %#v", environment)
	}
}

func TestPostInstallRejectsMissingBundledZephyrToolchain(t *testing.T) {
	root := t.TempDir()
	if err := runPostInstallAction(context.Background(), root, tool.ZephyrSDKSetup); err == nil {
		t.Fatal("Zephyr setup was allowed to run without the pinned bundled toolchain")
	}
}

func TestArchiveInstallerRefusesExistingTargetsBeforeDownloading(t *testing.T) {
	home := t.TempDir()
	parent, err := installParent(platform.Detect().OS, "go", home)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "1.27.3")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	called := false
	installer := &ArchiveInstaller{Downloader: sources.NewDownloader(sources.DownloadOptions{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			called = true
			return nil, io.EOF
		}),
	})}
	artifact := tool.Artifact{Platform: platform.Detect().OS, Architecture: platform.Detect().Architecture, Version: "1.27.3"}
	if _, err := installer.Install(context.Background(), "go", home, artifact, false); err == nil {
		t.Fatal("existing version directory was overwritten")
	}
	if called {
		t.Fatal("installer downloaded an archive before detecting the existing target")
	}
}

func TestArchiveLayoutCanPlaceCommandLineToolsAtExpectedSDKPath(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(staging, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "bin", "sdkmanager"), []byte("sdkmanager"), 0o700); err != nil {
		t.Fatal(err)
	}
	layout, err := placeInSubdirectory(staging, root, "cmdline-tools/latest")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(layout, "cmdline-tools", "latest", "bin", "sdkmanager")); err != nil {
		t.Fatalf("SDK command-line tool was not placed in the expected layout: %v", err)
	}
	remaining, err := os.ReadDir(staging)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("original staging directory is not empty after layout creation: %v, %v", remaining, err)
	}
	if _, err := placeInSubdirectory(staging, root, "../escape"); err == nil {
		t.Fatal("traversing install subdirectory was accepted")
	}
}

func sampleGoArchive(t *testing.T) ([]byte, string, tool.ArchiveFormat) {
	t.Helper()
	if runtime.GOOS == "windows" {
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		header := &zip.FileHeader{Name: "go/bin/go.exe"}
		header.SetMode(0o755)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("go binary")); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes(), filepath.Join("bin", "go.exe"), tool.Zip
	}
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	if err := writer.WriteHeader(&tar.Header{Name: "go/bin", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	binary := []byte("go binary")
	if err := writer.WriteHeader(&tar.Header{Name: "go/bin/go", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(binary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes(), filepath.Join("bin", "go"), tool.TarGzip
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
