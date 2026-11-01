package offline

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/platform"
	"developers-club-dx/internal/security"
)

type Manifest struct {
	SchemaVersion int                  `json:"schema_version"`
	Platform      tool.OperatingSystem `json:"platform"`
	Architecture  tool.Architecture    `json:"architecture"`
	Artifacts     []Payload            `json:"artifacts"`
	Bootstrap     *Payload             `json:"bootstrap,omitempty"`
}

type Payload struct {
	ToolID  string `json:"tool_id,omitempty"`
	Version string `json:"version,omitempty"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
}

type VerifiedBundle struct {
	Manifest  Manifest
	Artifacts map[string]string
	Bootstrap string
}

var fileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,200}$`)

const maxBundleArtifactSize int64 = 1 << 30

func LoadBundle(root string, items *catalog.Catalog, machine platform.Info, publicKey ed25519.PublicKey) (VerifiedBundle, error) {
	verified := VerifiedBundle{Artifacts: make(map[string]string)}
	if !filepath.IsAbs(root) {
		return verified, errors.New("offline bundle directory must be an absolute path")
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return verified, errors.New("pinned bundle verification key is unavailable")
	}
	if items == nil {
		return verified, errors.New("embedded tool catalog is required to verify the bundle")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return verified, fmt.Errorf("inspect offline bundle: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return verified, errors.New("offline bundle root must be a real directory")
	}
	manifestBytes, err := readRegular(filepath.Join(root, "bundle.json"), 4<<20)
	if err != nil {
		return verified, fmt.Errorf("read signed bundle metadata: %w", err)
	}
	signatureBytes, err := readRegular(filepath.Join(root, "bundle.sig"), 4096)
	if err != nil {
		return verified, fmt.Errorf("read bundle signature: %w", err)
	}
	signature, err := security.DecodeSignature(signatureBytes)
	if err != nil {
		return verified, err
	}
	if !ed25519.Verify(publicKey, manifestBytes, signature) {
		return verified, errors.New("offline bundle metadata signature is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&verified.Manifest); err != nil {
		return VerifiedBundle{}, fmt.Errorf("decode bundle metadata: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return VerifiedBundle{}, errors.New("bundle metadata contains trailing JSON data")
	}
	if err := validateManifest(verified.Manifest, machine); err != nil {
		return VerifiedBundle{}, err
	}
	expectedFiles := map[string]bool{"bundle.json": true, "bundle.sig": true}
	var total int64
	for _, payload := range verified.Manifest.Artifacts {
		if err := validatePayload(payload); err != nil {
			return VerifiedBundle{}, err
		}
		if expectedFiles[payload.File] {
			return VerifiedBundle{}, fmt.Errorf("bundle repeats payload filename %q", payload.File)
		}
		expectedFiles[payload.File] = true
		artifactManifest, exists := items.Tool(payload.ToolID)
		if !exists {
			return VerifiedBundle{}, fmt.Errorf("bundle contains unknown tool %q", payload.ToolID)
		}
		artifact, exists := artifactManifest.ArtifactFor(machine.OS, machine.Architecture)
		if !exists || artifact.Version != payload.Version || !strings.EqualFold(payload.SHA256, artifact.SHA256) {
			return VerifiedBundle{}, fmt.Errorf("bundle artifact %q is not pinned by the embedded catalog for this platform", payload.ToolID)
		}
		path := filepath.Join(root, payload.File)
		size, err := verifyPayload(path, payload.SHA256)
		if err != nil {
			return VerifiedBundle{}, fmt.Errorf("verify bundle artifact %q: %w", payload.ToolID, err)
		}
		total += size
		if total > 4*maxBundleArtifactSize {
			return VerifiedBundle{}, errors.New("offline bundle exceeds the aggregate payload size limit")
		}
		if _, duplicate := verified.Artifacts[payload.ToolID]; duplicate {
			return VerifiedBundle{}, fmt.Errorf("bundle repeats tool %q", payload.ToolID)
		}
		verified.Artifacts[payload.ToolID] = path
	}
	if verified.Manifest.Bootstrap != nil {
		payload := *verified.Manifest.Bootstrap
		if payload.ToolID != "" || payload.Version != "" {
			return VerifiedBundle{}, errors.New("bootstrap payload cannot identify a tool version")
		}
		if err := validatePayload(payload); err != nil {
			return VerifiedBundle{}, err
		}
		if expectedFiles[payload.File] {
			return VerifiedBundle{}, fmt.Errorf("bundle repeats payload filename %q", payload.File)
		}
		expectedFiles[payload.File] = true
		if _, err := verifyPayload(filepath.Join(root, payload.File), payload.SHA256); err != nil {
			return VerifiedBundle{}, fmt.Errorf("verify bundled bootstrap executable: %w", err)
		}
		verified.Bootstrap = filepath.Join(root, payload.File)
	}
	if len(verified.Manifest.Artifacts) == 0 {
		return VerifiedBundle{}, errors.New("offline bundle contains no tool artifacts")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return VerifiedBundle{}, fmt.Errorf("list offline bundle: %w", err)
	}
	if len(entries) != len(expectedFiles) {
		return VerifiedBundle{}, errors.New("offline bundle has missing or unsigned files")
	}
	for _, entry := range entries {
		if !expectedFiles[entry.Name()] {
			return VerifiedBundle{}, fmt.Errorf("offline bundle contains unsigned file %q", entry.Name())
		}
		info, err := os.Lstat(filepath.Join(root, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return VerifiedBundle{}, fmt.Errorf("offline bundle member %q is not a regular file", entry.Name())
		}
	}
	return verified, nil
}

func validateManifest(manifest Manifest, machine platform.Info) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("offline bundle schema version must be 1, got %d", manifest.SchemaVersion)
	}
	if manifest.Platform != machine.OS || manifest.Architecture != machine.Architecture {
		return fmt.Errorf("offline bundle targets %s/%s, but this machine is %s/%s", manifest.Platform, manifest.Architecture, machine.OS, machine.Architecture)
	}
	return nil
}

func validatePayload(payload Payload) error {
	if !fileNamePattern.MatchString(payload.File) || filepath.Base(payload.File) != payload.File || payload.File == "." || payload.File == ".." {
		return fmt.Errorf("bundle payload filename %q is unsafe", payload.File)
	}
	if len(payload.SHA256) != sha256.Size*2 {
		return fmt.Errorf("bundle payload %q must declare SHA-256", payload.File)
	}
	if _, err := hex.DecodeString(payload.SHA256); err != nil {
		return fmt.Errorf("bundle payload %q has malformed SHA-256", payload.File)
	}
	if payload.ToolID != "" && payload.Version == "" {
		return fmt.Errorf("tool payload %q must declare a version", payload.File)
	}
	return nil
}

func verifyPayload(path, expected string) (int64, error) {
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("bundle payload is not a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(linkInfo, info) || info.Size() < 0 || info.Size() > maxBundleArtifactSize {
		return 0, errors.New("bundle payload is not a regular file or exceeds the size limit")
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, io.LimitReader(file, maxBundleArtifactSize+1)); err != nil {
		return 0, err
	}
	if !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), expected) {
		return 0, errors.New("SHA-256 digest does not match the signed metadata")
	}
	return info.Size(), nil
}

func readRegular(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("bundle metadata member is not a regular file or is too large")
	}
	return os.ReadFile(path)
}

func SortManifest(manifest *Manifest) {
	sort.Slice(manifest.Artifacts, func(left, right int) bool {
		return manifest.Artifacts[left].ToolID < manifest.Artifacts[right].ToolID
	})
}
