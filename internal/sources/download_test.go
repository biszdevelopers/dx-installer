package sources

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadVerifiesHashAndEd25519Signature(t *testing.T) {
	contents := []byte("verified tool archive")
	digest := sha256.Sum256(contents)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, digest[:])
	server := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
		_, _ = output.Write(contents)
	}))
	defer server.Close()
	workspace := t.TempDir()
	var latestProgress DownloadProgress
	var progressEvents []DownloadProgress

	result, err := NewDownloader(DownloadOptions{Transport: server.Client().Transport}).DownloadWithProgress(context.Background(), OfficialOnly, []RemoteSource{{
		Name:      "publisher",
		Kind:      Official,
		URL:       server.URL,
		SHA256:    hex.EncodeToString(digest[:]),
		Size:      int64(len(contents)),
		PublicKey: publicKey,
		Signature: signature,
	}}, workspace, func(progress DownloadProgress) {
		latestProgress = progress
		progressEvents = append(progressEvents, progress)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceKind != Official || result.URL != server.URL || result.Size != int64(len(contents)) || result.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("download result = %#v", result)
	}
	if latestProgress.BytesReceived != int64(len(contents)) || latestProgress.TotalBytes != int64(len(contents)) || latestProgress.SourceName != "publisher" {
		t.Fatalf("download progress = %#v", latestProgress)
	}
	phases := make(map[string]bool)
	for _, progress := range progressEvents {
		phases[progress.Phase] = true
	}
	if !phases["downloading"] || !phases["verifying SHA-256 and signature"] || !phases["archive verified"] {
		t.Fatalf("download progress phases = %#v", phases)
	}
	got, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(contents) {
		t.Fatalf("downloaded bytes = %q, want %q", got, contents)
	}
}

func TestWithArtifactLimitRaisesOnlyTheDefaultSafetyLimit(t *testing.T) {
	const required = int64(2 << 30)
	defaultDownloader := NewDownloader(DownloadOptions{})
	large, err := defaultDownloader.WithArtifactLimit(required)
	if err != nil {
		t.Fatal(err)
	}
	if large.options.MaxBytes != required || defaultDownloader.options.MaxBytes != defaultMaxBytes {
		t.Fatalf("raised limit = %d, original limit = %d", large.options.MaxBytes, defaultDownloader.options.MaxBytes)
	}
	strict, err := NewDownloader(DownloadOptions{MaxBytes: 512}).WithArtifactLimit(required)
	if err == nil || strict != nil {
		t.Fatalf("explicitly capped downloader was raised: %#v, %v", strict, err)
	}
	if _, err := defaultDownloader.WithArtifactLimit(maxArtifactBytes + 1); err == nil {
		t.Fatal("limit above the implementation maximum was accepted")
	}
}

func TestCopyLocalVerifiesPinnedDigestWithoutChangingTheBundle(t *testing.T) {
	contents := []byte("offline archive payload")
	digest := sha256.Sum256(contents)
	root := t.TempDir()
	sourcePath := filepath.Join(root, "tool.tar.gz")
	if err := os.WriteFile(sourcePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	result, err := NewDownloader(DownloadOptions{}).CopyLocal(context.Background(), sourcePath, hex.EncodeToString(digest[:]), workspace, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceKind != Offline || result.Size != int64(len(contents)) || result.SHA256 != hex.EncodeToString(digest[:]) || !strings.HasPrefix(result.URL, "file:") {
		t.Fatalf("offline result = %#v", result)
	}
	if got, err := os.ReadFile(sourcePath); err != nil || !bytes.Equal(got, contents) {
		t.Fatalf("user bundle file changed: %q, %v", got, err)
	}
	if got, err := os.ReadFile(result.Path); err != nil || !bytes.Equal(got, contents) {
		t.Fatalf("temporary copied archive = %q, %v", got, err)
	}
}

func TestCopyLocalRejectsSymlinksAndDigestMismatch(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "archive.zip")
	if err := os.WriteFile(source, []byte("trusted bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	wrong := strings.Repeat("0", sha256.Size*2)
	if _, err := NewDownloader(DownloadOptions{}).CopyLocal(context.Background(), source, wrong, workspace, nil, nil); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("digest mismatch error = %v", err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(root, "archive-link.zip")
		if err := os.Symlink(source, link); err != nil {
			t.Fatal(err)
		}
		if _, err := NewDownloader(DownloadOptions{}).CopyLocal(context.Background(), link, wrong, workspace, nil, nil); err == nil {
			t.Fatal("symlink archive was accepted")
		}
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed local verification left temporary files: %#v", entries)
	}
}

func TestOfficialFirstRetriesThenUsesDigestBoundRegionalMirror(t *testing.T) {
	contents := []byte("same publisher artifact")
	digest := sha256.Sum256(contents)
	var officialAttempts atomic.Int32
	var regionalAttempts atomic.Int32
	official := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
		officialAttempts.Add(1)
		output.Header().Set("Retry-After", "0")
		http.Error(output, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer official.Close()
	regional := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
		regionalAttempts.Add(1)
		_, _ = output.Write(contents)
	}))
	defer regional.Close()
	workspace := t.TempDir()
	transport := trustedTransport(official, regional)

	result, err := NewDownloader(DownloadOptions{
		Transport:   transport,
		MaxAttempts: 2,
		Sleep:       func(context.Context, time.Duration) error { return nil },
		Jitter:      func(time.Duration) time.Duration { return 0 },
	}).Download(context.Background(), OfficialFirst, []RemoteSource{
		{Name: "publisher", Kind: Official, URL: official.URL, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(contents))},
		{Name: "verified mirror", Kind: Regional, URL: regional.URL, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(contents))},
	}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceName != "verified mirror" || officialAttempts.Load() != 2 || regionalAttempts.Load() != 1 {
		t.Fatalf("result = %#v; official attempts = %d, regional attempts = %d", result, officialAttempts.Load(), regionalAttempts.Load())
	}
}

func TestOfficialOnlyNeverUsesRegionalMirror(t *testing.T) {
	var mirrorAttempts atomic.Int32
	official := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
		http.Error(output, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer official.Close()
	mirror := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
		mirrorAttempts.Add(1)
	}))
	defer mirror.Close()
	digest := sha256.Sum256([]byte("archive"))

	_, err := NewDownloader(DownloadOptions{
		Transport:   official.Client().Transport,
		MaxAttempts: 1,
		Sleep:       func(context.Context, time.Duration) error { return nil },
	}).Download(context.Background(), OfficialOnly, []RemoteSource{
		{Name: "publisher", Kind: Official, URL: official.URL, SHA256: hex.EncodeToString(digest[:])},
		{Name: "mirror", Kind: Regional, URL: mirror.URL, SHA256: hex.EncodeToString(digest[:])},
	}, t.TempDir())
	if err == nil {
		t.Fatal("unavailable official source unexpectedly succeeded")
	}
	if mirrorAttempts.Load() != 0 {
		t.Fatalf("official-only policy contacted regional source %d times", mirrorAttempts.Load())
	}
}

func TestOfficialFirstDoesNotFallbackForHTTPOrIntegrityFailures(t *testing.T) {
	contents := []byte("trusted archive")
	digest := sha256.Sum256(contents)
	wrongDigest := sha256.Sum256([]byte("different archive"))
	for _, testCase := range []struct {
		name   string
		status int
		body   []byte
	}{
		{name: "not found", status: http.StatusNotFound, body: []byte("missing")},
		{name: "digest mismatch", status: http.StatusOK, body: []byte("tampered")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var mirrorAttempts atomic.Int32
			official := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
				output.WriteHeader(testCase.status)
				_, _ = output.Write(testCase.body)
			}))
			defer official.Close()
			mirror := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
				mirrorAttempts.Add(1)
				_, _ = output.Write(contents)
			}))
			defer mirror.Close()
			expected := digest
			if testCase.name == "digest mismatch" {
				expected = wrongDigest
			}
			_, err := NewDownloader(DownloadOptions{
				Transport:   official.Client().Transport,
				MaxAttempts: 1,
				Sleep:       func(context.Context, time.Duration) error { return nil },
			}).Download(context.Background(), OfficialFirst, []RemoteSource{
				{Name: "publisher", Kind: Official, URL: official.URL, SHA256: hex.EncodeToString(expected[:])},
				{Name: "mirror", Kind: Regional, URL: mirror.URL, SHA256: hex.EncodeToString(expected[:])},
			}, t.TempDir())
			if err == nil {
				t.Fatal("invalid official source unexpectedly succeeded")
			}
			if mirrorAttempts.Load() != 0 {
				t.Fatalf("non-retryable failure contacted mirror %d times", mirrorAttempts.Load())
			}
			if testCase.name == "digest mismatch" && !errors.Is(err, ErrIntegrity) {
				t.Fatalf("error = %v, want integrity failure", err)
			}
		})
	}
}

func trustedTransport(servers ...*httptest.Server) http.RoundTripper {
	roots := x509.NewCertPool()
	for _, server := range servers {
		roots.AddCert(server.Certificate())
	}
	return &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
}

func TestDownloadRejectsUnapprovedRedirectAndCleansTemporaryFile(t *testing.T) {
	var destinationAttempts atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
		destinationAttempts.Add(1)
		_, _ = io.WriteString(output, "unapproved")
	}))
	defer destination.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
		http.Redirect(output, request, destination.URL, http.StatusFound)
	}))
	defer redirect.Close()
	contents := []byte("unapproved")
	digest := sha256.Sum256(contents)
	workspace := t.TempDir()

	_, err := NewDownloader(DownloadOptions{Transport: redirect.Client().Transport}).Download(context.Background(), OfficialOnly, []RemoteSource{{
		Name:   "publisher",
		Kind:   Official,
		URL:    redirect.URL,
		SHA256: hex.EncodeToString(digest[:]),
	}}, workspace)
	if err == nil || !strings.Contains(err.Error(), "unapproved origin") {
		t.Fatalf("redirect error = %v, want rejected origin", err)
	}
	if destinationAttempts.Load() != 0 {
		t.Fatalf("unapproved redirect target received %d requests", destinationAttempts.Load())
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed download left temporary artifacts: %#v", entries)
	}
}

func TestSourcePolicyValidation(t *testing.T) {
	digest := sha256.Sum256([]byte("archive"))
	valid := RemoteSource{Name: "publisher", Kind: Official, URL: "https://downloads.example/tool.zip", SHA256: hex.EncodeToString(digest[:])}
	for _, testCase := range []struct {
		name    string
		policy  Policy
		sources []RemoteSource
		wantErr bool
	}{
		{name: "custom must be explicit", policy: CustomOnly, sources: []RemoteSource{valid}, wantErr: true},
		{name: "regional digest must match", policy: OfficialFirst, sources: []RemoteSource{valid, {Name: "mirror", Kind: Regional, URL: "https://mirror.example/tool.zip", SHA256: strings.Repeat("0", 64)}}, wantErr: true},
		{name: "reject plaintext HTTP", policy: OfficialOnly, sources: []RemoteSource{{Name: "publisher", Kind: Official, URL: "http://downloads.example/tool.zip", SHA256: valid.SHA256}}, wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := orderSources(testCase.policy, testCase.sources)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("orderSources error = %v, want error %v", err, testCase.wantErr)
			}
		})
	}
}
