package artifacts

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

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
		Format: archiveFormat, ArchiveRoot: "go",
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
