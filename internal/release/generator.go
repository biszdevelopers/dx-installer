package release

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"embed"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"developers-club-dx/internal/security"
)

//go:embed templates/*.tmpl
var templateFiles embed.FS

type Target struct {
	Platform     string
	Architecture string
}

type Config struct {
	Version     string
	Origin      string
	ArtifactDir string
	OutputDir   string
	Targets     []Target
	PublicKey   ed25519.PublicKey
	PrivateKey  ed25519.PrivateKey
}

type Result struct {
	Version   string
	Directory string
	Targets   []Target
	Checksums []security.Checksum
}

var releaseVersionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
var releaseHostnamePattern = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

func DefaultTargets() []Target {
	return []Target{
		{Platform: "windows", Architecture: "amd64"},
		{Platform: "windows", Architecture: "arm64"},
		{Platform: "macos", Architecture: "amd64"},
		{Platform: "macos", Architecture: "arm64"},
		{Platform: "linux", Architecture: "amd64"},
		{Platform: "linux", Architecture: "arm64"},
		{Platform: "freebsd", Architecture: "amd64"},
	}
}

func ParseTarget(value string) (Target, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return Target{}, fmt.Errorf("target %q must use platform/architecture format", value)
	}
	target := Target{Platform: parts[0], Architecture: parts[1]}
	if err := target.Validate(); err != nil {
		return Target{}, err
	}
	return target, nil
}

func (t Target) String() string { return t.Platform + "/" + t.Architecture }

func (t Target) Key() string { return t.Platform + "-" + t.Architecture }

func (t Target) FileName() string {
	name := "dx-bootstrap-" + t.Platform + "-" + t.Architecture
	if t.Platform == "windows" {
		name += ".exe"
	}
	return name
}

func (t Target) goOS() string {
	if t.Platform == "macos" {
		return "darwin"
	}
	return t.Platform
}

func (t Target) Validate() error {
	allowed := map[string]map[string]bool{
		"windows": {"amd64": true, "arm64": true},
		"macos":   {"amd64": true, "arm64": true},
		"linux":   {"amd64": true, "arm64": true},
		"freebsd": {"amd64": true},
	}
	if architectures, exists := allowed[t.Platform]; !exists || !architectures[t.Architecture] {
		return fmt.Errorf("unsupported release target %q", t.String())
	}
	return nil
}

func Generate(config Config) (Result, error) {
	if config.ArtifactDir == "" {
		return Result{}, errors.New("artifact directory is required when generating from prebuilt binaries")
	}
	if err := validateConfig(config); err != nil {
		return Result{}, err
	}
	if !bytes.Equal(config.PrivateKey.Public().(ed25519.PublicKey), config.PublicKey) {
		return Result{}, errors.New("signing private key does not match the supplied public key")
	}
	outputRoot, err := filepath.Abs(config.OutputDir)
	if err != nil {
		return Result{}, fmt.Errorf("resolve output directory: %w", err)
	}
	releaseRoot := filepath.Join(outputRoot, "releases")
	if err := os.MkdirAll(releaseRoot, 0o755); err != nil {
		return Result{}, fmt.Errorf("create release directory: %w", err)
	}
	releaseDirectory := filepath.Join(releaseRoot, config.Version)
	if _, err := os.Lstat(releaseDirectory); err == nil {
		return Result{}, fmt.Errorf("versioned release %s already exists and is immutable", config.Version)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("inspect versioned release path: %w", err)
	}
	stage, err := os.MkdirTemp(releaseRoot, ".staging-")
	if err != nil {
		return Result{}, fmt.Errorf("create release staging directory: %w", err)
	}
	defer os.RemoveAll(stage)

	artifactHashes := make(map[string]string, len(config.Targets))
	for _, target := range config.Targets {
		name := target.FileName()
		source := filepath.Join(config.ArtifactDir, name)
		contents, err := readAndVerifyArtifact(source, target)
		if err != nil {
			return Result{}, err
		}
		if err := writeFile(filepath.Join(stage, name), contents, 0o755); err != nil {
			return Result{}, fmt.Errorf("stage %s: %w", name, err)
		}
		artifactHashes[target.Key()] = security.SHA256(contents)
	}
	publicKey, err := security.EncodePublicKey(config.PublicKey)
	if err != nil {
		return Result{}, err
	}
	psTargets, shTargets := renderTargetMaps(config.Targets, artifactHashes)
	if hasPlatform(config.Targets, "windows") {
		psScript, err := renderTemplate("templates/install.ps1.tmpl", map[string]string{
			"@@VERSION@@":            config.Version,
			"@@ORIGIN@@":             config.Origin,
			"@@POWERSHELL_TARGETS@@": psTargets,
		})
		if err != nil {
			return Result{}, err
		}
		if err := writeFile(filepath.Join(stage, "install.ps1"), []byte(psScript), 0o644); err != nil {
			return Result{}, fmt.Errorf("write PowerShell entry point: %w", err)
		}
	}
	if hasUnixTarget(config.Targets) {
		shScript, err := renderTemplate("templates/install.sh.tmpl", map[string]string{
			"@@VERSION@@":       config.Version,
			"@@ORIGIN@@":        config.Origin,
			"@@SHELL_TARGETS@@": shTargets,
		})
		if err != nil {
			return Result{}, err
		}
		if err := writeFile(filepath.Join(stage, "install.sh"), []byte(shScript), 0o755); err != nil {
			return Result{}, fmt.Errorf("write POSIX entry point: %w", err)
		}
	}
	if err := writeReleaseMetadata(stage, config, publicKey); err != nil {
		return Result{}, err
	}
	checksums, err := checksumDirectory(stage)
	if err != nil {
		return Result{}, err
	}
	checksumBytes := formatChecksums(checksums)
	if err := writeFile(filepath.Join(stage, "SHA256SUMS"), checksumBytes, 0o644); err != nil {
		return Result{}, fmt.Errorf("write release checksums: %w", err)
	}
	signature := ed25519.Sign(config.PrivateKey, checksumBytes)
	encodedSignature, err := security.EncodeSignature(signature)
	if err != nil {
		return Result{}, err
	}
	if err := writeFile(filepath.Join(stage, "SHA256SUMS.sig"), []byte(encodedSignature+"\n"), 0o644); err != nil {
		return Result{}, fmt.Errorf("write release signature: %w", err)
	}
	verified, err := security.VerifySignedChecksums(checksumBytes, signature, config.PublicKey)
	if err != nil {
		return Result{}, fmt.Errorf("verify generated checksum signature: %w", err)
	}
	if err := security.VerifyReleaseFiles(stage, verified); err != nil {
		return Result{}, fmt.Errorf("verify staged release artifacts: %w", err)
	}
	if err := os.Rename(stage, releaseDirectory); err != nil {
		return Result{}, fmt.Errorf("atomically publish local versioned release: %w", err)
	}
	return Result{Version: config.Version, Directory: releaseDirectory, Targets: append([]Target(nil), config.Targets...), Checksums: verified}, nil
}

func BuildAndGenerate(ctx context.Context, sourceDirectory, goBinary string, config Config) (Result, error) {
	if err := validateConfig(config); err != nil {
		return Result{}, err
	}
	publicKey, err := security.EncodePublicKey(config.PublicKey)
	if err != nil {
		return Result{}, err
	}
	outputRoot, err := filepath.Abs(config.OutputDir)
	if err != nil {
		return Result{}, fmt.Errorf("resolve output directory: %w", err)
	}
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		return Result{}, fmt.Errorf("create output directory: %w", err)
	}
	artifactDirectory, err := os.MkdirTemp(outputRoot, ".build-artifacts-")
	if err != nil {
		return Result{}, fmt.Errorf("create temporary build directory: %w", err)
	}
	defer os.RemoveAll(artifactDirectory)
	for _, target := range config.Targets {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		outputPath := filepath.Join(artifactDirectory, target.FileName())
		linkerFlags := "-s -w -buildid= -X developers-club-dx/internal/security.PinnedEd25519PublicKey=" + publicKey
		command := exec.CommandContext(ctx, goBinary, "build", "-trimpath", "-buildvcs=false", "-ldflags", linkerFlags, "-o", outputPath, "./cmd/bootstrap")
		command.Dir = sourceDirectory
		command.Env = targetEnvironment(target)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return Result{}, fmt.Errorf("build bootstrap for %s: %w", target, err)
		}
		if _, err := readAndVerifyArtifact(outputPath, target); err != nil {
			return Result{}, err
		}
	}
	config.ArtifactDir = artifactDirectory
	return Generate(config)
}

func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect signing key file: %w", err)
	}
	if info.IsDir() {
		return nil, errors.New("signing key path must be a file")
	}
	if os.PathSeparator == '/' && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("signing key permissions %o are too broad; restrict the key to its owner", info.Mode().Perm())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read signing key file: %w", err)
	}
	if block, _ := pem.Decode(contents); block != nil {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#8 signing key: %w", err)
		}
		key, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("signing key is not Ed25519")
		}
		return key, nil
	}
	if len(contents) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(contents), nil
	}
	if len(contents) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(append([]byte(nil), contents...)), nil
	}
	trimmed := bytes.TrimSpace(contents)
	if len(trimmed) != ed25519.SeedSize && len(trimmed) != ed25519.PrivateKeySize {
		decoded, err := base64.StdEncoding.DecodeString(string(trimmed))
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(string(trimmed))
		}
		if err != nil {
			return nil, errors.New("signing key must be PEM, base64 Ed25519 key data, or raw 32-byte seed/64-byte key")
		}
		trimmed = decoded
	}
	if len(trimmed) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(trimmed), nil
	}
	if len(trimmed) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(append([]byte(nil), trimmed...)), nil
	}
	return nil, errors.New("Ed25519 signing key must be a 32-byte seed or 64-byte private key")
}

func validateConfig(config Config) error {
	if !releaseVersionPattern.MatchString(config.Version) {
		return fmt.Errorf("release version %q must use vMAJOR.MINOR.PATCH format", config.Version)
	}
	origin, err := url.Parse(config.Origin)
	hostname := ""
	if err == nil {
		hostname = origin.Hostname()
	}
	if err != nil || origin.Scheme != "https" || hostname == "" || origin.Host != hostname || !validReleaseHostname(hostname) || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return errors.New("release origin must be an HTTPS hostname without credentials, port, path, query, or fragment")
	}
	if config.OutputDir == "" {
		return errors.New("output directory is required")
	}
	if len(config.Targets) == 0 {
		return errors.New("at least one platform target is required")
	}
	if len(config.PublicKey) != ed25519.PublicKeySize || len(config.PrivateKey) != ed25519.PrivateKeySize {
		return errors.New("valid Ed25519 public and private keys are required")
	}
	seen := make(map[string]bool, len(config.Targets))
	for _, target := range config.Targets {
		if err := target.Validate(); err != nil {
			return err
		}
		if seen[target.Key()] {
			return fmt.Errorf("release target %q is duplicated", target)
		}
		seen[target.Key()] = true
	}
	return nil
}

func readAndVerifyArtifact(path string, target Target) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read bootstrap artifact for %s: %w", target, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("bootstrap artifact for %s must be a regular file", target)
	}
	if target.Platform != "windows" && info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("bootstrap artifact for %s is not executable", target)
	}
	if err := verifyBinary(path, target); err != nil {
		return nil, fmt.Errorf("bootstrap artifact for %s: %w", target, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bootstrap artifact for %s: %w", target, err)
	}
	return contents, nil
}

func verifyBinary(path string, target Target) error {
	switch target.Platform {
	case "windows":
		file, err := pe.Open(path)
		if err != nil {
			return fmt.Errorf("expected a PE executable: %w", err)
		}
		defer file.Close()
		var architecture string
		switch file.Machine {
		case pe.IMAGE_FILE_MACHINE_AMD64:
			architecture = "amd64"
		case pe.IMAGE_FILE_MACHINE_ARM64:
			architecture = "arm64"
		case pe.IMAGE_FILE_MACHINE_I386:
			architecture = "386"
		}
		if architecture != target.Architecture {
			return fmt.Errorf("PE architecture is %q; expected %q", architecture, target.Architecture)
		}
	case "macos":
		file, err := macho.Open(path)
		if err != nil {
			return fmt.Errorf("expected a Mach-O executable: %w", err)
		}
		defer file.Close()
		var architecture string
		switch file.Cpu {
		case macho.CpuAmd64:
			architecture = "amd64"
		case macho.CpuArm64:
			architecture = "arm64"
		case macho.Cpu386:
			architecture = "386"
		}
		if architecture != target.Architecture {
			return fmt.Errorf("Mach-O architecture is %q; expected %q", architecture, target.Architecture)
		}
	case "linux", "freebsd":
		file, err := elf.Open(path)
		if err != nil {
			return fmt.Errorf("expected an ELF executable: %w", err)
		}
		defer file.Close()
		var architecture string
		switch file.Machine {
		case elf.EM_X86_64:
			architecture = "amd64"
		case elf.EM_AARCH64:
			architecture = "arm64"
		case elf.EM_386:
			architecture = "386"
		case elf.EM_ARM:
			architecture = "arm"
		case elf.EM_RISCV:
			architecture = "riscv64"
		}
		if architecture != target.Architecture {
			return fmt.Errorf("ELF architecture is %q; expected %q", architecture, target.Architecture)
		}
		if target.Platform == "freebsd" && file.OSABI != elf.ELFOSABI_FREEBSD {
			return fmt.Errorf("ELF OS ABI is %s; expected FreeBSD", file.OSABI)
		}
		if target.Platform == "linux" && file.OSABI == elf.ELFOSABI_FREEBSD {
			return errors.New("ELF binary targets FreeBSD, not Linux")
		}
	default:
		return fmt.Errorf("unsupported platform %q", target.Platform)
	}
	return nil
}

func VerifyBinary(path string, target Target) error {
	if err := target.Validate(); err != nil {
		return err
	}
	return verifyBinary(path, target)
}

func renderTargetMaps(targets []Target, hashes map[string]string) (string, string) {
	sorted := append([]Target(nil), targets...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key() < sorted[j].Key() })
	var powershell, shell strings.Builder
	for _, target := range sorted {
		name := target.FileName()
		digest := hashes[target.Key()]
		if target.Platform == "windows" {
			fmt.Fprintf(&powershell, "    '%s' = @{ Name = '%s'; SHA256 = '%s' }\n", target.Key(), name, digest)
		} else {
			fmt.Fprintf(&shell, "    %s) binary_name='%s'; expected_hash='%s' ;;\n", target.Key(), name, digest)
		}
	}
	return strings.TrimSuffix(powershell.String(), "\n"), strings.TrimSuffix(shell.String(), "\n")
}

func renderTemplate(name string, replacements map[string]string) (string, error) {
	contents, err := templateFiles.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("read bootstrap template %s: %w", name, err)
	}
	keys := make([]string, 0, len(replacements))
	for key := range replacements {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		parts = append(parts, key, replacements[key])
	}
	rendered := strings.NewReplacer(parts...).Replace(string(contents))
	if strings.Contains(rendered, "@@") {
		return "", fmt.Errorf("bootstrap template %s has an unresolved value", name)
	}
	return rendered, nil
}

func writeReleaseMetadata(directory string, config Config, publicKey string) error {
	var contents strings.Builder
	fmt.Fprintf(&contents, "version=%s\norigin=%s\n", config.Version, config.Origin)
	targets := append([]Target(nil), config.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].Key() < targets[j].Key() })
	for _, target := range targets {
		fmt.Fprintf(&contents, "target=%s\n", target.String())
	}
	if err := writeFile(filepath.Join(directory, "RELEASE"), []byte(contents.String()), 0o644); err != nil {
		return fmt.Errorf("write release metadata: %w", err)
	}
	if err := writeFile(filepath.Join(directory, "SHA256SUMS.pub"), []byte(publicKey+"\n"), 0o644); err != nil {
		return fmt.Errorf("write release public key: %w", err)
	}
	return nil
}

func checksumDirectory(directory string) ([]security.Checksum, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("list staged release files: %w", err)
	}
	checksums := make([]security.Checksum, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "SHA256SUMS.pub" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("staged release member %q is not a regular file", entry.Name())
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read staged release member %q: %w", entry.Name(), err)
		}
		checksums = append(checksums, security.Checksum{SHA256: security.SHA256(contents), Name: entry.Name()})
	}
	sort.Slice(checksums, func(i, j int) bool { return checksums[i].Name < checksums[j].Name })
	return checksums, nil
}

func formatChecksums(checksums []security.Checksum) []byte {
	var output strings.Builder
	for _, checksum := range checksums {
		fmt.Fprintf(&output, "%s  %s\n", checksum.SHA256, checksum.Name)
	}
	return []byte(output.String())
}

func writeFile(path string, contents []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(contents)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return writeErr
	}
	if syncErr != nil {
		_ = os.Remove(path)
		return syncErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return closeErr
	}
	return nil
}

func targetEnvironment(target Target) []string {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	values["GOOS"] = target.goOS()
	values["GOARCH"] = target.Architecture
	values["CGO_ENABLED"] = "0"
	values["GOENV"] = "off"
	values["GOFLAGS"] = ""
	values["GOTOOLCHAIN"] = "local"
	values["GOWORK"] = "off"
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment
}

func validReleaseHostname(hostname string) bool {
	if !releaseHostnamePattern.MatchString(hostname) {
		return false
	}
	for _, label := range strings.Split(hostname, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}
	return len(hostname) <= 253
}

func hasPlatform(targets []Target, platform string) bool {
	for _, target := range targets {
		if target.Platform == platform {
			return true
		}
	}
	return false
}

func hasUnixTarget(targets []Target) bool {
	for _, target := range targets {
		if target.Platform != "windows" {
			return true
		}
	}
	return false
}
