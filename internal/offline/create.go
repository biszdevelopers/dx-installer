package offline

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"developers-club-dx/internal/platform"
	"developers-club-dx/internal/security"
)

func CreateBundle(output string, manifest Manifest, sourceFiles map[string]string, machine platform.Info, privateKey ed25519.PrivateKey) error {
	if !filepath.IsAbs(output) {
		return errors.New("bundle output directory must be an absolute path")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("bundle generation requires an Ed25519 private key")
	}
	if err := validateManifest(manifest, machine); err != nil {
		return err
	}
	SortManifest(&manifest)
	if len(manifest.Artifacts) == 0 {
		return errors.New("bundle must include at least one tool artifact")
	}
	expectedSources := make(map[string]bool, len(manifest.Artifacts)+1)
	for _, payload := range manifest.Artifacts {
		if err := validatePayload(payload); err != nil {
			return err
		}
		if expectedSources[payload.File] {
			return fmt.Errorf("bundle repeats payload filename %q", payload.File)
		}
		expectedSources[payload.File] = true
	}
	if manifest.Bootstrap != nil {
		if !manifest.Bootstrap.Executable || manifest.Bootstrap.ToolID != "" || manifest.Bootstrap.Version != "" {
			return errors.New("optional bootstrap payload must be executable and cannot identify a tool version")
		}
		if err := validatePayload(*manifest.Bootstrap); err != nil {
			return err
		}
		if expectedSources[manifest.Bootstrap.File] {
			return fmt.Errorf("bundle repeats payload filename %q", manifest.Bootstrap.File)
		}
		expectedSources[manifest.Bootstrap.File] = true
	}
	if len(sourceFiles) != len(expectedSources) {
		return errors.New("bundle source files must match the signed payload list exactly")
	}
	for name := range expectedSources {
		if sourceFiles[name] == "" {
			return fmt.Errorf("bundle source for %q is missing", name)
		}
	}
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create bundle parent directory: %w", err)
	}
	if _, err := os.Lstat(output); err == nil {
		return fmt.Errorf("bundle output already exists; refusing to replace %s", output)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect bundle output: %w", err)
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		return fmt.Errorf("create bundle output: %w", err)
	}
	outputInfo, err := os.Lstat(output)
	if err != nil {
		_ = os.Remove(output)
		return fmt.Errorf("inspect new bundle output: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		current, inspectErr := os.Lstat(output)
		if inspectErr == nil && os.SameFile(outputInfo, current) {
			_ = os.RemoveAll(output)
		}
	}()
	for _, payload := range manifest.Artifacts {
		if err := copyVerified(sourceFiles[payload.File], filepath.Join(output, payload.File), payload.SHA256, payload.Executable); err != nil {
			return fmt.Errorf("add bundle artifact %q: %w", payload.ToolID, err)
		}
	}
	if manifest.Bootstrap != nil {
		if err := copyVerified(sourceFiles[manifest.Bootstrap.File], filepath.Join(output, manifest.Bootstrap.File), manifest.Bootstrap.SHA256, manifest.Bootstrap.Executable); err != nil {
			return fmt.Errorf("add bundle bootstrap: %w", err)
		}
	}
	metadata, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode bundle metadata: %w", err)
	}
	metadata = append(metadata, '\n')
	signature, err := security.EncodeSignature(ed25519.Sign(privateKey, metadata))
	if err != nil {
		return err
	}
	if err := writeExclusive(filepath.Join(output, "bundle.json"), metadata); err != nil {
		return fmt.Errorf("write bundle metadata: %w", err)
	}
	if err := writeExclusive(filepath.Join(output, "bundle.sig"), []byte(signature+"\n")); err != nil {
		return fmt.Errorf("write bundle signature: %w", err)
	}
	committed = true
	return nil
}

func copyVerified(source, destination, expectedSHA256 string, executable bool) error {
	if !filepath.IsAbs(source) {
		return errors.New("bundle source file must be absolute")
	}
	linkInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("bundle source must be a regular non-symlink file")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	inputInfo, err := input.Stat()
	if err != nil || !os.SameFile(linkInfo, inputInfo) {
		return errors.New("bundle source changed while it was being opened")
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(output, hasher), io.LimitReader(input, maxBundleArtifactSize+1))
	if copyErr != nil {
		_ = output.Close()
		return copyErr
	}
	if written > maxBundleArtifactSize {
		_ = output.Close()
		return errors.New("bundle source exceeds the payload size limit")
	}
	if !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), expectedSHA256) {
		_ = output.Close()
		return errors.New("bundle source does not match its catalog SHA-256 digest")
	}
	if executable {
		if err := output.Chmod(0o700); err != nil {
			_ = output.Close()
			return err
		}
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func HashSource(path string) (string, int64, error) {
	if !filepath.IsAbs(path) {
		return "", 0, errors.New("bundle source file must be absolute")
	}
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 {
		return "", 0, errors.New("bundle source must be a regular non-symlink file")
	}
	if linkInfo.Size() < 0 || linkInfo.Size() > maxBundleArtifactSize {
		return "", 0, errors.New("bundle source exceeds the payload size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(linkInfo, openedInfo) {
		return "", 0, errors.New("bundle source changed while it was being opened")
	}
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.LimitReader(file, maxBundleArtifactSize+1))
	if err != nil {
		return "", 0, err
	}
	if written > maxBundleArtifactSize {
		return "", 0, errors.New("bundle source exceeds the payload size limit")
	}
	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}

func writeExclusive(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
