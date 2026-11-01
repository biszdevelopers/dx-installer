package security

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PinnedEd25519PublicKey is set in release builds with Go's -ldflags -X option.
var PinnedEd25519PublicKey string

type Checksum struct {
	SHA256 string
	Name   string
}

var checksumNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,200}$`)

func ParseChecksumList(contents []byte) ([]Checksum, error) {
	if len(contents) == 0 {
		return nil, errors.New("checksum list is empty")
	}
	text := strings.TrimSuffix(string(contents), "\n")
	lines := strings.Split(text, "\n")
	entries := make([]Checksum, 0, len(lines))
	seen := make(map[string]bool, len(lines))
	for number, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		if len(line) < 67 || line[64:66] != "  " {
			return nil, fmt.Errorf("checksum line %d must use SHA-256, two spaces, and a filename", number+1)
		}
		digest := strings.ToLower(line[:64])
		if _, err := hex.DecodeString(digest); err != nil {
			return nil, fmt.Errorf("checksum line %d has invalid hexadecimal digest", number+1)
		}
		name := line[66:]
		if !validChecksumName(name) {
			return nil, fmt.Errorf("checksum line %d has an invalid filename %q", number+1, name)
		}
		if reservedReleaseName(name) {
			return nil, fmt.Errorf("checksum line %d uses a reserved release metadata filename %q", number+1, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("checksum list repeats filename %q", name)
		}
		seen[name] = true
		entries = append(entries, Checksum{SHA256: digest, Name: name})
	}
	return entries, nil
}

func VerifySignedChecksums(contents, signature []byte, publicKey ed25519.PublicKey) ([]Checksum, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("Ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	if len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, contents, signature) {
		return nil, errors.New("checksum signature is invalid")
	}
	return ParseChecksumList(contents)
}

func VerifyPinnedChecksums(contents, signature []byte) ([]Checksum, error) {
	publicKey, err := DecodePublicKey(PinnedEd25519PublicKey)
	if err != nil {
		return nil, fmt.Errorf("pinned release public key is unavailable: %w", err)
	}
	return VerifySignedChecksums(contents, signature, publicKey)
}

func DecodePublicKey(encoded string) (ed25519.PublicKey, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, errors.New("public key is empty")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("Ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(decoded), nil
}

func EncodePublicKey(publicKey ed25519.PublicKey) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", fmt.Errorf("Ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	return base64.StdEncoding.EncodeToString(publicKey), nil
}

func EncodeSignature(signature []byte) (string, error) {
	if len(signature) != ed25519.SignatureSize {
		return "", fmt.Errorf("Ed25519 signature must be %d bytes", ed25519.SignatureSize)
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

func DecodeSignature(encoded []byte) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	if len(decoded) != ed25519.SignatureSize {
		return nil, fmt.Errorf("Ed25519 signature must be %d bytes", ed25519.SignatureSize)
	}
	return decoded, nil
}

func SignChecksums(contents []byte, privateKey ed25519.PrivateKey) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("Ed25519 private key must be %d bytes", ed25519.PrivateKeySize)
	}
	signature := ed25519.Sign(privateKey, contents)
	encoded, err := EncodeSignature(signature)
	return []byte(encoded + "\n"), err
}

func VerifyFiles(root string, entries []Checksum) error {
	for _, entry := range entries {
		if !validChecksumName(entry.Name) {
			return fmt.Errorf("release artifact filename %q is invalid", entry.Name)
		}
		path := filepath.Join(root, entry.Name)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect release artifact %q: %w", entry.Name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("release artifact %q is not a regular file", entry.Name)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read release artifact %q: %w", entry.Name, err)
		}
		if !VerifySHA256(contents, entry.SHA256) {
			return fmt.Errorf("release artifact %q does not match its SHA-256 digest", entry.Name)
		}
	}
	return nil
}

// VerifyReleaseFiles ensures that the checksum list covers every payload file
// and that the directory contains only the required release metadata files.
func VerifyReleaseFiles(root string, entries []Checksum) error {
	checksummed := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !validChecksumName(entry.Name) || reservedReleaseName(entry.Name) {
			return fmt.Errorf("release artifact filename %q is invalid or reserved", entry.Name)
		}
		if checksummed[entry.Name] {
			return fmt.Errorf("release checksum list repeats artifact %q", entry.Name)
		}
		checksummed[entry.Name] = true
	}
	if !checksummed["RELEASE"] {
		return errors.New("signed checksum list does not cover RELEASE metadata")
	}
	files, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("list release directory: %w", err)
	}
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		name := file.Name()
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("inspect release member %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("release member %q is not a regular file", name)
		}
		seen[name] = true
		if releaseMetadataName(name) {
			continue
		}
		if !checksummed[name] {
			return fmt.Errorf("release contains unsigned artifact %q", name)
		}
	}
	for _, name := range []string{"SHA256SUMS", "SHA256SUMS.sig", "SHA256SUMS.pub"} {
		if !seen[name] {
			return fmt.Errorf("release is missing %s", name)
		}
	}
	return VerifyFiles(root, entries)
}

func validChecksumName(name string) bool {
	return name != "" && !filepath.IsAbs(name) && filepath.Base(name) == name && name != "." && name != ".." && checksumNamePattern.MatchString(name)
}

func reservedReleaseName(name string) bool {
	return name == "SHA256SUMS" || name == "SHA256SUMS.sig" || name == "SHA256SUMS.pub"
}

func releaseMetadataName(name string) bool {
	return reservedReleaseName(name)
}

func SHA256(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func VerifySHA256(contents []byte, expected string) bool {
	if len(expected) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(expected)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(contents)
	var difference byte
	for index := range decoded {
		difference |= decoded[index] ^ digest[index]
	}
	return difference == 0
}
