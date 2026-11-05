package sources

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Kind string

const (
	Official Kind = "official"
	Regional Kind = "regional"
	Custom   Kind = "custom"
	Offline  Kind = "offline"
)

type Policy string

const (
	OfficialOnly  Policy = "official-only"
	OfficialFirst Policy = "official-first"
	CustomOnly    Policy = "custom-only"
)

type RemoteSource struct {
	Name                 string
	Kind                 Kind
	URL                  string
	SHA256               string
	Size                 int64
	PublicKey            ed25519.PublicKey
	Signature            []byte
	AllowedRedirectHosts []string
}

type DownloadResult struct {
	Path       string
	SourceName string
	SourceKind Kind
	URL        string
	SHA256     string
	Size       int64
}

type DownloadProgress struct {
	SourceName    string
	BytesReceived int64
	TotalBytes    int64
}

type DownloadOptions struct {
	Transport   http.RoundTripper
	MaxAttempts int
	MaxBytes    int64
	Timeout     time.Duration
	Backoff     time.Duration
	Sleep       func(context.Context, time.Duration) error
	Jitter      func(time.Duration) time.Duration
}

type Downloader struct {
	options DownloadOptions
}

const (
	defaultMaxAttempts = 3
	defaultMaxBytes    = 1 << 30
	maxArtifactBytes   = 8 << 30
	defaultTimeout     = 10 * time.Minute
	maxRedirects       = 5
	maxRetryDelay      = 30 * time.Second
)

var ErrIntegrity = errors.New("downloaded artifact failed integrity verification")

func NewDownloader(options DownloadOptions) *Downloader {
	if options.MaxAttempts < 1 {
		options.MaxAttempts = defaultMaxAttempts
	} else if options.MaxAttempts > 5 {
		options.MaxAttempts = 5
	}
	if options.MaxBytes < 1 {
		options.MaxBytes = defaultMaxBytes
	}
	if options.Timeout <= 0 {
		options.Timeout = defaultTimeout
	}
	if options.Backoff <= 0 {
		options.Backoff = 250 * time.Millisecond
	}
	if options.Sleep == nil {
		options.Sleep = sleepContext
	}
	if options.Jitter == nil {
		options.Jitter = func(delay time.Duration) time.Duration {
			if delay <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(delay) + 1))
		}
	}
	return &Downloader{options: options}
}

// WithArtifactLimit returns a copy whose download limit is raised only when
// the caller still has the default limit. Explicitly smaller limits stay in
// force, so tests and embedding applications can impose tighter bounds.
func (d *Downloader) WithArtifactLimit(maxBytes int64) (*Downloader, error) {
	if d == nil {
		return nil, errors.New("downloader is required")
	}
	if maxBytes < 1 || maxBytes > maxArtifactBytes {
		return nil, fmt.Errorf("artifact download limit must be between 1 byte and %d bytes", maxArtifactBytes)
	}
	copy := *d
	if maxBytes > copy.options.MaxBytes {
		if copy.options.MaxBytes != defaultMaxBytes {
			return nil, fmt.Errorf("artifact requires a %d byte download limit but the configured downloader is capped at %d bytes", maxBytes, copy.options.MaxBytes)
		}
		copy.options.MaxBytes = maxBytes
	}
	return &copy, nil
}

func (d *Downloader) Download(ctx context.Context, policy Policy, candidates []RemoteSource, workspace string) (DownloadResult, error) {
	return d.DownloadWithProgress(ctx, policy, candidates, workspace, nil)
}

func (d *Downloader) DownloadWithProgress(ctx context.Context, policy Policy, candidates []RemoteSource, workspace string, progress func(DownloadProgress)) (DownloadResult, error) {
	ordered, err := orderSources(policy, candidates)
	if err != nil {
		return DownloadResult{}, err
	}
	if workspace == "" {
		return DownloadResult{}, errors.New("temporary download workspace is required")
	}
	info, err := os.Lstat(workspace)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("inspect temporary download workspace: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return DownloadResult{}, errors.New("temporary download workspace must be a real directory")
	}

	var lastErr error
	for _, source := range ordered {
		for attempt := 0; attempt < d.options.MaxAttempts; attempt++ {
			result, retryAfter, attemptErr := d.downloadOnce(ctx, source, workspace, progress)
			if attemptErr == nil {
				return result, nil
			}
			lastErr = attemptErr
			if !retryable(attemptErr) {
				return DownloadResult{}, attemptErr
			}
			if attempt+1 < d.options.MaxAttempts {
				delay := retryDelay(d.options.Backoff, attempt, retryAfter, d.options.Jitter)
				if err := d.options.Sleep(ctx, delay); err != nil {
					return DownloadResult{}, err
				}
			}
		}
		if source.Kind != Official || len(ordered) == 1 || ordered[1].Kind != Regional {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no eligible download source was attempted")
	}
	return DownloadResult{}, lastErr
}

func orderSources(policy Policy, candidates []RemoteSource) ([]RemoteSource, error) {
	if len(candidates) == 0 {
		return nil, errors.New("no download sources are configured")
	}
	var official, regional, custom []RemoteSource
	for _, source := range candidates {
		if err := validateSource(source); err != nil {
			return nil, err
		}
		switch source.Kind {
		case Official:
			official = append(official, source)
		case Regional:
			regional = append(regional, source)
		case Custom:
			custom = append(custom, source)
		default:
			return nil, fmt.Errorf("source %q has unsupported kind %q", source.Name, source.Kind)
		}
	}
	switch policy {
	case OfficialOnly:
		if len(official) != 1 {
			return nil, errors.New("official-only policy requires exactly one official source")
		}
		return official, nil
	case OfficialFirst:
		if len(official) != 1 {
			return nil, errors.New("official-first policy requires exactly one official source")
		}
		for _, mirror := range regional {
			if mirror.SHA256 != official[0].SHA256 || (official[0].Size > 0 && mirror.Size != official[0].Size) {
				return nil, fmt.Errorf("regional source %q is not bound to the official artifact digest and size", mirror.Name)
			}
		}
		return append(append([]RemoteSource(nil), official...), regional...), nil
	case CustomOnly:
		if len(custom) != 1 {
			return nil, errors.New("custom-only policy requires exactly one custom source")
		}
		return custom, nil
	default:
		return nil, fmt.Errorf("unsupported source policy %q", policy)
	}
}

func validateSource(source RemoteSource) error {
	if strings.TrimSpace(source.Name) == "" {
		return errors.New("download source name is required")
	}
	u, err := url.Parse(source.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("source %q must use a valid HTTPS URL without credentials or fragments", source.Name)
	}
	if len(source.SHA256) != sha256.Size*2 {
		return fmt.Errorf("source %q must declare a SHA-256 digest", source.Name)
	}
	if _, err := hex.DecodeString(source.SHA256); err != nil {
		return fmt.Errorf("source %q has an invalid SHA-256 digest", source.Name)
	}
	if source.Size < 0 {
		return fmt.Errorf("source %q has a negative artifact size", source.Name)
	}
	if (len(source.PublicKey) == 0) != (len(source.Signature) == 0) {
		return fmt.Errorf("source %q must provide both a signing key and signature", source.Name)
	}
	if len(source.PublicKey) > 0 && (len(source.PublicKey) != ed25519.PublicKeySize || len(source.Signature) != ed25519.SignatureSize) {
		return fmt.Errorf("source %q has an invalid Ed25519 key or signature length", source.Name)
	}
	if source.Kind != Official && source.Kind != Regional && source.Kind != Custom {
		return fmt.Errorf("source %q has unsupported kind %q", source.Name, source.Kind)
	}
	for _, host := range source.AllowedRedirectHosts {
		if host == "" || strings.ContainsAny(host, "/:@?#") {
			return fmt.Errorf("source %q has an invalid redirect host %q", source.Name, host)
		}
	}
	return nil
}

func ValidateRemoteSource(source RemoteSource) error {
	return validateSource(source)
}

func (d *Downloader) CopyLocal(ctx context.Context, sourcePath, expectedSHA256, workspace string, publicKey ed25519.PublicKey, signature []byte) (DownloadResult, error) {
	return d.CopyLocalWithProgress(ctx, sourcePath, expectedSHA256, workspace, publicKey, signature, nil)
}

func (d *Downloader) CopyLocalWithProgress(ctx context.Context, sourcePath, expectedSHA256, workspace string, publicKey ed25519.PublicKey, signature []byte, progress func(DownloadProgress)) (DownloadResult, error) {
	if !filepath.IsAbs(sourcePath) {
		return DownloadResult{}, errors.New("offline archive path must be absolute")
	}
	if len(expectedSHA256) != sha256.Size*2 {
		return DownloadResult{}, errors.New("offline archive requires a pinned SHA-256 digest")
	}
	if _, err := hex.DecodeString(expectedSHA256); err != nil {
		return DownloadResult{}, errors.New("offline archive has an invalid expected SHA-256 digest")
	}
	if (len(publicKey) == 0) != (len(signature) == 0) || (len(publicKey) > 0 && (len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize)) {
		return DownloadResult{}, errors.New("offline archive signature metadata is incomplete or malformed")
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("inspect offline archive: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return DownloadResult{}, errors.New("offline archive must be a regular non-symlink file")
	}
	if info.Size() > d.options.MaxBytes {
		return DownloadResult{}, fmt.Errorf("offline archive exceeds the %d byte safety limit", d.options.MaxBytes)
	}
	input, err := os.Open(sourcePath)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("open offline archive: %w", err)
	}
	defer input.Close()
	openedInfo, err := input.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return DownloadResult{}, errors.New("offline archive changed while it was being opened")
	}
	output, err := os.CreateTemp(workspace, "dx-offline-*.partial")
	if err != nil {
		return DownloadResult{}, fmt.Errorf("create temporary offline archive: %w", err)
	}
	path := output.Name()
	cleanup := func() { _ = output.Close(); _ = os.Remove(path) }
	if err := output.Chmod(0o600); err != nil {
		cleanup()
		return DownloadResult{}, fmt.Errorf("restrict temporary offline archive permissions: %w", err)
	}
	hasher := sha256.New()
	reader := &progressReader{reader: io.LimitReader(contextReader{ctx: ctx, reader: input}, d.options.MaxBytes+1), sourceName: "offline local archive", totalBytes: info.Size(), report: progress}
	written, copyErr := io.Copy(io.MultiWriter(output, hasher), reader)
	reader.ReportFinal()
	if copyErr != nil {
		cleanup()
		return DownloadResult{}, fmt.Errorf("copy offline archive: %w", copyErr)
	}
	if written > d.options.MaxBytes {
		cleanup()
		return DownloadResult{}, fmt.Errorf("offline archive exceeds the %d byte safety limit", d.options.MaxBytes)
	}
	if err := ctx.Err(); err != nil {
		cleanup()
		return DownloadResult{}, err
	}
	actualBytes := hasher.Sum(nil)
	if !strings.EqualFold(hex.EncodeToString(actualBytes), expectedSHA256) {
		cleanup()
		return DownloadResult{}, fmt.Errorf("%w: offline archive SHA-256 mismatch", ErrIntegrity)
	}
	if len(publicKey) > 0 && !ed25519.Verify(publicKey, actualBytes, signature) {
		cleanup()
		return DownloadResult{}, fmt.Errorf("%w: offline archive Ed25519 signature mismatch", ErrIntegrity)
	}
	if err := output.Sync(); err != nil {
		cleanup()
		return DownloadResult{}, fmt.Errorf("flush temporary offline archive: %w", err)
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(path)
		return DownloadResult{}, fmt.Errorf("close temporary offline archive: %w", err)
	}
	return DownloadResult{Path: path, SourceName: "offline local archive", SourceKind: Offline, URL: (&url.URL{Scheme: "file", Path: filepath.ToSlash(sourcePath)}).String(), SHA256: hex.EncodeToString(actualBytes), Size: written}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func (d *Downloader) downloadOnce(parent context.Context, source RemoteSource, workspace string, progress func(DownloadProgress)) (DownloadResult, time.Duration, error) {
	ctx, cancel := context.WithTimeout(parent, d.options.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return DownloadResult{}, 0, fmt.Errorf("create request for %s: %w", source.Name, err)
	}
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "developers-club-dx")
	client := &http.Client{
		Transport: d.options.Transport,
		CheckRedirect: func(request *http.Request, previous []*http.Request) error {
			if len(previous) >= maxRedirects {
				return errors.New("download exceeded the redirect limit")
			}
			if request.URL.Scheme != "https" || request.URL.User != nil || !allowedRedirect(source, request.URL) {
				return fmt.Errorf("download redirected to an unapproved origin")
			}
			return nil
		},
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return DownloadResult{}, 0, ctx.Err()
		}
		return DownloadResult{}, 0, fmt.Errorf("download from %s: %w", source.Name, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		statusErr := &statusError{code: response.StatusCode}
		return DownloadResult{}, parseRetryAfter(response.Header.Get("Retry-After")), fmt.Errorf("download from %s returned HTTP %d: %w", source.Name, response.StatusCode, statusErr)
	}
	if response.ContentLength > d.options.MaxBytes || (source.Size > d.options.MaxBytes) {
		return DownloadResult{}, 0, fmt.Errorf("artifact from %s exceeds the %d byte safety limit", source.Name, d.options.MaxBytes)
	}
	file, err := os.CreateTemp(workspace, "dx-artifact-*.partial")
	if err != nil {
		return DownloadResult{}, 0, fmt.Errorf("create temporary artifact: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = file.Close(); _ = os.Remove(path) }
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return DownloadResult{}, 0, fmt.Errorf("restrict temporary artifact permissions: %w", err)
	}
	hasher := sha256.New()
	totalBytes := response.ContentLength
	if totalBytes < 0 {
		totalBytes = source.Size
	}
	reader := &progressReader{reader: io.LimitReader(response.Body, d.options.MaxBytes+1), sourceName: source.Name, totalBytes: totalBytes, report: progress}
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), reader)
	reader.ReportFinal()
	if copyErr != nil {
		cleanup()
		return DownloadResult{}, 0, fmt.Errorf("read artifact from %s: %w", source.Name, copyErr)
	}
	if written > d.options.MaxBytes {
		cleanup()
		return DownloadResult{}, 0, fmt.Errorf("artifact from %s exceeds the %d byte safety limit", source.Name, d.options.MaxBytes)
	}
	if response.ContentLength >= 0 && written != response.ContentLength {
		cleanup()
		return DownloadResult{}, 0, fmt.Errorf("download from %s ended before the HTTP content length: %w", source.Name, io.ErrUnexpectedEOF)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return DownloadResult{}, 0, fmt.Errorf("flush temporary artifact: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return DownloadResult{}, 0, fmt.Errorf("close temporary artifact: %w", err)
	}
	if source.Size > 0 && written != source.Size {
		_ = os.Remove(path)
		return DownloadResult{}, 0, fmt.Errorf("%w: source %s returned %d bytes; expected %d", ErrIntegrity, source.Name, written, source.Size)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, source.SHA256) {
		_ = os.Remove(path)
		return DownloadResult{}, 0, fmt.Errorf("%w: SHA-256 mismatch from %s", ErrIntegrity, source.Name)
	}
	if len(source.PublicKey) > 0 && !ed25519.Verify(source.PublicKey, hasher.Sum(nil), source.Signature) {
		_ = os.Remove(path)
		return DownloadResult{}, 0, fmt.Errorf("%w: Ed25519 signature mismatch from %s", ErrIntegrity, source.Name)
	}
	return DownloadResult{Path: path, SourceName: source.Name, SourceKind: source.Kind, URL: source.URL, SHA256: actual, Size: written}, 0, nil
}

type progressReader struct {
	reader            io.Reader
	sourceName        string
	bytesRead         int64
	totalBytes        int64
	lastReported      time.Time
	lastReportedBytes int64
	report            func(DownloadProgress)
}

func (reader *progressReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	if count > 0 {
		reader.bytesRead += int64(count)
		if reader.lastReported.IsZero() || time.Since(reader.lastReported) >= 250*time.Millisecond || (reader.totalBytes > 0 && reader.bytesRead >= reader.totalBytes) {
			reader.ReportFinal()
		}
	}
	return count, err
}

func (reader *progressReader) ReportFinal() {
	if reader.report == nil || reader.lastReportedBytes == reader.bytesRead {
		return
	}
	reader.report(DownloadProgress{SourceName: reader.sourceName, BytesReceived: reader.bytesRead, TotalBytes: reader.totalBytes})
	reader.lastReported = time.Now()
	reader.lastReportedBytes = reader.bytesRead
}

type statusError struct{ code int }

func (e *statusError) Error() string { return strconv.Itoa(e.code) }

func (e *statusError) Is(target error) bool {
	return target == errRetryableStatus && retryableStatus(e.code)
}

var errRetryableStatus = errors.New("retryable HTTP status")

func retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrIntegrity) {
		return false
	}
	if errors.Is(err, errRetryableStatus) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		var networkErr net.Error
		return errors.As(urlErr.Err, &networkErr)
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) || errors.Is(err, io.ErrUnexpectedEOF)
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func retryDelay(base time.Duration, retry int, retryAfter time.Duration, jitter func(time.Duration) time.Duration) time.Duration {
	delay := base << min(retry, 8)
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	if retryAfter > delay {
		delay = min(retryAfter, maxRetryDelay)
	}
	if jitter != nil {
		delay += jitter(delay)
	}
	return min(delay, maxRetryDelay)
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if timestamp, err := http.ParseTime(value); err == nil {
		return max(0, time.Until(timestamp))
	}
	return 0
}

func allowedRedirect(source RemoteSource, target *url.URL) bool {
	original, err := url.Parse(source.URL)
	if err != nil {
		return false
	}
	if strings.EqualFold(normalizedAuthority(original), normalizedAuthority(target)) {
		return true
	}
	for _, host := range source.AllowedRedirectHosts {
		if strings.EqualFold(host, target.Host) {
			return true
		}
	}
	return false
}

func normalizedAuthority(value *url.URL) string {
	if value.Port() == "" || value.Port() == "443" {
		return strings.ToLower(value.Hostname())
	}
	return strings.ToLower(value.Host)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
