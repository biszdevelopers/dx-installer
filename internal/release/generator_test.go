package release

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"developers-club-dx/internal/security"
)

func TestBuildGenerateAndPinnedBinaryVerificationEndToEnd(t *testing.T) {
	target, ok := runtimeTarget()
	if !ok {
		t.Skipf("no release target is defined for the test host %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate the release source directory")
	}
	sourceDirectory := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	outputDirectory := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := BuildAndGenerate(ctx, sourceDirectory, "go", Config{
		Version:    "v9.8.7-test.1",
		Origin:     "https://example.invalid",
		OutputDir:  outputDirectory,
		Targets:    []Target{target},
		PublicKey:  publicKey,
		PrivateKey: privateKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Checksums) == 0 {
		t.Fatal("release did not include checksums")
	}
	checksums, err := os.ReadFile(filepath.Join(result.Directory, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	signatureFile, err := os.ReadFile(filepath.Join(result.Directory, "SHA256SUMS.sig"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := security.DecodeSignature(signatureFile)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := security.VerifySignedChecksums(checksums, signature, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.VerifyFiles(result.Directory, entries); err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(result.Directory, target.FileName())
	verifyContext, verifyCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer verifyCancel()
	verify := exec.CommandContext(verifyContext, binary, "verify-release", "--directory", result.Directory)
	output, err := verify.CombinedOutput()
	if err != nil {
		t.Fatalf("generated executable could not verify its signed release: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Verified signed release metadata") {
		t.Fatalf("unexpected verifier output: %s", output)
	}

	if _, err := Generate(Config{
		Version:     result.Version,
		Origin:      "https://example.invalid",
		ArtifactDir: t.TempDir(),
		OutputDir:   outputDirectory,
		Targets:     []Target{target},
		PublicKey:   publicKey,
		PrivateKey:  privateKey,
	}); err == nil {
		t.Fatal("generator overwrote an immutable versioned release")
	}
}

func TestTargetValidationRejectsUnknownAndDuplicateTargets(t *testing.T) {
	if _, err := ParseTarget("windows/unknown"); err == nil {
		t.Fatal("unknown architecture was accepted")
	}
	if _, err := ParseTarget("../windows/amd64"); err == nil {
		t.Fatal("malformed platform target was accepted")
	}
	if _, err := ParseTarget("freebsd/amd64"); err != nil {
		t.Fatalf("supported explicit BSD build target was rejected: %v", err)
	}
	if err := validateConfig(Config{
		Version:     "v1.0.0",
		Origin:      "https://example.invalid",
		ArtifactDir: "artifacts",
		OutputDir:   "dist",
		Targets:     []Target{{Platform: "windows", Architecture: "amd64"}, {Platform: "windows", Architecture: "amd64"}},
		PublicKey:   make(ed25519.PublicKey, ed25519.PublicKeySize),
		PrivateKey:  make(ed25519.PrivateKey, ed25519.PrivateKeySize),
	}); err == nil {
		t.Fatal("duplicate targets were accepted")
	}
}

func TestDefaultTargetsIncludeIntegrationTestedFreeBSD(t *testing.T) {
	targets := DefaultTargets()
	for _, target := range targets {
		if target.Platform == "freebsd" && target.Architecture == "amd64" {
			return
		}
	}
	t.Fatalf("default targets omitted FreeBSD amd64: %#v", targets)
}

func TestReleaseConfigurationRequiresPlainHTTPSHostnameAndSemanticVersion(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	base := Config{
		Version:     "v1.2.3-rc.1",
		Origin:      "https://dx.example.invalid",
		ArtifactDir: "artifacts",
		OutputDir:   "dist",
		Targets:     []Target{{Platform: "linux", Architecture: "amd64"}},
		PublicKey:   publicKey,
		PrivateKey:  privateKey,
	}
	if err := validateConfig(base); err != nil {
		t.Fatalf("valid release configuration was rejected: %v", err)
	}
	for name, change := range map[string]func(*Config){
		"leading zero in version":  func(config *Config) { config.Version = "v01.2.3" },
		"empty prerelease segment": func(config *Config) { config.Version = "v1.2.3-rc..1" },
		"origin path":              func(config *Config) { config.Origin = "https://dx.example.invalid/" },
		"origin port":              func(config *Config) { config.Origin = "https://dx.example.invalid:8443" },
		"origin credentials":       func(config *Config) { config.Origin = "https://user@dx.example.invalid" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := base
			change(&invalid)
			if err := validateConfig(invalid); err == nil {
				t.Fatal("invalid release configuration was accepted")
			}
		})
	}
}

func TestPowerShellBootstrapRejectsRedirectsBeforeExecutingDownloadedBinary(t *testing.T) {
	script, err := renderTemplate("templates/install.ps1.tmpl", map[string]string{
		"@@VERSION@@":            "v1.2.3",
		"@@ORIGIN@@":             "https://dx.example.invalid",
		"@@POWERSHELL_TARGETS@@": "    'windows-amd64' = @{ Name = 'dx-bootstrap-windows-amd64.exe'; SHA256 = '" + strings.Repeat("0", 64) + "' }",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "-MaximumRedirection 0") || strings.Contains(script, "ResponseUri") {
		t.Fatal("PowerShell bootstrap must reject redirects without relying on a response object returned by OutFile")
	}
}

func TestTargetEnvironmentDisablesUserGoBuildOverrides(t *testing.T) {
	t.Setenv("GOFLAGS", "-race -overlay=unsafe.json")
	t.Setenv("GOENV", "C:\\unexpected\\go.env")
	t.Setenv("GOWORK", "C:\\unexpected\\go.work")
	values := make(map[string]string)
	for _, entry := range targetEnvironment(Target{Platform: "linux", Architecture: "arm64"}) {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	if values["GOFLAGS"] != "" || values["GOENV"] != "off" || values["GOWORK"] != "off" || values["GOTOOLCHAIN"] != "local" {
		t.Fatalf("build overrides were not isolated: %#v", values)
	}
	if values["GOOS"] != "linux" || values["GOARCH"] != "arm64" || values["CGO_ENABLED"] != "0" {
		t.Fatalf("target tuple was not set: %#v", values)
	}
}

func TestLoadPrivateKeyAcceptsRawSeedAndRejectsInvalidKey(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "release.key")
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != ed25519.PrivateKeySize {
		t.Fatalf("loaded key size = %d", len(key))
	}
	if err := os.WriteFile(path, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrivateKey(path); err == nil {
		t.Fatal("invalid signing key was accepted")
	}
}

func TestLoadPrivateKeyPreservesWhitespaceBytesInRawSeed(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = '\t'
	seed[len(seed)-1] = ' '
	path := filepath.Join(t.TempDir(), "release.key")
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatal(err)
	}

	key, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	want := ed25519.NewKeyFromSeed(seed)
	if string(key) != string(want) {
		t.Fatal("raw signing key bytes were changed while loading")
	}
}

func runtimeTarget() (Target, bool) {
	platform := ""
	switch runtime.GOOS {
	case "windows":
		platform = "windows"
	case "darwin":
		platform = "macos"
	case "linux":
		platform = "linux"
	case "freebsd":
		platform = "freebsd"
	}
	if platform == "" {
		return Target{}, false
	}
	return Target{Platform: platform, Architecture: runtime.GOARCH}, true
}
